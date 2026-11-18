package thordata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEverySpiderSchemaAndWireType(t *testing.T) {
	cases := []struct {
		id     string
		params WebScraperParams
		want   WebScraperParams
	}{
		{"linkedin_company_information_by-url", WebScraperParams{"url": "https://example.com/company"}, WebScraperParams{"url": "https://example.com/company"}},
		{"linkedin_job_listings_information_by-job-url", WebScraperParams{"job_url": "https://example.com/job"}, WebScraperParams{"job_url": "https://example.com/job"}},
		{"youtube_video-post_by-keyword", WebScraperParams{"keyword": "go", "num_of_posts": "003"}, WebScraperParams{"keyword": "go", "num_of_posts": "3"}},
		{"facebook_profile_by-profiles-url", WebScraperParams{"url": "https://example.com/profile"}, WebScraperParams{"url": "https://example.com/profile"}},
		{"youtube_video-post_by-explore", WebScraperParams{"url": "https://example.com/explore", "all_tabs": true}, WebScraperParams{"url": "https://example.com/explore", "all_tabs": "true"}},
		{"twitter_profile_by-profileurl", WebScraperParams{"url": "https://example.com/x"}, WebScraperParams{"url": "https://example.com/x"}},
		{"amazon_product_by-category-url", WebScraperParams{"url": "https://example.com/category", "page_turning": 2.0}, WebScraperParams{"url": "https://example.com/category", "page_turning": 2, "sort_by": "Best Sellers"}},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			original, _ := json.Marshal(tc.params)
			got, err := ValidateWebScraperParams(tc.id, tc.params)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got=%#v want=%#v err=%v", got, tc.want, err)
			}
			after, _ := json.Marshal(tc.params)
			if string(after) != string(original) {
				t.Fatal("modified caller params")
			}
			if _, err := ValidateWebScraperParams(tc.id, nil); !errors.As(err, new(*InvalidRequestError)) {
				t.Fatal("accepted missing required fields")
			}
			tc.params["unknown"] = true
			if _, err := ValidateWebScraperParams(tc.id, tc.params); !errors.As(err, new(*InvalidRequestError)) {
				t.Fatal("accepted unknown field")
			}
		})
	}
	for _, value := range []any{0, -1, true, 1.5, " 3", "3.0", "9007199254740992", uint64(9007199254740992), nil} {
		_, err := ValidateWebScraperParams("amazon_product_by-category-url", WebScraperParams{"url": "https://example.com", "page_turning": value})
		if !errors.As(err, new(*InvalidRequestError)) {
			t.Errorf("accepted page_turning=%v", value)
		}
	}
	for _, value := range []any{int64(9007199254740991), "9007199254740991"} {
		if _, err := ValidateWebScraperParams("youtube_video-post_by-keyword", WebScraperParams{"keyword": "x", "num_of_posts": value}); err != nil {
			t.Errorf("rejected boundary: %v", err)
		}
	}
	for _, sort := range []string{"Best Sellers", "Newest Arrivals", "Avg. Customer Review", "Price: High to Low", "Price: Low to High", "Featured"} {
		got, err := ValidateWebScraperParams("amazon_product_by-category-url", WebScraperParams{"url": "https://example.com", "page_turning": 1, "sort_by": sort})
		if err != nil || got["sort_by"] != sort {
			t.Fatalf("sort=%s: %v", sort, err)
		}
	}
	for _, value := range []any{nil, ""} {
		got, err := ValidateWebScraperParams("amazon_product_by-category-url", WebScraperParams{"url": "https://example.com", "page_turning": 1, "sort_by": value})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got["sort_by"]; ok {
			t.Fatal("explicit empty sort should be omitted")
		}
	}
}

func TestSharedLifecycleFixtureRunningToReady(t *testing.T) {
	raw, err := os.ReadFile("testdata/contract-fixtures/web-scraper/lifecycle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Builder json.RawMessage   `json:"builder"`
		Status  []json.RawMessage `json:"status"`
		Address json.RawMessage   `json:"download_address"`
		Records json.RawMessage   `json:"records"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	polls, submits := 0, 0
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/builder":
			submits++
			return response(200, string(fixture.Builder), r), nil
		case "/api/web-scraper-api/tasks-status":
			i := polls
			polls++
			if i >= len(fixture.Status) {
				t.Fatal("too many polls")
			}
			return response(200, string(fixture.Status[i]), r), nil
		case "/api/web-scraper-api/tasks-download":
			return response(200, string(fixture.Address), r), nil
		case "/download":
			return response(200, string(fixture.Records), r), nil
		default:
			t.Fatalf("unexpected URL %s", r.URL)
			return nil, nil
		}
	}))
	result, err := client.ScrapeResponse(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"}, ScrapeOptions{MaxWait: 10 * time.Second})
	if err != nil || result.TaskID != "task-scrape-1" || result.RecordCount != 1 || result.Status != StatusCompleted || polls != 2 || submits != 1 {
		t.Fatalf("fixture result=%#v polls=%d submits=%d err=%v", result, polls, submits, err)
	}
}

func TestSharedSERPEnvelopes(t *testing.T) {
	raw, err := os.ReadFile("testdata/contract-fixtures/serp/envelopes.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for name, body := range fixture {
		t.Run(name, func(t *testing.T) {
			client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { return response(200, string(body), r), nil }))
			result, err := client.SearchResponse(context.Background(), nil)
			if name == "success" {
				if err != nil || result.TaskID != "task-serp-1" {
					t.Fatalf("%#v %v", result, err)
				}
			} else if !errors.As(err, new(*NotCollectedError)) {
				t.Fatalf("wrong fixture error: %v", err)
			}
		})
	}
}

func TestConcurrentClientSearchesAndScrapes(t *testing.T) {
	client := scraperClientWithDownload(t, `[{"name":"ok"}]`)
	base := client.httpClient.Transport
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/request" {
			return response(200, `{"search_metadata":{"id":"search"}}`, r), nil
		}
		return base.RoundTrip(r)
	})
	var workers sync.WaitGroup
	failures := make(chan error, 24)
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			got, err := client.SearchResponse(context.Background(), SearchParams{"q": "x"})
			if err != nil || got.TaskID != "search" {
				failures <- fmt.Errorf("search: %v", err)
			}
			result, err := client.ScrapeResponse(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"})
			if err != nil || result.RecordCount != 1 {
				failures <- fmt.Errorf("scrape: %v", err)
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestAllPublicSearchEntrypointsAndRequest(t *testing.T) {
	handler := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(200, `{"search_metadata":{"id":"public"},"organic":[]}`, r), nil
	})
	opts := ClientOptions{APIKey: "key", HTTPClient: &http.Client{Transport: handler}}
	client, err := NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	var alias *ThorDataClient = client
	calls := []func() (any, error){func() (any, error) { return alias.SearchJSON(context.Background(), nil) }, func() (any, error) { return Search(context.Background(), nil, opts) }, func() (any, error) { return SearchJSON(context.Background(), nil, opts) }, func() (any, error) { return RawSearch(context.Background(), nil, opts) }}
	for _, call := range calls {
		if value, err := call(); err != nil || value == nil {
			t.Fatalf("public call: %#v %v", value, err)
		}
	}
	if _, err := client.Search(context.Background(), nil, SearchOptions{}, SearchOptions{}); !errors.As(err, new(*InvalidRequestError)) {
		t.Error("accepted two SearchOptions")
	}
	if _, err := client.Scrape(context.Background(), supportedScrapers[0], nil, ScrapeOptions{}, ScrapeOptions{}); !errors.As(err, new(*InvalidRequestError)) {
		t.Error("accepted two ScrapeOptions")
	}
	for _, call := range []func() error{func() error { _, err := client.Search(nil, nil); return err }, func() error { _, err := client.RawSearch(nil, nil); return err }, func() error { _, err := client.Request(nil, "GET", "", nil, RequestOptions{}); return err }, func() error { _, err := client.Scrape(nil, supportedScrapers[0], nil); return err }} {
		if !errors.As(call(), new(*ConfigurationError)) {
			t.Fatal("nil context accepted")
		}
	}
	low := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("custom") != "yes" {
			t.Error("did not preserve unknown query")
		}
		if r.Body != nil && r.ContentLength != 0 {
			t.Error("GET has body")
		}
		return response(429, "rate limited", r), nil
	}))
	resp, err := low.Request(context.Background(), "GET", "/custom", map[string]any{"custom": "yes"}, RequestOptions{FilterParams: Bool(false)})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 429 {
		t.Error("Request swallowed HTTP status")
	}
}

func TestConfigurationPrecedenceAndOwnership(t *testing.T) {
	t.Setenv("THORDATA_API_KEY", "env-key")
	t.Setenv("THORDATA_API_TOKEN", "env-token")
	t.Setenv("TALORDATA_API_TOKEN", "legacy")
	for _, tc := range []struct {
		opts ClientOptions
		want string
	}{{ClientOptions{APIKey: "a", APIToken: "b", Token: "c"}, "a"}, {ClientOptions{APIToken: "b", Token: "c"}, "b"}, {ClientOptions{Token: "c"}, "c"}, {ClientOptions{}, "env-key"}} {
		client, err := NewClient(tc.opts)
		if err != nil || client.apiKey != tc.want {
			t.Fatalf("priority: %v %v", client, err)
		}
		_ = client.Close()
	}
	t.Setenv("THORDATA_API_KEY", "")
	t.Setenv("THORDATA_API_TOKEN", "")
	client, err := NewClient(ClientOptions{})
	if err != nil || client.apiKey != "legacy" {
		t.Fatal("legacy environment not used")
	}
	_ = client.Close()
	headers := http.Header{"X-Example": []string{"original"}}
	jar, _ := cookiejar.New(nil)
	transport := &trackingTransport{}
	injected := &http.Client{Transport: transport, Jar: jar}
	client, err = NewClient(ClientOptions{APIKey: "key", Headers: headers, HTTPClient: injected})
	if err != nil {
		t.Fatal(err)
	}
	headers.Set("X-Example", "changed")
	if client.headers.Get("X-Example") != "original" {
		t.Error("retained caller header reference")
	}
	if client.httpClient == injected || injected.Jar != jar {
		t.Error("mutated caller client")
	}
	_ = client.Close()
	_ = client.Close()
	if transport.closed {
		t.Error("closed caller transport")
	}
}

type trackingTransport struct{ closed bool }

func (c *trackingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return response(200, `{}`, r), nil
}
func (c *trackingTransport) CloseIdleConnections() { c.closed = true }

func TestRealHTTPForm(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("engine") != "google" || r.Form.Get("q") != "hello" {
			t.Errorf("bad wire form: %v", r.Form)
		}
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Error("missing auth")
		}
		_, _ = io.WriteString(w, `{"code":200,"data":{"result":{"ok":true},"task_id":"http"}}`)
	}))
	defer server.Close()
	client, err := NewClient(ClientOptions{APIKey: "key", APIURL: server.URL + "/request"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, err := client.SearchResponse(context.Background(), SearchParams{"query": "hello"})
	if err != nil || result.TaskID != "http" {
		t.Fatalf("HTTP response: %#v %v", result, err)
	}
}

func TestDownloadHasNoCookiesOrCredentialHeaders(t *testing.T) {
	client := scraperClientWithDownload(t, `[{"name":"ok"}]`)
	base := client.httpClient.Transport
	client.headers = http.Header{"AUTHORIZATION": []string{"override"}, "Cookie": []string{"private"}, "X-Private": []string{"sensitive"}, "Sign": []string{"bad"}, "Origin": []string{"spoof"}}
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/download" {
			for _, name := range []string{"Authorization", "Cookie", "Sign", "X-Private", "Origin"} {
				if r.Header.Get(name) != "" {
					t.Errorf("download retained %s", name)
				}
			}
		} else {
			for _, name := range []string{"Origin", "Cookie", "platform", "api_source", "api-source", "source_platform"} {
				if r.Header.Get(name) != "" {
					t.Errorf("scraper retained %s", name)
				}
			}
			if r.URL.Host != "scraperapi.thordata.com" && r.URL.Host != "api.thordata.com" {
				t.Fatal("wrong task host")
			}
		}
		return base.RoundTrip(r)
	})
	if _, err := client.Scrape(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"}); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedPollEnvelopes(t *testing.T) {
	for _, body := range []string{`{"code":401,"data":[]}`, `{"code":200,"data":{}}`, `{"code":200,"data":[]}`, `{"code":200,"data":[{"task_id":"TASK-RESULT","status":"Ready"},{"task_id":"TASK-RESULT","status":"Ready"}]}`, `{"code":200,"data":[{"task_id":"TASK-RESULT","status":"ready"}]}`} {
		client := scraperClientWithDownload(t, "")
		base := client.httpClient.Transport
		client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "tasks-status") {
				return response(200, body, r), nil
			}
			return base.RoundTrip(r)
		})
		_, err := client.Scrape(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"})
		if !errors.As(err, new(*WebScraperError)) {
			t.Errorf("accepted poll %s", body)
		}
	}
}

func TestIncludeErrorsFalseAndFailedDiagnosticFallback(t *testing.T) {
	for _, include := range []bool{false, true} {
		client := scraperClientWithDownload(t, "")
		base := client.httpClient.Transport
		downloads := 0
		client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "tasks-status") {
				return response(200, `{"code":200,"data":[{"task_id":"TASK-RESULT","status":"Failed","msg":"original"}]}`, r), nil
			}
			if strings.HasSuffix(r.URL.Path, "tasks-download") {
				downloads++
				return response(500, "failure", r), nil
			}
			return base.RoundTrip(r)
		})
		_, err := client.Scrape(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"}, ScrapeOptions{IncludeErrors: Bool(include)})
		var se *WebScraperError
		if !errors.As(err, &se) || se.Code != ErrorScrapeFailed || se.Reason != "original" || !include && downloads != 0 || include && downloads != 1 {
			t.Errorf("failure diagnostics: %v downloads=%d", err, downloads)
		}
	}
}

func TestDefaultTransportCanBeWrapped(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) { return response(200, `{}`, r), nil })
	c, err := NewClient(ClientOptions{APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
}

func TestUnicodeDownloadKeyRedaction(t *testing.T) {
	got := replaceSecrets("safe 密钥 safe", []string{"密钥"})
	if got != "safe [redacted] safe" {
		t.Errorf("unicode redaction=%q", got)
	}
}
