package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSDoesNotSwallowBusinessOptions(t *testing.T) {
	calls := 0
	handler := CORS("https://example.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(200) }))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("OPTIONS", "/api/status", nil))
	if calls != 1 || w.Code != 200 {
		t.Fatal("business OPTIONS intercepted")
	}
	r := httptest.NewRequest("OPTIONS", "/api/status", nil)
	r.Header.Set("Origin", "https://example.test")
	r.Header.Set("Access-Control-Request-Method", "POST")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if calls != 1 || w.Code != 204 {
		t.Fatal("preflight not handled")
	}
}
