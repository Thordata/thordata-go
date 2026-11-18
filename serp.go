package thordata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
)

func (c *Client) SearchResponse(ctx context.Context, params SearchParams, options ...SearchOptions) (SearchResponse, error) {
	searchOptions, err := searchOptions(options)
	if err != nil {
		return SearchResponse{}, err
	}
	payload, err := c.buildSearchPayload(params, searchOptions.Engine)
	if err != nil {
		return SearchResponse{}, err
	}
	body, status, err := c.doJSON(ctx, http.MethodPost, "/request", payload, searchOptions.Headers, true)
	if err != nil {
		return SearchResponse{}, err
	}
	if status < 200 || status >= 300 {
		return SearchResponse{}, c.apiErrorFromBody(status, body)
	}
	return c.parseSearchResponse(body, status)
}

func (c *Client) rawSearch(ctx context.Context, params SearchParams, options ...SearchOptions) (string, error) {
	searchOptions, err := searchOptions(options)
	if err != nil {
		return "", err
	}
	payload, err := c.buildSearchPayload(params, searchOptions.Engine)
	if err != nil {
		return "", err
	}
	body, status, err := c.doJSON(ctx, http.MethodPost, "/request", payload, searchOptions.Headers, true)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", c.apiErrorFromBody(status, body)
	}
	return string(body), nil
}

func Search(ctx context.Context, params SearchParams, options ClientOptions, searchOptions ...SearchOptions) (any, error) {
	client, err := NewClient(options)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.Search(ctx, params, searchOptions...)
}

func SearchJSON(ctx context.Context, params SearchParams, options ClientOptions, searchOptions ...SearchOptions) (any, error) {
	return Search(ctx, params, options, searchOptions...)
}

// RawSearch 使用临时 Client 返回 HTTP 2xx 响应原文，不检查业务 code。
// 业务失败也可能返回 nil error；需要分类错误时使用 Search 或 SearchJSON。
func RawSearch(ctx context.Context, params SearchParams, options ClientOptions, searchOptions ...SearchOptions) (string, error) {
	client, err := NewClient(options)
	if err != nil {
		return "", err
	}
	defer client.Close()
	return client.RawSearch(ctx, params, searchOptions...)
}

func (c *Client) buildSearchPayload(params SearchParams, explicitEngine string) (map[string]string, error) {
	requestedEngine := explicitEngine
	if strings.TrimSpace(requestedEngine) == "" && params != nil {
		if value, ok := params["engine"]; ok {
			text, isString := value.(string)
			if !isString || strings.TrimSpace(text) == "" {
				return nil, newInvalidRequest("engine must be a non-empty string")
			}
			requestedEngine = text
		}
	}
	if strings.TrimSpace(requestedEngine) == "" {
		requestedEngine = "google_search"
	}
	requestedEngine = strings.ToLower(strings.TrimSpace(requestedEngine))
	engine := requestedEngine
	if alias, ok := engineAliases[engine]; ok {
		engine = alias
	}
	if webScraperEngines[engine] {
		return nil, newInvalidRequest("Web Scraper engines are not available in the SERP SDK")
	}
	if !containsString(supportedEngines, requestedEngine) && !containsString(supportedEngines, engine) {
		return nil, newInvalidRequest(fmt.Sprintf("Unsupported SERP engine %q; supported engines: %s", requestedEngine, strings.Join(supportedEngines, ", ")))
	}
	payload := make(map[string]string)
	if params != nil {
		for key, value := range params {
			if key == "engine" {
				continue
			}
			canonical := key
			if alias, ok := parameterAliases[key]; ok {
				canonical = alias
			}
			if !requestParameterSet[canonical] {
				continue
			}
			encoded, keep, err := normalizeValue(value)
			if err != nil {
				return nil, err
			}
			if keep {
				payload[canonical] = encoded
			}
		}
	}
	for alias, canonical := range parameterAliases {
		if value, ok := params[canonical]; ok {
			if encoded, keep, err := normalizeValue(value); err != nil {
				return nil, err
			} else if keep {
				payload[canonical] = encoded
			}
		} else if value, ok := params[alias]; ok {
			if _, exists := payload[canonical]; !exists {
				if encoded, keep, err := normalizeValue(value); err != nil {
					return nil, err
				} else if keep {
					payload[canonical] = encoded
				}
			}
		}
	}
	if engine == "yandex" {
		if _, hasText := payload["text"]; !hasText {
			if value, ok := payload["q"]; ok {
				payload["text"] = value
			}
		}
		delete(payload, "q")
	}
	payload["engine"] = engine
	if _, ok := payload["json"]; !ok {
		payload["json"] = "1"
	}
	if _, ok := payload["isjson"]; !ok {
		payload["isjson"] = "1"
	}
	return payload, nil
}

var parameterAliases = map[string]string{"query": "q", "country": "gl", "language": "hl"}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (c *Client) parseSearchResponse(body []byte, status int) (SearchResponse, error) {
	var payload any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return SearchResponse{}, &APIError{Message: "ThorData SERP API returned invalid JSON", HTTPStatus: status, Cause: err}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return SearchResponse{}, &APIError{Message: "ThorData SERP API returned multiple JSON documents", HTTPStatus: status}
	}
	object, ok := payload.(map[string]any)
	if !ok {
		return SearchResponse{}, &APIError{Message: "ThorData SERP API returned an unexpected response shape", HTTPStatus: status, ResponseData: redactValue(payload, []string{c.apiKey})}
	}
	code, hasCode := coerceCode(object["code"])
	if _, present := object["code"]; present && object["code"] != nil && !hasCode {
		return SearchResponse{}, &APIError{Message: "ThorData SERP API returned an invalid business code", HTTPStatus: status, ResponseData: redactValue(object, []string{c.apiKey})}
	}
	data := decodeNestedJSON(object["data"])
	if hasCode && code != 200 {
		message := stringValue(data)
		if message == "" {
			message = stringValue(object["msg"])
		}
		return SearchResponse{}, c.businessAPIError(code, message, status, data)
	}
	result := data
	taskID := ""
	if dataObject, ok := data.(map[string]any); ok {
		if inner, exists := dataObject["result"]; exists {
			result = decodeNestedJSON(inner)
			if value, ok := dataObject["task_id"]; ok && value != nil {
				taskID = stringValue(value)
			}
		}
	}
	if result == nil && !hasKey(object, "data") {
		result = object
	}
	if taskID == "" {
		if metadata, ok := object["search_metadata"].(map[string]any); ok {
			if value, ok := metadata["id"]; ok && value != nil {
				taskID = stringValue(value)
			}
		}
	}
	return SearchResponse{Result: result, TaskID: taskID, Raw: object}, nil
}

func (c *Client) apiErrorFromBody(status int, body []byte) error {
	var payload any
	_ = json.Unmarshal(body, &payload)
	message := fmt.Sprintf("ThorData API returned HTTP %d", status)
	code := status
	if object, ok := payload.(map[string]any); ok {
		if text := stringValue(object["msg"]); text != "" {
			message = text
		}
		if text := stringValue(object["data"]); text != "" {
			message = text
		}
	}
	return c.businessAPIError(code, message, status, payload)
}

func (c *Client) businessAPIError(code int, message string, status int, data any) error {
	classificationCode := code
	if code == 0 && strings.Contains(strings.ToLower(message), "collection failed") {
		classificationCode = 300
	}
	base := newAPIError(redactText(message, []string{c.apiKey}), intPtr(code), status, redactValue(data, []string{c.apiKey}), nil)
	switch classificationCode {
	case 300:
		return &NotCollectedError{APIError: base}
	case 401:
		return &AuthenticationError{APIError: base}
	case 402:
		return &PermissionError{APIError: base}
	case 429:
		return &RateLimitError{APIError: base}
	case 400:
		return &InvalidRequestError{APIError: base}
	default:
		return base
	}
}

func coerceCode(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v || math.Abs(v) > 9007199254740991 {
			return 0, false
		}
		return int(v), true
	case json.Number:
		n, err := strconv.Atoi(string(v))
		return n, err == nil
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return 0, false
		}
		for _, r := range text {
			if r < '0' || r > '9' {
				return 0, false
			}
		}
		n, err := strconv.Atoi(text)
		return n, err == nil
	default:
		return 0, false
	}
}

func decodeNestedJSON(value any) any {
	if text, ok := value.(string); ok {
		var decoded any
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		if decoder.Decode(&decoded) == nil {
			var trailing any
			if err := decoder.Decode(&trailing); err != io.EOF {
				return value
			}
			return decodeNestedJSON(decoded)
		}
		return value
	}
	if object, ok := value.(map[string]any); ok {
		copy := make(map[string]any, len(object))
		for key, item := range object {
			if key == "json" || key == "result" {
				copy[key] = decodeNestedJSON(item)
			} else {
				copy[key] = item
			}
		}
		return copy
	}
	return value
}

func hasKey(object map[string]any, key string) bool { _, ok := object[key]; return ok }
