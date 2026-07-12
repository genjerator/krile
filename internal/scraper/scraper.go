package scraper

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/genjerator/krile/internal/config"
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

	written := 0
	totalSkipped := 0
	totalFetched := 0
	totalWithEmail := 0
	uniqueEmails := make(map[string]struct{})

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
					uniqueEmails[email] = struct{}{}
					fmt.Fprintf(os.Stderr, "[INFO] %s: Email found on detail page: %s%s%s %s(#%d)%s\n",
						businesses[i].Name,
						colorBlue, email, colorReset,
						colorRed, len(uniqueEmails), colorReset)
				} else if businesses[i].Website != "" {
					fmt.Fprintf(os.Stderr, "[INFO] %s: Trying website contact page: %s\n",
						businesses[i].Name, businesses[i].Website)
					wsEmail, err := fetcher.FindEmailOnWebsite(businesses[i].Website)
					if err != nil {
						fmt.Fprintf(os.Stderr, "[WARN] %s: website fetch failed: %v\n",
							businesses[i].Name, err)
					} else if wsEmail != "" {
						businesses[i].Email = wsEmail
						uniqueEmails[wsEmail] = struct{}{}
						fmt.Fprintf(os.Stderr, "[INFO] %s: Email found on website: %s%s%s %s(#%d)%s\n",
							businesses[i].Name,
							colorBlue, wsEmail, colorReset,
							colorRed, len(uniqueEmails), colorReset)
					} else {
						fmt.Fprintf(os.Stderr, "[INFO] %s: No email found on website either\n",
							businesses[i].Name)
					}
				} else {
					fmt.Fprintf(os.Stderr, "[INFO] %s: No email found, no website available\n",
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
