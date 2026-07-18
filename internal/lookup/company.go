// Package lookup implements the Excel lookup mode: companies from a CRM
// address export are searched on gelbeseiten.de one by one, the best result
// is verified against the row's data, and found emails are written into the
// EMail column of an output copy of the workbook.
package lookup

// Company is one row of the input address export. Only the columns needed
// for searching and verification are mapped; the rest of the row is
// preserved untouched in the output workbook.
type Company struct {
	Row          int // 1-based row number in the sheet
	Kundennummer string
	Firma        string
	Strasse      string
	Hausnummer   string
	PLZ          string
	Ort          string
	Land         string
	Telefon      string
	Email        string // existing email from the export
	Homepage     string
	Status       string // GS_Status from a previous run ("" = not processed)
}

// Statuses written to the GS_Status column. Any non-empty status marks the
// row as processed, so a resumed run skips it.
const (
	StatusOK          = "ok"          // email written, phone confirms the match
	StatusUnconfirmed = "unconfirmed" // email written, phone missing or different (see log)
	StatusNoEmail     = "no_email"    // company matched, but no email found anywhere
	StatusNoMatch     = "no_match"    // search had results, none matched this company
	StatusNoResults   = "no_results"  // gelbeseiten returned nothing
	StatusNoLocation  = "no_location" // row has neither Ort nor PLZ to search with
	StatusError       = "error"       // network/parse failure; clear GS_Status to retry
)

// Result is what a lookup produced for one company.
type Result struct {
	Email  string
	Source string // URL the email was taken from
	Via    string // which stage found the email: "gelbeseiten listing", "gelbeseiten detail page", "company website"
	Status string
	Score  int    // match score of the accepted listing
	Match  string // name of the matched gelbeseiten listing
	Phone  string // phone of the matched listing (for the log)
}
