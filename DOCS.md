# krile — User & Developer Documentation

**Version:** 0.1.0 (Phase 1 — Static Scraper)

---

## Table of Contents

1. [What is krile?](#1-what-is-krile)
2. [Installation](#2-installation)
3. [Quick Start](#3-quick-start)
4. [CLI Reference](#4-cli-reference)
5. [Output Formats](#5-output-formats)
6. [Go Concepts Guide](#6-go-concepts-guide)
7. [Project Structure Walkthrough](#7-project-structure-walkthrough)
8. [How the Scraper Works (Step by Step)](#8-how-the-scraper-works-step-by-step)
9. [Extending krile](#9-extending-krile)
10. [Troubleshooting](#10-troubleshooting)

---

## 1. What is krile?

`krile` is a command-line tool that searches gelbeseiten.de (German Yellow Pages)
and exports business listings as JSON or CSV. It is also a learning project designed
to teach idiomatic Go by building something real and useful.

**Example use case:**
> "Give me all restaurant listings in Berlin as a CSV file."

```bash
krile --query "Restaurant" --city "Berlin" --format csv --output berlin_restaurants.csv
```

---

## 2. Installation

### Prerequisites
- Go 1.22 or newer — https://go.dev/dl/
- Internet connection

### Build from source

```bash
git clone <repo-url>
cd krile
go build -o krile ./cmd/scraper/
```

On Windows:
```bash
go build -o krile.exe ./cmd/scraper/
```

### Verify
```bash
./krile --version
# krile v0.1.0
```

---

## 3. Quick Start

### Search for restaurants in Berlin, print JSON to terminal
```bash
krile --query "Restaurant" --city "Berlin"
```

### Save results to a file
```bash
krile --query "Arzt" --city "München" --output aerzte.json
```

### Get CSV output
```bash
krile --query "Hotel" --city "Hamburg" --format csv --output hotels.csv
```

### Limit to first 20 results
```bash
krile --query "Bäckerei" --city "Köln" --limit 20
```

### Verbose mode (see what the scraper is doing)
```bash
krile --query "Apotheke" --city "Frankfurt" --verbose
```

---

## 4. CLI Reference

```
krile [flags]

Flags:
  -q, --query   string   What to search for (business category or name)
                         Examples: "Restaurant", "Arzt", "Hotel", "Autowerkstatt"
                         [REQUIRED]

  -c, --city    string   Where to search
                         Examples: "Berlin", "München", "Hamburg 20095"
                         [REQUIRED]

  -o, --output  string   File path to write results to
                         Default: print to stdout (terminal)
                         Example: --output results.json

  -f, --format  string   Output format
                         Choices: json | csv
                         Default: json

  -l, --limit   int      Stop after this many results (0 = fetch all)
                         Default: 0

  -d, --delay   int      Milliseconds to wait between page requests
                         Default: 1000
                         Minimum recommended: 500

  -v, --verbose          Print detailed logs to stderr while running

      --version          Print version number and exit
      --help             Show this help text and exit
```

---

## 5. Output Formats

### JSON (default)

Outputs a JSON array. Each element is one business listing.

```json
[
  {
    "name": "Restaurant Zur Linde",
    "category": "Gaststätten und Restaurants",
    "street": "Hauptstraße 12",
    "postal_code": "10115",
    "city": "Berlin",
    "phone": "030 123456",
    "website": "https://zurlinde.de",
    "email": "",
    "rating": 4.3,
    "review_count": 87,
    "hours": "Geöffnet – Schließt um 23:00",
    "source_url": "https://www.gelbeseiten.de/gsbiz/abc123",
    "scraped_at": "2026-03-21T14:05:00Z"
  }
]
```

**Fields explained:**

| Field          | Description                                          |
|----------------|------------------------------------------------------|
| `name`         | Business display name                                |
| `category`     | Business type/category                               |
| `street`       | Street name and number                               |
| `postal_code`  | German ZIP code (5 digits)                           |
| `city`         | City name                                            |
| `phone`        | Phone number as displayed on site                    |
| `website`      | External website URL (empty if not listed)           |
| `email`        | Email address (empty if not listed)                  |
| `rating`       | Average star rating (0.0–5.0, 0 if no ratings)      |
| `review_count` | Number of user reviews (0 if none)                   |
| `hours`        | Opening hours status text                            |
| `source_url`   | Direct link to the listing on gelbeseiten.de         |
| `scraped_at`   | UTC timestamp when this record was scraped           |

### CSV

First row is a header. One business per row. Empty fields are empty strings.

```csv
name,category,street,postal_code,city,phone,website,email,rating,review_count,hours,source_url,scraped_at
"Restaurant Zur Linde","Gaststätten und Restaurants","Hauptstraße 12","10115","Berlin","030 123456","https://zurlinde.de","",4.3,87,"Geöffnet – Schließt um 23:00","https://www.gelbeseiten.de/gsbiz/abc123","2026-03-21T14:05:00Z"
```

---

## 6. Go Concepts Guide

This section explains every major Go concept used in `krile`, in the order you
will encounter them while building the project.

---

### 6.1 Packages and Modules

Every Go file starts with a `package` declaration. A **module** is a collection
of packages with a shared version (defined in `go.mod`).

```go
// go.mod — the module root
module github.com/genjerator/krile

go 1.22
```

```go
// cmd/scraper/main.go
package main  // executable packages must be named "main"
```

```go
// internal/models/business.go
package models  // library package
```

- `internal/` is a special Go convention: packages inside it can only be imported
  by code within the same module. This enforces encapsulation.

---

### 6.2 Structs

A `struct` groups related data together — like a class with only fields, no methods.

```go
type Business struct {
    Name  string
    Phone string
    Rating float64
}
```

**Struct tags** are string metadata attached to fields, used by JSON/CSV encoders:

```go
type Business struct {
    Name  string  `json:"name"  csv:"name"`
    Phone string  `json:"phone" csv:"phone"`
}
```

`json:"name"` tells `encoding/json` to use the key `"name"` (not `"Name"`) when
marshaling/unmarshaling.

**Creating a struct:**
```go
b := Business{Name: "Restaurant Zur Linde", Phone: "030 123456"}
b2 := Business{}  // zero value: all fields are empty string / 0 / false
```

---

### 6.3 Functions and Multiple Return Values

Go functions can return multiple values. The idiomatic pattern is `(result, error)`:

```go
func Fetch(url string) (string, error) {
    // ...
    return html, nil     // success: return value + nil error
    return "", err       // failure: return zero value + error
}
```

**Calling:**
```go
html, err := Fetch("https://example.com")
if err != nil {
    log.Fatal(err)  // or handle gracefully
}
// use html here
```

> In Go, you must handle errors explicitly. There are no exceptions.

---

### 6.4 Interfaces

An **interface** defines behaviour (a set of method signatures). Any type that
implements those methods satisfies the interface — no `implements` keyword needed.

```go
// Writer interface — anything that can write businesses
type Writer interface {
    Write(businesses []models.Business) error
}

// JSONWriter satisfies Writer
type JSONWriter struct{ dest io.Writer }
func (w JSONWriter) Write(businesses []models.Business) error { ... }

// CSVWriter satisfies Writer
type CSVWriter struct{ dest io.Writer }
func (w CSVWriter) Write(businesses []models.Business) error { ... }
```

Usage:
```go
var w Writer
if format == "csv" {
    w = CSVWriter{dest: file}
} else {
    w = JSONWriter{dest: file}
}
w.Write(results)  // same call regardless of format
```

---

### 6.5 Error Handling

Go has no exceptions. Errors are values of type `error` (a built-in interface).

```go
// Creating an error
err := fmt.Errorf("HTTP %d from %s", statusCode, url)

// Wrapping an error (adds context while preserving the original)
err = fmt.Errorf("fetching page: %w", err)

// Checking error type
if errors.Is(err, io.EOF) { ... }
```

The `%w` verb in `fmt.Errorf` wraps the error so callers can use `errors.Is()`
to check the original cause, even through layers of wrapping.

---

### 6.6 net/http — Making HTTP Requests

```go
// Simple GET request
resp, err := http.Get("https://www.gelbeseiten.de/Suche/Restaurant/Berlin")
if err != nil {
    return err
}
defer resp.Body.Close()  // always close the response body

body, err := io.ReadAll(resp.Body)
```

**`defer`** schedules `resp.Body.Close()` to run when the surrounding function
returns — it always runs, even if there's a panic or early return.

**Custom client with timeout:**
```go
client := &http.Client{Timeout: 30 * time.Second}
req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
req.Header.Set("User-Agent", "krile/0.1 (+https://github.com/genjerator/krile)")
resp, err := client.Do(req)
```

---

### 6.7 goquery — HTML Parsing

`goquery` lets you query HTML with CSS selectors, similar to jQuery.

```go
import "github.com/PuerkitoBio/goquery"

doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
if err != nil { ... }

// Find all listing cards
doc.Find("article.listing").Each(func(i int, s *goquery.Selection) {
    name := s.Find(".name").Text()
    phone := s.Find("a[href^='tel:']").AttrOr("href", "")[4:] // strip "tel:"
    fmt.Println(name, phone)
})
```

Key methods:
| Method              | What it does                                      |
|---------------------|---------------------------------------------------|
| `.Find(selector)`   | Returns matching child elements                   |
| `.Text()`           | Returns inner text (strips HTML tags)             |
| `.Attr("href")`     | Returns (value, exists bool) for an attribute     |
| `.AttrOr("x","y")`  | Returns attribute value or default "y"            |
| `.Each(func)`       | Iterates over matched elements                    |
| `.First()`          | Returns only the first matched element            |
| `.Length()`         | Count of matched elements                         |

---

### 6.8 Context and Timeouts

`context.Context` is Go's mechanism for passing deadlines and cancellation signals.

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()  // always cancel to free resources

req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
```

If the request takes more than 30 seconds, it is automatically cancelled.

---

### 6.9 Goroutines and WaitGroups (Phase 1 bonus / Phase 2)

A **goroutine** is a lightweight thread managed by the Go runtime.

```go
go func() {
    fmt.Println("I run concurrently")
}()
```

A **WaitGroup** lets the main goroutine wait for others to finish:

```go
var wg sync.WaitGroup

for _, url := range urls {
    wg.Add(1)
    go func(u string) {
        defer wg.Done()
        scrape(u)
    }(url)
}

wg.Wait()  // blocks until all goroutines call Done()
```

---

### 6.10 encoding/json

```go
// Struct → JSON bytes
data, err := json.MarshalIndent(businesses, "", "  ")

// JSON bytes → Struct
var b Business
err := json.Unmarshal(data, &b)

// Stream encoding (efficient for large slices)
enc := json.NewEncoder(os.Stdout)
enc.SetIndent("", "  ")
enc.Encode(businesses)
```

---

### 6.11 encoding/csv

```go
w := csv.NewWriter(os.Stdout)

// Write header
w.Write([]string{"name", "phone", "city"})

// Write rows
for _, b := range businesses {
    w.Write([]string{b.Name, b.Phone, b.City})
}

w.Flush()  // must flush or data may be lost
if err := w.Error(); err != nil { ... }
```

---

## 7. Project Structure Walkthrough

```
krile/
│
├── cmd/scraper/main.go
│   Purpose: Entry point. Parse CLI flags, create Config, call scraper.Run().
│   Go concepts: package main, func main(), flag package, os.Exit
│
├── internal/models/business.go
│   Purpose: Define the Business data type.
│   Go concepts: struct, field tags, time.Time
│
├── internal/config/config.go
│   Purpose: Hold all user-provided settings in one place.
│   Go concepts: struct, string/int/bool fields, validation
│
├── internal/scraper/fetcher.go
│   Purpose: HTTP client. Fetch a URL → return HTML string.
│   Go concepts: net/http, context, defer, retry loop, time.Sleep
│
├── internal/parser/parser.go
│   Purpose: Parse HTML → extract []Business.
│   Go concepts: goquery, method receivers, string manipulation, strconv
│
├── internal/paginator/paginator.go
│   Purpose: Extract the URL of the next results page.
│   Go concepts: return multiple values, string contains, URL building
│
├── internal/scraper/scraper.go
│   Purpose: Orchestrate the whole flow: fetch → parse → paginate → write.
│   Go concepts: for loop, interfaces, passing functions
│
└── internal/output/
    ├── writer.go        Interface definition
    ├── json_writer.go   JSON implementation
    └── csv_writer.go    CSV implementation
    Go concepts: interfaces, io.Writer, encoding/json, encoding/csv
```

---

## 8. How the Scraper Works (Step by Step)

```
User runs: krile --query "Restaurant" --city "Berlin"
           │
           ▼
1. main.go parses flags → creates Config{Query:"Restaurant", City:"Berlin", ...}
           │
           ▼
2. scraper.Run(config) starts
           │
           ▼
3. Build URL: "https://www.gelbeseiten.de/Suche/Restaurant/Berlin"
           │
           ▼
4. fetcher.Fetch(url) → sends HTTP GET with User-Agent header
           │             waits for response (max 30s)
           │             reads response body as string
           ▼
5. parser.Parse(html) → uses goquery to find listing cards
           │             extracts fields from each card
           │             returns []Business
           ▼
6. Append results to master slice
           │
           ▼
7. paginator.NextURL(html) → finds the "next page" link
           │                 if found: sleep(delay), go to step 4
           │                 if not found: all pages done
           ▼
8. writer.Write(allBusinesses) → encodes to JSON or CSV
           │                     writes to file or stdout
           ▼
9. Done. Exit 0.
```

---

## 9. Extending krile

### Adding a new output format (e.g. SQLite)

1. Create `internal/output/sqlite_writer.go`
2. Define a `SQLiteWriter` struct that implements the `Writer` interface
3. Register it in `main.go` under `--format sqlite`

No other files need to change — this is the power of interfaces.

### Adding a new target site (e.g. yelp.com)

1. Create `internal/parser/yelp_parser.go` with site-specific CSS selectors
2. Add `--site yelp` flag to `main.go`
3. `scraper.go` chooses the right parser based on `config.Site`

---

## 10. Troubleshooting

### "No results found"
- The site may have changed its HTML structure. Run with `--verbose` and inspect the raw HTML.
- The site may be blocking the request. Try increasing `--delay`.

### "context deadline exceeded"
- Network is slow or the site is timing out. The default timeout is 30s per page.
- Phase 2 (chromedp) has better handling for slow JS-heavy pages.

### "unexpected end of JSON input"
- The response body was truncated. Could be a network issue or server-side truncation.
- Retry the request manually with `curl` to confirm.

### Results are empty strings for many fields
- gelbeseiten.de renders some data via JavaScript. Phase 1 (static scraper) will
  miss JavaScript-rendered content. Phase 2 (chromedp) solves this.

---

*Documentation generated alongside SPEC.md — update both when behaviour changes.*
