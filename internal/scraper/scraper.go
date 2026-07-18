package scraper

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/genjerator/krile/internal/config"
	"github.com/genjerator/krile/internal/models"
	"github.com/genjerator/krile/internal/output"
	"github.com/genjerator/krile/internal/parser"
)

func Run(ctx context.Context, cfg config.Config) error {
	start := time.Now()

	writer, closer, outPath, err := output.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer closer.Close()
	defer writer.Flush()

	fetcher, err := NewFetcher(ctx, cfg.Verbose, cfg.Debug, cfg.Delay, cfg.Distance)
	if err != nil {
		return fmt.Errorf("fetcher init: %w", err)
	}
	defer fetcher.Close()

	var ws *WebSearcher
	if cfg.WebSearch {
		ws = NewWebSearcher(ctx, cfg.Delay, cfg.Debug)
		defer ws.Close()
	}

	written := 0
	totalSkipped := 0
	totalFetched := 0
	totalWithEmail := 0
	uniqueEmails := make(map[string]struct{})
	var emailMu sync.Mutex

	// recordEmail registers a found email and returns the unique-email
	// count; safe to call from enrichment workers.
	recordEmail := func(email string) int {
		emailMu.Lock()
		defer emailMu.Unlock()
		uniqueEmails[email] = struct{}{}
		return len(uniqueEmails)
	}

	err = fetcher.FetchPages(cfg.Query, cfg.City, cfg.Limit, func(html string) error {
		businesses, err := parser.ParseDebug(html, cfg.Debug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] %s parse error: %v\n", ts(), err)
			return nil
		}

		totalFetched += len(businesses)

		// Fetch emails from detail pages / websites concurrently for
		// businesses without emails.
		enrichEmails(ctx, fetcher, ws, businesses, cfg.Workers, cfg.Debug, recordEmail)

		// Count businesses with emails
		for _, b := range businesses {
			if b.Email != "" {
				totalWithEmail++
			}
		}

		inserted, err := writer.Write(businesses)
		if err != nil {
			return fmt.Errorf("write error: %w", err)
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("flush error: %w", err)
		}

		written += inserted
		skipped := len(businesses) - inserted
		totalSkipped += skipped

		// Display operation info for postgres format
		if cfg.Format == "postgres" {
			tableName := cfg.DBTable
			if tableName == "" {
				tableName = "companies"
			}
			operation := "inserted"
			if cfg.UpdateExisting {
				operation = "inserted/updated"
			}
			if skipped > 0 {
				fmt.Fprintf(os.Stderr, "[INFO] %d records %s in %s table (%d duplicates skipped)\n", inserted, operation, tableName, skipped)
			} else {
				fmt.Fprintf(os.Stderr, "[INFO] %d records %s in %s table\n", inserted, operation, tableName)
			}
		}

		if cfg.Verbose || cfg.Debug {
			base := written - inserted
			for i, b := range businesses {
				if i < inserted {
					fmt.Fprintf(os.Stderr, "[INFO] #%d  %s | %s %s | %s | %s\n",
						base+i+1, b.Name, b.Street, b.City, b.Phone, b.Email)
				}
			}
			if cfg.Format != "postgres" {
				fmt.Fprintf(os.Stderr, "[INFO] %s wrote %d new listings (total: %d)\n",
					ts(), inserted, written)
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	// Display final summary
	end := time.Now()
	elapsed := end.Sub(start)
	withoutEmail := totalFetched - totalWithEmail
	emailPct := 0.0
	if totalFetched > 0 {
		emailPct = float64(totalWithEmail) / float64(totalFetched) * 100
	}
	recordsPerMin := 0.0
	emailsPerMin := 0.0
	if minutes := elapsed.Minutes(); minutes > 0 {
		recordsPerMin = float64(totalFetched) / minutes
		emailsPerMin = float64(totalWithEmail) / minutes
	}

	fmt.Fprintf(os.Stderr, "\n─────────────────────────────────────────\n")
	fmt.Fprintf(os.Stderr, "  Duration:           %s\n", elapsed.Round(time.Second))
	fmt.Fprintf(os.Stderr, "  Total collected:    %d addresses\n", totalFetched)
	fmt.Fprintf(os.Stderr, "  Records/minute:     %.1f\n", recordsPerMin)
	fmt.Fprintf(os.Stderr, "  With email:         %s%d%s\n", colorBlue, totalWithEmail, colorReset)
	fmt.Fprintf(os.Stderr, "  Unique emails:      %s%d%s\n", colorBlue, len(uniqueEmails), colorReset)
	fmt.Fprintf(os.Stderr, "  Emails/minute:      %.1f\n", emailsPerMin)
	fmt.Fprintf(os.Stderr, "  Without email:      %d\n", withoutEmail)
	fmt.Fprintf(os.Stderr, "  Email coverage:     %s%.1f%%%s\n", colorRed, emailPct, colorReset)
	if cfg.Format == "postgres" {
		tableName := cfg.DBTable
		if tableName == "" {
			tableName = "companies"
		}
		operation := "Inserted"
		if cfg.UpdateExisting {
			operation = "Inserted/Updated"
		}
		fmt.Fprintf(os.Stderr, "  %s:      %d (skipped %d duplicates) → %s\n", operation, written, totalSkipped, tableName)
	}
	fmt.Fprintf(os.Stderr, "─────────────────────────────────────────\n")

	statsPath := output.StatsPath(cfg, outPath)
	if err := writeStatsFile(statsPath, cfg, outPath, start, end,
		totalFetched, totalWithEmail, len(uniqueEmails), written, totalSkipped); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] could not write stats file: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[INFO] stats written to %s\n", statsPath)
	}

	return nil
}

// enrichEmails fills in missing emails by fetching detail pages (and
// business websites as fallback) with a pool of concurrent workers. Each
// worker owns distinct slice elements, so no locking is needed on
// businesses; recordEmail must be goroutine-safe.
func enrichEmails(ctx context.Context, fetcher *Fetcher, ws *WebSearcher, businesses []models.Business,
	workers int, debug bool, recordEmail func(string) int) {

	if workers <= 0 {
		workers = 8
	}

	jobs := make(chan int, len(businesses))
	for i := range businesses {
		if businesses[i].Email == "" && businesses[i].SourceURL != "" {
			jobs <- i
		}
	}
	close(jobs)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				enrichOne(fetcher, ws, &businesses[i], debug, recordEmail)
			}
		}()
	}
	wg.Wait()
}

// enrichOne tries to find an email for a single business: first on its
// gelbeseiten detail page, then on its own website, finally on a website
// found via web search.
func enrichOne(fetcher *Fetcher, ws *WebSearcher, b *models.Business, debug bool, recordEmail func(string) int) {
	fmt.Fprintf(os.Stderr, "[INFO] %s: No email found in listing, trying to fetch from source URL...\n", b.Name)

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] fetching detail page for: %s (%s)\n", b.Name, b.SourceURL)
	}

	detailHTML, err := fetcher.FetchDetailPage(b.SourceURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] %s: Failed to fetch detail page: %v\n", b.Name, err)
		return
	}

	email := parser.ExtractEmailFromDetailPage(detailHTML)
	if email != "" {
		b.Email = email
		fmt.Fprintf(os.Stderr, "[INFO] %s: Email found on detail page: %s%s%s %s(#%d)%s\n",
			b.Name, colorBlue, email, colorReset, colorRed, recordEmail(email), colorReset)
		return
	}

	if b.Website != "" {
		fmt.Fprintf(os.Stderr, "[INFO] %s: Trying website contact page: %s\n", b.Name, b.Website)
		wsEmail, err := fetcher.FindEmailOnWebsite(b.Website)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] %s: website fetch failed: %v\n", b.Name, err)
		} else if wsEmail != "" {
			b.Email = wsEmail
			fmt.Fprintf(os.Stderr, "[INFO] %s: Email found on website: %s%s%s %s(#%d)%s\n",
				b.Name, colorBlue, wsEmail, colorReset, colorRed, recordEmail(wsEmail), colorReset)
			return
		}
	}

	webSearchEmail(fetcher, ws, b, recordEmail)
}

// webSearchEmail is the last resort: find the company's website via web
// search and run the email extraction on it.
func webSearchEmail(fetcher *Fetcher, ws *WebSearcher, b *models.Business, recordEmail func(string) int) {
	if ws == nil {
		fmt.Fprintf(os.Stderr, "[INFO] %s: No email found\n", b.Name)
		return
	}

	query := strings.TrimSpace(b.Name + " " + b.City)
	site, err := ws.FindWebsite(query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] %s: web search failed: %v\n", b.Name, err)
		return
	}
	if site == "" || strings.EqualFold(site, b.Website) {
		fmt.Fprintf(os.Stderr, "[INFO] %s: No email found, web search has no new website\n", b.Name)
		return
	}

	fmt.Fprintf(os.Stderr, "[INFO] %s: Web search found %s, checking for email\n", b.Name, site)
	email, pages, err := fetcher.FindEmailAndPages(site)
	if err != nil || email == "" {
		fmt.Fprintf(os.Stderr, "[INFO] %s: No email on web-search site either\n", b.Name)
		return
	}
	if ok, reason := acceptWebEmail(email, site, b.Name, b.Phone, b.PostalCode, pages); !ok {
		fmt.Fprintf(os.Stderr, "[INFO] %s: Rejected %s from %s — %s\n", b.Name, email, site, reason)
		return
	}

	b.Email = email
	if b.Website == "" {
		b.Website = site
	}
	fmt.Fprintf(os.Stderr, "[INFO] %s: Email found via web search: %s%s%s %s(#%d)%s\n",
		b.Name, colorBlue, email, colorReset, colorRed, recordEmail(email), colorReset)
}

// writeStatsFile writes a plain-text run report next to the output file.
func writeStatsFile(path string, cfg config.Config, outPath string, start, end time.Time,
	totalFetched, totalWithEmail, uniqueEmails, written, skipped int) error {

	elapsed := end.Sub(start)
	recordsPerMin := 0.0
	emailsPerMin := 0.0
	if minutes := elapsed.Minutes(); minutes > 0 {
		recordsPerMin = float64(totalFetched) / minutes
		emailsPerMin = float64(totalWithEmail) / minutes
	}
	emailPct := 0.0
	if totalFetched > 0 {
		emailPct = float64(totalWithEmail) / float64(totalFetched) * 100
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Krile scrape statistics\n")
	fmt.Fprintf(&b, "=======================\n\n")
	fmt.Fprintf(&b, "Query:              %s\n", cfg.Query)
	fmt.Fprintf(&b, "City:               %s\n", cfg.City)
	if cfg.Distance > 0 {
		fmt.Fprintf(&b, "Radius:             %d km\n", cfg.Distance/1000)
	}
	fmt.Fprintf(&b, "Format:             %s\n", cfg.Format)
	if outPath != "" {
		fmt.Fprintf(&b, "Output:             %s\n", outPath)
	}
	if cfg.Format == "postgres" {
		tableName := cfg.DBTable
		if tableName == "" {
			tableName = "companies"
		}
		fmt.Fprintf(&b, "Table:              %s\n", tableName)
	}
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "Started:            %s\n", start.Format(time.RFC3339))
	fmt.Fprintf(&b, "Finished:           %s\n", end.Format(time.RFC3339))
	fmt.Fprintf(&b, "Duration:           %s\n", elapsed.Round(time.Second))
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "Total collected:    %d addresses\n", totalFetched)
	fmt.Fprintf(&b, "Records/minute:     %.1f\n", recordsPerMin)
	fmt.Fprintf(&b, "With email:         %d\n", totalWithEmail)
	fmt.Fprintf(&b, "Unique emails:      %d\n", uniqueEmails)
	fmt.Fprintf(&b, "Emails/minute:      %.1f\n", emailsPerMin)
	fmt.Fprintf(&b, "Without email:      %d\n", totalFetched-totalWithEmail)
	fmt.Fprintf(&b, "Email coverage:     %.1f%%\n", emailPct)
	if cfg.Format == "postgres" {
		fmt.Fprintf(&b, "Written to DB:      %d (skipped %d duplicates)\n", written, skipped)
	}

	return os.WriteFile(path, []byte(b.String()), 0o644)
}
