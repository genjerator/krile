package output

import (
	"fmt"
	"io"
	"os"

	"github.com/genjerator/krile/internal/models"
)

// Writer is implemented by JSON and CSV writers.
type Writer interface {
	Write(businesses []models.Business) error
	Flush() error
}

// New returns the appropriate Writer for the given format,
// writing to dest (file path) or stdout if dest is empty.
func New(format, dest string) (Writer, io.Closer, error) {
	var out io.Writer
	var closer io.Closer = io.NopCloser(nil)

	if dest == "" {
		out = os.Stdout
	} else {
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
		// Excel writes directly to file, no need for the generic out/closer
		closer.Close()
		return NewExcelWriter(dest), io.NopCloser(nil), nil
	default:
		return nil, nil, fmt.Errorf("unknown format %q (want json, csv, or xlsx)", format)
	}
}
