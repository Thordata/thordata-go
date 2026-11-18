package thordata

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	apiKey         string
	apiURL         string
	baseURL        string
	timeout        time.Duration
	headers        http.Header
	httpClient     *http.Client
	ownedTransport *http.Transport
}

func NewClient(opts ClientOptions) (*Client, error) {
	key := ""
	if opts.APIKey != "" {
		key = opts.APIKey
	} else if opts.APIToken != "" {
		key = opts.APIToken
	} else if opts.Token != "" {
		key = opts.Token
	} else {
		key = firstNonEmpty(os.Getenv("THORDATA_API_KEY"), os.Getenv("THORDATA_API_TOKEN"), os.Getenv("TALORDATA_API_TOKEN"))
	}
	if key == "" {
		return nil, &ConfigurationError{Message: "Thordata API key is required; pass APIKey or set THORDATA_API_KEY"}
	}
	for _, r := range key {
		if r < 32 || r > 126 {
			return nil, &ConfigurationError{Message: "Thordata API key must contain only printable ASCII characters"}
		}
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, &ConfigurationError{Message: "Thordata API key must not be empty"}
	}
	for _, r := range key {
		if r > 126 || r < 32 {
			return nil, &ConfigurationError{Message: "Thordata API key must contain only printable ASCII characters"}
		}
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if timeout < 0 {
		return nil, &ConfigurationError{Message: "timeout must be greater than zero"}
	}
	apiURL := firstNonEmpty(opts.APIURL, os.Getenv("THORDATA_SERP_API_URL"))
	if apiURL == "" && opts.BaseURL != "" {
		apiURL = strings.TrimRight(opts.BaseURL, "/") + "/request"
	}
	if apiURL == "" {
		apiURL = DefaultAPIURL
	}
	parsed, err := url.Parse(strings.TrimSpace(apiURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return nil, &ConfigurationError{Message: "apiURL must use http or https and include a host", Cause: safeError(err)}
	}
	base := strings.TrimRight(strings.TrimSuffix(strings.TrimSuffix(apiURL, "/request"), "/request/"), "/")
	client := opts.HTTPClient
	var owned *http.Transport
	if client == nil {
		if transport, ok := http.DefaultTransport.(*http.Transport); ok {
			owned = transport.Clone()
			client = &http.Client{Transport: owned}
		} else {
			client = &http.Client{Transport: http.DefaultTransport}
		}
	}
	copyClient := *client
	copyClient.Jar = nil
	copyClient.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	client = &copyClient
	return &Client{
		apiKey: key, apiURL: strings.TrimSpace(apiURL), baseURL: base, timeout: timeout,
		headers: cloneHeader(opts.Headers), httpClient: client, ownedTransport: owned,
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (c *Client) Close() error {
	if c != nil && c.ownedTransport != nil {
		c.ownedTransport.CloseIdleConnections()
	}
	return nil
}

func (c *Client) String() string {
	if c == nil {
		return "<ThorDataClient nil>"
	}
	return fmt.Sprintf("<ThorDataClient api_url=%q>", redactText(c.apiURL, []string{c.apiKey}))
}

func (c *Client) GoString() string { return c.String() }

func (c *Client) Search(ctx context.Context, params SearchParams, options ...SearchOptions) (any, error) {
	response, err := c.SearchResponse(ctx, params, options...)
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

func (c *Client) SearchJSON(ctx context.Context, params SearchParams, options ...SearchOptions) (any, error) {
	return c.Search(ctx, params, options...)
}

// RawSearch 返回 HTTP 2xx 响应原文，不解析业务 code；err 为 nil 不表示采集成功。
// 需要业务错误分类时使用 Search 或 SearchResponse；传输失败或非 2xx 响应仍返回错误。
func (c *Client) RawSearch(ctx context.Context, params SearchParams, options ...SearchOptions) (string, error) {
	return c.rawSearch(ctx, params, options...)
}

func (c *Client) Request(ctx context.Context, method, path string, params map[string]any, options RequestOptions) (*http.Response, error) {
	if ctx == nil {
		return nil, &ConfigurationError{Message: "context must not be nil"}
	}
	if c == nil {
		return nil, &ConfigurationError{Message: "client must not be nil"}
	}
	filter := true
	if options.FilterParams != nil {
		filter = *options.FilterParams
	}
	encoded, err := normalizeGenericParams(params, filter)
	if err != nil {
		return nil, err
	}
	return c.doForm(ctx, method, path, encoded, options.Headers, true)
}

func (c *Client) doForm(ctx context.Context, method, path string, params map[string]string, extra http.Header, serpHeaders bool) (*http.Response, error) {
	if ctx == nil {
		return nil, &ConfigurationError{Message: "context must not be nil"}
	}
	if c == nil {
		return nil, &ConfigurationError{Message: "client must not be nil"}
	}
	if ctx.Err() != nil {
		return nil, connectionError(ctx.Err())
	}
	target, err := c.resolveURL(path)
	if err != nil {
		return nil, err
	}
	ctx2, cancel := context.WithTimeout(ctx, c.timeout)
	requestMethod := strings.ToUpper(method)
	form := url.Values{}
	for key, value := range params {
		form.Set(key, value)
	}
	var body *strings.Reader
	if requestMethod == http.MethodGet || requestMethod == http.MethodHead {
		parsed, parseErr := url.Parse(target)
		if parseErr != nil {
			cancel()
			return nil, &ConfigurationError{Message: "invalid request URL", Cause: safeError(parseErr)}
		}
		query := parsed.Query()
		for key, values := range form {
			for _, value := range values {
				query.Add(key, value)
			}
		}
		parsed.RawQuery = query.Encode()
		target = parsed.String()
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx2, requestMethod, target, body)
	if err != nil {
		cancel()
		return nil, &ConfigurationError{Message: "could not build request", Cause: err}
	}
	trustedOrigin := sameOrigin(target, c.apiURL)
	for key, values := range c.headers {
		if !trustedOrigin && blockedExternalHeader(key) {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	for key, values := range extra {
		if !trustedOrigin && blockedExternalHeader(key) {
			continue
		}
		req.Header.Del(key)
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if requestMethod != http.MethodGet && requestMethod != http.MethodHead {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", "application/json")
	if trustedOrigin {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("User-Agent", "thordata-go/"+Version)
	if serpHeaders && trustedOrigin {
		req.Header.Set("Origin", "Go")
		req.Header.Set("platform", "go")
		req.Header.Set("api-source", "sdk")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		cancel()
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return nil, &ConnectionError{Message: "ThorData request was cancelled", Cause: context.Canceled}
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &TimeoutError{Message: "ThorData request timed out", Cause: context.DeadlineExceeded}
		}
		return nil, connectionError(err)
	}
	resp.Body = &cancelReadCloser{ReadCloser: resp.Body, cancel: cancel, ctx: ctx2}
	return resp, nil
}

func sameOrigin(left, right string) bool {
	a, errA := url.Parse(left)
	b, errB := url.Parse(right)
	if errA != nil || errB != nil || a == nil || b == nil {
		return false
	}
	port := func(parsed *url.URL) string {
		if parsed.Port() != "" {
			return parsed.Port()
		}
		if parsed.Scheme == "https" {
			return "443"
		}
		if parsed.Scheme == "http" {
			return "80"
		}
		return ""
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

func blockedExternalHeader(key string) bool {
	switch strings.ToLower(key) {
	case "authorization", "proxy-authorization", "sign", "cookie", "cookie2", "api-key", "api_key", "apikey", "x-api-key", "api-source", "api_source", "source_platform", "platform", "origin":
		return true
	default:
		return false
	}
}

func (c *Client) resolveURL(path string) (string, error) {
	if strings.TrimSpace(path) == "" || path == "/request" || path == "request" {
		return c.apiURL, nil
	}
	parsed, parseErr := url.Parse(path)
	if parseErr != nil && strings.Contains(path, "://") {
		return "", &ConfigurationError{Message: "invalid request URL", Cause: safeError(parseErr)}
	}
	if parseErr == nil && parsed.IsAbs() {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", &ConfigurationError{Message: "request URL must use http or https"}
		}
		if parsed.User != nil || parsed.Hostname() == "" {
			return "", &ConfigurationError{Message: "request URL must not include userinfo"}
		}
		return path, nil
	}
	if strings.HasPrefix(strings.ToLower(path), "http:") || strings.HasPrefix(strings.ToLower(path), "https:") {
		return "", &ConfigurationError{Message: "request URL must use http or https and include a valid host"}
	}
	return strings.TrimRight(c.baseURL, "/") + "/" + strings.TrimLeft(path, "/"), nil
}

func cloneHeader(input http.Header) http.Header {
	if input == nil {
		return make(http.Header)
	}
	return input.Clone()
}

func (c *Client) CloseResponse(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}

func (c *Client) doJSON(ctx context.Context, method, path string, params map[string]string, headers http.Header, serpHeaders bool) ([]byte, int, error) {
	resp, err := c.doForm(ctx, method, path, params, headers, serpHeaders)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, readErr := ioReadAllResponse(resp)
	if readErr != nil {
		return nil, resp.StatusCode, connectionError(readErr)
	}
	return body, resp.StatusCode, nil
}
