package lookup

import (
	"regexp"
	"strings"

	"github.com/genjerator/krile/internal/models"
)

// MinAcceptScore is the minimum Score for a search result to be accepted as
// the company from the Excel row. PLZ+city alone (35) or a full name match
// plus city (30) clear it; an unrelated business in another town does not.
const MinAcceptScore = 30

// digits returns only the digit characters of s.
func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// canonPhone reduces a phone number to comparable digits: country prefix
// (0049 / +49) and trunk zero are stripped.
func canonPhone(s string) string {
	d := digits(s)
	if strings.HasPrefix(d, "0049") {
		d = d[4:]
	} else if strings.HasPrefix(d, "49") && len(d) > 9 {
		d = d[2:]
	}
	return strings.TrimPrefix(d, "0")
}

// PhoneMatch reports whether two phone numbers denote the same line. It
// compares canonical digits; a suffix match of at least 6 digits also counts
// to tolerate formatting/extension differences.
func PhoneMatch(a, b string) bool {
	ca, cb := canonPhone(a), canonPhone(b)
	if len(ca) < 6 || len(cb) < 6 {
		return false
	}
	return strings.HasSuffix(ca, cb) || strings.HasSuffix(cb, ca)
}

// normStreet lowercases, folds ß→ss and straße/strasse→str, and drops all
// non-alphanumeric characters, so "St.-Johann-Str." matches "St. Johann
// Straße".
func normStreet(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "ß", "ss")
	s = strings.ReplaceAll(s, "strasse", "str")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r > 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// nameStopwords are legal forms and filler words ignored when comparing
// company names.
var nameStopwords = map[string]struct{}{
	"gmbh": {}, "ag": {}, "kg": {}, "ug": {}, "ohg": {}, "gbr": {}, "mbh": {},
	"co": {}, "cokg": {}, "ek": {}, "ev": {}, "e": {}, "k": {}, "v": {},
	"und": {}, "u": {}, "inh": {}, "inhaber": {}, "haftungsbeschraenkt": {},
	"haftungsbeschränkt": {},
}

var nonWord = regexp.MustCompile(`[^\pL\pN]+`)

// nameTokens splits a company name into lowercase tokens with legal forms
// removed.
func nameTokens(s string) []string {
	s = strings.ToLower(strings.ReplaceAll(s, "ß", "ss"))
	parts := nonWord.Split(s, -1)
	var out []string
	seen := map[string]struct{}{}
	for _, p := range parts {
		if p == "" {
			continue
		}
		if _, stop := nameStopwords[p]; stop {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// nameSimilarity returns the fraction (0..1) of the Excel company-name
// tokens that appear in the listing name.
func nameSimilarity(firma, listing string) float64 {
	ft := nameTokens(firma)
	if len(ft) == 0 {
		return 0
	}
	lt := map[string]struct{}{}
	for _, t := range nameTokens(listing) {
		lt[t] = struct{}{}
	}
	hits := 0
	for _, t := range ft {
		if _, ok := lt[t]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(ft))
}

// Score rates how well a gelbeseiten listing matches the Excel row:
// phone +40, PLZ +25, street +20 (house number +5), city +10, name
// similarity up to +20.
func Score(c Company, b models.Business) int {
	score := 0
	if PhoneMatch(c.Telefon, b.Phone) {
		score += 40
	}
	if plz := strings.TrimSpace(c.PLZ); plz != "" && plz == strings.TrimSpace(b.PostalCode) {
		score += 25
	}
	if st := normStreet(c.Strasse); st != "" {
		bs := normStreet(b.Street)
		if strings.Contains(bs, st) {
			score += 20
			if hn := digits(c.Hausnummer); hn != "" && strings.Contains(bs, st+hn) {
				score += 5
			}
		}
	}
	if ort := strings.TrimSpace(c.Ort); ort != "" && strings.EqualFold(ort, strings.TrimSpace(b.City)) {
		score += 10
	}
	score += int(nameSimilarity(c.Firma, b.Name)*20 + 0.5)
	return score
}

// legalForms is applied by CleanFirma to build a fallback search query.
var legalForms = regexp.MustCompile(`(?i)\s*(gmbh\s*&\s*co\.?\s*kg|gmbh\s*&\s*co\.?|e\.\s*k\.|e\.\s*v\.|gmbh|mbh|ohg|gbr|kgaa|ag|kg|ug\s*\(haftungsbeschränkt\)|ug)\s*$`)

// CleanFirma strips trailing legal forms and anything after a comma, giving
// a looser query when the exact name yields no results. It may return the
// input unchanged.
func CleanFirma(firma string) string {
	s := firma
	if i := strings.Index(s, ","); i > 0 {
		s = s[:i]
	}
	for {
		next := legalForms.ReplaceAllString(s, "")
		next = strings.Trim(next, " -&.")
		if next == s {
			break
		}
		s = next
	}
	if s == "" {
		return firma
	}
	return s
}
