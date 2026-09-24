package resilience

import (
	"io"
	"net/http"
	"time"
)

// RetryTransport retries only requests that can be replayed safely. It never
// retries a non-idempotent request body unless the request provides GetBody.
type RetryTransport struct {
	Base       http.RoundTripper
	Attempts   int
	OnRetry    func()
	RetryDelay time.Duration
}

func (t RetryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	attempts := t.Attempts
	if attempts < 0 {
		attempts = 0
	}
	if !replayable(request) {
		attempts = 0
	}
	var lastErr error
	for attempt := 0; attempt <= attempts; attempt++ {
		current, err := cloneRequest(request)
		if err != nil {
			return nil, err
		}
		response, err := base.RoundTrip(current)
		if err == nil && !retryableStatus(response.StatusCode) {
			return response, nil
		}
		if err != nil {
			lastErr = err
		}
		if attempt == attempts {
			return response, err
		}
		if response != nil && response.Body != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		if t.OnRetry != nil {
			t.OnRetry()
		}
		wait := t.RetryDelay
		if wait <= 0 {
			wait = 50 * time.Millisecond
		}
		wait *= time.Duration(1 << min(attempt, 4))
		select {
		case <-request.Context().Done():
			return nil, request.Context().Err()
		case <-time.After(wait):
		}
	}
	return nil, lastErr
}

func replayable(request *http.Request) bool {
	switch request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
	default:
		return false
	}
	return request.Body == nil || request.Body == http.NoBody || request.ContentLength == 0 || request.GetBody != nil
}

func cloneRequest(request *http.Request) (*http.Request, error) {
	clone := request.Clone(request.Context())
	if request.Body != nil && request.Body != http.NoBody && request.ContentLength != 0 {
		body, err := request.GetBody()
		if err != nil {
			return nil, err
		}
		clone.Body = body
	}
	return clone, nil
}

func retryableStatus(status int) bool {
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}
