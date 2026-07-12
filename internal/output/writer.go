package output

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/genjerator/krile/internal/config"
	"github.com/genjerator/krile/internal/models"
)

// exportDir is where output files land unless an explicit path is given.
const exportDir = "export"

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

// resolvePath places bare filenames in the export directory;
// explicit paths are kept as-is.
func resolvePath(dest string) string {
	if filepath.Dir(dest) == "." {
		return filepath.Join(exportDir, dest)
	}
	return dest
}

// StatsPath returns the path for the run statistics file: same as the
// output file with a .txt extension, or an auto-named .txt in the export
// directory when there is no output file (postgres format).
func StatsPath(cfg config.Config, outputPath string) string {
	if outputPath != "" {
		return strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + ".txt"
	}
	return resolvePath(autoFilename(cfg.Query, cfg.City, "txt", cfg.Distance))
}

// New returns the appropriate Writer for the given format, along with the
// resolved output file path ("" for postgres).
func New(ctx context.Context, cfg config.Config) (Writer, io.Closer, string, error) {
	format := cfg.Format
	dest := cfg.Output

	if format == "postgres" {
		dest = ""
	} else {
		if dest == "" {
			dest = autoFilename(cfg.Query, cfg.City, format, cfg.Distance)
		}
		dest = resolvePath(dest)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, nil, "", fmt.Errorf("create output directory: %w", err)
		}
		fmt.Fprintf(os.Stderr, "[INFO] writing output to %s\n", dest)
	}

	var out io.Writer
	var closer io.Closer = io.NopCloser(nil)

	if format != "postgres" && format != "xlsx" {
		f, err := os.Create(dest)
		if err != nil {
			return nil, nil, "", fmt.Errorf("open output file: %w", err)
		}
		out = f
		closer = f
	}

	switch format {
	case "csv":
		return NewCSVWriter(out), closer, dest, nil
	case "json":
		return NewJSONWriter(out), closer, dest, nil
	case "xlsx":
		return NewExcelWriter(dest), io.NopCloser(nil), dest, nil
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
			return nil, nil, "", fmt.Errorf("create postgres writer: %w", err)
		}
		return pgWriter, pgWriter, "", nil
	default:
		return nil, nil, "", fmt.Errorf("unknown format %q (want json, csv, xlsx, or postgres)", format)
	}
}
