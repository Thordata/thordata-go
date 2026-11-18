package thordata

import (
	"net/http"
	"time"
)

type SearchParams map[string]any

type ThorDataClient = Client

type WebScraperParams map[string]any

type ClientOptions struct {
	APIKey     string
	APIToken   string
	Token      string
	APIURL     string
	BaseURL    string
	Timeout    time.Duration
	Headers    http.Header
	HTTPClient *http.Client
}

type SearchOptions struct {
	Engine  string
	Headers http.Header
}

type RequestOptions struct {
	Headers      http.Header
	FilterParams *bool
}

type ScrapeOptions struct {
	// MaxWait 必须为 1～300 的整数秒；0 使用默认 300 秒。更短的等待可通过 context 限制。
	MaxWait       time.Duration
	IncludeErrors *bool
	FileName      string
}

type SearchResponse struct {
	Result any    `json:"result"`
	TaskID string `json:"task_id,omitempty"`
	Raw    any    `json:"raw"`
}

type ScrapeStatus string

const (
	StatusCompleted ScrapeStatus = "completed"
	StatusPartial   ScrapeStatus = "partial"
	StatusEmpty     ScrapeStatus = "empty"
)

type WebScraperResponse struct {
	Result           []map[string]any `json:"result"`
	TaskID           string           `json:"task_id"`
	SpiderID         string           `json:"spider_id"`
	Status           ScrapeStatus     `json:"status"`
	RecordCount      int              `json:"record_count"`
	ErrorRecordCount int              `json:"error_record_count"`
	Raw              any              `json:"raw"`
}

func Bool(value bool) *bool { return &value }

func searchOptions(options []SearchOptions) (SearchOptions, error) {
	if len(options) > 1 {
		return SearchOptions{}, newInvalidRequest("at most one SearchOptions value is allowed")
	}
	if len(options) == 1 {
		return options[0], nil
	}
	return SearchOptions{}, nil
}

func scrapeOptions(options []ScrapeOptions) (ScrapeOptions, error) {
	if len(options) > 1 {
		return ScrapeOptions{}, newInvalidRequest("at most one ScrapeOptions value is allowed")
	}
	if len(options) == 1 {
		return options[0], nil
	}
	return ScrapeOptions{}, nil
}
