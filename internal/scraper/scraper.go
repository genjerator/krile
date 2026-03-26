package scraper

import (
	"context"
	"fmt"
	"os"

	"github.com/genjerator/krile/internal/config"
	"github.com/genjerator/krile/internal/output"
	"github.com/genjerator/krile/internal/parser"
)

func Run(ctx context.Context, cfg config.Config) error {
	writer, closer, err := output.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer closer.Close()
	defer writer.Flush()

	fetcher, err := NewFetcher(ctx, cfg.Verbose, cfg.Debug)
	if err != nil {
		return fmt.Errorf("fetcher init: %w", err)
	}
	defer fetcher.Close()

	written := 0
	totalSkipped := 0
	totalFetched := 0
	totalWithEmail := 0

	err = fetcher.FetchPages(cfg.Query, cfg.City, cfg.Limit, func(html string) error {
		businesses, err := parser.ParseDebug(html, cfg.Debug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] %s parse error: %v\n", ts(), err)
			return nil
		}

		totalFetched += len(businesses)

		// Try to fetch emails from detail pages for businesses without emails
		for i := range businesses {
			if businesses[i].Email == "" && businesses[i].SourceURL != "" {
				fmt.Fprintf(os.Stderr, "[INFO] %s: No email found in listing, trying to fetch from source URL...\n",
					businesses[i].Name)

				if cfg.Debug {
					fmt.Fprintf(os.Stderr, "[DEBUG] fetching detail page for: %s (%s)\n",
						businesses[i].Name, businesses[i].SourceURL)
				}

				detailHTML, err := fetcher.FetchDetailPage(businesses[i].SourceURL)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[WARN] %s: Failed to fetch detail page: %v\n",
						businesses[i].Name, err)
					continue
				}

				email := parser.ExtractEmailFromDetailPage(detailHTML)
				if email != "" {
					businesses[i].Email = email
					fmt.Fprintf(os.Stderr, "[INFO] %s: Email found on detail page: %s\n",
						businesses[i].Name, email)
				} else {
					fmt.Fprintf(os.Stderr, "[INFO] %s: No email found on detail page either\n",
						businesses[i].Name)
				}
			}
		}

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
	if cfg.Format == "postgres" {
		tableName := cfg.DBTable
		if tableName == "" {
			tableName = "companies"
		}
		emailPercentage := 0.0
		if totalFetched > 0 {
			emailPercentage = float64(totalWithEmail) / float64(totalFetched) * 100
		}
		operation := "Inserted"
		if cfg.UpdateExisting {
			operation = "Inserted/Updated"
		}
		fmt.Fprintf(os.Stderr, "\n[SUMMARY] Fetched: %d records | %s: %d | Skipped: %d duplicates\n", totalFetched, operation, written, totalSkipped)
		fmt.Fprintf(os.Stderr, "[SUMMARY] Email coverage: %d/%d records (%.1f%%) in %s table\n", totalWithEmail, totalFetched, emailPercentage, tableName)
	} else if cfg.Verbose || cfg.Debug {
		fmt.Fprintf(os.Stderr, "[INFO] %s finished — %d businesses total\n", ts(), written)
	}

	return nil
}
