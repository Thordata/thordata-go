package thordata

import (
	"fmt"
	"math"
	"net/url"
	"reflect"
	"strings"
)

type scraperField struct {
	kind         string
	required     bool
	defaultValue any
	options      []string
}
type scraperDefinition struct {
	spiderName string
	fields     map[string]scraperField
}

var scraperDefinitions = map[string]scraperDefinition{
	"linkedin_company_information_by-url":          {"linkedin.com", map[string]scraperField{"url": {kind: "url", required: true}}},
	"linkedin_job_listings_information_by-job-url": {"linkedin.com", map[string]scraperField{"job_url": {kind: "url", required: true}}},
	"youtube_video-post_by-keyword": {"youtube.com", map[string]scraperField{
		"keyword": {kind: "string", required: true}, "num_of_posts": {kind: "positive-string"},
	}},
	"facebook_profile_by-profiles-url": {"facebook.com", map[string]scraperField{"url": {kind: "url", required: true}}},
	"youtube_video-post_by-explore": {"youtube.com", map[string]scraperField{
		"url": {kind: "url", required: true}, "all_tabs": {kind: "all-tabs"},
	}},
	"twitter_profile_by-profileurl": {"x.com", map[string]scraperField{"url": {kind: "url", required: true}}},
	"amazon_product_by-category-url": {"amazon.com", map[string]scraperField{
		"url": {kind: "url", required: true}, "page_turning": {kind: "positive", required: true},
		"sort_by": {kind: "option", defaultValue: "Best Sellers", options: []string{"Best Sellers", "Newest Arrivals", "Avg. Customer Review", "Price: High to Low", "Price: Low to High", "Featured"}},
	}},
}

var supportedScrapers = []string{
	"linkedin_company_information_by-url", "linkedin_job_listings_information_by-job-url", "youtube_video-post_by-keyword",
	"facebook_profile_by-profiles-url", "youtube_video-post_by-explore", "twitter_profile_by-profileurl", "amazon_product_by-category-url",
}

func SupportedWebScrapers() []string { return append([]string(nil), supportedScrapers...) }

func ValidateWebScraperParams(spiderID string, params WebScraperParams) (WebScraperParams, error) {
	definition, ok := scraperDefinitions[spiderID]
	if !ok {
		return nil, newInvalidRequest(fmt.Sprintf("Unsupported Web Scraper spider %q", spiderID))
	}
	if params == nil {
		params = WebScraperParams{}
	}
	for key := range params {
		if _, ok := definition.fields[key]; !ok {
			return nil, newInvalidRequest(fmt.Sprintf("Unknown Web Scraper parameter: %s", key))
		}
	}
	normalized := make(WebScraperParams, len(definition.fields))
	for key, field := range definition.fields {
		value, exists := params[key]
		if !exists {
			value = field.defaultValue
		}
		if value == nil || value == "" {
			if field.required {
				return nil, newInvalidRequest(fmt.Sprintf("Missing required Web Scraper parameter: %s", key))
			}
			continue
		}
		var err error
		switch field.kind {
		case "url":
			normalized[key], err = validateScraperURL(value, key)
		case "string":
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				err = newInvalidRequest(fmt.Sprintf("%s must be a non-empty string", key))
			} else {
				normalized[key] = text
			}
		case "positive-string":
			number, numberErr := positiveInteger(value, key)
			err = numberErr
			if err == nil {
				normalized[key] = fmt.Sprintf("%d", number)
			}
		case "positive":
			normalized[key], err = positiveInteger(value, key)
		case "all-tabs":
			boolean, ok := value.(bool)
			if !ok {
				err = newInvalidRequest("all_tabs must be a boolean")
			} else if boolean {
				normalized[key] = "true"
			} else {
				normalized[key] = "flase"
			}
		case "option":
			text, ok := value.(string)
			if !ok || !containsString(field.options, text) {
				err = newInvalidRequest(fmt.Sprintf("%s must be one of the supported options", key))
			} else {
				normalized[key] = text
			}
		default:
			err = newInvalidRequest(fmt.Sprintf("unsupported Web Scraper field type %q", field.kind))
		}
		if err != nil {
			return nil, err
		}
	}
	return normalized, nil
}

func validateScraperURL(value any, key string) (string, error) {
	text, ok := value.(string)
	if !ok || text == "" || text != strings.TrimSpace(text) {
		return "", newInvalidRequest(fmt.Sprintf("%s must be an HTTP or HTTPS URL", key))
	}
	parsed, err := url.Parse(text)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", newInvalidRequest(fmt.Sprintf("%s must be an HTTP or HTTPS URL", key))
	}
	return text, nil
}

func positiveInteger(value any, key string) (int, error) {
	const max = int64(9007199254740991)
	var number int64
	switch typed := value.(type) {
	case int:
		number = int64(typed)
	case int8:
		number = int64(typed)
	case int16:
		number = int64(typed)
	case int32:
		number = int64(typed)
	case int64:
		number = typed
	case uint:
		if uint64(typed) > uint64(max) {
			return 0, newInvalidRequest(fmt.Sprintf("%s must be a positive integer", key))
		}
		number = int64(typed)
	case uint8:
		number = int64(typed)
	case uint16:
		number = int64(typed)
	case uint32:
		number = int64(typed)
	case uint64:
		if typed > uint64(max) {
			return 0, newInvalidRequest(fmt.Sprintf("%s must be a positive integer", key))
		}
		number = int64(typed)
	case float32:
		if float32(int64(typed)) != typed || math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return 0, newInvalidRequest(fmt.Sprintf("%s must be a positive integer", key))
		}
		number = int64(typed)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed > float64(max) {
			return 0, newInvalidRequest(fmt.Sprintf("%s must be a positive integer", key))
		}
		number = int64(typed)
	case string:
		if typed == "" {
			return 0, newInvalidRequest(fmt.Sprintf("%s must be a positive integer", key))
		}
		for _, char := range typed {
			if char < '0' || char > '9' {
				return 0, newInvalidRequest(fmt.Sprintf("%s must be a positive integer", key))
			}
		}
		var err error
		number, err = parseInt64(typed)
		if err != nil {
			return 0, newInvalidRequest(fmt.Sprintf("%s must be a positive integer", key))
		}
	default:
		if reflect.TypeOf(value) != nil {
			return 0, newInvalidRequest(fmt.Sprintf("%s must be a positive integer", key))
		}
	}
	if number < 1 || number > max {
		return 0, newInvalidRequest(fmt.Sprintf("%s must be a positive integer", key))
	}
	return int(number), nil
}

func parseInt64(value string) (int64, error) {
	var number int64
	for _, char := range value {
		if number > (math.MaxInt64-int64(char-'0'))/10 {
			return 0, fmt.Errorf("overflow")
		}
		number = number*10 + int64(char-'0')
	}
	return number, nil
}
