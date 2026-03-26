package config

type Config struct {
	Query   string
	City    string
	Output  string
	Format  string
	Limit   int
	Delay   int
	Verbose bool
	Debug   bool

	// PostgreSQL parameters
	DBHost         string
	DBPort         int
	DBName         string
	DBUser         string
	DBPassword     string
	DBTable        string
	UpdateExisting bool // Update records with same source_url instead of skipping
}
