package thordata

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, body string, request *http.Request) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func testClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	client, err := NewClient(ClientOptions{
		APIKey:     "secret-token",
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func TestNewClientConfigurationAndErrorChain(t *testing.T) {
	if _, err := NewClient(ClientOptions{}); !errors.As(err, new(*ConfigurationError)) {
		t.Fatalf("missing key error = %T %v", err, err)
	}
	if _, err := NewClient(ClientOptions{APIKey: "密钥"}); !errors.As(err, new(*ConfigurationError)) {
		t.Fatalf("non-ASCII key error = %T %v", err, err)
	}
	if got := *Bool(false); got {
		t.Fatal("Bool(false) = true")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, context.Canceled
	}))
	_, err := client.Search(ctx, SearchParams{"q": "x"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestClientStringDoesNotExposeKey(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"code":200,"data":{"result":{}}}`, r), nil
	}))
	if got := client.String(); strings.Contains(got, "secret-token") {
		t.Fatalf("String() leaked key: %s", got)
	}
}

func TestRequestHonorsContextAndReturnsRawResponse(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Fatalf("request = %s auth=%q", r.Method, r.Header.Get("Authorization"))
		}
		return response(http.StatusOK, `{"ok":true}`, r), nil
	}))
	resp, err := client.Request(context.Background(), http.MethodPost, "/request", map[string]any{"safe": true}, RequestOptions{})
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %s", body)
	}
}

func TestRequestDoesNotSendClientKeyToAnotherOrigin(t *testing.T) {
	client := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" {
			t.Fatalf("external Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("api-source") != "" || r.Header.Get("Origin") != "" {
			t.Fatal("SDK source headers leaked to another origin")
		}
		return response(http.StatusOK, "ok", r), nil
	}))
	resp, err := client.Request(context.Background(), http.MethodGet, "https://outside.example/collect", nil, RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func TestClientTimeoutMustBePositive(t *testing.T) {
	_, err := NewClient(ClientOptions{APIKey: "key", Timeout: -time.Second})
	var target *ConfigurationError
	if !errors.As(err, &target) {
		t.Fatalf("error = %T %v", err, err)
	}
}
