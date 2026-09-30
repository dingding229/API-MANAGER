package gateway

import (
	"api-manager/internal/upstream"
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"api-manager/internal/model"
)

func TestStripGatewayCredentials(t *testing.T) {
	header := http.Header{
		"Authorization": {"Bearer secret"}, "Cookie": {"session=secret"}, "X-Api-Key": {"key"},
		"X-Admin-Token": {"admin"}, "X-Timestamp": {"1"}, "X-Nonce": {"nonce"}, "X-Signature": {"sig"},
		"X-Custom": {"kept"},
	}
	stripGatewayCredentials(header, model.API{AuthMode: "api_key"})
	for _, name := range []string{"Authorization", "Cookie", "X-API-Key", "X-Admin-Token", "X-Timestamp", "X-Nonce", "X-Signature"} {
		if header.Get(name) != "" {
			t.Fatalf("sensitive header %s was retained", name)
		}
	}
	if header.Get("X-Custom") != "kept" {
		t.Fatal("non-sensitive header was removed")
	}
}

func TestResponseValidatorBoundsBufferedBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	capture := newCaptureWriter(recorder)
	validator := newResponseValidator(capture, []byte(`{"type":"string"}`))
	payload := bytes.Repeat([]byte("x"), maxSchemaBodyBytes+1024)
	written, err := validator.Write(payload)
	if err != nil || written != len(payload) {
		t.Fatalf("Write = %d, %v", written, err)
	}
	if !validator.overflow || validator.body.Len() != maxSchemaBodyBytes {
		t.Fatalf("buffer was not bounded: overflow=%v bytes=%d", validator.overflow, validator.body.Len())
	}
}

func TestResponseValidatorStreamsNonSuccessResponses(t *testing.T) {
	recorder := httptest.NewRecorder()
	capture := newCaptureWriter(recorder)
	validator := newResponseValidator(capture, []byte(`{"type":"object"}`))
	validator.WriteHeader(http.StatusBadGateway)
	payload := bytes.Repeat([]byte("x"), maxSchemaBodyBytes+1024)
	written, err := validator.Write(payload)
	if err != nil || written != len(payload) {
		t.Fatalf("Write = %d, %v", written, err)
	}
	if validator.body.Len() != 0 || validator.overflow {
		t.Fatalf("non-success response was buffered: overflow=%v bytes=%d", validator.overflow, validator.body.Len())
	}
	if err := validator.Commit(); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusBadGateway || recorder.Body.Len() != len(payload) {
		t.Fatalf("streamed response = status %d, bytes %d", recorder.Code, recorder.Body.Len())
	}
}

func TestProxyForwardsIncomingPostBodyOnce(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "payload" {
			t.Errorf("body=%q error=%v", body, err)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-API-Key") != "upstream-only-key" {
			t.Error("gateway credentials leaked or upstream credential not injected")
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("upstream-response"))
	}))
	defer up.Close()
	g := New(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	g.SetUpstreamCredentials(upstream.Credentials{"test": {Origin: up.URL, APIKey: "upstream-only-key"}})
	r := httptest.NewRequest(http.MethodPost, "http://gateway.invalid/api/test", strings.NewReader("payload"))
	r.Header.Set("Authorization", "Bearer client-secret")
	r.Header.Set("Cookie", "session=client-secret")
	r.Header.Set("X-API-Key", "client-secret")
	w := httptest.NewRecorder()
	g.proxy(newCaptureWriter(w), r, model.API{ID: "body-test", UpstreamURL: up.URL, UpstreamAuthRef: "test", UpstreamRetries: 3})
	if calls != 1 || w.Code != http.StatusServiceUnavailable || w.Body.String() != "upstream-response" {
		t.Fatalf("calls=%d code=%d response=%q", calls, w.Code, w.Body.String())
	}
}
