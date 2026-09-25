package gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"api-manager/internal/model"
)

func TestStripGatewayCredentials(t *testing.T) {
	header := http.Header{
		"Authorization": {"Bearer secret"}, "Cookie": {"session=secret"}, "X-Api-Key": {"key"},
		"X-Admin-Token": {"admin"}, "X-Timestamp": {"1"}, "X-Nonce": {"nonce"}, "X-Signature": {"sig"},
		"X-Custom": {"kept"}, "X-Custom-Timestamp": {"timestamp"}, "X-Custom-Signature": {"signature"},
	}
	stripGatewayCredentials(header, model.API{AuthMode: "hmac", AuthConfig: map[string]string{"timestamp_header": "X-Custom-Timestamp", "signature_header": "X-Custom-Signature"}})
	for _, name := range []string{"Authorization", "Cookie", "X-API-Key", "X-Admin-Token", "X-Timestamp", "X-Nonce", "X-Signature", "X-Custom-Timestamp", "X-Custom-Signature"} {
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
