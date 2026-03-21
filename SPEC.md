# Technical Specification — gelbeseiten Scraper

**Project:** `krile`
**Language:** Go 1.22+
**Status:** Draft
**Author:** Genjerator

---

## 1. Overview

`krile` is a command-line web scraper written in Go that extracts business listing
data from gelbeseiten.de (German Yellow Pages). It is designed as a learning project
that progressively introduces Go concepts, starting with a static HTTP scraper and
evolving into a headless-browser scraper.

---

## 2. Goals

### Primary
- Extract structured business data (name, address, phone, rating, hours, website) from gelbeseiten.de
- Output results as JSON or CSV
- Be a vehicle for learning idiomatic Go

### Secondary
- Respectful scraping: rate limiting, robots.txt awareness, proper User-Agent
- Extensible to other directory sites with minimal changes

### Non-goals (out of scope)
- Login / account scraping
- CAPTCHA solving
- Bypassing paywalls or access controls
- Real-time monitoring / scheduling

---

## 3. Functional Requirements

### FR-1: Search
- User specifies a search query (business category) and a city/location
- Scraper fetches results from: `https://www.gelbeseiten.de/Suche/{query}/{city}`

### FR-2: Data Extraction
Each business listing must extract the following fields (where available):

| Field         | Type     | Source               | Required |
|---------------|----------|----------------------|----------|
| `Name`        | string   | Listing title        | Yes      |
| `Category`    | string   | Category label       | No       |
| `Street`      | string   | Address line 1       | No       |
| `PostalCode`  | string   | ZIP code             | No       |
| `City`        | string   | City name            | No       |
| `Phone`       | string   | Phone number         | No       |
| `Website`     | string   | External URL         | No       |
| `Email`       | string   | Email address        | No       |
| `Rating`      | float64  | Star rating (0-5)    | No       |
| `ReviewCount` | int      | Number of reviews    | No       |
| `Hours`       | string   | Opening hours status | No       |
| `SourceURL`   | string   | Listing detail URL   | Yes      |
| `ScrapedAt`   | time.Time| Timestamp of scrape  | Yes      |

### FR-3: Pagination
- Automatically follow pagination until results are exhausted or `--limit` is reached
- gelbeseiten.de shows 50 results per page

### FR-4: Output
- Write results to stdout (default) or to a file (`--output` flag)
- Supported formats: `json`, `csv` (selected via `--format` flag)
- JSON output: newline-delimited JSON (one object per line) or pretty-printed array
- CSV output: header row + one row per business

### FR-5: CLI Interface
```
krile [flags]

Flags:
  -q, --query    string   Business category to search (e.g. "Restaurant")   [required]
  -c, --city     string   City or location to search in (e.g. "Berlin")     [required]
  -o, --output   string   Output file path (default: stdout)
  -f, --format   string   Output format: json | csv (default: "json")
  -l, --limit    int      Maximum number of results to fetch (default: 0 = all)
  -d, --delay    int      Delay between requests in milliseconds (default: 1000)
  -v, --verbose           Enable verbose logging
      --version           Print version and exit
```

### FR-6: Rate Limiting
- Minimum configurable delay between HTTP requests (default: 1000ms)
- Jitter: ±20% random variance on delay to appear more natural

### FR-7: Error Handling
- Network errors: retry up to 3 times with exponential backoff
- Parsing errors: log and skip the problematic listing, continue
- Fatal errors (invalid flags, unreachable host): exit with code 1 and clear message

---

## 4. Non-Functional Requirements

### NFR-1: Performance
- Phase 1: Sequential scraping (simple, predictable)
- Phase 2: Optional concurrent scraping with configurable worker count (`--workers`)

### NFR-2: Reliability
- Graceful shutdown on SIGINT / SIGTERM (flush partial results before exit)
- Context-based timeout on all HTTP requests (default: 30s per request)

### NFR-3: Observability
- `--verbose` flag enables per-request logging to stderr
- Log format: `[LEVEL] timestamp message`
- Errors always printed to stderr, results always to stdout/file

### NFR-4: Ethics & Legality
- `User-Agent` header identifies the scraper honestly
- Scraper checks and respects `robots.txt` (Phase 2)
- No credential bypass, CAPTCHA solving, or fingerprint spoofing

---

## 5. Architecture

### Phase 1 — Static HTTP Scraper

```
main.go
  └─ CLI parsing (flag / cobra)
       └─ scraper.Run(config)
            ├─ fetcher.Fetch(url) → raw HTML          [net/http]
            ├─ parser.Parse(html) → []Business         [goquery]
            ├─ paginator.NextURL(html) → url | ""
            └─ writer.Write([]Business, format, dest)  [encoding/json, encoding/csv]
```

**Data flow:**
```
URL → HTTP GET → HTML string → CSS selectors → []Business struct → JSON/CSV
```

### Phase 2 — Headless Browser Scraper

```
main.go
  └─ scraper.Run(config)
       ├─ browser.NewSession()                          [chromedp]
       │    └─ Navigate → WaitVisible → GetHTML
       ├─ parser.Parse(html) → []Business               [goquery]
       └─ writer.Write(...)
```

**Data flow:**
```
URL → chromedp → rendered HTML → CSS selectors → []Business struct → JSON/CSV
```

---

## 6. Module Structure

```
krile/
├── cmd/
│   └── scraper/
│       └── main.go            # Entry point: parse flags, call scraper.Run()
├── internal/
│   ├── models/
│   │   └── business.go        # Business struct + JSON/CSV tags
│   ├── config/
│   │   └── config.go          # Config struct from CLI flags
│   ├── scraper/
│   │   ├── scraper.go         # Orchestrates fetch → parse → paginate → write
│   │   └── fetcher.go         # HTTP client wrapper (headers, timeout, retry)
│   ├── parser/
│   │   └── parser.go          # goquery-based HTML → []Business
│   ├── paginator/
│   │   └── paginator.go       # Extracts next-page URL from HTML
│   └── output/
│       ├── writer.go          # Interface: Writer
│       ├── json_writer.go     # JSON implementation
│       └── csv_writer.go      # CSV implementation
├── go.mod
├── go.sum
├── SPEC.md                    # This file
└── DOCS.md                    # User documentation
```

---

## 7. Key Go Concepts Per Module

| Module         | Go Concepts Introduced                                      |
|----------------|-------------------------------------------------------------|
| `models`       | `struct`, field tags (`json:"..."`, `csv:"..."`)           |
| `config`       | value types, string/int/bool fields, default values        |
| `fetcher`      | `net/http`, `http.Client`, headers, `context`, retry loop  |
| `parser`       | `goquery`, method receivers, string manipulation            |
| `paginator`    | return multiple values, pointer vs value                    |
| `scraper`      | interfaces, `for` loops, `time.Sleep`, goroutines (Ph.2)   |
| `output`       | interfaces (`io.Writer`), type switch, `encoding/json`      |
| `main`         | `os.Args`, `flag` package, `log`, `os.Exit`                |

---

## 8. Data Model Detail

```go
// internal/models/business.go

type Business struct {
    Name        string    `json:"name"         csv:"name"`
    Category    string    `json:"category"     csv:"category"`
    Street      string    `json:"street"       csv:"street"`
    PostalCode  string    `json:"postal_code"  csv:"postal_code"`
    City        string    `json:"city"         csv:"city"`
    Phone       string    `json:"phone"        csv:"phone"`
    Website     string    `json:"website"      csv:"website"`
    Email       string    `json:"email"        csv:"email"`
    Rating      float64   `json:"rating"       csv:"rating"`
    ReviewCount int       `json:"review_count" csv:"review_count"`
    Hours       string    `json:"hours"        csv:"hours"`
    SourceURL   string    `json:"source_url"   csv:"source_url"`
    ScrapedAt   time.Time `json:"scraped_at"   csv:"scraped_at"`
}
```

---

## 9. CSS Selectors (gelbeseiten.de)

> These selectors are based on the current site structure and may require updates
> if the site redesigns its HTML.

| Data          | Selector (to be verified during implementation)           |
|---------------|-----------------------------------------------------------|
| Listing card  | `article[data-wipe-name]` or `[class*="mod-Treffer"]`    |
| Name          | `[class*="mod-Treffer__name"]` or `h2 > span`            |
| Address       | `[class*="mod-Treffer__address"]` or `address`           |
| Phone         | `[class*="mod-Treffer__telefon"]` or `a[href^="tel:"]`   |
| Website       | `a[href^="http"]:not([href*="gelbeseiten"])` (external)  |
| Rating        | `[itemprop="ratingValue"]` or `[class*="rating"]`        |
| Review count  | `[itemprop="reviewCount"]`                               |
| Hours         | `[class*="oeffnungszeiten"]` or `[class*="hours"]`       |
| Next page     | `a[rel="next"]` or pagination link                       |

> Note: Selectors must be validated by inspecting live HTML during implementation.

---

## 10. Error Codes

| Exit Code | Meaning                              |
|-----------|--------------------------------------|
| 0         | Success                              |
| 1         | Invalid CLI arguments                |
| 2         | Network / HTTP error (unrecoverable) |
| 3         | Parse error (all pages failed)       |
| 4         | Output write error                   |

---

## 11. Development Phases

### Phase 1 — Static Scraper (Foundation)
**Goal:** Working scraper for pages that render HTML server-side.

- [ ] Project scaffold + `go.mod`
- [ ] `Business` struct with tags
- [ ] `Config` struct + flag parsing
- [ ] HTTP fetcher with User-Agent and timeout
- [ ] goquery parser (best-effort, partial data ok)
- [ ] Pagination loop
- [ ] JSON + CSV writers
- [ ] Rate limiting
- [ ] Retry on failure
- [ ] `--verbose` logging

### Phase 2 — Headless Browser (Dynamic content)
**Goal:** Handle JavaScript-rendered pages.

- [ ] Integrate `chromedp`
- [ ] Replace static `Fetch()` with `browser.Fetch()`
- [ ] Context + timeout management
- [ ] Optional: `--workers N` for concurrent fetching
- [ ] Optional: `robots.txt` checker

### Phase 3 — Extensions (Optional)
- [ ] Detail page scraping (fetch individual listing page for more data)
- [ ] SQLite output format
- [ ] Proxy support
- [ ] Config file (YAML/TOML) instead of CLI flags

---

## 12. Dependencies

| Package                        | Purpose                  | Phase |
|--------------------------------|--------------------------|-------|
| `github.com/PuerkitoBio/goquery` | HTML parsing           | 1     |
| `github.com/chromedp/chromedp`   | Headless browser       | 2     |
| Standard library only for:       | HTTP, JSON, CSV, flags | 1     |

No web framework, no ORM, no heavy dependencies.

---

## 13. Testing Strategy

| Layer     | Approach                                                   |
|-----------|------------------------------------------------------------|
| Parser    | Unit tests with fixture HTML files (no network needed)     |
| Fetcher   | Mock HTTP server using `net/http/httptest`                 |
| Scraper   | Integration test against mock server                       |
| Output    | Table-driven tests comparing output bytes                  |

---

## 14. Versioning

```
v0.1.0 — Phase 1 complete (static scraper)
v0.2.0 — Phase 2 complete (headless browser)
v0.3.0 — Phase 3 extensions
```
