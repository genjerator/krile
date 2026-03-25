package scraper

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	ajaxURL   = "https://www.gelbeseiten.de/ajaxsuche"
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
)

type AjaxResponse struct {
	AnzahlTreffer       int    `json:"anzahlTreffer"`
	AnzahlMehrTreffer   int    `json:"anzahlMehrTreffer"`
	GesamtanzahlTreffer int    `json:"gesamtanzahlTreffer"`
	HTML                string `json:"html"`
}

type Fetcher struct {
	client  *http.Client
	verbose bool
	debug   bool
}

func NewFetcher(ctx interface{}, verbose, debug bool) (*Fetcher, error) {
	return &Fetcher{
		client:  &http.Client{Timeout: 30 * time.Second},
		verbose: verbose,
		debug:   debug,
	}, nil
}

func (f *Fetcher) Close() {}

func (f *Fetcher) FetchPages(query, city string, maxClicks int, onHTML func(string) error) error {
	position := 1
	const anzahl = 10

	if f.verbose || f.debug {
		fmt.Fprintf(os.Stderr, "[INFO] %s total results available: fetching...\n", ts())
	}

	clicks := 0
	for {
		if f.verbose || f.debug {
			fmt.Fprintf(os.Stderr, "[INFO] %s fetching position=%d\n", ts(), position)
		}

		resp, err := f.post(query, city, position, anzahl)
		if err != nil {
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
			if f.verbose || f.debug {
				fmt.Fprintf(os.Stderr, "[INFO] %s reached click limit (%d)\n", ts(), maxClicks)
			}
			break
		}

		if resp.AnzahlMehrTreffer == 0 {
			if f.verbose || f.debug {
				fmt.Fprintf(os.Stderr, "[INFO] %s no more results\n", ts())
			}
			break
		}
	}

	return nil
}

func (f *Fetcher) post(query, city string, position, anzahl int) (*AjaxResponse, error) {
	var body strings.Builder
	mw := multipart.NewWriter(&body)

	fields := map[string]string{
		"umkreis":    "-1",
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

	req, err := http.NewRequest("POST", ajaxURL, strings.NewReader(body.String()))
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

func ts() string {
	return time.Now().Format(time.RFC3339)
}
