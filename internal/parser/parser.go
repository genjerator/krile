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

// ExtractEmailFromDetailPage attempts to extract email from a business detail page
func ExtractEmailFromDetailPage(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ""
	}

	var email string

	// 1. Try chat button data-parameters
	if raw, exists := doc.Find(`button[id^="mod-Chat__button--"]`).Attr("data-parameters"); exists {
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
			email = params.InboxConfig.OrganizationQuery.Generic.Email
		}
	}

	// 2. Try email button with mailto
	if email == "" {
		selectors := []string{
			`[data-link^="mailto:"]`,
			`#email_versenden[data-link^="mailto:"]`,
			`div[data-link^="mailto:"]`,
			`.button[data-link^="mailto:"]`,
			`a[href^="mailto:"]`,
		}
		for _, selector := range selectors {
			var dataLink string
			var exists bool

			// Try data-link first
			if dataLink, exists = doc.Find(selector).Attr("data-link"); !exists {
				// Try href
				dataLink, exists = doc.Find(selector).Attr("href")
			}

			if exists && strings.HasPrefix(dataLink, "mailto:") {
				emailPart := strings.TrimPrefix(dataLink, "mailto:")
				if idx := strings.Index(emailPart, "?"); idx > 0 {
					emailPart = emailPart[:idx]
				}
				email = strings.TrimSpace(emailPart)
				if email != "" {
					break
				}
			}
		}
	}

	// 3. Look for email in various contact sections
	if email == "" {
		// Try finding email in contact details
		doc.Find(".text, .kontaktdaten, .contact-info, p, span, div").Each(func(_ int, s *goquery.Selection) {
			text := s.Text()
			// Simple email regex pattern
			if idx := strings.Index(text, "@"); idx > 0 {
				// Extract potential email around the @ symbol
				start := idx
				for start > 0 && isEmailChar(text[start-1]) {
					start--
				}
				end := idx
				for end < len(text) && isEmailChar(text[end]) {
					end++
				}
				potentialEmail := text[start:end]
				// Basic validation
				if strings.Contains(potentialEmail, "@") && strings.Contains(potentialEmail, ".") {
					email = potentialEmail
					return
				}
			}
		})
	}

	return email
}

// isEmailChar checks if a byte is valid in an email address
func isEmailChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '@' || c == '.' ||
		c == '-' || c == '_' || c == '+'
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

		// Email — try multiple sources
		// 1. From chat button data-parameters
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

		// 2. From email button data-link (mailto:...)
		if b.Email == "" {
			// Try multiple selectors for email button
			selectors := []string{
				`[data-link^="mailto:"]`,
				`#email_versenden[data-link^="mailto:"]`,
				`div[data-link^="mailto:"]`,
				`.button[data-link^="mailto:"]`,
			}
			for _, selector := range selectors {
				if dataLink, exists := s.Find(selector).Attr("data-link"); exists {
					// Extract email from "mailto:info@example.com?subject=..."
					if strings.HasPrefix(dataLink, "mailto:") {
						emailPart := strings.TrimPrefix(dataLink, "mailto:")
						// Remove query parameters if present
						if idx := strings.Index(emailPart, "?"); idx > 0 {
							emailPart = emailPart[:idx]
						}
						b.Email = strings.TrimSpace(emailPart)
						if debug && b.Email != "" {
							fmt.Fprintf(os.Stderr, "[DEBUG] email found with selector: %s\n", selector)
						}
						break
					}
				}
			}
		}

		if debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] parsed: %s | %s %s | %s | %s\n",
				b.Name, b.Street, b.City, b.Phone, b.Email)
			if b.Email == "" {
				// Dump full HTML for this article to see what's available
				articleHTML, _ := s.Html()
				fmt.Fprintf(os.Stderr, "\n[DEBUG] ========================================\n")
				fmt.Fprintf(os.Stderr, "[DEBUG] STOPPED: No email found for: %s\n", b.Name)
				fmt.Fprintf(os.Stderr, "[DEBUG] ========================================\n")
				fmt.Fprintf(os.Stderr, "[DEBUG] Full HTML of this article:\n\n")
				fmt.Fprintf(os.Stderr, "%s\n\n", articleHTML)
				fmt.Fprintf(os.Stderr, "[DEBUG] ========================================\n")
				fmt.Fprintf(os.Stderr, "[DEBUG] Program stopped for debugging. Press Ctrl+C if needed.\n")
				fmt.Fprintf(os.Stderr, "[DEBUG] ========================================\n")
				//os.Exit(0)
			}
		}

		businesses = append(businesses, b)
	})

	return businesses, nil
}
