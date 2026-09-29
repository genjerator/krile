package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/genjerator/krile/internal/config"
	"github.com/genjerator/krile/internal/scraper"
	"github.com/genjerator/krile/internal/webui"
	"github.com/joho/godotenv"
)

const version = "0.1.0"

func main() {
	// Load environment files. KRILE_ENV selects the profile (development |
	// production | test), defaulting to development for safe local behaviour
	// (mailpit, not real SES). The env-specific file is listed first so it wins:
	// godotenv.Load never overrides an already-set key, so ".env.<env>" takes
	// precedence over the shared ".env" (DB and other common settings).
	appEnv := os.Getenv("KRILE_ENV")
	if appEnv == "" {
		appEnv = "development"
	}
	_ = godotenv.Load(".env."+appEnv, ".env")

	var cfg config.Config
	var showVersion bool

	// Get defaults from environment variables
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnvInt("DB_PORT", 5432)
	dbName := getEnv("DB_NAME", "krile")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "")
	dbTable := getEnv("DB_TABLE", "companies")

	// SMTP defaults from environment (send-emails mode)
	smtpHost := getEnv("SMTP_HOST", "")
	smtpPort := getEnvInt("SMTP_PORT", 587)
	smtpUser := getEnv("SMTP_USER", "")
	smtpPassword := getEnv("SMTP_PASSWORD", "")
	smtpFrom := getEnv("SMTP_FROM", "")
	smtpFromName := getEnv("SMTP_FROM_NAME", "")
	smtpTLS := getEnv("SMTP_TLS", "")

	flag.StringVar(&cfg.Query, "q", "", "Business category to search (e.g. \"Restaurant\") [required]")
	flag.StringVar(&cfg.Query, "query", "", "Business category to search (e.g. \"Restaurant\") [required]")
	flag.StringVar(&cfg.City, "c", "", "City or location (e.g. \"Berlin\") [optional]")
	flag.StringVar(&cfg.City, "city", "", "City or location (e.g. \"Berlin\") [optional]")
	flag.StringVar(&cfg.ExcelPath, "excel", "", "Excel lookup mode: fill emails for companies in this address export .xlsx")
	flag.StringVar(&cfg.GuessPath, "guess", "", "Email-guess mode: generate candidate emails from Vorname/Name columns in this .csv/.xlsx")
	flag.StringVar(&cfg.Patterns, "patterns", "", "Email-guess mode: comma-separated local-part patterns to generate (default: all). E.g. \"name.vorname\" or \"vorname.name,name.vorname\"")
	flag.StringVar(&cfg.GuessProviders, "providers", "", "Email-guess mode: comma-separated providers to generate (default: all). E.g. \"gmx.de,t-online.de\"")
	flag.StringVar(&cfg.Output, "o", "", "Output file path (default: auto-named file in export/)")
	flag.StringVar(&cfg.Output, "output", "", "Output file path (default: auto-named file in export/)")
	flag.StringVar(&cfg.Format, "f", "json", "Output format: json | csv | xlsx | postgres")
	flag.StringVar(&cfg.Format, "format", "json", "Output format: json | csv | xlsx | postgres")
	flag.IntVar(&cfg.Limit, "l", 0, "Max number of 'Mehr Anzeigen' clicks (0 = all)")
	flag.IntVar(&cfg.Limit, "limit", 0, "Max number of 'Mehr Anzeigen' clicks (0 = all)")
	flag.IntVar(&cfg.StartPosition, "start-position", 0, "Resume scraping from this result position (from a previous run's 'position=' log; 0 = start)")
	flag.IntVar(&cfg.StartPage, "start-page", 0, "Resume scraping from this page number (from a previous run's 'page X/Y' log; 0 = start). Ignored if --start-position is set")
	flag.IntVar(&cfg.Delay, "d", 1000, "Delay between requests in milliseconds")
	flag.IntVar(&cfg.Delay, "delay", 1000, "Delay between requests in milliseconds")
	flag.IntVar(&cfg.Distance, "r", 0, "Search radius in meters (0 = no restriction, e.g. 50000 = 50km)")
	flag.IntVar(&cfg.Distance, "radius", 0, "Search radius in meters (0 = no restriction, e.g. 50000 = 50km)")
	flag.IntVar(&cfg.Workers, "w", 8, "Concurrent workers for email enrichment")
	flag.IntVar(&cfg.Workers, "workers", 8, "Concurrent workers for email enrichment")
	flag.BoolVar(&cfg.WebSearch, "websearch", true, "Web-search fallback (DuckDuckGo) to find the company website when no email is found")
	flag.BoolVar(&cfg.RecheckEmails, "recheck-emails", false, "Recheck mode: re-run website + web-search enrichment for DB companies with empty email (no gelbeseiten scraping)")
	flag.BoolVar(&cfg.CheckWebsites, "check-websites", false, "Check-websites mode: classify each company website (ok/dead/404/parked) into a CSV report (uses DB flags/.env)")
	flag.BoolVar(&cfg.Serve, "serve", false, "Serve mode: start a local web page (127.0.0.1) showing DB email-coverage stats and companies")
	flag.IntVar(&cfg.Port, "port", 8080, "Port for --serve web UI")

	// Send-emails mode
	flag.BoolVar(&cfg.SendEmails, "send-emails", false, "Send-emails mode: email the generated subject/personalized_email to DB companies (dry-run unless --confirm-send)")
	flag.BoolVar(&cfg.ConfirmSend, "confirm-send", false, "Safety switch: actually send. WITHOUT this, --send-emails only dry-runs (prints what it would send)")
	flag.IntVar(&cfg.DailyLimit, "daily-limit", 10, "Send-emails mode: max emails to send per calendar day (counts rows already sent today)")
	flag.StringVar(&cfg.TestTo, "test-to", "", "Send-emails mode: redirect every message to this address (rows are NOT marked sent)")
	flag.StringVar(&cfg.SendEmailID, "email-id", "", "Send-emails mode: send only this one company_emails row (its uuid); ignores the daily limit")
	flag.StringVar(&cfg.UTMCampaign, "utm-campaign", getEnv("UTM_CAMPAIGN", ""), "Send-emails mode: when set, append utm_* params to planetaindustries.de links (utm_content = the email's uuid). Empty = no tagging (env: UTM_CAMPAIGN)")
	flag.StringVar(&cfg.UTMSource, "utm-source", getEnv("UTM_SOURCE", "email"), "Send-emails mode: utm_source value for link tagging (env: UTM_SOURCE)")
	flag.StringVar(&cfg.UTMMedium, "utm-medium", getEnv("UTM_MEDIUM", "cold-outreach"), "Send-emails mode: utm_medium value for link tagging (env: UTM_MEDIUM)")
	flag.StringVar(&cfg.SESConfigSet, "ses-config-set", getEnv("SES_CONFIGURATION_SET", ""), "Send-emails mode: SES configuration set name; tags each message so SES tracks opens/clicks (env: SES_CONFIGURATION_SET)")
	flag.StringVar(&cfg.UnsubscribeMailto, "unsubscribe-mailto", getEnv("UNSUBSCRIBE_MAILTO", ""), "Send-emails mode: address for a List-Unsubscribe (mailto:) header so clients show a native unsubscribe button (env: UNSUBSCRIBE_MAILTO)")
	flag.StringVar(&cfg.SMTPHost, "smtp-host", smtpHost, "SMTP host (env: SMTP_HOST)")
	flag.IntVar(&cfg.SMTPPort, "smtp-port", smtpPort, "SMTP port: 587=STARTTLS, 465=implicit TLS (env: SMTP_PORT)")
	flag.StringVar(&cfg.SMTPUser, "smtp-user", smtpUser, "SMTP username (env: SMTP_USER)")
	flag.StringVar(&cfg.SMTPPassword, "smtp-password", smtpPassword, "SMTP password / app password (env: SMTP_PASSWORD)")
	flag.StringVar(&cfg.SMTPFrom, "smtp-from", smtpFrom, "From address (env: SMTP_FROM; defaults to SMTP_USER)")
	flag.StringVar(&cfg.SMTPFromName, "smtp-from-name", smtpFromName, "From display name (env: SMTP_FROM_NAME)")
	flag.StringVar(&cfg.SMTPTLS, "smtp-tls", smtpTLS, "SMTP TLS policy: ssl | starttls | none (default: auto — ssl for port 465, else STARTTLS). Use \"none\" for local mailpit")
	flag.BoolVar(&cfg.Verbose, "v", false, "Enable verbose logging")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&cfg.Debug, "debug", false, "Dump raw HTML responses and detailed debug info to stderr")
	flag.BoolVar(&showVersion, "version", false, "Print version and exit")

	// PostgreSQL flags (with defaults from environment)
	flag.StringVar(&cfg.DBHost, "db-host", dbHost, "PostgreSQL host (env: DB_HOST)")
	flag.IntVar(&cfg.DBPort, "db-port", dbPort, "PostgreSQL port (env: DB_PORT)")
	flag.StringVar(&cfg.DBName, "db-name", dbName, "PostgreSQL database name (env: DB_NAME)")
	flag.StringVar(&cfg.DBUser, "db-user", dbUser, "PostgreSQL user (env: DB_USER)")
	flag.StringVar(&cfg.DBPassword, "db-password", dbPassword, "PostgreSQL password (env: DB_PASSWORD)")
	flag.StringVar(&cfg.DBTable, "db-table", dbTable, "PostgreSQL table name (env: DB_TABLE)")
	flag.BoolVar(&cfg.UpdateExisting, "update-existing", false, "Update existing records with same source_url instead of skipping")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: krile [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fmt.Fprintf(os.Stderr, "  -q, --query   string  Business category to search (e.g. \"Restaurant\") [required]\n")
		fmt.Fprintf(os.Stderr, "  -c, --city    string  City or location (e.g. \"Berlin\") [optional]\n")
		fmt.Fprintf(os.Stderr, "      --excel   string  Excel lookup mode: fill emails for companies in this .xlsx\n")
		fmt.Fprintf(os.Stderr, "                        (replaces -q/-c; -o = output copy, -l = max companies)\n")
		fmt.Fprintf(os.Stderr, "      --guess   string  Email-guess mode: generate candidate emails from a .csv/.xlsx\n")
		fmt.Fprintf(os.Stderr, "                        with Vorname/Name columns (replaces -q/-c; -o = output CSV)\n")
		fmt.Fprintf(os.Stderr, "  -o, --output  string  Output file path (default: auto-named file in export/)\n")
		fmt.Fprintf(os.Stderr, "  -f, --format  string  Output format: json | csv | xlsx | postgres (default: json)\n")
		fmt.Fprintf(os.Stderr, "  -l, --limit   int     Max results to fetch (0 = all)\n")
		fmt.Fprintf(os.Stderr, "  -d, --delay   int     Delay between requests in ms (default: 1000)\n")
		fmt.Fprintf(os.Stderr, "  -r, --radius  int     Search radius in meters (0 = no restriction, e.g. 50000 = 50km)\n")
		fmt.Fprintf(os.Stderr, "  -w, --workers int     Concurrent workers for email enrichment (default: 8)\n")
		fmt.Fprintf(os.Stderr, "      --websearch       Web-search fallback for missing emails (default: true, disable with --websearch=false)\n")
		fmt.Fprintf(os.Stderr, "  -v, --verbose         Enable verbose logging\n")
		fmt.Fprintf(os.Stderr, "      --debug           Dump raw HTML and detailed debug info to stderr\n")
		fmt.Fprintf(os.Stderr, "      --version         Print version and exit\n")
		fmt.Fprintf(os.Stderr, "\nPostgreSQL flags (only needed when --format=postgres):\n")
		fmt.Fprintf(os.Stderr, "  --db-host          string  Database host (env: DB_HOST, default: localhost)\n")
		fmt.Fprintf(os.Stderr, "  --db-port          int     Database port (env: DB_PORT, default: 5432)\n")
		fmt.Fprintf(os.Stderr, "  --db-name          string  Database name (env: DB_NAME, default: krile)\n")
		fmt.Fprintf(os.Stderr, "  --db-user          string  Database user (env: DB_USER, default: postgres)\n")
		fmt.Fprintf(os.Stderr, "  --db-password      string  Database password (env: DB_PASSWORD)\n")
		fmt.Fprintf(os.Stderr, "  --db-table         string  Table name (env: DB_TABLE, default: companies)\n")
		fmt.Fprintf(os.Stderr, "  --update-existing          Update records with same source_url instead of skipping\n")
		fmt.Fprintf(os.Stderr, "\nNote: Database credentials can be set in .env file\n")
	}

	flag.Parse()

	if showVersion {
		fmt.Printf("krile v%s\n", version)
		os.Exit(0)
	}

	if !cfg.Serve && !cfg.RecheckEmails && !cfg.CheckWebsites && !cfg.SendEmails && cfg.GuessPath == "" && cfg.ExcelPath == "" && cfg.Query == "" {
		fmt.Fprintln(os.Stderr, "error: --query is required (or --excel lookup, --guess email-guess, --recheck-emails, --check-websites, --send-emails, or --serve web UI)")
		flag.Usage()
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch {
	case cfg.Serve:
		err = webui.Serve(ctx, cfg)
	case cfg.RecheckEmails:
		err = scraper.RunRecheck(ctx, cfg)
	case cfg.CheckWebsites:
		err = scraper.RunCheckWebsites(ctx, cfg)
	case cfg.SendEmails:
		err = scraper.RunSendEmails(ctx, cfg)
	case cfg.GuessPath != "":
		err = scraper.RunGuess(cfg)
	case cfg.ExcelPath != "":
		err = scraper.RunLookup(ctx, cfg)
	default:
		err = scraper.Run(ctx, cfg)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
}

// getEnv returns environment variable value or default if not set
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt returns environment variable as int or default if not set
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
