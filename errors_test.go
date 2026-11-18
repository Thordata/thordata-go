package thordata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestTypedAPIErrorsExposeCodeAndPreserveErrorChain(t *testing.T) {
	code := 401
	base := &APIError{Message: "unauthorized", Code: &code, HTTPStatus: 200}
	err := &AuthenticationError{APIError: base}
	var typed *AuthenticationError
	var api *APIError
	if !errors.As(err, &typed) || !errors.As(err, &api) {
		t.Fatalf("error chain does not expose typed and base errors: %T", err)
	}
	if api.Code == nil || *api.Code != 401 || api.HTTPStatus != 200 {
		t.Fatalf("api error = %#v", api)
	}
}

func TestTimeoutErrorUnwrapsContext(t *testing.T) {
	err := &TimeoutError{Message: "request timed out", Cause: context.DeadlineExceeded}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("TimeoutError does not unwrap deadline: %v", err)
	}
}

func TestWebScraperErrorKeepsStageButRedactsSecret(t *testing.T) {
	err := &WebScraperError{
		Code:     ErrorDownloadURLInvalid,
		Stage:    "download_url",
		TaskID:   "TASK-1",
		SpiderID: "linkedin_company_information_by-url",
		Reason:   "bad https://scraperapi.thordata.com/download?api_key=secret-token",
		secrets:  []string{"secret-token"},
	}
	if err.Stage != "download_url" || err.TaskID != "TASK-1" {
		t.Fatalf("error metadata = %#v", err)
	}
	if containsSecret(err.Error(), "secret-token") || containsSecret(err.Error(), "api_key=") {
		t.Fatalf("error leaked signed URL: %s", err)
	}
}

func TestRedactValueHandlesNestedAndEncodedCredentials(t *testing.T) {
	input := map[string]any{
		"message": "token secret-token",
		"nested":  []any{"https://host/path?api_key=server%2Dkey", map[string]any{"secret-token": "secret-token"}},
	}
	got := redactValue(input, []string{"secret-token", "server-key"})
	text := stringifyForTest(got)
	if containsSecret(text, "secret-token") || containsSecret(text, "server%2Dkey") || containsSecret(text, "api_key=") {
		t.Fatalf("redacted value leaked credentials: %s", text)
	}
	if !containsSecret(text, "[redacted URL]") || !containsSecret(text, "message") {
		t.Fatalf("redaction removed useful context: %s", text)
	}
}

func containsSecret(text, secret string) bool { return strings.Contains(text, secret) }

func stringifyForTest(value any) string { return fmt.Sprintf("%#v", value) }
