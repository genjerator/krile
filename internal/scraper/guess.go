package scraper

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/genjerator/krile/internal/config"
	"github.com/genjerator/krile/internal/emailgen"
	"github.com/xuri/excelize/v2"
)

// splitCSVList splits a comma-separated flag value into trimmed, non-empty
// entries.
func splitCSVList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// person is one input row: a first name and surname to permute.
type person struct {
	Vorname string
	Name    string
}

// headerAliases maps a canonical column to the header names accepted for it
// (compared case-insensitively after trimming).
var headerAliases = map[string][]string{
	"vorname": {"vorname", "firstname", "first name", "first_name", "prenom"},
	"name":    {"name", "nachname", "surname", "lastname", "last name", "last_name", "familienname"},
	"email":   {"email", "e-mail", "mail"},
}

// RunGuess is the email-permutation mode: it reads a CSV or Excel file with a
// first-name and surname column, generates ranked candidate addresses across
// emailgen.Providers, and writes a Brevo-ready CSV (one row per candidate,
// most-likely first). No email is ever sent or verified.
func RunGuess(cfg config.Config) error {
	onlyPatterns := splitCSVList(cfg.Patterns)
	if len(onlyPatterns) > 0 {
		if bad := emailgen.ValidatePatterns(onlyPatterns); len(bad) > 0 {
			return fmt.Errorf("unknown pattern(s) %v; valid patterns are: %s",
				bad, strings.Join(emailgen.PatternNames(), ", "))
		}
	}
	onlyProviders := splitCSVList(cfg.GuessProviders)
	if len(onlyProviders) > 0 {
		if bad := emailgen.ValidateProviders(onlyProviders); len(bad) > 0 {
			return fmt.Errorf("unknown provider(s) %v; valid providers are: %s",
				bad, strings.Join(emailgen.Providers, ", "))
		}
	}

	people, err := readPeople(cfg.GuessPath)
	if err != nil {
		return err
	}

	dest := cfg.Output
	if dest == "" {
		base := strings.TrimSuffix(filepath.Base(cfg.GuessPath), filepath.Ext(cfg.GuessPath))
		dest = base + "-emails.csv"
	}
	if filepath.Dir(dest) == "." {
		dest = filepath.Join("export", dest)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"EMAIL", "FIRSTNAME", "LASTNAME", "PATTERN", "PROVIDER", "RANK"}); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	rows, skipped := 0, 0
	for _, p := range people {
		cands := emailgen.Generate(p.Vorname, p.Name, onlyPatterns, onlyProviders)
		if len(cands) == 0 {
			skipped++
			continue
		}
		for _, c := range cands {
			if err := w.Write([]string{c.Email, p.Vorname, p.Name, c.Pattern, c.Provider, strconv.Itoa(c.Rank)}); err != nil {
				return fmt.Errorf("write row: %w", err)
			}
			rows++
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("flush %s: %w", dest, err)
	}

	fmt.Fprintf(os.Stderr, "Wrote %d candidate emails for %d people to %s\n", rows, len(people)-skipped, dest)
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "Skipped %d rows with a missing/empty name\n", skipped)
	}
	return nil
}

// readPeople loads the first-name/surname rows from a .csv or .xlsx file.
func readPeople(path string) ([]person, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".xlsx", ".xlsm", ".xls":
		return readPeopleExcel(path)
	case ".csv", ".txt":
		return readPeopleCSV(path)
	default:
		return nil, fmt.Errorf("%s: unsupported input type (use .csv or .xlsx)", path)
	}
}

func readPeopleExcel(path string) ([]person, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("%s: workbook has no sheets", path)
	}
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("read rows of %s: %w", path, err)
	}
	return rowsToPeople(rows)
}

func readPeopleCSV(path string) ([]person, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	// Sniff the delimiter: German exports commonly use ';'.
	br := bufio.NewReader(f)
	first, _ := br.Peek(4096)
	delim := ','
	if strings.Count(string(first), ";") > strings.Count(string(first), ",") {
		delim = ';'
	}

	r := csv.NewReader(br)
	r.Comma = delim
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return rowsToPeople(rows)
}

// rowsToPeople finds the first-name/surname columns from the header row and
// extracts the person rows.
func rowsToPeople(rows [][]string) ([]person, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("input has no rows")
	}
	header := rows[0]
	idx := make(map[string]int, len(header)) // trimmed lowercase header -> 0-based index
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}

	find := func(canonical string) int {
		for _, alias := range headerAliases[canonical] {
			if i, ok := idx[alias]; ok {
				return i
			}
		}
		return -1
	}
	vi := find("vorname")
	ni := find("name")
	ei := find("email") // -1 if the input has no email column
	if vi == -1 || ni == -1 {
		return nil, fmt.Errorf("could not find first-name and surname columns in header %v (need one of Vorname/Firstname and one of Name/Nachname/Surname)", header)
	}

	get := func(row []string, i int) string {
		if i < 0 || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	people := make([]person, 0, len(rows)-1)
	for _, row := range rows[1:] {
		v, n := get(row, vi), get(row, ni)
		if v == "" && n == "" {
			continue
		}
		// Only guess for rows that don't already have an email.
		if get(row, ei) != "" {
			continue
		}
		people = append(people, person{Vorname: v, Name: n})
	}
	return people, nil
}
