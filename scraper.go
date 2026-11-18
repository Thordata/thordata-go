package thordata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)
var spiderIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,120}$`)

func (c *Client) Scrape(ctx context.Context, spiderID string, params WebScraperParams, options ...ScrapeOptions) ([]map[string]any, error) {
	response, err := c.ScrapeResponse(ctx, spiderID, params, options...)
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

func (c *Client) ScrapeResponse(ctx context.Context, spiderID string, params WebScraperParams, options ...ScrapeOptions) (WebScraperResponse, error) {
	if ctx == nil {
		return WebScraperResponse{}, &ConfigurationError{Message: "context must not be nil"}
	}
	if c == nil {
		return WebScraperResponse{}, &ConfigurationError{Message: "client must not be nil"}
	}
	opts, err := scrapeOptions(options)
	if err != nil {
		return WebScraperResponse{}, err
	}
	normalized, err := ValidateWebScraperParams(spiderID, params)
	if err != nil {
		return WebScraperResponse{}, err
	}
	maxWait := opts.MaxWait
	if maxWait == 0 {
		maxWait = DefaultMaxWait
	}
	if maxWait < time.Second || maxWait > DefaultMaxWait || maxWait%time.Second != 0 {
		return WebScraperResponse{}, newInvalidRequest("MaxWait must be whole seconds between 1 and 300 (0 uses the default)")
	}
	includeErrors := true
	if opts.IncludeErrors != nil {
		includeErrors = *opts.IncludeErrors
	}
	fileName := opts.FileName
	if fileName == "" {
		fileName = "{{TasksID}}"
	}
	if strings.TrimSpace(fileName) == "" {
		return WebScraperResponse{}, newInvalidRequest("FileName must be a non-empty string")
	}
	deadline := time.Now().Add(maxWait)
	if parentDeadline, ok := ctx.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	callCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	secrets := []string{c.apiKey}
	definition := scraperDefinitions[spiderID]
	form := map[string]string{"spider_name": definition.spiderName, "spider_id": spiderID, "spider_parameters": mustJSON([]WebScraperParams{normalized}), "spider_errors": fmt.Sprintf("%t", includeErrors), "file_name": fileName, "api_source": "sdk", "source_platform": "go"}
	submission, status, err := c.scraperJSON(callCtx, http.MethodPost, WebScraperBuilderURL, form, "submit", "", spiderID, nil)
	if err != nil {
		return WebScraperResponse{}, err
	}
	data, err := checkScraperEnvelope(submission, 200, "submit", "", spiderID, c.apiKey, status)
	if err != nil {
		return WebScraperResponse{}, err
	}
	taskID, ok := data["task_id"].(string)
	if !ok || !taskIDPattern.MatchString(taskID) || strings.Contains(taskID, c.apiKey) {
		return WebScraperResponse{}, scraperError(ErrorSubmissionUnknown, "submit", "", spiderID, "", nil, 0, nil, secrets)
	}
	var lastStatus map[string]any
	for {
		if err := remainingOrTimeout(callCtx, deadline, "poll", taskID, spiderID, secrets); err != nil {
			return WebScraperResponse{}, err
		}
		pollPayload, pollStatus, err := c.scraperJSON(callCtx, http.MethodPost, WebScraperTaskAPIURL+"/tasks-status", map[string]string{"tasks_ids": taskID, "api_source": "sdk", "source_platform": "go"}, "poll", taskID, spiderID, nil)
		if err != nil {
			return WebScraperResponse{}, err
		}
		row, err := statusRow(pollPayload, taskID, spiderID, c.apiKey, pollStatus)
		if err != nil {
			return WebScraperResponse{}, err
		}
		lastStatus = row
		state, _ := row["status"].(string)
		if state == "Ready" {
			break
		}
		if state == "Failed" {
			failure := scraperError(ErrorScrapeFailed, "poll", taskID, spiderID, reason(row), row["error_code"], pollStatus, nil, secrets)
			if !includeErrors {
				return WebScraperResponse{}, failure
			}
			diagnostic, diagnosticErr := c.downloadScraperResult(callCtx, deadline, taskID, spiderID, normalized, secrets, true)
			var detail *WebScraperError
			if errors.As(diagnosticErr, &detail) && detail.Code == ErrorScrapeFailed {
				failure.Reason = detail.Reason
				failure.ServiceCode = detail.ServiceCode
			} else if diagnosticErr == nil {
				for _, record := range diagnostic.Result {
					if failedRecord(record) {
						failure.Reason = redactText(record["error"], secrets)
						failure.ServiceCode = record["error_code"]
						break
					}
				}
			}
			return WebScraperResponse{}, failure
		}
		wait := PollInterval
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return WebScraperResponse{}, scraperError(ErrorWaitTimeout, "poll", taskID, spiderID, "", nil, 0, nil, secrets)
		}
		if remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-callCtx.Done():
			timer.Stop()
			if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
				return WebScraperResponse{}, scraperError(ErrorWaitTimeout, "poll", taskID, spiderID, "", nil, 0, callCtx.Err(), secrets)
			}
			return WebScraperResponse{}, scraperError(ErrorRequestFailed, "poll", taskID, spiderID, callCtx.Err().Error(), nil, 0, callCtx.Err(), secrets)
		case <-timer.C:
		}
	}
	result, err := c.downloadScraperResult(callCtx, deadline, taskID, spiderID, normalized, secrets, false)
	if err != nil {
		return WebScraperResponse{}, err
	}
	result.Raw = map[string]any{"submission": redactValue(submission, secrets), "status": redactValue(lastStatus, secrets), "download": map[string]any{"tasks_ids": taskID}}
	return result, nil
}

func (c *Client) downloadScraperResult(ctx context.Context, deadline time.Time, taskID, spiderID string, normalized WebScraperParams, secrets []string, diagnostic bool) (WebScraperResponse, error) {
	addressPayload, status, err := c.scraperJSON(ctx, http.MethodPost, WebScraperTaskAPIURL+"/tasks-download", map[string]string{"tasks_id": taskID, "type": "json", "api_source": "sdk", "source_platform": "go"}, "download_url", taskID, spiderID, nil)
	if err != nil {
		return WebScraperResponse{}, err
	}
	data, err := checkScraperEnvelope(addressPayload, 200, "download_url", taskID, spiderID, c.apiKey, status)
	if err != nil {
		return WebScraperResponse{}, err
	}
	if value, _ := data["tasks_ids"].(string); value != taskID {
		return WebScraperResponse{}, scraperError(ErrorProtocol, "download_url", taskID, spiderID, "task ID did not match", nil, status, nil, secrets)
	}
	address, _ := data["download"].(string)
	parsed, downloadSecret, err := validateDownloadURL(address, taskID, spiderID)
	if err != nil {
		return WebScraperResponse{}, err
	}
	if downloadSecret != "" {
		secrets = append(secrets, downloadSecret)
	}
	if err := remainingOrTimeout(ctx, deadline, "download", taskID, spiderID, secrets); err != nil {
		return WebScraperResponse{}, err
	}
	body, downloadStatus, err := c.scraperBody(ctx, http.MethodGet, parsed.String(), nil, "download", taskID, spiderID, true, downloadSecret)
	if err != nil {
		return WebScraperResponse{}, err
	}
	if err := remainingOrTimeout(ctx, deadline, "parse", taskID, spiderID, secrets); err != nil {
		return WebScraperResponse{}, err
	}
	value, err := parseJSONDocuments(body)
	if err != nil {
		return WebScraperResponse{}, scraperError(ErrorProtocol, "download", taskID, spiderID, "Response was not valid JSON", nil, downloadStatus, err, secrets)
	}
	result, err := parseScraperResult(value, taskID, spiderID, normalized, secrets, diagnostic)
	if err != nil {
		return WebScraperResponse{}, err
	}
	if err := remainingOrTimeout(ctx, deadline, "parse", taskID, spiderID, secrets); err != nil {
		return WebScraperResponse{}, err
	}
	return result, nil
}

func (c *Client) scraperJSON(ctx context.Context, method, endpoint string, form map[string]string, stage, taskID, spiderID string, extra http.Header) (any, int, error) {
	body, status, err := c.scraperBody(ctx, method, endpoint, form, stage, taskID, spiderID, false)
	if err != nil {
		return nil, status, err
	}
	value, err := parseJSONDocuments(body)
	if err != nil {
		return nil, status, scraperError(ErrorProtocol, stage, taskID, spiderID, "Response was not valid JSON", nil, status, err, []string{c.apiKey})
	}
	return value, status, nil
}

func (c *Client) scraperBody(ctx context.Context, method, endpoint string, form map[string]string, stage, taskID, spiderID string, download bool, extraSecrets ...string) ([]byte, int, error) {
	secrets := append([]string{c.apiKey}, extraSecrets...)
	if ctx == nil {
		return nil, 0, scraperError(ErrorRequestFailed, stage, taskID, spiderID, "context is nil", nil, 0, nil, []string{c.apiKey})
	}
	if ctx.Err() != nil {
		return nil, 0, scraperError(ErrorRequestFailed, stage, taskID, spiderID, "request canceled before sending", nil, 0, ctx.Err(), []string{c.apiKey})
	}
	requestURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, 0, scraperError(ErrorProtocol, stage, taskID, spiderID, "invalid endpoint", nil, 0, err, []string{c.apiKey})
	}
	var body io.Reader
	if method == http.MethodGet {
		body = nil
	} else {
		values := url.Values{}
		for key, value := range form {
			values.Set(key, value)
		}
		body = strings.NewReader(values.Encode())
	}
	requestContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestContext, method, requestURL.String(), body)
	if err != nil {
		return nil, 0, scraperError(ErrorRequestFailed, stage, taskID, spiderID, "could not build request", nil, 0, err, []string{c.apiKey})
	}
	for key, values := range c.headers {
		if !blockedScraperHeader(key, download) {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}
	req.Header.Del("X-Thordata-Platform")
	req.Header.Del("X-Thordata-Source")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "thordata-go/"+Version)
	if !download {
		if stage == "submit" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		} else {
			req.Header.Set("sign", c.apiKey)
		}
		if method != http.MethodGet {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	httpClient := *c.httpClient
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	httpClient.Jar = nil
	resp, err := httpClient.Do(req)
	if err != nil {
		code := ErrorRequestFailed
		if stage == "submit" {
			code = ErrorSubmissionUnknown
		}
		if stage != "submit" && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = ErrorWaitTimeout
		}
		return nil, 0, scraperError(code, stage, taskID, spiderID, "request failed", nil, 0, err, secrets)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		code := ErrorRequestFailed
		if stage == "submit" {
			code = ErrorSubmissionUnknown
		}
		if stage != "submit" && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = ErrorWaitTimeout
		}
		return nil, resp.StatusCode, scraperError(code, stage, taskID, spiderID, "response read failed", nil, resp.StatusCode, readErr, secrets)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code := scraperBusinessCode(resp.StatusCode, stage)
		if code == ErrorProtocol {
			code = ErrorRequestFailed
		}
		if stage == "submit" && resp.StatusCode >= 500 {
			code = ErrorSubmissionUnknown
		}
		return nil, resp.StatusCode, scraperError(code, stage, taskID, spiderID, reasonFromBody(raw), resp.StatusCode, resp.StatusCode, nil, secrets)
	}
	return raw, resp.StatusCode, nil
}

func blockedScraperHeader(key string, download bool) bool {
	key = strings.ToLower(key)
	blocked := map[string]bool{"authorization": true, "proxy-authorization": true, "sign": true, "cookie": true, "cookie2": true, "api-key": true, "api_key": true, "apikey": true, "x-api-key": true, "api-source": true, "api_source": true, "source_platform": true, "platform": true, "origin": true}
	if download {
		return true
	}
	return blocked[key]
}

func remainingOrTimeout(ctx context.Context, deadline time.Time, stage, taskID, spiderID string, secrets []string) error {
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return scraperError(ErrorWaitTimeout, stage, taskID, spiderID, "", nil, 0, err, secrets)
		}
		return scraperError(ErrorRequestFailed, stage, taskID, spiderID, err.Error(), nil, 0, err, secrets)
	}
	if time.Until(deadline) <= 0 {
		return scraperError(ErrorWaitTimeout, stage, taskID, spiderID, "", nil, 0, nil, secrets)
	}
	return nil
}

func statusRow(payload any, taskID, spiderID, key string, status int) (map[string]any, error) {
	object, ok := payload.(map[string]any)
	if !ok {
		return nil, scraperError(ErrorProtocol, "poll", taskID, spiderID, "status response was not an object", nil, status, nil, []string{key})
	}
	code, codeOK := coerceCode(object["code"])
	if !codeOK || code != 200 {
		return nil, scraperError(scraperBusinessCode(code, "poll"), "poll", taskID, spiderID, reason(object), code, status, nil, []string{key})
	}
	data, ok := object["data"].([]any)
	if !ok {
		return nil, scraperError(ErrorProtocol, "poll", taskID, spiderID, "status data was not an array", nil, status, nil, []string{key})
	}
	var match map[string]any
	for _, item := range data {
		row, rowOK := item.(map[string]any)
		if rowOK {
			if row["task_id"] == taskID {
				if match != nil {
					return nil, scraperError(ErrorProtocol, "poll", taskID, spiderID, "multiple matching tasks", nil, status, nil, []string{key})
				}
				match = row
			}
		}
	}
	if match == nil {
		return nil, scraperError(ErrorProtocol, "poll", taskID, spiderID, "task was not found", nil, status, nil, []string{key})
	}
	state, _ := match["status"].(string)
	if state != "Running" && state != "Ready" && state != "Failed" {
		return nil, scraperError(ErrorProtocol, "poll", taskID, spiderID, "unknown task status", nil, status, nil, []string{key})
	}
	return match, nil
}

func checkScraperEnvelope(payload any, expected int, stage, taskID, spiderID, key string, status int) (map[string]any, error) {
	object, ok := payload.(map[string]any)
	if !ok {
		return nil, scraperError(ErrorProtocol, stage, taskID, spiderID, "response was not an object", nil, status, nil, []string{key})
	}
	code, ok := coerceCode(object["code"])
	if !ok || code != expected {
		return nil, scraperError(scraperBusinessCode(code, stage), stage, taskID, spiderID, reason(object), code, status, nil, []string{key})
	}
	data, ok := object["data"].(map[string]any)
	if !ok {
		return nil, scraperError(ErrorProtocol, stage, taskID, spiderID, "response data was not an object", nil, status, nil, []string{key})
	}
	return data, nil
}

func scraperBusinessCode(code int, stage string) ErrorCode {
	switch code {
	case 401, 10013, 10014, 10047, 10048:
		return ErrorAuthentication
	case 402:
		return ErrorInsufficientBalance
	case 403:
		return ErrorPermission
	default:
		if stage == "submit" {
			return ErrorSubmissionFailed
		}
		return ErrorProtocol
	}
}

func scraperError(code ErrorCode, stage, taskID, spiderID, detail string, service any, status int, cause error, secrets []string) *WebScraperError {
	if code != ErrorRequestFailed && scraperErrorMessage(code) == scraperErrorMessage(ErrorRequestFailed) {
		code = ErrorRequestFailed
	}
	safeTask := ""
	if taskIDPattern.MatchString(taskID) && !containsAny(taskID, secrets) {
		safeTask = taskID
	}
	safeSpider := ""
	if spiderIDPattern.MatchString(spiderID) && !containsAny(spiderID, secrets) {
		safeSpider = spiderID
	}
	return &WebScraperError{Code: code, Stage: stage, TaskID: safeTask, SpiderID: safeSpider, Reason: redactText(detail, secrets), ServiceCode: redactValue(service, secrets), HTTPStatus: status, Cause: safeError(cause), secrets: append([]string(nil), secrets...)}
}

func containsAny(value string, secrets []string) bool {
	for _, secret := range secrets {
		if secret != "" && strings.Contains(value, secret) {
			return true
		}
	}
	return false
}
func reason(value map[string]any) string {
	for _, key := range []string{"msg", "message", "error", "data"} {
		if text := stringValue(value[key]); text != "" {
			return text
		}
	}
	return ""
}
func reasonFromBody(body []byte) string {
	var object map[string]any
	if json.Unmarshal(body, &object) == nil {
		return reason(object)
	}
	return string(body)
}
func mustJSON(value any) string { encoded, _ := json.Marshal(value); return string(encoded) }
