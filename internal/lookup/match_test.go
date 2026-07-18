package lookup

import (
	"testing"

	"github.com/genjerator/krile/internal/models"
)

func TestPhoneMatch(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"0231 / 599 074", "(0231) 59 90 74", true},
		{"+49 231 599074", "0231 599074", true},
		{"0049 231 599074", "0231/599074", true},
		{"08261 / 2201915-0", "08261 22019150", true},
		{"0231 599 074", "0231 599 075", false},
		{"", "0231 599074", false},
		{"12345", "12345", false}, // too short to be trusted
	}
	for _, tt := range tests {
		if got := PhoneMatch(tt.a, tt.b); got != tt.want {
			t.Errorf("PhoneMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestCleanFirma(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Malz GmbH & Co. KG", "Malz"},
		{"Roland Haibl, IT-Consultant", "Roland Haibl"},
		{"Zur Sonne", "Zur Sonne"},
		{"Müller Bau GmbH", "Müller Bau"},
		{"Schmidt AG", "Schmidt"},
		{"GmbH", "GmbH"}, // never return empty
	}
	for _, tt := range tests {
		if got := CleanFirma(tt.in); got != tt.want {
			t.Errorf("CleanFirma(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestScore(t *testing.T) {
	company := Company{
		Firma:      "Malz GmbH & Co. KG",
		Strasse:    "Oberste Wilmsstr.",
		Hausnummer: "29",
		PLZ:        "44309",
		Ort:        "Dortmund",
		Telefon:    "0231 / 599 074",
	}

	exact := models.Business{
		Name:       "Malz GmbH & Co. KG",
		Street:     "Oberste Wilmsstr. 29",
		PostalCode: "44309",
		City:       "Dortmund",
		Phone:      "0231 599074",
	}
	if got := Score(company, exact); got < 90 {
		t.Errorf("exact match scored %d, want >= 90", got)
	}

	stranger := models.Business{
		Name:       "Bäckerei Schulte",
		Street:     "Hauptstr. 1",
		PostalCode: "80331",
		City:       "München",
		Phone:      "089 123456",
	}
	if got := Score(company, stranger); got >= MinAcceptScore {
		t.Errorf("unrelated business scored %d, want < %d", got, MinAcceptScore)
	}

	// Same town, same category, different company: name mismatch must keep
	// it under a phone-confirmed or address-confirmed row.
	neighbor := models.Business{
		Name:       "Bäckerei Schulte",
		Street:     "Andere Str. 5",
		PostalCode: "44309",
		City:       "Dortmund",
		Phone:      "0231 777777",
	}
	nScore := Score(company, neighbor)
	eScore := Score(company, exact)
	if nScore >= eScore {
		t.Errorf("neighbor (%d) should score below exact match (%d)", nScore, eScore)
	}
}

func TestNameSimilarity(t *testing.T) {
	if s := nameSimilarity("Malz GmbH & Co. KG", "Malz"); s != 1.0 {
		t.Errorf("legal forms should be ignored, got %v", s)
	}
	if s := nameSimilarity("Gasthof Zur Sonne", "Hotel Zur Sonne"); s < 0.6 {
		t.Errorf("partial name overlap too low: %v", s)
	}
}
