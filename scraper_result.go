package thordata

import (
	"encoding/json"
	"io"
	"net/url"
	"strconv"
	"strings"
)

func validateDownloadURL(value, taskID, spiderID string) (*url.URL, string, error) {
	if value == "" || value != strings.TrimSpace(value) {
		return nil, "", scraperError(ErrorDownloadURLInvalid, "download_url", taskID, spiderID, "download URL was not a trimmed string", nil, 0, nil, nil)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, "", scraperError(ErrorDownloadURLInvalid, "download_url", taskID, spiderID, "download URL was invalid", nil, 0, err, nil)
	}
	trusted, _ := url.Parse(WebScraperDownloadURL)
	port := parsed.Port()
	if parsed.Scheme != trusted.Scheme || !strings.EqualFold(parsed.Hostname(), trusted.Hostname()) || (port != "" && port != "443") || parsed.Path != trusted.Path || parsed.User != nil || parsed.Fragment != "" {
		return nil, "", scraperError(ErrorDownloadURLInvalid, "download_url", taskID, spiderID, "download URL origin or path was not trusted", nil, 0, nil, nil)
	}
	query, queryErr := url.ParseQuery(parsed.RawQuery)
	if queryErr != nil {
		return nil, "", scraperError(ErrorDownloadURLInvalid, "download_url", taskID, spiderID, "download URL query was invalid", nil, 0, queryErr, nil)
	}
	for key, values := range query {
		if key != "task_id" && key != "type" && key != "api_key" || len(values) != 1 {
			return nil, "", scraperError(ErrorDownloadURLInvalid, "download_url", taskID, spiderID, "download URL query was invalid", nil, 0, nil, nil)
		}
	}
	if query.Get("task_id") != taskID || query.Get("type") != "json" {
		return nil, "", scraperError(ErrorDownloadURLInvalid, "download_url", taskID, spiderID, "download URL task did not match", nil, 0, nil, nil)
	}
	return parsed, query.Get("api_key"), nil
}

func parseJSONDocuments(body []byte) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var values []any
	for {
		var value any
		err := decoder.Decode(&value)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if len(values) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	if len(values) == 1 {
		return values[0], nil
	}
	flattened := []any{}
	for _, value := range values {
		array, ok := value.([]any)
		if !ok {
			return nil, &json.SyntaxError{}
		}
		flattened = append(flattened, array...)
	}
	return flattened, nil
}

func parseScraperResult(value any, taskID, spiderID string, normalized WebScraperParams, secrets []string, diagnostic bool) (WebScraperResponse, error) {
	if object, ok := value.(map[string]any); ok {
		if code, valid := coerceCode(object["code"]); valid && code != 200 && (hasKey(object, "msg") || hasKey(object, "message")) {
			errorCode := scraperBusinessCode(code, "parse")
			if errorCode == ErrorProtocol {
				errorCode = ErrorScrapeFailed
			}
			return WebScraperResponse{}, scraperError(errorCode, "parse", taskID, spiderID, reason(object), object["code"], 0, nil, secrets)
		}
	}
	rows, ok := value.([]any)
	if !ok {
		if object, objectOK := value.(map[string]any); objectOK {
			rows = []any{object}
		} else {
			return WebScraperResponse{}, scraperError(ErrorInvalidResult, "parse", taskID, spiderID, "download result was not an object array", nil, 0, nil, secrets)
		}
	}
	records := make([]map[string]any, 0, len(rows))
	errorsCount := 0
	meaningful := false
	for _, item := range rows {
		object, objectOK := item.(map[string]any)
		if !objectOK {
			return WebScraperResponse{}, scraperError(ErrorInvalidResult, "parse", taskID, spiderID, "download result contained a non-object record", nil, 0, nil, secrets)
		}
		records = append(records, object)
		if failedRecord(object) {
			errorsCount++
		}
		for key, entry := range object {
			if _, input := normalized[key]; input || key == "input" || key == "input_url" || key == "url" || key == "job_url" || key == "keyword" || key == "success" || key == "error" || key == "error_code" {
				continue
			}
			if meaningfulValue(entry) {
				meaningful = true
			}
		}
	}
	if errorsCount == len(records) && len(records) > 0 {
		return WebScraperResponse{}, scraperError(ErrorScrapeFailed, "parse", taskID, spiderID, firstRecordReason(records), firstRecordCode(records), 0, nil, secrets)
	}
	result := WebScraperResponse{Result: redactRecords(records, secrets), TaskID: taskID, SpiderID: spiderID, RecordCount: len(records), ErrorRecordCount: errorsCount, Raw: redactValue(value, secrets)}
	if errorsCount > 0 {
		result.Status = StatusPartial
	} else if meaningful {
		result.Status = StatusCompleted
	} else {
		result.Status = StatusEmpty
	}
	return result, nil
}

func redactRecords(records []map[string]any, secrets []string) []map[string]any {
	result := make([]map[string]any, len(records))
	for i, record := range records {
		value := redactValue(record, secrets).(map[string]any)
		result[i] = value
	}
	return result
}
func failedRecord(record map[string]any) bool {
	if value, ok := record["success"].(bool); ok && !value {
		return true
	}
	if value, ok := record["error"]; ok && value != nil && value != false && value != 0 && !isZeroNumberValue(value) && meaningfulValue(value) {
		return true
	}
	if value, ok := record["error_code"]; ok {
		if value == nil {
			return false
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			return false
		}
		if text, ok := value.(json.Number); ok && isZeroNumber(text.String()) {
			return false
		}
		if value == "" {
			return false
		}
		switch number := value.(type) {
		case string:
			n, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
			return err != nil || (n != 0 && n != 200)
		case json.Number:
			n, err := number.Float64()
			return err != nil || (n != 0 && n != 200)
		default:
			code, good := coerceCode(value)
			return !good || (code != 0 && code != 200)
		}
	}
	return false
}

func isZeroNumberValue(value any) bool {
	switch typed := value.(type) {
	case json.Number:
		return isZeroNumber(typed.String())
	case float64:
		return typed == 0
	case float32:
		return typed == 0
	default:
		return false
	}
}

func isZeroNumber(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	number, err := strconv.ParseFloat(value, 64)
	return err == nil && number == 0
}
func meaningfulValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		for _, item := range typed {
			if meaningfulValue(item) {
				return true
			}
		}
		return false
	case map[string]any:
		for _, item := range typed {
			if meaningfulValue(item) {
				return true
			}
		}
		return false
	default:
		return true
	}
}
func firstRecordReason(records []map[string]any) string {
	if len(records) == 0 {
		return ""
	}
	return stringValue(records[0]["error"])
}
func firstRecordCode(records []map[string]any) any {
	if len(records) == 0 {
		return nil
	}
	return records[0]["error_code"]
}
