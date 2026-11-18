package thordata

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
	ctx    context.Context
}

func (b *cancelReadCloser) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

func (b *cancelReadCloser) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.cancel()
	} else if err != nil {
		if b.ctx != nil && errors.Is(b.ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return n, &TimeoutError{Message: "ThorData response body timed out", Cause: context.DeadlineExceeded}
		}
		if b.ctx != nil && errors.Is(b.ctx.Err(), context.Canceled) {
			return n, &ConnectionError{Message: "ThorData response body was cancelled", Cause: context.Canceled}
		}
		return n, &ConnectionError{Message: "Unable to read the ThorData response body", Cause: safeError(err)}
	}
	return n, err
}

func ioReadAllResponse(resp *http.Response) ([]byte, error) { return io.ReadAll(resp.Body) }

func normalizeGenericParams(params map[string]any, filter bool) (map[string]string, error) {
	result := make(map[string]string, len(params))
	for key, value := range params {
		if filter && !requestParameterSet[key] {
			continue
		}
		if value == nil {
			continue
		}
		encoded, keep, err := normalizeValue(value)
		if err != nil {
			return nil, err
		}
		if keep {
			result[key] = encoded
		}
	}
	return result, nil
}

func normalizeValue(value any) (string, bool, error) {
	switch typed := value.(type) {
	case bool:
		if typed {
			return "1", true, nil
		}
		return "0", true, nil
	case string:
		text := strings.TrimSpace(typed)
		return text, text != "", nil
	case json.Number:
		if _, err := json.Marshal(typed); err != nil {
			return "", false, newInvalidRequest("invalid JSON number")
		}
		return string(typed), true, nil
	}
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return "", false, nil
	}
	if rv.Kind() == reflect.Map || rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array || rv.Kind() == reflect.Struct {
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", false, &InvalidRequestError{APIError: &APIError{Message: "SERP parameters must contain JSON-serializable values", Code: intPtr(400)}}
		}
		return string(encoded), true, nil
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), true, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(rv.Uint(), 10), true, nil
	case reflect.Float32, reflect.Float64:
		if math.IsNaN(rv.Float()) || math.IsInf(rv.Float(), 0) {
			return "", false, &InvalidRequestError{APIError: &APIError{Message: "parameter value must be finite", Code: intPtr(400)}}
		}
		return strconv.FormatFloat(rv.Float(), 'g', -1, rv.Type().Bits()), true, nil
	case reflect.Invalid:
		return "", false, nil
	default:
		return "", false, &InvalidRequestError{APIError: &APIError{Message: "parameter value is not supported", Code: intPtr(400)}}
	}
}

func intPtr(value int) *int { return &value }

func encodeForm(params map[string]string) url.Values {
	form := make(url.Values, len(params))
	for key, value := range params {
		form.Set(key, value)
	}
	return form
}
