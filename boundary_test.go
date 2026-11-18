package thordata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestConfigurationBoundaries(t *testing.T) {
	t.Setenv("THORDATA_API_KEY", "env-key")
	for _, opts := range []ClientOptions{{APIKey: "   "}, {APIKey: "key\x7f"}, {APIKey: "\nkey"}, {APIKey: "key", APIURL: "https://user:pass@example.com"}} {
		if _, err := NewClient(opts); !errors.As(err, new(*ConfigurationError)) {
			t.Errorf("configuration accepted: %#v", opts)
		}
	}
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { return response(200, `{}`, r), nil }))
	if strings.Contains(fmt.Sprintf("%#v", client), "secret-token") {
		t.Error("GoString leaked key")
	}
}

func TestSERPParsingBoundaries(t *testing.T) {
	for _, body := range []string{`{} {}`, `{} garbage`, `{"code":"200bad"}`, `{"code":false}`, `[]`, `null`} {
		t.Run(body, func(t *testing.T) {
			client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { return response(200, body, r), nil }))
			_, err := client.Search(context.Background(), nil)
			if !errors.As(err, new(*APIError)) {
				t.Fatalf("invalid response accepted: %s, %v", body, err)
			}
		})
	}
	for _, body := range []string{`{"code":0,"data":"Collection failed"}`, `{"code":401,"msg":"invalid"}`} {
		client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { return response(200, body, r), nil }))
		_, err := client.Search(context.Background(), nil)
		var api *APIError
		if !errors.As(err, &api) {
			t.Fatal(err)
		}
		if strings.Contains(body, `"code":0`) && (api.Code == nil || *api.Code != 0) {
			t.Errorf("lost original business code: %#v", api)
		}
	}
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(401, `{"code":500,"msg":"bad"}`, r), nil
	}))
	_, err := client.Search(context.Background(), nil)
	if !errors.As(err, new(*AuthenticationError)) {
		t.Fatalf("HTTP 401 must win: %T", err)
	}
}

func TestSERPParameterBoundaries(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid params reached network")
		return nil, nil
	}))
	for _, value := range []any{math.NaN(), math.Inf(1), json.Number("bad"), func() {}, make(chan int)} {
		_, err := client.Search(context.Background(), SearchParams{"q": value})
		if !errors.As(err, new(*InvalidRequestError)) {
			t.Errorf("accepted value %v", value)
		}
	}
	for i := 0; i < 50; i++ {
		form, err := client.buildSearchPayload(SearchParams{"q": "canonical", "query": "alias", "gl": nil, "country": "us", "engine": "bing"}, "")
		if err != nil || form["q"] != "canonical" || form["gl"] != "us" {
			t.Fatalf("alias conflict: %v %v", form, err)
		}
	}
}

func TestScraperRawAndAuthentication(t *testing.T) {
	client := scraperClientWithDownload(t, `[{"name":"Example"}]`)
	got, err := client.ScrapeResponse(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprint(got.Raw)
	if !strings.Contains(raw, "submission") || strings.Contains(raw, "server-key") {
		t.Errorf("incomplete/unsafe Raw: %s", raw)
	}
	for _, code := range []int{401, 402, 403} {
		client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { return response(code, `{"msg":"denied"}`, r), nil }))
		_, err := client.Scrape(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"})
		var se *WebScraperError
		if !errors.As(err, &se) || se.Code != scraperBusinessCode(code, "submit") {
			t.Errorf("HTTP %d: %v", code, err)
		}
	}
}

func TestScraperResultBoundaries(t *testing.T) {
	cases := []struct {
		body   string
		status ScrapeStatus
		count  int
	}{
		{"[{\"name\":\"One\"}]\n[{\"name\":\"Two\"}]", StatusCompleted, 2},
		{`[]`, StatusEmpty, 0}, {`{"url":"https://example.com","success":true}`, StatusEmpty, 1},
		{`[{"name":"ok","error":0,"error_code":null}]`, StatusCompleted, 1},
		{`[{"name":"ok","error_code":""}]`, StatusCompleted, 1},
	}
	for _, tc := range cases {
		client := scraperClientWithDownload(t, tc.body)
		got, err := client.ScrapeResponse(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"})
		if err != nil || got.Status != tc.status || got.RecordCount != tc.count {
			t.Errorf("%s: %#v %v", tc.body, got, err)
		}
	}
}

func TestScraperDownloadValidation(t *testing.T) {
	for _, tail := range []string{"&extra=1", "&task_id=TASK-1", "&bad=%zz", "&bad;ignored=1", "#fragment"} {
		_, _, err := validateDownloadURL("https://scraperapi.thordata.com/download?task_id=TASK-1&type=json"+tail, "TASK-1", supportedScrapers[0])
		if !errors.As(err, new(*WebScraperError)) {
			t.Errorf("accepted query: %s", tail)
		}
	}
}

func TestSharedFixtureCopiesStaySynchronized(t *testing.T) {
	for _, file := range []string{"serp/basic-request.json", "serp/envelopes.json", "web-scraper/lifecycle.json"} {
		local, err := os.ReadFile(filepath.Join("testdata/contract-fixtures", file))
		if err != nil {
			t.Fatal(err)
		}
		shared, err := os.ReadFile(filepath.Join("../contract-fixtures", file))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if string(local) != string(shared) {
			t.Errorf("fixture differs: %s", file)
		}
	}
}

func TestRedactionBoundaries(t *testing.T) {
	secret := "key+/="
	encoded := strings.ToLower(url.QueryEscape(secret))
	got := redactText("credential="+encoded+" a\nb\tc "+strings.Repeat("x", 300), []string{secret})
	if strings.ContainsAny(got, "\n\t\r") || len([]rune(got)) > 250 || strings.Contains(strings.ToLower(got), strings.ToLower(encoded)) {
		t.Errorf("unsafe reason: %s", got)
	}
	err := scraperError(ErrorRequestFailed, "download", "TASK-1", supportedScrapers[0], "failure", nil, 0, &url.Error{Op: "Get", URL: "https://host?api_key=" + secret, Err: errors.New("network " + secret)}, []string{secret})
	for cause := error(err); cause != nil; cause = errors.Unwrap(cause) {
		if strings.Contains(cause.Error(), secret) {
			t.Errorf("error chain leaked key: %s", cause)
		}
	}
	long := "first\nsecond\n" + strings.Repeat("x", 400)
	if redactValue(long, []string{secret}) != long {
		t.Error("truncated business data")
	}
}

func TestResponseModelsJSONNames(t *testing.T) {
	encoded, _ := json.Marshal(SearchResponse{TaskID: "x"})
	var obj map[string]any
	_ = json.Unmarshal(encoded, &obj)
	if obj["task_id"] != "x" {
		t.Errorf("JSON model=%s", encoded)
	}
}

func TestRequestDeadlineSurvivesHeadersAndEndsAtEOF(t *testing.T) {
	var requestContext context.Context
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requestContext = r.Context()
		return response(200, `ok`, r), nil
	}))
	resp, err := client.Request(context.Background(), "POST", "/request", nil, RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if requestContext.Err() != nil {
		t.Fatal("context canceled at headers")
	}
	_, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if requestContext.Err() != context.Canceled {
		t.Error("EOF did not release request context")
	}
	_ = resp.Body.Close()
}

func TestScraperPreCancelledDoesNotSend(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { t.Fatal("sent canceled request"); return nil, nil }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Scrape(ctx, supportedScrapers[0], WebScraperParams{"url": "https://example.com"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancel chain: %v", err)
	}
}

func TestScraperSubmitTimeoutIsUnknown(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() }))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.Scrape(ctx, supportedScrapers[0], WebScraperParams{"url": "https://example.com"})
	var se *WebScraperError
	if !errors.As(err, &se) || se.Code != ErrorSubmissionUnknown || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("submit timeout: %v", err)
	}
}

func TestScraperFailureDiagnostics(t *testing.T) {
	client := scraperClientWithDownload(t, `[{"error":"downloaded detail","error_code":123}]`)
	base := client.httpClient.Transport
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "tasks-status") {
			return response(200, `{"code":200,"data":[{"task_id":"TASK-RESULT","status":"Failed","msg":"original"}]}`, r), nil
		}
		return base.RoundTrip(r)
	})
	_, err := client.Scrape(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"})
	var se *WebScraperError
	if !errors.As(err, &se) || !strings.Contains(se.Reason, "downloaded detail") {
		t.Errorf("lost diagnostics: %v", err)
	}
}

func TestSourceModelCompatibility(t *testing.T) {
	if !reflect.DeepEqual(SupportedWebScrapers(), supportedScrapers) {
		t.Fatal("spider list mismatch")
	}
}
