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

	err = fetcher.FetchPages(cfg.Query, cfg.City, cfg.Limit, func(html string) error {
		businesses, err := parser.ParseDebug(html, cfg.Debug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] %s parse error: %v\n", ts(), err)
			return nil
		}

		if err := writer.Write(businesses); err != nil {
			return fmt.Errorf("write error: %w", err)
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("flush error: %w", err)
		}

		written += len(businesses)

		if cfg.Verbose || cfg.Debug {
			base := written - len(businesses)
			for i, b := range businesses {
				fmt.Fprintf(os.Stderr, "[INFO] #%d  %s | %s %s | %s | %s\n",
					base+i+1, b.Name, b.Street, b.City, b.Phone, b.Email)
			}
			fmt.Fprintf(os.Stderr, "[INFO] %s wrote %d new listings (total: %d)\n",
				ts(), len(businesses), written)
		}

		return nil
	})

	if err != nil {
		return err
	}

	if cfg.Verbose || cfg.Debug {
		fmt.Fprintf(os.Stderr, "[INFO] %s finished — %d businesses total\n", ts(), written)
	}

	return nil
}
