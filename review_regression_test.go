package thordata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type cancelOnEOFBody struct {
	reader *strings.Reader
	cancel context.CancelFunc
}

func (b *cancelOnEOFBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if err == io.EOF {
		b.cancel()
	}
	return n, err
}
func (b *cancelOnEOFBody) Close() error { return nil }

func TestCancelledAfterDownloadNeverReturnsSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := scraperClientWithDownload(t, `[{"name":"x"}]`)
	base := client.httpClient.Transport
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		resp, err := base.RoundTrip(r)
		if r.URL.Path == "/download" {
			resp.Body = &cancelOnEOFBody{strings.NewReader(`[{"name":"x"}]`), cancel}
		}
		return resp, err
	})
	_, err := client.Scrape(ctx, supportedScrapers[0], WebScraperParams{"url": "https://example.com"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("completed after cancellation: %v", err)
	}
}

func TestDownloadErrorRedactsServerSideKey(t *testing.T) {
	client := scraperClientWithDownload(t, "")
	base := client.httpClient.Transport
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/download" {
			return response(403, `{"msg":"failed server-key"}`, r), nil
		}
		return base.RoundTrip(r)
	})
	_, err := client.Scrape(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"})
	var se *WebScraperError
	if !errors.As(err, &se) {
		t.Fatal(err)
	}
	if strings.Contains(se.Reason, "server-key") || strings.Contains(err.Error(), "server-key") {
		t.Errorf("download key leaked: %v", err)
	}
}

func TestRedactionMatchesOnlyPercentEscapesCaseInsensitively(t *testing.T) {
	got := replaceSecrets("AbC%2b%2F%3d abc%2b%2f%3d", []string{"AbC+/="})
	if got != "[redacted] abc%2b%2f%3d" {
		t.Errorf("changed case-sensitive business text: %q", got)
	}
}

func TestNestedSERPJSONRequiresWholeDocument(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(200, `{"code":200,"data":"{\"x\":1} trailing"}`, r), nil
	}))
	got, err := client.Search(context.Background(), nil)
	if err != nil || got != `{"x":1} trailing` {
		t.Fatalf("nested value=%#v error=%v", got, err)
	}
}

func TestErrorCodeZeroNumericVariants(t *testing.T) {
	for _, body := range []string{`[{"name":"ok","error":0.0}]`, `[{"name":"ok","error_code":"200.0"}]`, `[{"name":"ok","error_code":200.0}]`} {
		client := scraperClientWithDownload(t, body)
		got, err := client.ScrapeResponse(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"})
		if err != nil || got.Status != StatusCompleted {
			t.Errorf("%s: %v %#v", body, err, got)
		}
	}
}

func TestScraperDiagnosticsPartialDownload(t *testing.T) {
	client := scraperClientWithDownload(t, `[{"name":"OK"},{"error":"server-key failed","error_code":123}]`)
	base := client.httpClient.Transport
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "tasks-status") {
			return response(200, `{"code":200,"data":[{"task_id":"TASK-RESULT","status":"Failed","msg":"original"}]}`, r), nil
		}
		return base.RoundTrip(r)
	})
	_, err := client.Scrape(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"})
	var se *WebScraperError
	if !errors.As(err, &se) || !strings.Contains(se.Reason, "failed") || strings.Contains(se.Reason, "server-key") {
		t.Errorf("lost/unsafe partial diagnostics: %v", err)
	}
}

func TestRequestURLValidationDoesNotLeakUserinfo(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { t.Fatal("invalid URL reached network"); return nil, nil }))
	for _, address := range []string{"https://user:password@example.com/request", "https:/bad", "http://", "https://host%zz/"} {
		_, err := client.Request(context.Background(), "POST", address, nil, RequestOptions{})
		if !errors.As(err, new(*ConfigurationError)) {
			t.Errorf("accepted %q: %v", address, err)
		}
	}
}

func TestRequestBodyErrorIsTypedAndRedacted(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: readerErrorBody{fmt.Errorf("secret-token: %w", context.DeadlineExceeded)}, Request: r}, nil
	}))
	resp, err := client.Request(context.Background(), "POST", "/request", nil, RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, new(*TimeoutError)) || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("body error: %T %v", err, err)
	}
}

type readerErrorBody struct{ err error }

func (r readerErrorBody) Read([]byte) (int, error) { return 0, r.err }
func (r readerErrorBody) Close() error             { return nil }

func TestInvalidDownloadDocumentIsProtocolError(t *testing.T) {
	client := scraperClientWithDownload(t, "not JSON")
	_, err := client.Scrape(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"})
	var se *WebScraperError
	if !errors.As(err, &se) || se.Code != ErrorProtocol || se.Stage != "download" {
		t.Errorf("wrong decode error: %v", err)
	}
}

func TestScraperExtraSourceHeadersBlocked(t *testing.T) {
	client := scraperClientWithDownload(t, `[{"name":"ok"}]`)
	base := client.httpClient.Transport
	client.headers = http.Header{"X-Thordata-Platform": []string{"spoof"}, "X-Thordata-Source": []string{"spoof"}}
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Thordata-Platform") != "" || r.Header.Get("X-Thordata-Source") != "" {
			t.Error("source override leaked")
		}
		return base.RoundTrip(r)
	})
	if _, err := client.Scrape(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"}); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownErrorNameAndUnsafeMetadata(t *testing.T) {
	err := scraperError(ErrorCode("unknown"), "parse", "TASK-1", supportedScrapers[0], "why", nil, 0, nil, []string{supportedScrapers[0]})
	if err.Code != ErrorRequestFailed || err.SpiderID != "" {
		t.Errorf("unsafe metadata: %+v code=%s spider=%s", err, err.Code, err.SpiderID)
	}
}

func TestSERPUsesCanonicalEncodingAndNumericIDs(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(200, `{"code":200,"data":{"result":"{\"n\":9007199254740993}","task_id":123}}`, r), nil
	}))
	result, err := client.SearchResponse(context.Background(), nil)
	if err != nil || result.TaskID != "123" || result.Result.(map[string]any)["n"] != json.Number("9007199254740993") {
		t.Fatalf("numeric metadata: %#v %v", result, err)
	}
}

func TestUnknownPollErrorPreservesHTTPStatus(t *testing.T) {
	client := scraperClientWithDownload(t, "")
	base := client.httpClient.Transport
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "tasks-status") {
			return response(429, `{"msg":"rate limited"}`, r), nil
		}
		return base.RoundTrip(r)
	})
	_, err := client.Scrape(context.Background(), supportedScrapers[0], WebScraperParams{"url": "https://example.com"})
	var se *WebScraperError
	if !errors.As(err, &se) || se.HTTPStatus != 429 || se.Code != ErrorRequestFailed {
		t.Fatalf("poll error: %v", err)
	}
}

func TestRequestTimeoutStillActiveDuringBody(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: contextBody{r.Context()}, Request: r}, nil
	}))
	client.timeout = 20 * time.Millisecond
	_, err := client.Search(context.Background(), nil)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, new(*TimeoutError)) {
		t.Fatalf("body deadline: %T %v", err, err)
	}
}

type contextBody struct{ ctx context.Context }

func (b contextBody) Read([]byte) (int, error) { <-b.ctx.Done(); return 0, b.ctx.Err() }
func (b contextBody) Close() error             { return nil }

var _ = url.QueryEscape
