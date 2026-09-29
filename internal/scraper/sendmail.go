package scraper

import (
	"context"
	"fmt"
	"html"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/genjerator/krile/internal/config"
	"github.com/genjerator/krile/internal/output"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	gomail "github.com/wneessen/go-mail"
)

// emailsTable holds the per-company generated emails (subject, body, and
// send-tracking), one-to-many with the companies table via company_id.
const emailsTable = "company_emails"

// sendRow is one company_emails row (joined with its company) eligible for a
// send. emailID identifies the company_emails row to mark; companyID/name/city
// come from the parent companies row and email is the recipient address.
type sendRow struct {
	emailID   string
	companyID int64
	name      string
	city      string
	email     string
	subject   string
	body      string
}

// RunSendEmails sends the generated subject/email_body to companies.
//
// Safety model (this mode is deliberately hard to fire by accident):
//   - Nothing is sent unless BOTH --send-emails AND --confirm-send are passed.
//     Without --confirm-send it is a DRY RUN: it prints exactly what it would
//     send and touches neither SMTP nor the database.
//   - A row is only ever sent once: rows already marked email_status='sent'
//     (with a non-null email_sent_at) are excluded, and each row is marked
//     'sent' immediately after a successful delivery.
//   - A per-calendar-day cap (--daily-limit, default 10) counts rows already
//     sent today, so repeated runs in one day cannot exceed it.
//   - Only rows with a valid recipient address AND a non-empty subject AND a
//     non-empty email_body are ever considered.
//   - --test-to redirects every message to one address and does NOT mark the
//     row as sent (for verifying rendering without emailing companies).
func RunSendEmails(ctx context.Context, cfg config.Config) error {
	start := time.Now()

	table := cfg.DBTable
	if table == "" {
		table = "companies"
	}
	dailyLimit := cfg.DailyLimit
	if dailyLimit <= 0 {
		dailyLimit = 10
	}

	pool, err := pgxpool.New(ctx, output.PostgresConnString(cfg))
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}

	if err := ensureSendColumns(ctx, pool, table); err != nil {
		return err
	}

	// Targeted single send by company_emails uuid bypasses the daily budget.
	emailID := strings.TrimSpace(cfg.SendEmailID)
	var rows []sendRow
	if emailID != "" {
		if !uuidRe.MatchString(emailID) {
			return fmt.Errorf("--email-id %q is not a valid uuid", emailID)
		}
		fmt.Fprintf(os.Stderr, "[SEND] table=%q  email-id=%s (single, daily-limit ignored)\n", table, emailID)
		rows, err = loadSendableRows(ctx, pool, table, emailID, 1)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			fmt.Fprintf(os.Stderr, "[SEND] email-id %s is not sendable (unknown id, missing recipient/subject/body, or already sent)\n", emailID)
			return nil
		}
	} else {
		// How much of today's budget is left (shared across runs on the same day).
		sentToday, err := countSentToday(ctx, pool, table)
		if err != nil {
			return err
		}
		budget := dailyLimit - sentToday
		if budget < 0 {
			budget = 0
		}
		if cfg.Limit > 0 && cfg.Limit < budget {
			budget = cfg.Limit
		}

		fmt.Fprintf(os.Stderr, "[SEND] table=%q  daily-limit=%d  already-sent-today=%d  budget-this-run=%s%d%s\n",
			table, dailyLimit, sentToday, colorCyan, budget, colorReset)

		if budget == 0 {
			fmt.Fprintf(os.Stderr, "[SEND] daily limit reached — nothing to send today\n")
			return nil
		}

		rows, err = loadSendableRows(ctx, pool, table, "", budget)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			fmt.Fprintf(os.Stderr, "[SEND] no eligible rows (need email + subject + email_body, not already sent)\n")
			return nil
		}
	}

	// ---- DRY RUN --------------------------------------------------------
	if !cfg.ConfirmSend {
		fmt.Fprintf(os.Stderr, "\n%s[SEND] DRY RUN — no emails sent, database untouched.%s\n", colorCyan, colorReset)
		fmt.Fprintf(os.Stderr, "%d message(s) would be sent (add --confirm-send to actually send):\n\n", len(rows))
		for i := range rows {
			r := &rows[i]
			to := r.email
			if cfg.TestTo != "" {
				to = cfg.TestTo + "  (redirected via --test-to; real recipient " + r.email + ")"
			}
			preview := bodyPreview(r.body, 200)
			fmt.Fprintf(os.Stderr, "  company=%d email=%s  %s <%s>\n    Subject: %s\n    %s\n\n", r.companyID, r.emailID, r.name, to, r.subject, preview)
		}
		fmt.Fprintf(os.Stderr, "%s[SEND] DRY RUN complete — re-run with --confirm-send to send the above.%s\n", colorCyan, colorReset)
		return nil
	}

	// ---- LIVE SEND ------------------------------------------------------
	client, from, err := newMailClient(cfg)
	if err != nil {
		return err
	}

	// Log file next to the other exports.
	if err := os.MkdirAll("export", 0o755); err != nil {
		return fmt.Errorf("create export dir: %w", err)
	}
	logPath := filepath.Join("export", "send-emails.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()
	var logMu sync.Mutex
	logf := func(format string, args ...interface{}) {
		logMu.Lock()
		defer logMu.Unlock()
		fmt.Fprintf(logFile, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
	}

	mode := "LIVE"
	if cfg.TestTo != "" {
		mode = "TEST → " + cfg.TestTo
	}
	fmt.Fprintf(os.Stderr, "\n%s[SEND] LIVE SEND (%s) — %d message(s), from %s%s\n", colorRed, mode, len(rows), from, colorReset)
	logf("=== send start: %d messages, mode=%s, from=%s, table=%q ===", len(rows), mode, from, table)

	var sent, failed int
	for i := range rows {
		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "[SEND] interrupted — stopping\n")
			break
		}
		r := &rows[i]

		recipient := r.email
		if cfg.TestTo != "" {
			recipient = cfg.TestTo
		}

		msg, err := buildMessage(cfg, from, recipient, r)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "[SEND] company=%d email=%s %q: build failed: %v\n", r.companyID, r.emailID, r.name, err)
			logf("company=%d email=%s %q <%s>: build failed: %v", r.companyID, r.emailID, r.name, r.email, err)
			continue
		}

		if err := client.DialAndSendWithContext(ctx, msg); err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "[SEND] company=%d email=%s %q <%s>: %sSEND FAILED%s: %v\n", r.companyID, r.emailID, r.name, recipient, colorRed, colorReset, err)
			logf("company=%d email=%s %q <%s>: SEND FAILED: %v", r.companyID, r.emailID, r.name, r.email, err)
			if cfg.TestTo == "" {
				_ = markSendResult(ctx, pool, r.emailID, "error", err.Error())
			}
			continue
		}

		sent++
		fmt.Fprintf(os.Stderr, "[SEND] company=%d email=%s %q → %s%s%s  %s(%d/%d)%s\n",
			r.companyID, r.emailID, r.name, colorBlue, recipient, colorReset, colorCyan, sent, len(rows), colorReset)

		if cfg.TestTo != "" {
			logf("company=%d email=%s %q: TEST sent to %s (row NOT marked)", r.companyID, r.emailID, r.name, recipient)
		} else if err := markSendResult(ctx, pool, r.emailID, "sent", ""); err != nil {
			// Delivered but bookkeeping failed — warn loudly so it isn't re-sent silently.
			fmt.Fprintf(os.Stderr, "[SEND] %sWARNING%s email=%s sent but DB mark failed: %v — mark it manually to avoid a re-send\n",
				colorRed, colorReset, r.emailID, err)
			logf("company=%d email=%s %q <%s>: SENT but DB mark failed: %v", r.companyID, r.emailID, r.name, r.email, err)
		} else {
			logf("company=%d email=%s %q <%s>: SENT", r.companyID, r.emailID, r.name, r.email)
		}

		// Pace between sends (skip after the last one).
		if i < len(rows)-1 && cfg.Delay > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(time.Duration(cfg.Delay) * time.Millisecond):
			}
		}
	}

	fmt.Fprintf(os.Stderr, "\n[SEND] done in %s\n", time.Since(start).Round(time.Second))
	fmt.Fprintf(os.Stderr, "  Sent:    %s%d%s\n", colorBlue, sent, colorReset)
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "  Failed:  %d\n", failed)
	}
	fmt.Fprintf(os.Stderr, "  Log:     %s\n", logPath)
	logf("=== send done in %s: %d sent, %d failed ===", time.Since(start).Round(time.Second), sent, failed)
	return nil
}

// newMailClient builds an SMTP client from the config, validating that the
// required SMTP settings are present. It returns the client and the resolved
// From address string.
func newMailClient(cfg config.Config) (*gomail.Client, string, error) {
	if cfg.SMTPHost == "" {
		return nil, "", fmt.Errorf("SMTP host is required for --confirm-send (set SMTP_HOST or --smtp-host)")
	}
	from := cfg.SMTPFrom
	if from == "" {
		from = cfg.SMTPUser
	}
	if from == "" {
		return nil, "", fmt.Errorf("no From address (set SMTP_FROM or SMTP_USER)")
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return nil, "", fmt.Errorf("invalid From address %q: %w", from, err)
	}

	port := cfg.SMTPPort
	if port == 0 {
		port = 587
	}

	opts := []gomail.Option{gomail.WithPort(port)}

	// Auth is only used when a username is set. Local relays like mailpit
	// accept mail with no credentials, so we skip AUTH entirely then.
	if cfg.SMTPUser != "" {
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthPlain),
			gomail.WithUsername(cfg.SMTPUser),
			gomail.WithPassword(cfg.SMTPPassword),
		)
	} else {
		opts = append(opts, gomail.WithSMTPAuth(gomail.SMTPAuthNoAuth))
	}

	// TLS policy: an explicit SMTP_TLS/--smtp-tls wins; otherwise infer from
	// the port (465 → implicit TLS, anything else → mandatory STARTTLS).
	switch strings.ToLower(strings.TrimSpace(cfg.SMTPTLS)) {
	case "ssl", "tls", "implicit":
		opts = append(opts, gomail.WithSSL())
	case "none", "off", "disable", "plain", "insecure":
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS)) // e.g. local mailpit
	case "starttls", "opportunistic":
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSOpportunistic))
	case "mandatory":
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	case "":
		if port == 465 {
			opts = append(opts, gomail.WithSSL()) // implicit TLS
		} else {
			opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory)) // STARTTLS, required
		}
	default:
		return nil, "", fmt.Errorf("invalid SMTP_TLS %q (want: ssl | starttls | none)", cfg.SMTPTLS)
	}

	client, err := gomail.NewClient(cfg.SMTPHost, opts...)
	if err != nil {
		return nil, "", fmt.Errorf("create SMTP client: %w", err)
	}
	return client, from, nil
}

// buildMessage constructs a multipart/alternative message (HTML + plaintext)
// for one row.
func buildMessage(cfg config.Config, from, recipient string, r *sendRow) (*gomail.Msg, error) {
	msg := gomail.NewMsg()
	if cfg.SMTPFromName != "" {
		if err := msg.FromFormat(cfg.SMTPFromName, from); err != nil {
			return nil, fmt.Errorf("set From: %w", err)
		}
	} else if err := msg.From(from); err != nil {
		return nil, fmt.Errorf("set From: %w", err)
	}
	if err := msg.To(recipient); err != nil {
		return nil, fmt.Errorf("set To: %w", err)
	}

	// Ask SES to apply this configuration set (open/click tracking + events).
	if cfg.SESConfigSet != "" {
		msg.SetGenHeader(gomail.Header("X-SES-CONFIGURATION-SET"), cfg.SESConfigSet)
	}
	// Native unsubscribe control (mailto-based, no hosting required).
	if cfg.UnsubscribeMailto != "" {
		msg.SetGenHeader(gomail.Header("List-Unsubscribe"),
			"<mailto:"+cfg.UnsubscribeMailto+"?subject=unsubscribe>")
	}

	subject := r.subject
	if cfg.TestTo != "" {
		subject = "[TEST] " + subject
	}
	msg.Subject(subject)

	body := addUTMParams(r.body, r.emailID, cfg.UTMCampaign, cfg.UTMSource, cfg.UTMMedium)
	htmlBody, textBody := buildBodies(body)
	// Plaintext first as the fallback part, HTML as the preferred alternative.
	msg.SetBodyString(gomail.TypeTextPlain, textBody)
	msg.AddAlternativeString(gomail.TypeTextHTML, htmlBody)
	return msg, nil
}

var htmlTagRe = regexp.MustCompile(`(?is)<[a-z/!][^>]*>`)

// uuidRe validates the --email-id value before it reaches the uuid column.
var uuidRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// planetaLinkRe matches http(s) links to planetaindustries.de (with or without
// www) up to the first whitespace, quote, angle bracket or closing paren.
var planetaLinkRe = regexp.MustCompile(`https?://(?:www\.)?planetaindustries\.de[^\s"'<>)]*`)

// addUTMParams appends utm_* query params to every planetaindustries.de link in
// content so clicks are attributable in Google Analytics. utm_content carries
// the company_emails row uuid (a per-recipient signature). Tagging is a no-op
// when campaign is empty; links already carrying utm_* params are left alone.
func addUTMParams(content, emailID, campaign, source, medium string) string {
	if campaign == "" {
		return content
	}
	if source == "" {
		source = "email"
	}
	return planetaLinkRe.ReplaceAllStringFunc(content, func(link string) string {
		if strings.Contains(link, "utm_") {
			return link // already tagged
		}
		frag := ""
		if i := strings.IndexByte(link, '#'); i >= 0 {
			frag, link = link[i:], link[:i]
		}
		params := url.Values{}
		params.Set("utm_source", source)
		params.Set("utm_medium", medium)
		params.Set("utm_campaign", campaign)
		if emailID != "" {
			params.Set("utm_content", emailID)
		}
		sep := "?"
		if strings.ContainsRune(link, '?') {
			sep = "&"
		}
		return link + sep + params.Encode() + frag
	})
}

// buildBodies returns an (htmlBody, textBody) pair from stored content that may
// be either HTML or plain text. If the content already contains HTML tags it is
// used as-is for the HTML part and stripped for the text part; otherwise the
// plain text is wrapped in minimal HTML (newlines → <br>).
func buildBodies(content string) (htmlBody, textBody string) {
	if htmlTagRe.MatchString(content) {
		return content, htmlToText(content)
	}
	// Plain text: escape and convert line breaks for the HTML alternative.
	escaped := html.EscapeString(content)
	escaped = strings.ReplaceAll(escaped, "\n", "<br>\n")
	return "<html><body>" + escaped + "</body></html>", content
}

var (
	blockTagRe = regexp.MustCompile(`(?is)</(p|div|br|tr|li|h[1-6])>|<br\s*/?>`)
	anyTagRe   = regexp.MustCompile(`(?is)<[^>]+>`)
	wsRunRe    = regexp.MustCompile(`[ \t]+`)
	nlRunRe    = regexp.MustCompile(`\n{3,}`)
)

// htmlToText produces a readable plaintext rendering of an HTML body for the
// multipart/alternative text part.
func htmlToText(h string) string {
	s := blockTagRe.ReplaceAllString(h, "\n")
	s = anyTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = wsRunRe.ReplaceAllString(s, " ")
	s = nlRunRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// bodyPreview returns a single-line, length-capped preview of a body for the
// dry-run listing.
func bodyPreview(body string, max int) string {
	s := htmlToText(body)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// ensureSendColumns makes sure the company_emails table exists (idempotent).
// The generated subject/body and send-tracking now live in a one-to-many child
// table keyed by company_id, so email content is never stored on the companies
// row itself. `table` is the parent (companies) table the FK references.
func ensureSendColumns(ctx context.Context, pool *pgxpool.Pool, table string) error {
	q := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		company_id         INTEGER NOT NULL REFERENCES %s(id) ON DELETE CASCADE,
		relevant_products  TEXT,
		reasoning          TEXT,
		subject            TEXT,
		email_body         TEXT,
		email_status       TEXT,
		email_sent_at      TIMESTAMPTZ,
		email_error        TEXT,
		created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
		pgx.Identifier{emailsTable}.Sanitize(), pgx.Identifier{table}.Sanitize())
	if _, err := pool.Exec(ctx, q); err != nil {
		return fmt.Errorf("create %s table: %w", emailsTable, err)
	}
	idx := fmt.Sprintf("CREATE INDEX IF NOT EXISTS company_emails_company_id_idx ON %s (company_id)",
		pgx.Identifier{emailsTable}.Sanitize())
	if _, err := pool.Exec(ctx, idx); err != nil {
		return fmt.Errorf("index %s: %w", emailsTable, err)
	}
	return nil
}

// countSentToday counts email rows already delivered on the current calendar day.
func countSentToday(ctx context.Context, pool *pgxpool.Pool, table string) (int, error) {
	q := fmt.Sprintf(
		"SELECT count(*) FROM %s WHERE email_status = 'sent' AND email_sent_at IS NOT NULL AND email_sent_at::date = current_date",
		pgx.Identifier{emailsTable}.Sanitize())
	var n int
	if err := pool.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("count sent today: %w", err)
	}
	return n, nil
}

// loadSendableRows reads up to `limit` company_emails rows that have a subject
// and body and have not already been sent, joined to their company for the
// recipient address and name. Ordered by created_at so runs are deterministic
// and resumable (the uuid id is not chronological). When emailID is non-empty
// it restricts the selection to that single company_emails row.
func loadSendableRows(ctx context.Context, pool *pgxpool.Pool, table, emailID string, limit int) ([]sendRow, error) {
	var idClause string
	var args []interface{}
	if emailID != "" {
		idClause = " AND e.id = $1"
		args = append(args, emailID)
	}
	q := fmt.Sprintf(
		`SELECT e.id, c.id, c.name, COALESCE(c.city,''), c.email, e.subject, e.email_body
		 FROM %s e
		 JOIN %s c ON c.id = e.company_id
		 WHERE c.email IS NOT NULL AND c.email <> ''
		   AND e.subject IS NOT NULL AND e.subject <> ''
		   AND e.email_body IS NOT NULL AND e.email_body <> ''
		   AND (e.email_status IS DISTINCT FROM 'sent' OR e.email_sent_at IS NULL)%s
		 ORDER BY e.created_at, e.id
		 LIMIT %d`,
		pgx.Identifier{emailsTable}.Sanitize(), pgx.Identifier{table}.Sanitize(), idClause, limit)
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query sendable rows: %w", err)
	}
	defer rows.Close()

	var out []sendRow
	for rows.Next() {
		var r sendRow
		if err := rows.Scan(&r.emailID, &r.companyID, &r.name, &r.city, &r.email, &r.subject, &r.body); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		// Final application-level guard against a malformed recipient.
		if _, err := mail.ParseAddress(strings.TrimSpace(r.email)); err != nil {
			fmt.Fprintf(os.Stderr, "[SEND] skipping company=%d email=%s %q: invalid email %q\n", r.companyID, r.emailID, r.name, r.email)
			continue
		}
		r.email = strings.TrimSpace(r.email)
		out = append(out, r)
	}
	return out, rows.Err()
}

// markSendResult records the outcome for one company_emails row (by its own
// id). status "sent" stamps email_sent_at=now(); any other status records the
// error without a timestamp so the row stays eligible for a later retry.
func markSendResult(ctx context.Context, pool *pgxpool.Pool, emailID string, status, errMsg string) error {
	var q string
	if status == "sent" {
		q = fmt.Sprintf("UPDATE %s SET email_status='sent', email_sent_at=now(), email_error=NULL WHERE id=$1",
			pgx.Identifier{emailsTable}.Sanitize())
		_, err := pool.Exec(ctx, q, emailID)
		return err
	}
	q = fmt.Sprintf("UPDATE %s SET email_status=$1, email_error=$2 WHERE id=$3",
		pgx.Identifier{emailsTable}.Sanitize())
	_, err := pool.Exec(ctx, q, status, nullIfBlank(errMsg), emailID)
	return err
}
