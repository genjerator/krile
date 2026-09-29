package scraper

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/genjerator/krile/internal/config"
	"github.com/genjerator/krile/internal/lookup"
	"github.com/genjerator/krile/internal/models"
	"github.com/genjerator/krile/internal/parser"
)

// RunLookup is the Excel lookup mode: every company row of the address
// export that has a Firma but no EMail is searched on gelbeseiten.de and
// the found email is written into an output copy of the workbook.
func RunLookup(ctx context.Context, cfg config.Config) error {
	start := time.Now()

	dest := cfg.Output
	if dest == "" {
		base := strings.TrimSuffix(filepath.Base(cfg.ExcelPath), filepath.Ext(cfg.ExcelPath))
		dest = base + "-filled.xlsx"
	}
	if filepath.Dir(dest) == "." {
		dest = filepath.Join("export", dest)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	wb, companies, resumed, err := lookup.Open(cfg.ExcelPath, dest)
	if err != nil {
		return err
	}
	defer wb.Close()

	logPath := strings.TrimSuffix(dest, filepath.Ext(dest)) + ".log"
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()
	var logMu sync.Mutex
	logf := func(format string, args ...interface{}) {
		logMu.Lock()
		defer logMu.Unlock()
		fmt.Fprintf(logFile, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
	}

	// Build the work set: companies with a name, no email yet, not yet
	// processed in an earlier (resumed) run.
	var work []*lookup.Company
	hasEmail, doneBefore := 0, 0
	for i := range companies {
		c := &companies[i]
		switch {
		case c.Firma == "":
			// private person / empty row — out of scope
		case c.Status != "":
			doneBefore++
		case c.Email != "":
			hasEmail++
		default:
			work = append(work, c)
		}
	}
	if cfg.Limit > 0 && len(work) > cfg.Limit {
		work = work[:cfg.Limit]
	}

	if resumed {
		fmt.Fprintf(os.Stderr, "[INFO] resuming %s (%d rows already processed)\n", dest, doneBefore)
	} else {
		fmt.Fprintf(os.Stderr, "[INFO] writing output to %s\n", dest)
	}
	fmt.Fprintf(os.Stderr, "[INFO] %s%d%s companies to look up (%d rows total, %d with email already, %d done before)\n",
		colorCyan, len(work), colorReset, len(companies), hasEmail, doneBefore)
	fmt.Fprintf(os.Stderr, "[INFO] log file: %s\n", logPath)
	logf("run started: input=%s output=%s workset=%d resumed=%v", cfg.ExcelPath, dest, len(work), resumed)

	fetcher, err := NewFetcher(ctx, cfg.Verbose, cfg.Debug, cfg.Delay, 0)
	if err != nil {
		return fmt.Errorf("fetcher init: %w", err)
	}
	defer fetcher.Close()

	var ws *WebSearcher
	if cfg.WebSearch {
		ws = NewWebSearcher(ctx, cfg.Delay, cfg.Debug)
		defer ws.Close()
	}

	// Each worker paces its own searches, so up to `workers` requests run
	// in parallel and the total rate is roughly workers/delay.
	delay := time.Duration(cfg.Delay) * time.Millisecond
	if delay <= 0 {
		delay = time.Second
	}

	workers := cfg.Workers
	if workers <= 0 {
		workers = 4
	}

	var doneCount int64
	counts := make(map[string]int)
	viaCounts := make(map[string]int)
	var countsMu sync.Mutex

	jobs := make(chan *lookup.Company)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				res, ok := lookupOne(ctx, fetcher, ws, delay, c, logf)
				if !ok { // interrupted mid-lookup: leave row unprocessed
					return
				}
				if err := wb.SetResult(c, res); err != nil {
					fmt.Fprintf(os.Stderr, "[WARN] row %d: write result: %v\n", c.Row, err)
					logf("row %d %q: write result failed: %v", c.Row, c.Firma, err)
				}
				countsMu.Lock()
				counts[res.Status]++
				if res.Email != "" {
					viaCounts[res.Via]++
				}
				countsMu.Unlock()

				n := atomic.AddInt64(&doneCount, 1)
				emailInfo := res.Email
				if emailInfo != "" {
					emailInfo = colorBlue + emailInfo + colorReset + " "
				}
				viaInfo := ""
				if res.Via != "" {
					viaInfo = ", via " + res.Via
				}
				fmt.Fprintf(os.Stderr, "[%s%d/%d%s] %s (%s) → %s%s (score %d%s)\n",
					colorCyan, n, len(work), colorReset, c.Firma, c.Ort, emailInfo, res.Status, res.Score, viaInfo)
			}
		}()
	}

feed:
	for _, c := range work {
		select {
		case <-ctx.Done():
			break feed
		case jobs <- c:
		}
	}
	close(jobs)
	wg.Wait()

	if err := wb.Save(); err != nil {
		return err
	}

	elapsed := time.Since(start).Round(time.Second)
	processed := atomic.LoadInt64(&doneCount)
	found := counts[lookup.StatusOK] + counts[lookup.StatusUnconfirmed]
	fmt.Fprintf(os.Stderr, "\n─────────────────────────────────────────\n")
	fmt.Fprintf(os.Stderr, "  Duration:            %s\n", elapsed)
	fmt.Fprintf(os.Stderr, "  Companies processed: %d / %d\n", processed, len(work))
	fmt.Fprintf(os.Stderr, "  Emails written:      %s%d%s\n", colorBlue, found, colorReset)
	fmt.Fprintf(os.Stderr, "    phone confirmed:   %d\n", counts[lookup.StatusOK])
	fmt.Fprintf(os.Stderr, "    unconfirmed:       %d (see log)\n", counts[lookup.StatusUnconfirmed])
	fmt.Fprintf(os.Stderr, "    from listing:      %d\n", viaCounts["gelbeseiten listing"])
	fmt.Fprintf(os.Stderr, "    from detail page:  %d\n", viaCounts["gelbeseiten detail page"])
	fmt.Fprintf(os.Stderr, "    from website:      %d\n", viaCounts["company website"])
	fmt.Fprintf(os.Stderr, "    from web search:   %d\n", viaCounts["web search"])
	fmt.Fprintf(os.Stderr, "  Matched, no email:   %d\n", counts[lookup.StatusNoEmail])
	fmt.Fprintf(os.Stderr, "  No match:            %d\n", counts[lookup.StatusNoMatch])
	fmt.Fprintf(os.Stderr, "  No results:          %d\n", counts[lookup.StatusNoResults])
	fmt.Fprintf(os.Stderr, "  No location:         %d\n", counts[lookup.StatusNoLocation])
	fmt.Fprintf(os.Stderr, "  Errors:              %d\n", counts[lookup.StatusError])
	fmt.Fprintf(os.Stderr, "  Output:              %s\n", dest)
	fmt.Fprintf(os.Stderr, "─────────────────────────────────────────\n")
	logf("run finished: processed=%d emails=%d ok=%d unconfirmed=%d no_email=%d no_match=%d no_results=%d errors=%d duration=%s",
		processed, found, counts[lookup.StatusOK], counts[lookup.StatusUnconfirmed], counts[lookup.StatusNoEmail],
		counts[lookup.StatusNoMatch], counts[lookup.StatusNoResults], counts[lookup.StatusError], elapsed)

	return nil
}

// lookupOne searches gelbeseiten for a single company and returns the
// outcome. delay spaces this worker's search requests. ok=false means the
// run was interrupted before a conclusive result, and nothing should be
// written for the row.
func lookupOne(ctx context.Context, f *Fetcher, ws *WebSearcher, delay time.Duration, c *lookup.Company,
	logf func(string, ...interface{})) (lookup.Result, bool) {

	// Query attempts, in order: exact name in the city, cleaned name
	// (legal form stripped) in the city, cleaned name at the PLZ.
	where := c.Ort
	if where == "" {
		where = c.PLZ
	}
	clean := lookup.CleanFirma(c.Firma)
	var attempts [][2]string
	if where != "" {
		attempts = append(attempts, [2]string{c.Firma, where})
		if !strings.EqualFold(clean, c.Firma) {
			attempts = append(attempts, [2]string{clean, where})
		}
	}
	if c.PLZ != "" && c.PLZ != where {
		attempts = append(attempts, [2]string{clean, c.PLZ})
	}
	if len(attempts) == 0 {
		logf("row %d %q: skipped, no Ort/PLZ to search with", c.Row, c.Firma)
		return lookup.Result{Status: lookup.StatusNoLocation}, true
	}

	sawResults := false
	var lastErr error
	var best models.Business
	bestScore := -1

	for _, a := range attempts {
		select {
		case <-ctx.Done():
			return lookup.Result{}, false
		case <-time.After(delay):
		}

		resp, err := f.SearchOnce(a[0], a[1], 25)
		if err != nil {
			if ctx.Err() != nil {
				return lookup.Result{}, false
			}
			lastErr = err
			continue
		}
		if resp.AnzahlTreffer == 0 {
			continue
		}
		businesses, err := parser.Parse(resp.HTML)
		if err != nil {
			lastErr = err
			continue
		}
		if len(businesses) > 0 {
			sawResults = true
		}
		for i := range businesses {
			if s := lookup.Score(*c, businesses[i]); s > bestScore {
				bestScore, best = s, businesses[i]
			}
		}
		if bestScore >= lookup.MinAcceptScore {
			break // confident match, no need for looser queries
		}
	}

	if bestScore < lookup.MinAcceptScore {
		switch {
		case sawResults:
			logf("row %d %q (%s %s): %d-scored best result %q rejected (< %d)",
				c.Row, c.Firma, c.PLZ, c.Ort, bestScore, best.Name, lookup.MinAcceptScore)
			return lookup.Result{Status: lookup.StatusNoMatch, Score: bestScore}, true
		case lastErr != nil:
			logf("row %d %q: search failed: %v", c.Row, c.Firma, lastErr)
			return lookup.Result{Status: lookup.StatusError}, true
		default:
			return lookup.Result{Status: lookup.StatusNoResults}, true
		}
	}

	res := lookup.Result{Score: bestScore, Match: best.Name, Phone: best.Phone}

	// Email: listing card → gelbeseiten detail page → company website.
	sourceURL := best.SourceURL
	if !strings.HasPrefix(sourceURL, "http") {
		sourceURL = "https://www.gelbeseiten.de" + sourceURL
	}
	res.Email = best.Email
	res.Source = sourceURL
	res.Via = "gelbeseiten listing"

	if res.Email == "" && best.SourceURL != "" {
		if html, err := f.FetchDetailPage(best.SourceURL); err == nil {
			res.Email = parser.ExtractEmailFromDetailPage(html)
			res.Via = "gelbeseiten detail page"
		} else if ctx.Err() != nil {
			return lookup.Result{}, false
		}
	}
	if res.Email == "" && best.Website != "" {
		if email, err := f.FindEmailOnWebsite(best.Website); err == nil && email != "" {
			res.Email = email
			res.Source = best.Website
			res.Via = "company website"
		} else if ctx.Err() != nil {
			return lookup.Result{}, false
		}
	}

	// Last resort: find the company's own website via web search and run
	// the email extraction on it.
	if res.Email == "" && ws != nil {
		query := strings.TrimSpace(lookup.CleanFirma(c.Firma) + " " + where)
		site, err := ws.FindWebsite(query, lookup.CleanFirma(c.Firma))
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return lookup.Result{}, false
			}
			logf("row %d %q: web search failed: %v", c.Row, c.Firma, err)
		case site != "" && !strings.EqualFold(site, best.Website):
			logf("row %d %q: web search found %s", c.Row, c.Firma, site)
			if email, pages, err := f.FindEmailAndPages(site); err == nil && email != "" {
				// Verify against the Excel row first, then against the
				// matched gelbeseiten listing.
				okE, reason := acceptWebEmail(email, site, c.Firma, c.Telefon, c.PLZ, pages)
				if !okE {
					okE, reason = acceptWebEmail(email, site, best.Name, best.Phone, best.PostalCode, pages)
				}
				if okE {
					res.Email = email
					res.Source = site
					res.Via = "web search"
					logf("row %d %q: web-search email %s accepted — %s", c.Row, c.Firma, email, reason)
				} else {
					logf("row %d %q: web-search email %s from %s rejected — %s", c.Row, c.Firma, email, site, reason)
				}
			} else if ctx.Err() != nil {
				return lookup.Result{}, false
			}
		}
	}

	if res.Email == "" {
		res.Via = ""
		logf("row %d %q: matched %q (score %d) but no email on listing, detail page, website %s or web search",
			c.Row, c.Firma, best.Name, bestScore, best.Website)
		res.Status = lookup.StatusNoEmail
		return res, true
	}
	logf("row %d %q: email %s found via %s (listing %q, score %d, source %s)",
		c.Row, c.Firma, res.Email, res.Via, best.Name, bestScore, res.Source)

	// Phone check decides ok vs unconfirmed; the email is written either way.
	switch {
	case lookup.PhoneMatch(c.Telefon, best.Phone):
		res.Status = lookup.StatusOK
	case c.Telefon == "":
		res.Status = lookup.StatusUnconfirmed
		logf("row %d %q: email %s written UNCONFIRMED — excel row has no phone (listing %q, phone %q, score %d, source %s)",
			c.Row, c.Firma, res.Email, best.Name, best.Phone, bestScore, res.Source)
	case best.Phone == "":
		res.Status = lookup.StatusUnconfirmed
		logf("row %d %q: email %s written UNCONFIRMED — listing %q has no phone to compare (excel %q, score %d, source %s)",
			c.Row, c.Firma, res.Email, best.Name, c.Telefon, bestScore, res.Source)
	default:
		res.Status = lookup.StatusUnconfirmed
		logf("row %d %q: email %s written UNCONFIRMED — phone differs (excel %q vs gelbeseiten %q, listing %q, score %d, source %s)",
			c.Row, c.Firma, res.Email, c.Telefon, best.Phone, best.Name, bestScore, res.Source)
	}
	return res, true
}
