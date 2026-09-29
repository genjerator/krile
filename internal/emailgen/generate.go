// Package emailgen generates candidate personal email addresses from a
// first name (Vorname) and surname (Name) by permuting common local-part
// patterns across a fixed list of German freemail providers. It performs no
// verification — every candidate is a guess, ordered most-likely first so a
// downstream validator (e.g. Brevo) can prioritise.
package emailgen

import "strings"

// Providers is the list of freemail domains to permute, ordered by rough
// popularity in Germany. gmx.net/.com and online.de are far less common but
// requested explicitly.
var Providers = []string{
	"gmx.de",
	"web.de",
	"t-online.de",
	"gmx.net",
	"gmx.com",
	"online.de",
}

// pattern builds a local-part from the slugged first name and surname plus
// their initials. Patterns are listed most-likely first; that order becomes
// the candidate rank.
type pattern struct {
	name  string // human-readable label, e.g. "vorname.name"
	build func(v, n, vi, ni string) string
}

var patterns = []pattern{
	{"vorname.name", func(v, n, vi, ni string) string { return v + "." + n }},
	{"name.vorname", func(v, n, vi, ni string) string { return n + "." + v }},
	{"vornamename", func(v, n, vi, ni string) string { return v + n }},
	{"namevorname", func(v, n, vi, ni string) string { return n + v }},
	{"vorname-name", func(v, n, vi, ni string) string { return v + "-" + n }},
	{"name-vorname", func(v, n, vi, ni string) string { return n + "-" + v }},
	{"v.name", func(v, n, vi, ni string) string { return vi + "." + n }},
	{"n.vorname", func(v, n, vi, ni string) string { return ni + "." + v }},
	{"name.v", func(v, n, vi, ni string) string { return n + "." + vi }},
}

// Candidate is one generated email address with the pattern and provider it
// came from and its overall rank (1 = most likely).
type Candidate struct {
	Email    string
	Pattern  string
	Provider string
	Rank     int
}

// PatternNames returns the available pattern labels in likelihood order.
func PatternNames() []string {
	names := make([]string, len(patterns))
	for i, p := range patterns {
		names[i] = p.name
	}
	return names
}

// ValidatePatterns reports any names not matching a known pattern label
// (case-insensitive). Returns nil when all names are valid.
func ValidatePatterns(names []string) []string {
	var bad []string
	for _, name := range names {
		if patternIndex(name) < 0 {
			bad = append(bad, name)
		}
	}
	return bad
}

func patternIndex(name string) int {
	name = strings.ToLower(strings.TrimSpace(name))
	for i, p := range patterns {
		if p.name == name {
			return i
		}
	}
	return -1
}

// ValidateProviders reports any names not in the Providers list
// (case-insensitive). Returns nil when all names are valid.
func ValidateProviders(names []string) []string {
	var bad []string
	for _, name := range names {
		n := strings.ToLower(strings.TrimSpace(name))
		found := false
		for _, p := range Providers {
			if p == n {
				found = true
				break
			}
		}
		if !found {
			bad = append(bad, name)
		}
	}
	return bad
}

// Generate returns candidate addresses for a person, ordered by pattern
// likelihood first and provider popularity second. When onlyPatterns or
// onlyProviders is non-empty, generation is restricted to those labels/domains
// (unknown entries are ignored — call ValidatePatterns/ValidateProviders first
// to reject them). Duplicate addresses are emitted once. An empty slice is
// returned if either name slugs to nothing.
func Generate(vorname, name string, onlyPatterns, onlyProviders []string) []Candidate {
	v := Slug(vorname)
	n := Slug(name)
	if v == "" || n == "" {
		return nil
	}
	vi := v[:1]
	ni := n[:1]

	allowPat := make(map[string]bool, len(onlyPatterns))
	for _, o := range onlyPatterns {
		allowPat[strings.ToLower(strings.TrimSpace(o))] = true
	}
	allowProv := make(map[string]bool, len(onlyProviders))
	for _, o := range onlyProviders {
		allowProv[strings.ToLower(strings.TrimSpace(o))] = true
	}

	var out []Candidate
	seen := make(map[string]struct{})
	rank := 0
	for _, p := range patterns {
		if len(allowPat) > 0 && !allowPat[p.name] {
			continue
		}
		local := p.build(v, n, vi, ni)
		if local == "" {
			continue
		}
		for _, prov := range Providers {
			if len(allowProv) > 0 && !allowProv[prov] {
				continue
			}
			email := local + "@" + prov
			if _, dup := seen[email]; dup {
				continue
			}
			seen[email] = struct{}{}
			rank++
			out = append(out, Candidate{
				Email:    email,
				Pattern:  p.name,
				Provider: prov,
				Rank:     rank,
			})
		}
	}
	return out
}

// umlauts maps German special characters to their email-safe transliteration.
var umlauts = strings.NewReplacer(
	"ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss",
	"Ä", "ae", "Ö", "oe", "Ü", "ue", "ẞ", "ss",
)

// accents folds common Latin accented letters to ASCII so names like "José"
// or "Renée" produce clean local-parts.
var accents = map[rune]rune{
	'á': 'a', 'à': 'a', 'â': 'a', 'ã': 'a', 'å': 'a', 'ā': 'a',
	'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e', 'ē': 'e',
	'í': 'i', 'ì': 'i', 'î': 'i', 'ï': 'i', 'ī': 'i',
	'ó': 'o', 'ò': 'o', 'ô': 'o', 'õ': 'o', 'ø': 'o', 'ō': 'o',
	'ú': 'u', 'ù': 'u', 'û': 'u', 'ū': 'u',
	'ç': 'c', 'ñ': 'n', 'ý': 'y', 'ÿ': 'y',
}

// Slug lowercases a name, transliterates German umlauts/ß, folds Latin
// accents, and drops everything that is not a-z or 0-9 (spaces, dots,
// hyphens in compound names). "Müller-Schmidt" -> "muellerschmidt".
func Slug(s string) string {
	s = umlauts.Replace(strings.TrimSpace(s))
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if folded, ok := accents[r]; ok {
				b.WriteRune(folded)
			}
		}
	}
	return b.String()
}
