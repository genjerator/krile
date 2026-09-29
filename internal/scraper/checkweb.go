package scraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/genjerator/krile/internal/config"
	"github.com/genjerator/krile/internal/output"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// parkedSignatures are lowercase substrings that mark a placeholder/parked page
// (a domain registered at a host but with no real site). The example
// pension-am-schacht.de is a STRATO "Domain reserved" page.
var parkedSignatures = []string{
	// STRATO reserved/placeholder (multi-language on one page)
	"domain reserved", "this domain is now reserved", "no content has been uploaded",
	"domain wurde soeben freigeschaltet", "es wurden noch keine inhalte hinterlegt",
	// IONOS / 1&1 / generic hoster placeholders
	"für einen unserer kunden reserviert", "diese domain wurde reserviert",
	"domain wurde erfolgreich registriert", "website wurde erfolgreich",
	"diese website wurde eingestellt",
	// under-construction placeholders
	"im aufbau", "under construction", "diese seite befindet sich im aufbau", "baustelle",
	// for-sale / parking
	"this domain is for sale", "buy this domain", "domain is parked",
	"diese domain steht zum verkauf", "domain kaufen",
}

// parkedHosts are parking/for-sale services a dead domain often redirects to.
var parkedHosts = []string{
	"sedoparking.com", "sedo.com", "parkingcrew.net", "bodis.com", "dan.com",
	"afternic.com", "hugedomains.com", "above.com", "undeveloped.com",
}

// webResult is one website's check outcome.
type webResult struct {
	id        int64
	name      string
	city      string
	website   string
	finalURL  string
	status    string // ok | dead | parked | http_<code>
	httpCode  int
	signature string // which parked marker / error detail
}

// RunCheckWebsites checks every companies-table row that has a website and
// classifies it as ok, dead (unreachable), http_404 / http_<code>, or parked
// (a hosting-provider placeholder such as STRATO "Domain reserved"). Results
// are written back to the row (website_status, website_http_code,
// website_detail, website_checked_at); a summary is printed to stderr.
func RunCheckWebsites(ctx context.Context, cfg config.Config) error {
	start := time.Now()
	table := cfg.DBTable
	if table == "" {
		table = "companies"
	}

	pool, err := pgxpool.New(ctx, output.PostgresConnString(cfg))
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}

	if err := ensureWebColumns(ctx, pool, table); err != nil {
		return err
	}

	results, err := loadWebsiteRows(ctx, pool, table, cfg.Limit)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "[CHECK] %s%d%s companies with a website in %q — writing results to the table\n",
		colorCyan, len(results), colorReset, table)
	if len(results) == 0 {
		return nil
	}

	client := &http.Client{Timeout: 15 * time.Second}

	workers := cfg.Workers
	if workers <= 0 {
		workers = 8
	}
	var mu sync.Mutex
	counts := map[string]int{}
	var done, updateErrs int64

	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if ctx.Err() != nil {
					return
				}
				r := &results[idx]
				classifyWebsite(ctx, client, r)
				if err := updateWebResult(ctx, pool, table, r); err != nil {
					mu.Lock()
					updateErrs++
					mu.Unlock()
					fmt.Fprintf(os.Stderr, "[CHECK] id=%d DB update failed: %v\n", r.id, err)
				}
				mu.Lock()
				counts[r.status]++
				done++
				n := done
				mu.Unlock()
				if n%100 == 0 {
					fmt.Fprintf(os.Stderr, "[CHECK] %d/%d checked\n", n, len(results))
				}
			}
		}()
	}
	for i := range results {
		select {
		case <-ctx.Done():
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()

	printWebSummary(counts, len(results), updateErrs, start)
	return nil
}

// ensureWebColumns adds the result columns to the table if they are missing.
func ensureWebColumns(ctx context.Context, pool *pgxpool.Pool, table string) error {
	q := fmt.Sprintf(`ALTER TABLE %s
		ADD COLUMN IF NOT EXISTS website_status TEXT,
		ADD COLUMN IF NOT EXISTS website_http_code INTEGER,
		ADD COLUMN IF NOT EXISTS website_detail TEXT,
		ADD COLUMN IF NOT EXISTS website_checked_at TIMESTAMPTZ`,
		pgx.Identifier{table}.Sanitize())
	if _, err := pool.Exec(ctx, q); err != nil {
		return fmt.Errorf("add website columns: %w", err)
	}
	return nil
}

// updateWebResult writes one check outcome back to its row.
func updateWebResult(ctx context.Context, pool *pgxpool.Pool, table string, r *webResult) error {
	var code interface{}
	if r.httpCode > 0 {
		code = r.httpCode
	}
	detail := r.signature
	if r.finalURL != "" && r.finalURL != r.website {
		if detail != "" {
			detail += " | "
		}
		detail += "→ " + r.finalURL
	}
	q := fmt.Sprintf(
		`UPDATE %s SET website_status=$1, website_http_code=$2, website_detail=$3, website_checked_at=now() WHERE id=$4`,
		pgx.Identifier{table}.Sanitize())
	_, err := pool.Exec(ctx, q, r.status, code, nullIfBlank(detail), r.id)
	return err
}

func nullIfBlank(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// classifyWebsite fetches one website and fills in its status.
func classifyWebsite(ctx context.Context, client *http.Client, r *webResult) {
	rawURL := r.website
	if !strings.HasPrefix(strings.ToLower(rawURL), "http://") && !strings.HasPrefix(strings.ToLower(rawURL), "https://") {
		rawURL = "http://" + rawURL
	}

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		r.status, r.signature = "dead", "bad url: "+err.Error()
		return
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		r.status, r.signature = "dead", cleanErr(err)
		return
	}
	defer resp.Body.Close()

	r.httpCode = resp.StatusCode
	if resp.Request != nil && resp.Request.URL != nil {
		r.finalURL = resp.Request.URL.String()
	}

	// Redirected onto a known parking service → parked regardless of status.
	if host := hostOf(r.finalURL); host != "" {
		for _, ph := range parkedHosts {
			if host == ph || strings.HasSuffix(host, "."+ph) {
				r.status, r.signature = "parked", "redirect to "+ph
				return
			}
		}
	}

	if resp.StatusCode == http.StatusNotFound {
		r.status = "http_404"
		return
	}
	if resp.StatusCode >= 400 {
		r.status = "http_" + strconv.Itoa(resp.StatusCode)
		return
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	low := strings.ToLower(string(body))
	for _, sig := range parkedSignatures {
		if strings.Contains(low, sig) {
			r.status, r.signature = "parked", sig
			return
		}
	}
	r.status = "ok"
}

func hostOf(rawURL string) string {
	i := strings.Index(rawURL, "://")
	if i < 0 {
		return ""
	}
	rest := rawURL[i+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	return strings.ToLower(strings.TrimPrefix(rest, "www."))
}

// cleanErr shortens a network error to a compact reason.
func cleanErr(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "no such host"), strings.Contains(s, "ERR_NAME_NOT_RESOLVED"):
		return "dns: no such host"
	case strings.Contains(s, "timeout"), strings.Contains(s, "deadline exceeded"):
		return "timeout"
	case strings.Contains(s, "connection refused"):
		return "connection refused"
	case strings.Contains(s, "tls"), strings.Contains(s, "certificate"):
		return "tls error"
	default:
		if i := strings.LastIndex(s, ": "); i >= 0 {
			return s[i+2:]
		}
		return s
	}
}

func loadWebsiteRows(ctx context.Context, pool *pgxpool.Pool, table string, limit int) ([]webResult, error) {
	q := fmt.Sprintf(
		"SELECT id, name, COALESCE(city,''), website FROM %s WHERE website IS NOT NULL AND website <> '' ORDER BY id",
		pgx.Identifier{table}.Sanitize())
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query websites: %w", err)
	}
	defer rows.Close()
	var out []webResult
	for rows.Next() {
		var r webResult
		if err := rows.Scan(&r.id, &r.name, &r.city, &r.website); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func printWebSummary(counts map[string]int, total int, updateErrs int64, start time.Time) {
	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range counts {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })

	fmt.Fprintf(os.Stderr, "\n[CHECK] done in %s — %d websites (written to DB)\n", time.Since(start).Round(time.Second), total)
	for _, e := range sorted {
		fmt.Fprintf(os.Stderr, "  %-12s %d\n", e.k, e.v)
	}
	if updateErrs > 0 {
		fmt.Fprintf(os.Stderr, "  (%d DB update errors)\n", updateErrs)
	}
}
