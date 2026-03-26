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
	"github.com/joho/godotenv"
)

const version = "0.1.0"

func main() {
	// Load .env file if present (ignore error if not found)
	_ = godotenv.Load()

	var cfg config.Config
	var showVersion bool

	// Get defaults from environment variables
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnvInt("DB_PORT", 5432)
	dbName := getEnv("DB_NAME", "krile")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "")
	dbTable := getEnv("DB_TABLE", "businesses")

	flag.StringVar(&cfg.Query, "q", "", "Business category to search (e.g. \"Restaurant\") [required]")
	flag.StringVar(&cfg.Query, "query", "", "Business category to search (e.g. \"Restaurant\") [required]")
	flag.StringVar(&cfg.City, "c", "", "City or location (e.g. \"Berlin\") [required]")
	flag.StringVar(&cfg.City, "city", "", "City or location (e.g. \"Berlin\") [required]")
	flag.StringVar(&cfg.Output, "o", "", "Output file path (default: stdout)")
	flag.StringVar(&cfg.Output, "output", "", "Output file path (default: stdout)")
	flag.StringVar(&cfg.Format, "f", "json", "Output format: json | csv | xlsx | postgres")
	flag.StringVar(&cfg.Format, "format", "json", "Output format: json | csv | xlsx | postgres")
	flag.IntVar(&cfg.Limit, "l", 0, "Max number of 'Mehr Anzeigen' clicks (0 = all)")
	flag.IntVar(&cfg.Limit, "limit", 0, "Max number of 'Mehr Anzeigen' clicks (0 = all)")
	flag.IntVar(&cfg.Delay, "d", 1000, "Delay between requests in milliseconds")
	flag.IntVar(&cfg.Delay, "delay", 1000, "Delay between requests in milliseconds")
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

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: krile [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fmt.Fprintf(os.Stderr, "  -q, --query   string  Business category to search (e.g. \"Restaurant\") [required]\n")
		fmt.Fprintf(os.Stderr, "  -c, --city    string  City or location (e.g. \"Berlin\") [required]\n")
		fmt.Fprintf(os.Stderr, "  -o, --output  string  Output file path (default: stdout)\n")
		fmt.Fprintf(os.Stderr, "  -f, --format  string  Output format: json | csv | xlsx | postgres (default: json)\n")
		fmt.Fprintf(os.Stderr, "  -l, --limit   int     Max results to fetch (0 = all)\n")
		fmt.Fprintf(os.Stderr, "  -d, --delay   int     Delay between requests in ms (default: 1000)\n")
		fmt.Fprintf(os.Stderr, "  -v, --verbose         Enable verbose logging\n")
		fmt.Fprintf(os.Stderr, "      --debug           Dump raw HTML and detailed debug info to stderr\n")
		fmt.Fprintf(os.Stderr, "      --version         Print version and exit\n")
		fmt.Fprintf(os.Stderr, "\nPostgreSQL flags (only needed when --format=postgres):\n")
		fmt.Fprintf(os.Stderr, "  --db-host     string  Database host (env: DB_HOST, default: localhost)\n")
		fmt.Fprintf(os.Stderr, "  --db-port     int     Database port (env: DB_PORT, default: 5432)\n")
		fmt.Fprintf(os.Stderr, "  --db-name     string  Database name (env: DB_NAME, default: krile)\n")
		fmt.Fprintf(os.Stderr, "  --db-user     string  Database user (env: DB_USER, default: postgres)\n")
		fmt.Fprintf(os.Stderr, "  --db-password string  Database password (env: DB_PASSWORD)\n")
		fmt.Fprintf(os.Stderr, "  --db-table    string  Table name (env: DB_TABLE, default: businesses)\n")
		fmt.Fprintf(os.Stderr, "\nNote: Database credentials can be set in .env file\n")
	}

	flag.Parse()

	if showVersion {
		fmt.Printf("krile v%s\n", version)
		os.Exit(0)
	}

	if cfg.Query == "" || cfg.City == "" {
		fmt.Fprintln(os.Stderr, "error: --query and --city are required")
		flag.Usage()
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := scraper.Run(ctx, cfg); err != nil {
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
