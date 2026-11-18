package thordata

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"testing"
)

func TestSearchNormalizesFormAndEnvelope(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/request" || r.Header.Get("Origin") != "Go" || r.Header.Get("platform") != "go" || r.Header.Get("api-source") != "sdk" || r.Header.Get("Accept") != "application/json" {
			t.Fatalf("url=%s origin=%q platform=%q source=%q", r.URL, r.Header.Get("Origin"), r.Header.Get("platform"), r.Header.Get("api-source"))
		}
		body, _ := ioReadAll(r)
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("form parse error = %v", err)
		}
		checks := map[string]string{
			"engine": "google", "q": "pizza", "gl": "us", "hl": "en", "safe": "1", "json": "1", "isjson": "1",
		}
		for key, want := range checks {
			if form.Get(key) != want {
				t.Errorf("form[%s] = %q, want %q", key, form.Get(key), want)
			}
		}
		if form.Get("ignored") != "" {
			t.Error("unknown field was sent")
		}
		return response(http.StatusOK, `{"code":200,"data":{"result":{"organic":[{"title":"Pizza"}]},"task_id":"task-1"}}`, r), nil
	}))
	got, err := client.SearchResponse(context.Background(), SearchParams{
		"engine": "google_search", "query": "pizza", "country": "us", "language": "en", "safe": true, "ignored": "drop",
	})
	if err != nil {
		t.Fatalf("SearchResponse() error = %v", err)
	}
	if got.TaskID != "task-1" {
		t.Fatalf("TaskID = %q", got.TaskID)
	}
	result, ok := got.Result.(map[string]any)
	if !ok || result["organic"] == nil {
		t.Fatalf("Result = %#v", got.Result)
	}
}

func TestSearchSupportsYandexAndRawSearch(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := ioReadAll(r)
		form, _ := url.ParseQuery(string(body))
		if form.Get("engine") != "yandex" || form.Get("text") != "hello" || form.Get("q") != "" {
			t.Fatalf("form = %v", form)
		}
		return response(http.StatusOK, `raw-body`, r), nil
	}))
	got, err := client.RawSearch(context.Background(), SearchParams{"engine": "yandex", "q": "hello"})
	if err != nil {
		t.Fatalf("RawSearch() error = %v", err)
	}
	if got != "raw-body" {
		t.Fatalf("RawSearch() = %q", got)
	}
}

func TestRawSearchPreservesBusinessFailureWhileSearchClassifiesIt(t *testing.T) {
	cases := []struct {
		name string
		body string
		code int
	}{
		{"collection_failed", " {\"code\":0,\"data\":\"error, Collection failed\"}\n", 0},
		{"not_collected", `{"code":300,"data":"error, not search"}`, 300},
		{"authentication", `{"code":401,"data":"invalid key"}`, 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return response(http.StatusOK, tc.body, r), nil
			})
			client := testClient(t, transport)
			opts := ClientOptions{APIKey: "secret-token", HTTPClient: &http.Client{Transport: transport}}
			for _, call := range []func() (string, error){
				func() (string, error) {
					return client.RawSearch(context.Background(), SearchParams{"engine": "yandex", "q": "x"})
				},
				func() (string, error) {
					return RawSearch(context.Background(), SearchParams{"engine": "yandex", "q": "x"}, opts)
				},
			} {
				body, err := call()
				if err != nil || body != tc.body {
					t.Fatalf("RawSearch() = %q, %v; want unchanged body and nil error", body, err)
				}
			}
			_, err := client.Search(context.Background(), SearchParams{"engine": "yandex", "q": "x"})
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code == nil || *apiErr.Code != tc.code || apiErr.HTTPStatus != http.StatusOK {
				t.Fatalf("Search() did not retain business failure: %T %v", err, err)
			}
			if tc.code == 401 {
				if !errors.As(err, new(*AuthenticationError)) {
					t.Fatalf("expected AuthenticationError, got %T", err)
				}
			} else if !errors.As(err, new(*NotCollectedError)) {
				t.Fatalf("expected NotCollectedError, got %T", err)
			}
		})
	}
}

func TestRawSearchStillRejectsHTTPFailure(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusServiceUnavailable, `{"code":200,"data":{}}`, r), nil
	}))
	body, err := client.RawSearch(context.Background(), nil)
	var apiErr *APIError
	if body != "" || !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("RawSearch() = %q, %v; expected HTTP error", body, err)
	}
}

func TestSearchAcceptsMetadataOnlyFinanceResult(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"code":200,"data":{"result":{"search_metadata":{"id":"finance-only"},"spider_parameter":{"q":"AAPL"}},"task_id":"finance-only"}}`, r), nil
	}))
	got, err := client.SearchResponse(context.Background(), SearchParams{"engine": "google_finance", "q": "AAPL"})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := got.Result.(map[string]any)
	if !ok || len(result) != 2 || result["search_metadata"] == nil || result["spider_parameter"] == nil {
		t.Fatalf("SDK changed the metadata-only result: %#v", got.Result)
	}
}

func TestSearchMapsBusinessErrorsAndTaskIDFallback(t *testing.T) {
	cases := []struct {
		body string
		want any
	}{
		{`{"code":300,"data":"not collected"}`, new(*NotCollectedError)},
		{`{"code":401,"data":"bad key"}`, new(*AuthenticationError)},
		{`{"code":0,"data":"error, Collection failed"}`, new(*NotCollectedError)},
	}
	for _, tc := range cases {
		client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { return response(http.StatusOK, tc.body, r), nil }))
		_, err := client.Search(context.Background(), SearchParams{"q": "x"})
		if !errors.As(err, tc.want) {
			t.Fatalf("body=%s error=%T %v", tc.body, err, err)
		}
	}
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"search_metadata":{"id":"metadata-id"},"organic":[{"title":"x"}]}`, r), nil
	}))
	got, err := client.SearchResponse(context.Background(), SearchParams{"q": "x"})
	if err != nil || got.TaskID != "metadata-id" {
		t.Fatalf("fallback response = %#v, error = %v", got, err)
	}
}

func TestSearchRejectsUnsupportedEngineAndUnserializableValue(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { return response(http.StatusOK, `{}`, r), nil }))
	_, err := client.Search(context.Background(), SearchParams{"engine": "webpage"})
	var invalid *InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("engine error = %T %v", err, err)
	}
	_, err = client.Search(context.Background(), SearchParams{"q": func() {}})
	if !errors.As(err, &invalid) {
		t.Fatalf("value error = %T %v", err, err)
	}
}

func ioReadAll(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	return io.ReadAll(r.Body)
}
