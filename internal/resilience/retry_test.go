package resilience

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRetryTransportDoesNotCallMissingGetBody(t *testing.T) {
	var calls atomic.Int32
	transport := RetryTransport{
		Attempts: 3,
		Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			if string(body) != "request-body" {
				t.Fatalf("request body = %q", body)
			}
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header), Request: r}, nil
		}),
	}
	request := httptest.NewRequest(http.MethodPost, "http://upstream.invalid", bytes.NewBufferString("request-body"))
	request.ContentLength = int64(len("request-body"))
	request.GetBody = nil

	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if calls.Load() != 1 {
		t.Fatalf("round trips = %d, want 1", calls.Load())
	}
}

func TestRetryTransportReplaysSafeRequest(t *testing.T) {
	var calls atomic.Int32
	transport := RetryTransport{
		Attempts:   1,
		RetryDelay: 1,
		Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			if string(body) != "replayable" {
				t.Fatalf("replayed body=%q", body)
			}
			_ = r.Body.Close()
			status := http.StatusServiceUnavailable
			if calls.Load() == 2 {
				status = http.StatusOK
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header), Request: r}, nil
		}),
	}
	request, err := http.NewRequest(http.MethodPut, "http://upstream.invalid", bytes.NewBufferString("replayable"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("status=%d calls=%d", response.StatusCode, calls.Load())
	}
}

func TestRetryTransportNeverReplaysIncomingStream(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodPut} {
		for _, length := range []int64{-1, 0, 12} {
			t.Run(fmt.Sprintf("%s/length=%d", method, length), func(t *testing.T) {
				calls := 0
				transport := RetryTransport{Attempts: 3, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					body, err := io.ReadAll(r.Body)
					if err != nil || string(body) != "request-body" {
						t.Fatalf("body=%q err=%v", body, err)
					}
					_ = r.Body.Close()
					return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody}, nil
				})}
				request := httptest.NewRequest(method, "http://upstream.invalid", bytes.NewBufferString("request-body"))
				request.ContentLength = length
				request.GetBody = nil
				response, err := transport.RoundTrip(request)
				if err != nil {
					t.Fatal(err)
				}
				_ = response.Body.Close()
				if calls != 1 {
					t.Fatalf("round trips = %d, want 1", calls)
				}
			})
		}
	}
}

type unreadableResponseBody struct{ closed bool }

func (b *unreadableResponseBody) Read([]byte) (int, error) {
	panic("retry must not drain an untrusted error body")
}
func (b *unreadableResponseBody) Close() error { b.closed = true; return nil }

func TestRetryTransportClosesErrorResponseWithoutDraining(t *testing.T) {
	body := &unreadableResponseBody{}
	calls := 0
	transport := RetryTransport{Attempts: 1, RetryDelay: time.Nanosecond, Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: body}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})}
	request := httptest.NewRequest(http.MethodGet, "http://upstream.invalid", nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if !body.closed || calls != 2 {
		t.Fatalf("closed=%v calls=%d", body.closed, calls)
	}
}
