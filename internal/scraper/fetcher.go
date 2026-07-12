package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
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

func (f *Fetcher) FetchPages(query, city string, maxClicks int, onHTML func(string) error) error {
	position := 1
	const anzahl = 10

	clicks := 0
	page := 0
	for {
		if err := f.ctx.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "\n[INFO] interrupted, stopping\n")
			return nil
		}

		page++
		fmt.Fprintf(os.Stderr, "[INFO] fetching page %s%d%s (position=%d)\n", colorCyan, page, colorReset, position)
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

		if err := onHTML(resp.HTML); err != nil {
			return err
		}

		position += anzahl
		clicks++

		if maxClicks > 0 && clicks >= maxClicks {
			fmt.Fprintf(os.Stderr, "[INFO] reached page limit after %s%d%s pages\n", colorCyan, page, colorReset)
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
	html, err := f.fetchPage(websiteURL)
	if err != nil {
		return "", err
	}

	if email := parser.ExtractEmailFromHTML(html); email != "" {
		return email, nil
	}

	contactURL := parser.FindContactPageURL(html, websiteURL)
	if contactURL == "" || contactURL == websiteURL {
		return "", nil
	}

	if f.verbose || f.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] contact page found: %s\n", contactURL)
	}

	contactHTML, err := f.fetchPage(contactURL)
	if err != nil {
		return "", err
	}

	return parser.ExtractEmailFromHTML(contactHTML), nil
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
