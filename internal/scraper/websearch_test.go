package scraper

import "testing"

func TestPickResultPrefersCompanyDomain(t *testing.T) {
	// Real DuckDuckGo result order for "Hotel Mariahilf Wien": the official
	// site is present but surrounded by un-blocked aggregators/OTAs.
	hrefs := []string{
		"https://www.mariahilf-hotel.at/",
		"https://www.booking.com/hotel/at/ibis-wien-mariahilf.de.html",
		"https://ibis-wien-mariahilf.meinhotel.top/",
		"https://www.holidaycheck.de/hi/hotel-ibis-wien-mariahilf/x",
		"https://pension-mariahilf.viennabesthotels.com/de/",
	}
	if got := pickResult(hrefs, "Hotel Mariahilf"); got != "https://www.mariahilf-hotel.at/" {
		t.Fatalf("want official site, got %q", got)
	}
}

func TestPickResultSkipsAggregatorSubdomainToken(t *testing.T) {
	// The company token appears only in an aggregator's subdomain; with no
	// registrable-domain match, the first non-blocked result is used.
	hrefs := []string{
		"https://www.booking.com/x",                 // blocked
		"https://ibis-wien-mariahilf.meinhotel.top/", // token in subdomain only
		"https://www.holidaycheck.de/y",
	}
	got := pickResult(hrefs, "Hotel Mariahilf")
	if got != "https://ibis-wien-mariahilf.meinhotel.top/" {
		t.Fatalf("expected host-token match as best available, got %q", got)
	}
}

func TestPickResultFallsBackToFirstNonBlocked(t *testing.T) {
	hrefs := []string{
		"https://www.booking.com/x", // blocked
		"https://example-directory.de/listing",
		"https://another.de/",
	}
	if got := pickResult(hrefs, "Zahnarzt Dr. Müller"); got != "https://example-directory.de/listing" {
		t.Fatalf("want first non-blocked, got %q", got)
	}
}
