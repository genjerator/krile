package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/genjerator/krile/internal/parser"
)

const (
	ajaxURL   = "https://www.gelbeseiten.de/ajaxsuche"
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
	colorReset = "\033[0m"
	colorRed   = "\033[31m"
	colorBlue  = "\033[34m"
	colorCyan  = "\033[36m"
)

type AjaxResponse struct {
	AnzahlTreffer       int    `json:"anzahlTreffer"`
	AnzahlMehrTreffer   int    `json:"anzahlMehrTreffer"`
	GesamtanzahlTreffer int    `json:"gesamtanzahlTreffer"`
	HTML                string `json:"html"`
}

type Fetcher struct {
	ctx      context.Context
	client   *http.Client
	delay    time.Duration
	distance int
	verbose  bool
	debug    bool
}

func NewFetcher(ctx context.Context, verbose, debug bool, delayMs, distance int) (*Fetcher, error) {
	return &Fetcher{
		ctx:      ctx,
		client:   &http.Client{Timeout: 30 * time.Second},
		delay:    time.Duration(delayMs) * time.Millisecond,
		distance: distance,
		verbose:  verbose,
		debug:    debug,
	}, nil
}

func (f *Fetcher) Close() {}

func (f *Fetcher) FetchPages(query, city string, maxClicks, startPosition, startPage int, onHTML func(string) error) error {
	// Request 50 results per page (matches the site's own "1 - 50 von N
	// Einträgen" page size). Pagination advances by the count actually
	// returned, so a lower server-side cap still works correctly.
	const anzahl = 50

	clicks := 0
	page := 0
	pageSize := 0
	total := 0
	totalPages := 0

	position := 1
	switch {
	case startPosition > 1:
		position = startPosition
		fmt.Fprintf(os.Stderr, "[INFO] resuming from position=%s%d%s\n", colorCyan, position, colorReset)
	case startPage > 1:
		// Convert a page number to a result position. The page size isn't
		// known until the server answers, so probe page 1 once (its results
		// are not processed) to learn it, then jump to the requested page.
		probe, err := f.post(query, city, 1, anzahl)
		if err != nil {
			return err
		}
		if probe.AnzahlTreffer > 0 {
			pageSize = probe.AnzahlTreffer
			total = probe.GesamtanzahlTreffer
			totalPages = (total + pageSize - 1) / pageSize
			position = (startPage-1)*pageSize + 1
			fmt.Fprintf(os.Stderr, "[INFO] resuming from page %s%d/%d%s (position=%d, %d results/page)\n",
				colorCyan, startPage, totalPages, colorReset, position, pageSize)
		}
	}
	for {
		if err := f.ctx.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "\n[INFO] interrupted, stopping\n")
			return nil
		}

		page++
		// Display the absolute page (derived from position) once the real
		// page size is known, so a resumed run shows the true page number.
		displayPage := page
		if pageSize > 0 {
			displayPage = (position-1)/pageSize + 1
		}
		if totalPages > 0 {
			fmt.Fprintf(os.Stderr, "[INFO] fetching page %s%d/%d%s (position=%d of %d)\n",
				colorCyan, displayPage, totalPages, colorReset, position, total)
		} else {
			fmt.Fprintf(os.Stderr, "[INFO] fetching page %s%d%s (position=%d)\n", colorCyan, displayPage, colorReset, position)
		}
		if f.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] position=%d\n", position)
		}

		resp, err := f.post(query, city, position, anzahl)
		if err != nil {
			if f.ctx.Err() != nil {
				fmt.Fprintf(os.Stderr, "\n[INFO] interrupted, stopping\n")
				return nil
			}
			return err
		}

		if f.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] anzahlTreffer=%d anzahlMehrTreffer=%d gesamtanzahl=%d\n",
				resp.AnzahlTreffer, resp.AnzahlMehrTreffer, resp.GesamtanzahlTreffer)
		}

		if resp.AnzahlTreffer == 0 {
			break
		}

		if totalPages == 0 && resp.GesamtanzahlTreffer > 0 {
			total = resp.GesamtanzahlTreffer
			pageSize = resp.AnzahlTreffer
			totalPages = (total + resp.AnzahlTreffer - 1) / resp.AnzahlTreffer
			fmt.Fprintf(os.Stderr, "[INFO] %s%d%s total results (%d per page, %d pages)\n",
				colorCyan, total, colorReset, resp.AnzahlTreffer, totalPages)
		}

		if err := onHTML(resp.HTML); err != nil {
			return err
		}

		position += resp.AnzahlTreffer
		clicks++

		if maxClicks > 0 && clicks >= maxClicks {
			fmt.Fprintf(os.Stderr, "[INFO] reached page limit after %s%d%s pages\n", colorCyan, page, colorReset)
			break
		}

		if total > 0 && position > total {
			fmt.Fprintf(os.Stderr, "[INFO] all %s%d%s results fetched (%d pages)\n", colorCyan, total, colorReset, page)
			break
		}

		if resp.AnzahlMehrTreffer == 0 {
			fmt.Fprintf(os.Stderr, "[INFO] no more results after %s%d%s pages\n", colorCyan, page, colorReset)
			break
		}

		select {
		case <-f.ctx.Done():
			fmt.Fprintf(os.Stderr, "\n[INFO] interrupted, stopping\n")
			return nil
		case <-time.After(f.delay):
		}
	}

	return nil
}

func (f *Fetcher) post(query, city string, position, anzahl int) (*AjaxResponse, error) {
	var body strings.Builder
	mw := multipart.NewWriter(&body)

	umkreis := "-1"
	distance := "0"
	if f.distance > 0 {
		umkreis = fmt.Sprintf("%d", f.distance)
		distance = fmt.Sprintf("%d", f.distance)
	}

	fields := map[string]string{
		"umkreis":    umkreis,
		"distance":   distance,
		"verwandt":   "false",
		"WAS":        strings.ToLower(query),
		"WO":         strings.ToLower(city),
		"position":   fmt.Sprintf("%d", position),
		"anzahl":     fmt.Sprintf("%d", anzahl),
		"sortierung": "relevanz",
	}
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	mw.Close()

	req, err := http.NewRequestWithContext(f.ctx, "POST", ajaxURL, strings.NewReader(body.String()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", "https://www.gelbeseiten.de/")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST ajaxsuche: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ajaxsuche returned HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if f.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] raw response (%d bytes): %s\n", len(data), string(data))
	}

	var result AjaxResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse ajax response: %w", err)
	}

	return &result, nil
}

// SearchOnce performs a single ajaxsuche request (first page only) and
// returns the raw response. Used by the Excel lookup mode.
func (f *Fetcher) SearchOnce(query, city string, anzahl int) (*AjaxResponse, error) {
	return f.post(query, city, 1, anzahl)
}

// FetchDetailPage fetches a single gelbeseiten detail page by URL.
func (f *Fetcher) FetchDetailPage(rawURL string) (string, error) {
	if !strings.HasPrefix(rawURL, "http") {
		rawURL = "https://www.gelbeseiten.de" + rawURL
	}
	return f.fetchPage(rawURL)
}

// FindEmailOnWebsite fetches the business website and looks for an email on the
// homepage first, then on the first contact/impressum page found.
func (f *Fetcher) FindEmailOnWebsite(websiteURL string) (string, error) {
	email, _, err := f.FindEmailAndPages(websiteURL)
	return email, err
}

// commonContactPaths are contact/imprint pages to probe directly when the
// homepage neither carries an email nor links to a contact page. German and
// English variants, with and without a .html suffix.
var commonContactPaths = []string{
	"/kontakt", "/kontakt/", "/kontakt.html", "/kontakt.php",
	"/contact", "/contact/", "/contact.html", "/contact.php",
	"/impressum", "/impressum/", "/impressum.html", "/impressum.php",
	"/datenschutz", "/datenschutz/", "/datenschutz.html", "/datenschutz.php",
}

// commonContactURLs returns absolute contact-page guesses at the site root.
func commonContactURLs(websiteURL string) []string {
	u, err := url.Parse(websiteURL)
	if err != nil || u.Host == "" {
		return nil
	}
	root := u.Scheme + "://" + u.Host
	out := make([]string, 0, len(commonContactPaths))
	for _, p := range commonContactPaths {
		out = append(out, root+p)
	}
	return out
}

// FindEmailAndPages is like FindEmailOnWebsite but also returns the fetched
// HTML (homepage plus contact page), so callers can verify the site really
// belongs to the business. Order: homepage → contact page linked from the
// homepage → common guessed contact/imprint URLs (/kontakt, /contact, …).
func (f *Fetcher) FindEmailAndPages(websiteURL string) (string, string, error) {
	html, err := f.fetchPage(websiteURL)
	if err != nil {
		return "", "", err
	}
	pages := html
	tried := map[string]bool{websiteURL: true}

	if email := parser.ExtractEmailFromHTML(html); email != "" {
		return email, pages, nil
	}

	// 1. Contact page linked from the homepage.
	if contactURL := parser.FindContactPageURL(html, websiteURL); contactURL != "" && !tried[contactURL] {
		tried[contactURL] = true
		if f.verbose || f.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] contact page found: %s\n", contactURL)
		}
		if contactHTML, err := f.fetchPage(contactURL); err == nil {
			pages += contactHTML
			if email := parser.ExtractEmailFromHTML(contactHTML); email != "" {
				return email, pages, nil
			}
		}
	}

	// 2. Guess common contact/imprint URLs even when the homepage has no link.
	for _, guess := range commonContactURLs(websiteURL) {
		if tried[guess] {
			continue
		}
		tried[guess] = true
		guessHTML, err := f.fetchPage(guess)
		if err != nil {
			continue // 404 / not present — try the next candidate
		}
		pages += guessHTML
		if email := parser.ExtractEmailFromHTML(guessHTML); email != "" {
			if f.verbose || f.debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] email found on guessed contact page: %s\n", guess)
			}
			return email, pages, nil
		}
	}

	return "", pages, nil
}

func (f *Fetcher) fetchPage(rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(f.ctx, "GET", rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")

	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: HTTP %d", rawURL, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ts() string {
	return time.Now().Format(time.RFC3339)
}
