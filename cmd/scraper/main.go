package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/genjerator/krile/internal/config"
	"github.com/genjerator/krile/internal/scraper"
)

const version = "0.1.0"

func main() {
	var cfg config.Config
	var showVersion bool

	flag.StringVar(&cfg.Query, "q", "", "Business category to search (e.g. \"Restaurant\") [required]")
	flag.StringVar(&cfg.Query, "query", "", "Business category to search (e.g. \"Restaurant\") [required]")
	flag.StringVar(&cfg.City, "c", "", "City or location (e.g. \"Berlin\") [required]")
	flag.StringVar(&cfg.City, "city", "", "City or location (e.g. \"Berlin\") [required]")
	flag.StringVar(&cfg.Output, "o", "", "Output file path (default: stdout)")
	flag.StringVar(&cfg.Output, "output", "", "Output file path (default: stdout)")
	flag.StringVar(&cfg.Format, "f", "json", "Output format: json | csv")
	flag.StringVar(&cfg.Format, "format", "json", "Output format: json | csv")
	flag.IntVar(&cfg.Limit, "l", 0, "Max number of 'Mehr Anzeigen' clicks (0 = all)")
	flag.IntVar(&cfg.Limit, "limit", 0, "Max number of 'Mehr Anzeigen' clicks (0 = all)")
	flag.IntVar(&cfg.Delay, "d", 1000, "Delay between requests in milliseconds")
	flag.IntVar(&cfg.Delay, "delay", 1000, "Delay between requests in milliseconds")
	flag.BoolVar(&cfg.Verbose, "v", false, "Enable verbose logging")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&cfg.Debug, "debug", false, "Dump raw HTML responses and detailed debug info to stderr")
	flag.BoolVar(&showVersion, "version", false, "Print version and exit")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: krile [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fmt.Fprintf(os.Stderr, "  -q, --query   string  Business category to search (e.g. \"Restaurant\") [required]\n")
		fmt.Fprintf(os.Stderr, "  -c, --city    string  City or location (e.g. \"Berlin\") [required]\n")
		fmt.Fprintf(os.Stderr, "  -o, --output  string  Output file path (default: stdout)\n")
		fmt.Fprintf(os.Stderr, "  -f, --format  string  Output format: json | csv (default: json)\n")
		fmt.Fprintf(os.Stderr, "  -l, --limit   int     Max results to fetch (0 = all)\n")
		fmt.Fprintf(os.Stderr, "  -d, --delay   int     Delay between requests in ms (default: 1000)\n")
		fmt.Fprintf(os.Stderr, "  -v, --verbose         Enable verbose logging\n")
		fmt.Fprintf(os.Stderr, "      --debug           Dump raw HTML and detailed debug info to stderr\n")
		fmt.Fprintf(os.Stderr, "      --version         Print version and exit\n")
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
