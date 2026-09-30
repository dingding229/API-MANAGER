package resilience

import (
	"errors"
	"net/http"
	"time"
)

// RetryTransport retries only requests that can be replayed safely. It never
// retries a request body unless the request provides GetBody. Incoming server
// requests normally do not provide GetBody, so their body is sent once.
type RetryTransport struct {
	Base       http.RoundTripper
	Attempts   int
	OnRetry    func()
	RetryDelay time.Duration
}

func (t RetryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, errors.New("nil HTTP request")
	}
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
		current := request.Clone(request.Context())
		// The base transport owns and closes the original stream on the first
		// attempt. Only subsequent attempts may obtain a fresh body.
		if attempt > 0 && request.Body != nil && request.Body != http.NoBody {
			body, err := request.GetBody()
			if err != nil {
				return nil, err
			}
			current.Body = body
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
			// Do not drain an untrusted, potentially infinite error response.
			// Closing it promptly also cancels its network read.
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
	return request.Body == nil || request.Body == http.NoBody || request.GetBody != nil
}

func retryableStatus(status int) bool {
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}
