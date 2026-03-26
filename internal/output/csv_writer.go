package output

import (
	"encoding/csv"
	"io"

	"github.com/genjerator/krile/internal/models"
)

// CSVWriter writes a header row followed by one row per business.
type CSVWriter struct {
	w       *csv.Writer
	wroteHdr bool
}

func NewCSVWriter(w io.Writer) *CSVWriter {
	return &CSVWriter{w: csv.NewWriter(w)}
}

var csvHeader = []string{
	"name", "category", "street", "postal_code", "city",
	"phone", "website", "email", "source_url", "scraped_at",
}

func (c *CSVWriter) Write(businesses []models.Business) (int, error) {
	if !c.wroteHdr {
		if err := c.w.Write(csvHeader); err != nil {
			return 0, err
		}
		c.wroteHdr = true
	}
	for _, b := range businesses {
		row := []string{
			b.Name,
			b.Category,
			b.Street,
			b.PostalCode,
			b.City,
			b.Phone,
			b.Website,
			b.Email,
			b.SourceURL,
			b.ScrapedAt.Format("2006-01-02T15:04:05Z"),
		}
		if err := c.w.Write(row); err != nil {
			return 0, err
		}
	}
	return len(businesses), nil
}

func (c *CSVWriter) Flush() error {
	c.w.Flush()
	return c.w.Error()
}
