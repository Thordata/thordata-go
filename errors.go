package thordata

import (
	"context"
	"errors"
	"fmt"
	"net"
)

type ErrorCode string

const (
	ErrorSchemaInvalid       ErrorCode = "schema_invalid"
	ErrorProductUnavailable  ErrorCode = "product_unavailable"
	ErrorInvalidParameters   ErrorCode = "invalid_parameters"
	ErrorSubmissionUnknown   ErrorCode = "submission_unknown"
	ErrorSubmissionFailed    ErrorCode = "submission_failed"
	ErrorPollFailed          ErrorCode = "poll_failed"
	ErrorScrapeFailed        ErrorCode = "scrape_failed"
	ErrorDownloadURLInvalid  ErrorCode = "download_url_invalid"
	ErrorDownloadFailed      ErrorCode = "download_failed"
	ErrorInvalidResult       ErrorCode = "invalid_result"
	ErrorWaitTimeout         ErrorCode = "wait_timeout"
	ErrorProtocol            ErrorCode = "protocol_error"
	ErrorAuthentication      ErrorCode = "authentication_failed"
	ErrorPermission          ErrorCode = "permission_denied"
	ErrorInsufficientBalance ErrorCode = "insufficient_balance"
	ErrorRequestFailed       ErrorCode = "request_failed"
)

type ConfigurationError struct {
	Message string
	Cause   error
}

func (e *ConfigurationError) Error() string { return e.Message }
func (e *ConfigurationError) Unwrap() error { return e.Cause }

type ConnectionError struct {
	Message string
	Cause   error
}

func (e *ConnectionError) Error() string { return e.Message }
func (e *ConnectionError) Unwrap() error { return e.Cause }

type TimeoutError struct {
	Message string
	Cause   error
}

func (e *TimeoutError) Error() string { return e.Message }
func (e *TimeoutError) Unwrap() error {
	return &ConnectionError{Message: e.Message, Cause: e.Cause}
}

func (e *TimeoutError) Timeout() bool { return true }

func safeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return context.DeadlineExceeded
	}
	return errors.New("transport or response processing failed")
}

func connectionError(err error) error {
	cause := safeError(err)
	if errors.Is(cause, context.DeadlineExceeded) {
		return &TimeoutError{Message: "ThorData request timed out", Cause: cause}
	}
	if errors.Is(cause, context.Canceled) {
		return &ConnectionError{Message: "ThorData request was cancelled", Cause: cause}
	}
	return &ConnectionError{Message: "Unable to communicate with the ThorData API", Cause: cause}
}

type APIError struct {
	Message      string
	Code         *int
	HTTPStatus   int
	ResponseData any
	Cause        error
}

func (e *APIError) Error() string { return e.Message }
func (e *APIError) Unwrap() error { return e.Cause }

type AuthenticationError struct{ *APIError }
type PermissionError struct{ *APIError }
type RateLimitError struct{ *APIError }
type InvalidRequestError struct{ *APIError }
type NotCollectedError struct{ *APIError }

func (e *AuthenticationError) Unwrap() error { return e.APIError }
func (e *PermissionError) Unwrap() error     { return e.APIError }
func (e *RateLimitError) Unwrap() error      { return e.APIError }
func (e *InvalidRequestError) Unwrap() error { return e.APIError }
func (e *NotCollectedError) Unwrap() error   { return e.APIError }

type WebScraperError struct {
	Code        ErrorCode
	Stage       string
	TaskID      string
	SpiderID    string
	Reason      string
	ServiceCode any
	HTTPStatus  int
	Cause       error
	secrets     []string
}

func (e *WebScraperError) Error() string {
	message := scraperErrorMessage(e.Code)
	if e.Reason != "" {
		message += " " + redactText(e.Reason, e.secrets)
	}
	if e.TaskID != "" {
		message += " Task ID: " + e.TaskID + "."
	}
	return message
}

func (e *WebScraperError) Unwrap() error { return e.Cause }

func newInvalidRequest(message string) error {
	code := 400
	return &InvalidRequestError{APIError: &APIError{Message: message, Code: &code, HTTPStatus: 0}}
}

func newAPIError(message string, code *int, status int, data any, cause error) *APIError {
	return &APIError{Message: message, Code: code, HTTPStatus: status, ResponseData: redactValue(data, nil), Cause: cause}
}

func scraperErrorMessage(code ErrorCode) string {
	switch code {
	case ErrorSchemaInvalid:
		return "Web Scraper schema is incompatible."
	case ErrorProductUnavailable:
		return "This Web Scraper product is unavailable."
	case ErrorInvalidParameters:
		return "Web Scraper parameters are invalid."
	case ErrorSubmissionUnknown:
		return "Web Scraper submission outcome is unknown."
	case ErrorSubmissionFailed:
		return "Web Scraper submission was rejected."
	case ErrorPollFailed:
		return "Web Scraper task status could not be verified."
	case ErrorScrapeFailed:
		return "Web Scraper returned only failed records or the task failed."
	case ErrorDownloadURLInvalid:
		return "Web Scraper returned an invalid download address."
	case ErrorDownloadFailed:
		return "Web Scraper results could not be downloaded."
	case ErrorInvalidResult:
		return "Web Scraper results are not valid JSON records."
	case ErrorWaitTimeout:
		return "Web Scraper exceeded the maximum wait time."
	case ErrorProtocol:
		return "Web Scraper returned an incompatible response."
	case ErrorAuthentication:
		return "Web Scraper authentication failed."
	case ErrorPermission:
		return "Web Scraper permission was denied."
	case ErrorInsufficientBalance:
		return "Web Scraper account balance is insufficient."
	default:
		return "Web Scraper request failed."
	}
}

func (e *WebScraperError) Format(f fmt.State, verb rune) {
	f.Write([]byte(e.Error()))
}
