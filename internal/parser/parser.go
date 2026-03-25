package parser

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/genjerator/krile/internal/models"
)

func Parse(html string) ([]models.Business, error) {
	return ParseDebug(html, false)
}

func ParseDebug(html string, debug bool) ([]models.Business, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	var businesses []models.Business

	doc.Find("article.mod-Treffer").Each(func(_ int, s *goquery.Selection) {
		b := models.Business{ScrapedAt: now}

		// Name
		b.Name = strings.TrimSpace(s.Find("h2.mod-Treffer__name").Text())
		if b.Name == "" {
			return
		}

		// Source URL — first <a href> in the article
		b.SourceURL, _ = s.Find("a[href]").First().Attr("href")

		// Category
		b.Category = strings.TrimSpace(s.Find("p.mod-Treffer--besteBranche").Text())

		// Phone — text of the phone link
		b.Phone = strings.TrimSpace(s.Find("a.mod-TelefonnummerKompakt__phoneNumber").Text())

		// Website — base64 encoded in data-webseiteLink
		if encoded, exists := s.Find("span[data-webseitelink]").Attr("data-webseitelink"); exists {
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				decoded, err = base64.RawStdEncoding.DecodeString(encoded)
			}
			if err == nil {
				b.Website = string(decoded)
			}
		}

		// Address
		addrSel := s.Find(".mod-AdresseKompakt__adress-text")
		if addrSel.Length() > 0 {
			// Postal code + city from the nobr span e.g. "10963 Berlin"
			ortText := strings.TrimSpace(addrSel.Find("span.nobr").Text())
			if parts := strings.SplitN(ortText, " ", 2); len(parts) == 2 {
				b.PostalCode = parts[0]
				b.City = parts[1]
			}
			// Street: full addrSel text minus the nobr span and distance span
			addrSel.Find("span").Remove()
			streetRaw := strings.TrimSpace(addrSel.Text())
			streetRaw = strings.TrimSuffix(streetRaw, ",")
			b.Street = strings.TrimSpace(streetRaw)
		}

		// Email — from chat button data-parameters if present
		if raw, exists := s.Find(`button[id^="mod-Chat__button--"]`).Attr("data-parameters"); exists {
			var params struct {
				InboxConfig struct {
					OrganizationQuery struct {
						Generic struct {
							Email string `json:"email"`
						} `json:"generic"`
					} `json:"organizationQuery"`
				} `json:"inboxConfig"`
			}
			if err := json.Unmarshal([]byte(raw), &params); err == nil {
				b.Email = params.InboxConfig.OrganizationQuery.Generic.Email
			}
		}

		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] parsed: %s | %s %s | %s | %s\n",
				b.Name, b.Street, b.City, b.Phone, b.Email)
		}

		businesses = append(businesses, b)
	})

	return businesses, nil
}
