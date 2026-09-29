package parser

import "testing"

func TestExtractObfuscatedEmail(t *testing.T) {
	cases := map[string]string{
		"info ( at ) chin-imbiss.de":        "info@chin-imbiss.de",
		"kontakt (at) beispiel.de":          "kontakt@beispiel.de",
		"mail [AT] example.com":             "mail@example.com",
		"info (at) firma (dot) de":          "info@firma.de",
		"name (at) beispiel (punkt) de":     "name@beispiel.de",
		"plain info@example.org here":       "info@example.org",
		"no email here at all":              "",
		"call us at the office downtown":    "",
	}
	for in, want := range cases {
		if got := extractObfuscatedEmail(in); got != want {
			t.Errorf("extractObfuscatedEmail(%q) = %q, want %q", in, got, want)
		}
	}
}
