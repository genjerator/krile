package lookup

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/xuri/excelize/v2"
)

// Result columns appended to the sheet. GS_Status doubles as the resume
// marker: rows with a non-empty status are skipped on a re-run.
const (
	headerStatus = "GS_Status"
	headerSource = "GS_Source"
)

// saveEvery is how many written results trigger an intermediate save of the
// output workbook, so an interrupted run loses little work.
const saveEvery = 25

// Workbook wraps the output copy of the address export. All methods are
// safe for concurrent use.
type Workbook struct {
	mu        sync.Mutex
	f         *excelize.File
	sheet     string
	dest      string
	emailCol  int // 1-based column indexes
	statusCol int
	sourceCol int
	dirty     int
}

// Open loads the address export and prepares the output workbook. When dest
// already exists (an earlier run), it is opened instead of inputPath so
// processed rows keep their results — the returned Resumed flag reports
// this. The returned companies include already-processed rows; callers skip
// those via Company.Status.
func Open(inputPath, dest string) (wb *Workbook, companies []Company, resumed bool, err error) {
	path := inputPath
	if fi, statErr := os.Stat(dest); statErr == nil && fi.Size() > 0 {
		path = dest
		resumed = true
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, nil, false, fmt.Errorf("open %s: %w", path, err)
	}
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		f.Close()
		return nil, nil, false, fmt.Errorf("%s: workbook has no sheets", path)
	}
	sheet := sheets[0]

	rows, err := f.GetRows(sheet)
	if err != nil || len(rows) == 0 {
		f.Close()
		return nil, nil, false, fmt.Errorf("read rows of %s: %w", path, err)
	}

	header := rows[0]
	col := make(map[string]int, len(header)) // header name → 1-based index
	for i, h := range header {
		col[strings.TrimSpace(h)] = i + 1
	}
	for _, required := range []string{"Firma", "EMail"} {
		if col[required] == 0 {
			f.Close()
			return nil, nil, false, fmt.Errorf("%s: required column %q not found in header row", path, required)
		}
	}

	wb = &Workbook{
		f:         f,
		sheet:     sheet,
		dest:      dest,
		emailCol:  col["EMail"],
		statusCol: col[headerStatus],
		sourceCol: col[headerSource],
	}
	if wb.statusCol == 0 {
		wb.statusCol = len(header) + 1
		wb.sourceCol = len(header) + 2
		for i, h := range []string{headerStatus, headerSource} {
			cell, _ := excelize.CoordinatesToCellName(wb.statusCol+i, 1)
			if err := f.SetCellValue(sheet, cell, h); err != nil {
				f.Close()
				return nil, nil, false, fmt.Errorf("write header %s: %w", h, err)
			}
		}
	} else if wb.sourceCol == 0 {
		wb.sourceCol = wb.statusCol + 1
	}

	get := func(row []string, name string) string {
		i := col[name]
		if i == 0 || i > len(row) {
			return ""
		}
		return strings.TrimSpace(row[i-1])
	}

	companies = make([]Company, 0, len(rows)-1)
	for r := 1; r < len(rows); r++ {
		row := rows[r]
		companies = append(companies, Company{
			Row:          r + 1,
			Kundennummer: get(row, "Kundennummer"),
			Firma:        get(row, "Firma"),
			Strasse:      get(row, "Strasse"),
			Hausnummer:   get(row, "Hausnummer"),
			PLZ:          get(row, "PLZ"),
			Ort:          get(row, "Ort"),
			Land:         get(row, "Land"),
			Telefon:      get(row, "Telefon"),
			Email:        get(row, "EMail"),
			Homepage:     get(row, "Homepage"),
			Status:       get(row, headerStatus),
		})
	}

	return wb, companies, resumed, nil
}

// SetResult writes the outcome for one row: the email into the EMail column
// (only when one was found) plus status and source. Every saveEvery results
// the workbook is saved to dest.
func (w *Workbook) SetResult(c *Company, r Result) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	set := func(colIdx int, v interface{}) error {
		cell, err := excelize.CoordinatesToCellName(colIdx, c.Row)
		if err != nil {
			return err
		}
		return w.f.SetCellValue(w.sheet, cell, v)
	}

	if r.Email != "" {
		if err := set(w.emailCol, r.Email); err != nil {
			return err
		}
	}
	if err := set(w.statusCol, r.Status); err != nil {
		return err
	}
	source := r.Source
	if source != "" {
		source += " "
	}
	if err := set(w.sourceCol, fmt.Sprintf("%s(score %d, %s)", source, r.Score, time.Now().Format("2006-01-02 15:04"))); err != nil {
		return err
	}

	w.dirty++
	if w.dirty >= saveEvery {
		return w.saveLocked()
	}
	return nil
}

// Save writes the workbook to the destination path.
func (w *Workbook) Save() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saveLocked()
}

func (w *Workbook) saveLocked() error {
	w.dirty = 0
	// Write to a temp file and rename, so a crash mid-save can never leave
	// a truncated workbook behind — the previous version stays intact.
	tmp := w.dest + ".tmp"
	if err := w.f.SaveAs(tmp); err != nil {
		return fmt.Errorf("save %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, w.dest); err != nil {
		return fmt.Errorf("replace %s: %w", w.dest, err)
	}
	return nil
}

// Close saves pending changes and releases the workbook.
func (w *Workbook) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	saveErr := w.saveLocked()
	if err := w.f.Close(); err != nil && saveErr == nil {
		saveErr = err
	}
	return saveErr
}
