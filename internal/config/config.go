package config

type Config struct {
	Query    string
	City     string
	Output   string
	Format   string
	Limit    int
	Delay    int
	Distance int // search radius in meters, 0 = no restriction
	Verbose  bool
	Debug    bool

	// PostgreSQL parameters
	DBHost         string
	DBPort         int
	DBName         string
	DBUser         string
	DBPassword     string
	DBTable        string
	UpdateExisting bool // Update records with same source_url instead of skipping
}
