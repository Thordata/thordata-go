package thordata

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestValidateWebScraperParams(t *testing.T) {
	got, err := ValidateWebScraperParams("youtube_video-post_by-explore", WebScraperParams{
		"url": "https://www.youtube.com/feed/explore", "all_tabs": false,
	})
	if err != nil {
		t.Fatalf("ValidateWebScraperParams() error = %v", err)
	}
	if got["all_tabs"] != "flase" {
		t.Fatalf("all_tabs = %#v", got["all_tabs"])
	}
	amazon, err := ValidateWebScraperParams("amazon_product_by-category-url", WebScraperParams{
		"url": "https://example.com/category", "page_turning": 2,
	})
	if err != nil || amazon["sort_by"] != "Best Sellers" {
		t.Fatalf("amazon = %#v, error = %v", amazon, err)
	}
	if _, err := ValidateWebScraperParams("unknown", nil); !errors.As(err, new(*InvalidRequestError)) {
		t.Fatalf("unknown scraper error = %T %v", err, err)
	}
	if _, err := ValidateWebScraperParams("linkedin_company_information_by-url", WebScraperParams{"url": "https://user:pass@example.com"}); !errors.As(err, new(*InvalidRequestError)) {
		t.Fatalf("userinfo URL error = %T %v", err, err)
	}
}

func TestScrapeMaxWaitValidationExplainsWholeSeconds(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid MaxWait reached the network")
		return nil, nil
	}))
	for _, maxWait := range []time.Duration{-time.Second, 500 * time.Millisecond, 1500 * time.Millisecond, 301 * time.Second} {
		t.Run(maxWait.String(), func(t *testing.T) {
			_, err := client.Scrape(context.Background(), "linkedin_company_information_by-url", WebScraperParams{"url": "https://example.com"}, ScrapeOptions{MaxWait: maxWait})
			var invalid *InvalidRequestError
			if !errors.As(err, &invalid) || invalid.Code == nil || *invalid.Code != 400 {
				t.Fatalf("MaxWait=%s: expected InvalidRequestError(400), got %v", maxWait, err)
			}
			if !strings.Contains(err.Error(), "whole seconds") {
				t.Fatalf("MaxWait validation must explain whole seconds: %v", err)
			}
		})
	}
}

func TestScrapeMaxWaitAcceptsDefaultAndWholeSecondBounds(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("canceled request reached the network")
		return nil, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, maxWait := range []time.Duration{0, time.Second, 300 * time.Second} {
		t.Run(maxWait.String(), func(t *testing.T) {
			_, err := client.Scrape(ctx, "linkedin_company_information_by-url", WebScraperParams{"url": "https://example.com"}, ScrapeOptions{MaxWait: maxWait})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("MaxWait=%s should pass validation and honor cancellation; got %v", maxWait, err)
			}
		})
	}
}

func TestScrapeRunsLifecycleAndReturnsMetadata(t *testing.T) {
	var paths []string
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		body, _ := ioReadAll(r)
		form, _ := url.ParseQuery(string(body))
		switch r.URL.Path {
		case "/builder":
			if form.Get("source_platform") != "go" || form.Get("api_source") != "sdk" || r.Header.Get("Authorization") != "Bearer secret-token" {
				t.Fatalf("builder form=%v headers=%v", form, r.Header)
			}
			return response(http.StatusOK, `{"code":200,"data":{"task_id":"TASK-1"}}`, r), nil
		case "/api/web-scraper-api/tasks-status":
			if r.Header.Get("sign") != "secret-token" || form.Get("tasks_ids") != "TASK-1" {
				t.Fatalf("status form=%v headers=%v", form, r.Header)
			}
			return response(http.StatusOK, `{"code":200,"data":[{"task_id":"TASK-1","status":"Ready"}]}`, r), nil
		case "/api/web-scraper-api/tasks-download":
			return response(http.StatusOK, `{"code":200,"data":{"tasks_ids":"TASK-1","download":"https://scraperapi.thordata.com/download?task_id=TASK-1&type=json&api_key=server-key"}}`, r), nil
		case "/download":
			return response(http.StatusOK, `[{"name":"Example","success":true}]`, r), nil
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
			return nil, nil
		}
	}))
	got, err := client.ScrapeResponse(context.Background(), "linkedin_company_information_by-url", WebScraperParams{"url": "https://example.com/company"})
	if err != nil {
		t.Fatalf("ScrapeResponse() error = %v", err)
	}
	if got.TaskID != "TASK-1" || got.Status != StatusCompleted || got.RecordCount != 1 || got.ErrorRecordCount != 0 {
		t.Fatalf("response = %#v", got)
	}
	if strings.Join(paths, ",") != "/builder,/api/web-scraper-api/tasks-status,/api/web-scraper-api/tasks-download,/download" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestScrapePartialAndRejectsUntrustedDownload(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/builder":
			return response(http.StatusOK, `{"code":200,"data":{"task_id":"TASK-2"}}`, r), nil
		case "/api/web-scraper-api/tasks-status":
			return response(http.StatusOK, `{"code":200,"data":[{"task_id":"TASK-2","status":"Ready"}]}`, r), nil
		case "/api/web-scraper-api/tasks-download":
			return response(http.StatusOK, `{"code":200,"data":{"tasks_ids":"TASK-2","download":"https://scraperapi.thordata.com/download?task_id=TASK-2&type=json&api_key=server-key"}}`, r), nil
		case "/download":
			return response(http.StatusOK, `[{"name":"Example","success":true},{"success":false,"error":"blocked"}]`, r), nil
		default:
			return nil, errors.New("unexpected request")
		}
	}))
	got, err := client.ScrapeResponse(context.Background(), "linkedin_company_information_by-url", WebScraperParams{"url": "https://example.com/company"})
	if err != nil || got.Status != StatusPartial || got.ErrorRecordCount != 1 {
		t.Fatalf("partial response=%#v error=%v", got, err)
	}
	bad := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/builder" {
			return response(http.StatusOK, `{"code":200,"data":{"task_id":"TASK-3"}}`, r), nil
		}
		if r.URL.Path == "/api/web-scraper-api/tasks-status" {
			return response(http.StatusOK, `{"code":200,"data":[{"task_id":"TASK-3","status":"Ready"}]}`, r), nil
		}
		return response(http.StatusOK, `{"code":200,"data":{"tasks_ids":"TASK-3","download":"https://evil.example/download?task_id=TASK-3&type=json"}}`, r), nil
	}))
	_, err = bad.Scrape(context.Background(), "linkedin_company_information_by-url", WebScraperParams{"url": "https://example.com/company"})
	var scraperErr *WebScraperError
	if !errors.As(err, &scraperErr) || scraperErr.Code != ErrorDownloadURLInvalid || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("download URL error = %T %#v", err, err)
	}
}

func TestScrapeTimeoutDoesNotResubmit(t *testing.T) {
	submissions := 0
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/builder" {
			submissions++
			return response(http.StatusOK, `{"code":200,"data":{"task_id":"TASK-4"}}`, r), nil
		}
		return response(http.StatusOK, `{"code":200,"data":[{"task_id":"TASK-4","status":"Running"}]}`, r), nil
	}))
	_, err := client.ScrapeResponse(context.Background(), "linkedin_company_information_by-url", WebScraperParams{"url": "https://example.com/company"}, ScrapeOptions{MaxWait: time.Second})
	var scraperErr *WebScraperError
	if !errors.As(err, &scraperErr) || scraperErr.Code != ErrorWaitTimeout || scraperErr.TaskID != "TASK-4" || submissions != 1 {
		t.Fatalf("timeout error=%#v submissions=%d", err, submissions)
	}
}

func TestScrapeRejectsMissingPollBusinessCode(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/builder" {
			return response(http.StatusOK, `{"code":200,"data":{"task_id":"TASK-CODE"}}`, r), nil
		}
		return response(http.StatusOK, `{"data":[{"task_id":"TASK-CODE","status":"Ready"}]}`, r), nil
	}))
	_, err := client.ScrapeResponse(context.Background(), "linkedin_company_information_by-url", WebScraperParams{"url": "https://example.com/company"})
	var scraperErr *WebScraperError
	if !errors.As(err, &scraperErr) || scraperErr.Code != ErrorProtocol {
		t.Fatalf("poll envelope error = %T %v", err, err)
	}
}

func TestScrapeRejectsAllFailedAndMalformedRecords(t *testing.T) {
	for _, downloaded := range []string{
		`[{"success":false,"error":"blocked"}]`,
		`["not a record"]`,
		`{"code":300,"msg":"not collected"}`,
	} {
		client := scraperClientWithDownload(t, downloaded)
		_, err := client.ScrapeResponse(context.Background(), "linkedin_company_information_by-url", WebScraperParams{"url": "https://example.com/company"})
		var scraperErr *WebScraperError
		if !errors.As(err, &scraperErr) {
			t.Errorf("download %s error = %T %v", downloaded, err, err)
			continue
		}
		if downloaded == `[{"success":false,"error":"blocked"}]` && scraperErr.Code != ErrorScrapeFailed {
			t.Errorf("all-failed code = %s", scraperErr.Code)
		}
		if downloaded == `["not a record"]` && scraperErr.Code != ErrorInvalidResult {
			t.Errorf("invalid-record code = %s", scraperErr.Code)
		}
	}
}

func TestScrapeHandlesStructuredErrorFieldWithoutPanicking(t *testing.T) {
	client := scraperClientWithDownload(t, `[{"name":"Example","error":{"code":"blocked"}},{"name":"Success"}]`)
	response, err := client.ScrapeResponse(context.Background(), "linkedin_company_information_by-url", WebScraperParams{"url": "https://example.com/company"})
	if err != nil || response.Status != StatusPartial || response.ErrorRecordCount != 1 {
		t.Fatalf("response=%#v error=%v", response, err)
	}
}

func TestScraperHonorsPerRequestTimeout(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/builder" {
			return response(http.StatusOK, `{"code":200,"data":{"task_id":"TASK-HTTP-TIMEOUT"}}`, r), nil
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	client.timeout = 50 * time.Millisecond
	_, err := client.ScrapeResponse(context.Background(), "linkedin_company_information_by-url", WebScraperParams{"url": "https://example.com/company"}, ScrapeOptions{MaxWait: 2 * time.Second})
	var scraperErr *WebScraperError
	if !errors.As(err, &scraperErr) || scraperErr.Code != ErrorRequestFailed {
		t.Fatalf("per-request timeout error = %T %#v", err, err)
	}
}

func scraperClientWithDownload(t *testing.T, downloaded string) *Client {
	t.Helper()
	return testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/builder":
			return response(http.StatusOK, `{"code":200,"data":{"task_id":"TASK-RESULT"}}`, r), nil
		case "/api/web-scraper-api/tasks-status":
			return response(http.StatusOK, `{"code":200,"data":[{"task_id":"TASK-RESULT","status":"Ready"}]}`, r), nil
		case "/api/web-scraper-api/tasks-download":
			return response(http.StatusOK, `{"code":200,"data":{"tasks_ids":"TASK-RESULT","download":"https://scraperapi.thordata.com/download?task_id=TASK-RESULT&type=json&api_key=server-key"}}`, r), nil
		case "/download":
			return response(http.StatusOK, downloaded, r), nil
		default:
			return nil, errors.New("unexpected request")
		}
	}))
}

func TestScraperRoundTripperTestDoesNotAccessNetwork(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(r.URL.Host, "scraperapi.thordata.com") && !strings.HasPrefix(r.URL.Host, "api.thordata.com") {
			t.Fatalf("unexpected network host %q", r.URL.Host)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		return response(http.StatusBadGateway, `{"code":502}`, r), nil
	}))
	_, err := client.ScrapeResponse(context.Background(), "linkedin_company_information_by-url", WebScraperParams{"url": "https://example.com/company"}, ScrapeOptions{MaxWait: time.Second})
	if err == nil {
		t.Fatal("expected mocked gateway error")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("body read failed") }
func (failingReader) Close() error             { return nil }

func TestScrapeSubmitBodyReadFailureIsUnknown(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: failingReader{}, Request: r}, nil
	}))
	_, err := client.Scrape(context.Background(), "linkedin_company_information_by-url", WebScraperParams{"url": "https://example.com"})
	var scraperErr *WebScraperError
	if !errors.As(err, &scraperErr) || scraperErr.Code != ErrorSubmissionUnknown {
		t.Fatalf("submit body error = %#v", err)
	}
}

func TestValidateDownloadAcceptsExplicitDefaultHTTPSPort(t *testing.T) {
	_, _, err := validateDownloadURL("https://scraperapi.thordata.com:443/download?task_id=TASK-1&type=json", "TASK-1", supportedScrapers[0])
	if err != nil {
		t.Fatalf("explicit port 443 rejected: %v", err)
	}
}
