package thordata

import "time"

const (
	Version               = "0.1.0"
	DefaultAPIURL         = "https://scraperapi.thordata.com/request"
	DefaultMaxWait        = 300 * time.Second
	PollInterval          = 5 * time.Second
	IntegrationPlatform   = "sdk"
	IntegrationSource     = "go-sdk"
	WebScraperBuilderURL  = "https://scraperapi.thordata.com/builder"
	WebScraperTaskAPIURL  = "https://api.thordata.com/api/web-scraper-api"
	WebScraperDownloadURL = "https://scraperapi.thordata.com/download"
)

const DefaultTimeout = 30 * time.Second
