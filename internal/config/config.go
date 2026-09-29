package config

type Config struct {
	Query          string
	City           string
	ExcelPath      string // Excel lookup mode: path to the address export .xlsx
	GuessPath      string // Email-guess mode: path to a .csv/.xlsx with Vorname/Name columns
	Patterns       string // Email-guess mode: comma-separated pattern labels to generate (empty = all)
	GuessProviders string // Email-guess mode: comma-separated providers to generate (empty = all)
	Output         string
	Format         string
	Limit          int
	StartPosition  int // resume scraping from this 1-based result position (0/1 = start)
	StartPage      int // resume scraping from this 1-based page number (0/1 = start)
	Delay          int
	Distance       int  // search radius in meters, 0 = no restriction
	Workers        int  // concurrent workers for email enrichment
	WebSearch      bool // web-search fallback (DuckDuckGo) when no email is found
	RecheckEmails  bool // recheck mode: re-run web-search enrichment for DB rows with empty email
	CheckWebsites  bool // check-websites mode: classify company websites (ok/dead/404/parked)
	Serve          bool // serve mode: start the local web UI showing DB stats
	Port           int  // web UI port (default 8080)
	Verbose        bool
	Debug          bool

	// Send-emails mode: send the generated subject/personalized_email to DB rows.
	SendEmails  bool   // send-emails mode: send generated emails to companies
	ConfirmSend bool   // master safety switch — WITHOUT this, send-emails only dry-runs
	DailyLimit  int    // max emails to send per calendar day (default 10)
	TestTo      string // redirect every send to this address (rows are NOT marked sent)

	// SMTP parameters (send-emails mode; defaults from env)
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string
	SMTPFromName string
	SMTPTLS      string // TLS policy: ""(auto by port) | ssl | starttls | none
	SendEmailID  string // send-emails mode: send only this company_emails row (uuid)

	// UTM link tagging (send-emails mode). When UTMCampaign is non-empty,
	// planetaindustries.de links in the body get utm_* params appended, with
	// utm_content set to the company_emails row uuid (per-recipient signature).
	UTMCampaign string
	UTMSource   string
	UTMMedium   string

	// SES tracking (send-emails mode). SESConfigSet, when set, tags each message
	// with the X-SES-CONFIGURATION-SET header so SES applies open/click tracking
	// and publishes events. UnsubscribeMailto, when set, adds a List-Unsubscribe
	// header (mailto:) so clients show a native unsubscribe control.
	SESConfigSet      string
	UnsubscribeMailto string

	// PostgreSQL parameters
	DBHost         string
	DBPort         int
	DBName         string
	DBUser         string
	DBPassword     string
	DBTable        string
	UpdateExisting bool // Update records with same source_url instead of skipping
}
