package output

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/genjerator/krile/internal/config"
	"github.com/genjerator/krile/internal/models"
)

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func autoFilename(query, city, format string, distanceM int) string {
	slug := func(s string) string {
		s = strings.ToLower(s)
		s = nonAlnum.ReplaceAllString(s, "-")
		return strings.Trim(s, "-")
	}
	name := slug(query) + "-" + slug(city)
	if distanceM > 0 {
		name += fmt.Sprintf("-%dkm", distanceM/1000)
	}
	return name + "." + format
}

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

	if dest == "" && format != "postgres" {
		dest = autoFilename(cfg.Query, cfg.City, format, cfg.Distance)
		fmt.Fprintf(os.Stderr, "[INFO] no output file specified, writing to %s\n", dest)
	}

	var out io.Writer
	var closer io.Closer = io.NopCloser(nil)

	if format != "postgres" && format != "xlsx" {
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
