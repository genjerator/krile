# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Database Rules

**CRITICAL: Never modify the database schema or structure in this project.**

- Do NOT create, alter, or drop tables, indexes, or constraints
- Do NOT run any database migrations
- All duplicate prevention and data validation must be handled at the application level

The database schema is managed separately by the Laravel application. This scraper only writes data to existing tables.

## Build & Run

```bash
# Build
go build -o krile.exe ./cmd/scraper/

# Run (query and city are required)
./krile.exe -q "Restaurant" -c "Berlin"
./krile.exe -q "Hotel" -c "Hamburg" -f csv -o results.csv
./krile.exe -q "Arzt" -c "München" -f postgres --update-existing

# Test
go test ./...
go test ./internal/parser/...
```

## Key CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-q, --query` | required | Business category (e.g. "Restaurant") |
| `-c, --city` | required | City to search (e.g. "Berlin") |
| `-f, --format` | json | `json`, `csv`, `xlsx`, `postgres` |
| `-o, --output` | stdout | Output file path |
| `-l, --limit` | 0 (all) | Max pagination steps |
| `-d, --delay` | 1000 | Delay between requests in ms |
| `--update-existing` | false | Update records by source_url instead of skipping |
| `--db-table` | companies | PostgreSQL table (also env: `DB_TABLE`) |

DB credentials default from `.env` (see `.env.example`).

## Architecture

End-to-end flow:

```
main.go → Config → scraper.Run()
  → Fetcher (HTTP POST to gelbeseiten.de/ajaxsuche)
  → parser.ParseDebug() → []Business
  → For each business without email: FetchDetailPage() → ExtractEmailFromDetailPage()
  → writer.Write(businesses)
  → Paginate (position += 10) until no more results
  → writer.Flush()
```

**Fetcher** (`internal/scraper/fetcher.go`): Sends AJAX POST requests to `https://www.gelbeseiten.de/ajaxsuche` with form data (`WAS`, `WO`, `position`, `anzahl`). Also fetches individual detail pages for email extraction.

**Parser** (`internal/parser/parser.go`): Uses `goquery` CSS selectors against the AJAX HTML response. Email extraction has 3-tier fallback: (1) chat button `data-parameters` JSON path, (2) `data-link="mailto:..."` attribute, (3) text search for `@`.

**PostgreSQL Writer** (`internal/output/postgres_writer.go`): Auto-detects table schema (UUID vs SERIAL primary key, presence of `updated_at`). Deduplicates by email (if present) or name. In `--update-existing` mode, matches by `source_url` and runs UPDATE. Uses pgx batch API for bulk operations.

**Output factory** (`internal/output/writer.go`): Returns the correct writer implementation based on `--format`. All writers implement `Write([]Business) error` and `Flush() error`.

## Data Model

`internal/models/business.go` — `Business` struct fields: `Name`, `Category`, `Street`, `PostalCode`, `City`, `Phone`, `Website`, `Email`, `SourceURL`, `ScrapedAt`. All fields are strings except `ScrapedAt` (time.Time).
