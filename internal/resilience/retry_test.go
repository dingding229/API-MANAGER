package resilience

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestRetryTransportRetriesRecoverableResponse(t *testing.T) {
	var calls atomic.Int32
	transport := RetryTransport{Attempts: 2, RetryDelay: time.Millisecond, Base: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: http.NoBody, Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody, Request: r}, nil
	})}
	request := httptest.NewRequest(http.MethodGet, "http://example.test", nil)
	response, err := transport.RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("response=%v err=%v calls=%d", response, err, calls.Load())
	}
}

func TestRetryTransportDoesNotRetryPost(t *testing.T) {
	var calls atomic.Int32
	transport := RetryTransport{Attempts: 2, RetryDelay: time.Millisecond, Base: roundTripperFunc(func(*http.Request) (*http.Response, error) { calls.Add(1); return nil, errors.New("unavailable") })}
	_, _ = transport.RoundTrip(httptest.NewRequest(http.MethodPost, "http://example.test", nil))
	if calls.Load() != 1 {
		t.Fatalf("POST was retried %d times", calls.Load())
	}
}
