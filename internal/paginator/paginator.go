package paginator

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// NextURL returns the URL of the next page, or an empty string if there is none.
func NextURL(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ""
	}

	// Try rel="next" link first
	if href, exists := doc.Find("a[rel='next']").First().Attr("href"); exists && href != "" {
		return resolve(href)
	}

	// Fallback: pagination link with aria-label="Nächste Seite" or similar
	var next string
	doc.Find("a").Each(func(_ int, s *goquery.Selection) {
		if next != "" {
			return
		}
		label, _ := s.Attr("aria-label")
		if strings.Contains(strings.ToLower(label), "nächste") || strings.Contains(strings.ToLower(label), "next") {
			href, exists := s.Attr("href")
			if exists && href != "" {
				next = resolve(href)
			}
		}
	})

	return next
}

func resolve(href string) string {
	if strings.HasPrefix(href, "http") {
		return href
	}
	if strings.HasPrefix(href, "/") {
		return "https://www.gelbeseiten.de" + href
	}
	return href
}
