package scraper

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/genjerator/krile/internal/config"
	"github.com/genjerator/krile/internal/models"
	"github.com/genjerator/krile/internal/output"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dbCompany is a companies-table row being rechecked.
type dbCompany struct {
	id  int64
	biz models.Business
}

// RunRecheck re-runs email discovery for every companies-table row that has
// no email, WITHOUT scraping gelbeseiten: it only re-tries the company's own
// website and the DuckDuckGo web-search fallback (which now prefers the
// company's own domain over aggregators). Newly found emails are written back
// to the row. Progress is logged to stderr and to export/recheck-emails.log.
func RunRecheck(ctx context.Context, cfg config.Config) error {
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

	companies, err := loadEmptyEmailRows(ctx, pool, table, cfg.Limit)
	if err != nil {
		return err
	}

	// Log file next to the other exports.
	if err := os.MkdirAll("export", 0o755); err != nil {
		return fmt.Errorf("create export dir: %w", err)
	}
	logPath := filepath.Join("export", "recheck-emails.log")
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

	fmt.Fprintf(os.Stderr, "[RECHECK] %s%d%s companies with empty email in %q — re-checking website + web search (no gelbeseiten)\n",
		colorCyan, len(companies), colorReset, table)
	logf("=== recheck start: %d companies with empty email in %q ===", len(companies), table)

	if len(companies) == 0 {
		fmt.Fprintln(os.Stderr, "[RECHECK] nothing to do")
		return nil
	}

	fetcher, err := NewFetcher(ctx, cfg.Verbose, cfg.Debug, cfg.Delay, 0)
	if err != nil {
		return fmt.Errorf("create fetcher: %w", err)
	}
	defer fetcher.Close()

	var ws *WebSearcher
	if cfg.WebSearch {
		ws = NewWebSearcher(ctx, cfg.Delay, cfg.Debug)
		defer ws.Close()
	} else {
		fmt.Fprintln(os.Stderr, "[RECHECK] warning: --websearch=false, only the company website is retried")
	}

	var uniqueMu sync.Mutex
	uniqueEmails := make(map[string]struct{})
	recordEmail := func(email string) int {
		uniqueMu.Lock()
		defer uniqueMu.Unlock()
		uniqueEmails[email] = struct{}{}
		return len(uniqueEmails)
	}

	workers := cfg.Workers
	if workers <= 0 {
		workers = 8
	}

	var found, updated, failed int64
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
				c := &companies[idx]
				recheckOne(fetcher, ws, &c.biz, recordEmail)
				if c.biz.Email == "" {
					logf("row id=%d %q (%s): still no email", c.id, c.biz.Name, c.biz.City)
					continue
				}
				atomic.AddInt64(&found, 1)
				if err := updateEmail(ctx, pool, table, c.id, c.biz.Email); err != nil {
					atomic.AddInt64(&failed, 1)
					fmt.Fprintf(os.Stderr, "[RECHECK] %s: found %s but DB update failed: %v\n", c.biz.Name, c.biz.Email, err)
					logf("row id=%d %q: found %s but UPDATE failed: %v", c.id, c.biz.Name, c.biz.Email, err)
					continue
				}
				atomic.AddInt64(&updated, 1)
				fmt.Fprintf(os.Stderr, "[RECHECK] %sNEW EMAIL%s %s (%s): %s%s%s → row id=%d updated\n",
					colorRed, colorReset, c.biz.Name, c.biz.City, colorBlue, c.biz.Email, colorReset, c.id)
				logf("row id=%d %q (%s): NEW EMAIL %s [source=%s]", c.id, c.biz.Name, c.biz.City, c.biz.Email, c.biz.Website)
			}
		}()
	}
	for i := range companies {
		select {
		case <-ctx.Done():
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()

	elapsed := time.Since(start)
	fmt.Fprintf(os.Stderr, "\n[RECHECK] done in %s\n", elapsed.Round(time.Second))
	fmt.Fprintf(os.Stderr, "  Rechecked:      %d\n", len(companies))
	fmt.Fprintf(os.Stderr, "  New emails:     %s%d%s\n", colorRed, found, colorReset)
	fmt.Fprintf(os.Stderr, "  Rows updated:   %d\n", updated)
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "  Update errors:  %d\n", failed)
	}
	fmt.Fprintf(os.Stderr, "  Still empty:    %d\n", int64(len(companies))-found)
	fmt.Fprintf(os.Stderr, "  Log:            %s\n", logPath)
	logf("=== recheck done in %s: %d rechecked, %d new emails, %d updated, %d still empty ===",
		elapsed.Round(time.Second), len(companies), found, updated, int64(len(companies))-found)
	return nil
}

// recheckOne finds an email for one company WITHOUT gelbeseiten: it retries
// the company's own website, then the web-search fallback.
func recheckOne(fetcher *Fetcher, ws *WebSearcher, b *models.Business, recordEmail func(string) int) {
	if b.Website != "" {
		if email, err := fetcher.FindEmailOnWebsite(b.Website); err == nil && email != "" {
			b.Email = email
			fmt.Fprintf(os.Stderr, "[RECHECK] %s: email on website %s: %s%s%s %s(#%d)%s\n",
				b.Name, b.Website, colorBlue, email, colorReset, colorRed, recordEmail(email), colorReset)
			return
		}
	}
	webSearchEmail(fetcher, ws, b, recordEmail)
}

// loadEmptyEmailRows reads companies with no email. limit > 0 caps the count.
func loadEmptyEmailRows(ctx context.Context, pool *pgxpool.Pool, table string, limit int) ([]dbCompany, error) {
	q := fmt.Sprintf(
		"SELECT id, name, COALESCE(city,''), COALESCE(phone,''), COALESCE(website,''), COALESCE(postal_code,''), COALESCE(source_url,'') "+
			"FROM %s WHERE email IS NULL OR email = '' ORDER BY id",
		pgx.Identifier{table}.Sanitize())
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query empty-email rows: %w", err)
	}
	defer rows.Close()

	var out []dbCompany
	for rows.Next() {
		var c dbCompany
		if err := rows.Scan(&c.id, &c.biz.Name, &c.biz.City, &c.biz.Phone, &c.biz.Website, &c.biz.PostalCode, &c.biz.SourceURL); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// updateEmail writes a found email back to its row.
func updateEmail(ctx context.Context, pool *pgxpool.Pool, table string, id int64, email string) error {
	q := fmt.Sprintf("UPDATE %s SET email = $1 WHERE id = $2", pgx.Identifier{table}.Sanitize())
	_, err := pool.Exec(ctx, q, email, id)
	return err
}
