# Claude Instructions for Krile Project

## Database Rules

**CRITICAL: Never modify the database schema or structure in this project.**

- Do NOT create, alter, or drop tables
- Do NOT create, alter, or drop indexes
- Do NOT create, alter, or drop constraints
- Do NOT run any database migrations
- All duplicate prevention and data validation must be handled at the application level

The database schema is managed separately by the Laravel application. This scraper only writes data to existing tables.

## Project Overview

Krile is a web scraper for gelbeseiten.de (German Yellow Pages) that extracts business information and saves it to various formats including PostgreSQL.

### Key Files
- `cmd/scraper/main.go` - CLI entry point
- `internal/parser/parser.go` - HTML parsing logic
- `internal/output/postgres_writer.go` - PostgreSQL writer
- `internal/scraper/scraper.go` - Main scraping orchestration

### Output Formats
- JSON (stdout or file)
- CSV
- Excel (XLSX)
- PostgreSQL (direct database insert to companies table)

## Development Guidelines

1. Keep code simple and focused on scraping
2. Handle duplicates at the application level
3. Use environment variables from .env for database credentials
4. Follow existing code patterns and structure
