package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestIDRejectsInjection(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(RequestIDHeader) != RequestIDFromContext(r.Context()) {
			t.Fatal("request ID not propagated")
		}
	}))
	for _, bad := range []string{"injected\nline", strings.Repeat("a", 200), "bad value"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set(RequestIDHeader, bad)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Header().Get(RequestIDHeader) == bad {
			t.Fatalf("accepted unsafe request ID: %q", bad)
		}
	}
}
