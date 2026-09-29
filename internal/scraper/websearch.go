package scraper

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

// blockedDomains are portals, directories, marketplaces and social networks
// that a web search may return but that are never the company's own website.
// Entries containing a dot are matched as domain suffixes ("x.com" blocks
// x.com and sub.x.com but not xerox.com); entries without a dot are matched
// as substrings of the host (covers cylex.de, cylex-branchenbuch.de, ...).
var blockedDomains = []string{
	"gelbeseiten.de", "11880.com", "dasoertliche.de", "dastelefonbuch.de",
	"goyellow.de", "meinestadt.de", "wlw.de", "kompass.com", "firmania.de",
	"north-data.de", "firmenwissen.de", "werhatoffen.de", "oeffnungszeitenbuch.de",
	"facebook.com", "instagram.com", "linkedin.com", "xing.com",
	"youtube.com", "twitter.com", "x.com", "tiktok.com", "wikipedia.org",
	"bing.com", "duckduckgo.com", "immobilienscout24.de", "kununu.com",
	"cylex", "branchenbuch", "handelsregister", "unternehmensregister",
	"creditreform", "tripadvisor", "yelp", "google", "amazon", "ebay",
	"pinterest", "speisekarte", "restaurantfuehrer", "lieferando", "booking",
}

func isBlockedHost(host string) bool {
	host = strings.ToLower(host)
	for _, d := range blockedDomains {
		if strings.Contains(d, ".") {
			if host == d || strings.HasSuffix(host, "."+d) {
				return true
			}
		} else if strings.Contains(host, d) {
			return true
		}
	}
	return false
}

// registrableLabel returns the second-level label of a host — the part that
// usually carries the brand ("mariahilf-hotel" of "www.mariahilf-hotel.at").
// This lets a company-name match ignore aggregator subdomains such as
// "ibis-wien-mariahilf.meinhotel.top", whose brand token sits in a subdomain
// rather than the registrable domain.
func registrableLabel(host string) string {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	labels := strings.Split(host, ".")
	if len(labels) >= 2 {
		return labels[len(labels)-2]
	}
	return host
}

// pickResult chooses the best candidate URL for a company's own website.
// Directory/social hosts are always skipped; among the rest it prefers, in
// order: (1) a result whose registrable domain contains a distinctive token of
// the company name (the official site, even when it ranks below aggregators),
// (2) a result whose host contains such a token anywhere, (3) the first
// non-blocked result (previous behaviour). Returns "" when nothing qualifies.
func pickResult(hrefs []string, companyName string) string {
	toks := distinctiveTokens(companyName)
	var domainMatch, hostMatch, firstValid string
	for _, h := range hrefs {
		u, err := url.Parse(h)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		host := u.Hostname()
		if isBlockedHost(host) {
			continue
		}
		if firstValid == "" {
			firstValid = h
		}
		sld := registrableLabel(host)
		lowHost := strings.ToLower(host)
		for _, t := range toks {
			if strings.Contains(sld, t) {
				if domainMatch == "" {
					domainMatch = h
				}
			} else if strings.Contains(lowHost, t) {
				if hostMatch == "" {
					hostMatch = h
				}
			}
		}
	}
	switch {
	case domainMatch != "":
		return domainMatch
	case hostMatch != "":
		return hostMatch
	default:
		return firstValid
	}
}

// freemailDomains are consumer mail providers commonly used by small German
// businesses; such an address can belong to the company even though its
// domain has no relation to the company name.
var freemailDomains = []string{
	"gmx.", "web.de", "t-online.de", "gmail.com", "googlemail.com",
	"yahoo.", "hotmail.", "outlook.", "aol.", "freenet.de", "online.de",
	"arcor.de", "posteo.de", "mail.de", "icloud.com", "magenta.de",
	"vodafone.de", "kabelmail.de", "unitybox.de", "gmx.de",
}

func isFreemail(domain string) bool {
	for _, d := range freemailDomains {
		if strings.Contains(domain, d) {
			return true
		}
	}
	return false
}

// genericNameTokens are business-category words that appear in many company
// names and domains; a match on them proves nothing about ownership.
var genericNameTokens = map[string]struct{}{
	"metzgerei": {}, "metzger": {}, "fleischerei": {}, "baeckerei": {}, "backhaus": {},
	"restaurant": {}, "gasthof": {}, "gasthaus": {}, "gaststaette": {}, "cafe": {},
	"hotel": {}, "pension": {}, "elektro": {}, "elektrotechnik": {}, "friseur": {},
	"salon": {}, "apotheke": {}, "praxis": {}, "zahnarzt": {}, "arzt": {},
	"rechtsanwalt": {}, "anwalt": {}, "notar": {}, "steuerberater": {}, "autohaus": {},
	"werkstatt": {}, "auto": {}, "immobilien": {}, "versicherung": {}, "partyservice": {},
	"catering": {}, "getraenke": {}, "landtechnik": {}, "haustechnik": {}, "transporte": {},
	"spedition": {}, "malerbetrieb": {}, "maler": {}, "schreinerei": {}, "zimmerei": {},
	"gartenbau": {}, "physiotherapie": {}, "kanzlei": {}, "studio": {},
}

var tokenSplit = regexp.MustCompile(`[^\pL\pN]+`)

// distinctiveTokens returns the parts of a company name that identify it
// (names, brand words), skipping short words and generic category terms.
func distinctiveTokens(name string) []string {
	name = strings.ToLower(strings.ReplaceAll(name, "ß", "ss"))
	var out []string
	for _, t := range tokenSplit.Split(name, -1) {
		if len([]rune(t)) < 4 {
			continue
		}
		if _, generic := genericNameTokens[t]; generic {
			continue
		}
		out = append(out, t)
	}
	return out
}

func digitsOf(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// canonPhoneDigits reduces a phone number to comparable digits (country
// prefix and trunk zero stripped), mirroring internal/lookup.
func canonPhoneDigits(s string) string {
	d := digitsOf(s)
	if strings.HasPrefix(d, "0049") {
		d = d[4:]
	} else if strings.HasPrefix(d, "49") && len(d) > 9 {
		d = d[2:]
	}
	return strings.TrimPrefix(d, "0")
}

// pageVerifies reports whether the fetched pages mention the business's
// phone number or postal code — evidence the page is about this business.
func pageVerifies(pages, phone, plz string) bool {
	if plz = strings.TrimSpace(plz); plz != "" && strings.Contains(pages, plz) {
		return true
	}
	if p := canonPhoneDigits(phone); len(p) >= 6 && strings.Contains(digitsOf(pages), p) {
		return true
	}
	return false
}

// acceptWebEmail decides whether an email found on a web-search result page
// can be trusted to belong to the company. Search results may be portals or
// blog posts about the business whose extracted email belongs to someone
// else entirely, so the email is only accepted with positive evidence.
func acceptWebEmail(email, site, companyName, phone, plz, pages string) (bool, string) {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false, "malformed email"
	}
	domain := strings.ToLower(email[at+1:])
	toks := distinctiveTokens(companyName)

	for _, t := range toks {
		if strings.Contains(domain, t) {
			return true, "email domain matches company name"
		}
	}

	if !pageVerifies(pages, phone, plz) {
		return false, "page shows neither the company's phone nor PLZ"
	}
	if isFreemail(domain) {
		return true, "freemail address on a page verified by phone/PLZ"
	}

	host := ""
	if u, err := url.Parse(site); err == nil {
		host = strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	}
	if domain == host || strings.HasSuffix(host, "."+domain) || strings.HasSuffix(domain, "."+host) {
		for _, t := range toks {
			if strings.Contains(host, t) {
				return true, "site's own email, site domain matches company name"
			}
		}
		return false, "site's own email, but site domain unrelated to company name"
	}
	return false, "email domain unrelated to company and site"
}

// WebSearcher finds company websites via DuckDuckGo. It first tries the
// plain-HTML endpoint with a simple GET (like the gelbeseiten requests); if
// DuckDuckGo starts answering with a bot challenge, it transparently
// switches to driving a local headless Chrome, which passes those checks.
// Searches are serialized and paced. Safe for concurrent use.
type WebSearcher struct {
	mu     sync.Mutex
	parent context.Context
	client *http.Client
	pace   time.Duration
	next   time.Time
	debug  bool

	plainFails int // consecutive challenges; plain HTTP is dropped after 2

	// headless Chrome fallback, started lazily
	chromeStarted bool
	chromeErr     error
	browser       context.Context
	cancels       []context.CancelFunc
}

func NewWebSearcher(ctx context.Context, delayMs int, debug bool) *WebSearcher {
	pace := time.Duration(delayMs) * time.Millisecond
	if pace < time.Second {
		pace = time.Second
	}
	return &WebSearcher{
		parent: ctx,
		client: &http.Client{Timeout: 20 * time.Second},
		pace:   pace,
		debug:  debug,
	}
}

func (w *WebSearcher) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range w.cancels {
		c()
	}
	w.cancels = nil
}

// FindWebsite searches DuckDuckGo for query and returns the best organic
// result for the company's own website (see pickResult); companyName steers
// the choice toward a domain matching the company name. Returns "" without
// error when nothing suitable is found.
func (w *WebSearcher) FindWebsite(query, companyName string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Pace searches so the engine sees a human-ish rhythm.
	now := time.Now()
	if w.next.After(now) {
		select {
		case <-w.parent.Done():
			return "", w.parent.Err()
		case <-time.After(w.next.Sub(now)):
		}
	}
	w.next = time.Now().Add(w.pace)

	if w.plainFails < 2 {
		hrefs, err := w.plainSearch(query)
		if err == nil {
			w.plainFails = 0
			if w.debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] web search %q: %d results (plain)\n", query, len(hrefs))
			}
			return pickResult(hrefs, companyName), nil
		}
		if w.parent.Err() != nil {
			return "", err
		}
		w.plainFails++
		fmt.Fprintf(os.Stderr, "[WARN] plain web search failed (%v), trying headless chrome\n", err)
	}

	hrefs, err := w.chromeSearch(query)
	if err != nil {
		return "", err
	}
	if w.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] web search %q: %d results (chrome)\n", query, len(hrefs))
	}
	return pickResult(hrefs, companyName), nil
}

// plainSearch queries DuckDuckGo's HTML endpoint with a simple GET and
// returns the organic result URLs. A bot challenge surfaces as an error.
func (w *WebSearcher) plainSearch(query string) ([]string, error) {
	u := "https://html.duckduckgo.com/html/?kl=de-de&q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(w.parent, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "de-DE,de;q=0.9,en;q=0.8")

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET duckduckgo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("duckduckgo returned HTTP %d (bot challenge)", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	var hrefs []string
	doc.Find("a.result__a").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		if target := unwrapDDGLink(href); target != "" {
			hrefs = append(hrefs, target)
		}
	})
	return hrefs, nil
}

// unwrapDDGLink resolves DuckDuckGo's redirect links
// (//duckduckgo.com/l/?uddg=<url>&rut=...) to the target URL.
func unwrapDDGLink(href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		if u, err := url.Parse(href); err == nil && u.Host != "duckduckgo.com" {
			return href
		}
	}
	if i := strings.Index(href, "duckduckgo.com/l/?"); i >= 0 {
		if u, err := url.Parse(href[i+len("duckduckgo.com/l/"):]); err == nil {
			return u.Query().Get("uddg")
		}
	}
	return ""
}

// chromeSearch runs the query on duckduckgo.com in a headless Chrome, which
// executes the JavaScript that search engines require of plain clients.
func (w *WebSearcher) chromeSearch(query string) ([]string, error) {
	if err := w.startChrome(); err != nil {
		return nil, err
	}

	searchURL := "https://duckduckgo.com/?kl=de-de&q=" + url.QueryEscape(query)
	ctx, cancel := context.WithTimeout(w.browser, 25*time.Second)
	defer cancel()

	var hrefs []string
	err := chromedp.Run(ctx,
		chromedp.Navigate(searchURL),
		chromedp.WaitVisible(`a[data-testid="result-title-a"]`, chromedp.ByQuery),
		chromedp.Evaluate(
			`Array.from(document.querySelectorAll('a[data-testid="result-title-a"]')).map(a => a.href)`,
			&hrefs),
	)
	if err != nil {
		return nil, fmt.Errorf("web search %q: %w", query, err)
	}
	return hrefs, nil
}

// startChrome launches the headless browser once; a failure (e.g. Chrome
// not installed) is remembered and disables the fallback for the run.
func (w *WebSearcher) startChrome() error {
	if w.chromeStarted {
		return w.chromeErr
	}
	w.chromeStarted = true

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", "new"),
		chromedp.UserAgent(userAgent),
		chromedp.WindowSize(1366, 900),
		chromedp.Flag("blink-settings", "imagesEnabled=false"),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(w.parent, opts...)
	browser, cancelBrowser := chromedp.NewContext(allocCtx)
	if err := chromedp.Run(browser); err != nil {
		cancelBrowser()
		cancelAlloc()
		w.chromeErr = fmt.Errorf("start headless chrome: %w", err)
		fmt.Fprintf(os.Stderr, "[WARN] headless chrome unavailable: %v\n", w.chromeErr)
		return w.chromeErr
	}
	w.browser = browser
	w.cancels = []context.CancelFunc{cancelBrowser, cancelAlloc}
	fmt.Fprintf(os.Stderr, "[INFO] headless chrome started for web search\n")
	return nil
}
