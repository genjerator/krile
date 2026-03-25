package output

import (
	"fmt"

	"github.com/xuri/excelize/v2"
	"github.com/genjerator/krile/internal/models"
)

type ExcelWriter struct {
	dest string
	f    *excelize.File
	row  int
}

func NewExcelWriter(dest string) *ExcelWriter {
	f := excelize.NewFile()
	w := &ExcelWriter{dest: dest, f: f, row: 1}

	headers := []string{
		"Name", "Category", "Street", "Postal Code", "City",
		"Phone", "Website", "Email", "Source URL", "Scraped At",
	}
	for col, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(col+1, 1)
		f.SetCellValue("Sheet1", cell, h)
	}

	// Bold header style
	style, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
	})
	f.SetRowStyle("Sheet1", 1, 1, style)

	w.row = 2
	return w
}

func (w *ExcelWriter) Write(businesses []models.Business) error {
	for _, b := range businesses {
		values := []interface{}{
			b.Name,
			b.Category,
			b.Street,
			b.PostalCode,
			b.City,
			b.Phone,
			b.Website,
			b.Email,
			b.SourceURL,
			b.ScrapedAt.Format("2006-01-02 15:04:05"),
		}
		for col, v := range values {
			cell, _ := excelize.CoordinatesToCellName(col+1, w.row)
			w.f.SetCellValue("Sheet1", cell, v)
		}
		w.row++
	}
	return nil
}

func (w *ExcelWriter) Flush() error {
	if err := w.f.SaveAs(w.dest); err != nil {
		return fmt.Errorf("save excel: %w", err)
	}
	return nil
}
