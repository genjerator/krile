package output

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/genjerator/krile/internal/config"
	"github.com/genjerator/krile/internal/models"
)

// Writer is implemented by JSON and CSV writers.
type Writer interface {
	Write(businesses []models.Business) (int, error)
	Flush() error
}

// New returns the appropriate Writer for the given format,
// writing to dest (file path) or stdout if dest is empty.
func New(ctx context.Context, cfg config.Config) (Writer, io.Closer, error) {
	format := cfg.Format
	dest := cfg.Output

	var out io.Writer
	var closer io.Closer = io.NopCloser(nil)

	if dest == "" && format != "postgres" {
		out = os.Stdout
	} else if format != "postgres" && format != "xlsx" {
		f, err := os.Create(dest)
		if err != nil {
			return nil, nil, fmt.Errorf("open output file: %w", err)
		}
		out = f
		closer = f
	}

	switch format {
	case "csv":
		return NewCSVWriter(out), closer, nil
	case "json":
		return NewJSONWriter(out), closer, nil
	case "xlsx":
		if dest == "" {
			return nil, nil, fmt.Errorf("xlsx format requires an output file path (-o results.xlsx)")
		}
		return NewExcelWriter(dest), io.NopCloser(nil), nil
	case "postgres":
		// Build connection string
		connString := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
			cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName)

		tableName := cfg.DBTable
		if tableName == "" {
			tableName = "companies"
		}

		pgWriter, err := NewPostgresWriter(ctx, connString, tableName, cfg.UpdateExisting, cfg.Debug)
		if err != nil {
			return nil, nil, fmt.Errorf("create postgres writer: %w", err)
		}
		return pgWriter, pgWriter, nil
	default:
		return nil, nil, fmt.Errorf("unknown format %q (want json, csv, xlsx, or postgres)", format)
	}
}
