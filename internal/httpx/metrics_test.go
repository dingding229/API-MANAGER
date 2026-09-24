package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProtectMetrics(t *testing.T) {
	calls := 0
	handler := ProtectMetrics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusOK) }), "a-distinct-long-metrics-secret-1234567890")
	for _, tc := range []struct {
		authorization string
		status        int
	}{
		{"", http.StatusUnauthorized},
		{"Bearer wrong", http.StatusUnauthorized},
		{"a-distinct-long-metrics-secret-1234567890", http.StatusUnauthorized},
		{"Basic a-distinct-long-metrics-secret-1234567890", http.StatusUnauthorized},
		{"Bearer a-distinct-long-metrics-secret-1234567890", http.StatusOK},
	} {
		r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		r.Header.Set("Authorization", tc.authorization)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status %d for %q, expected %d", w.Code, tc.authorization, tc.status)
		}
	}
	if calls != 1 {
		t.Fatalf("handler called %d times", calls)
	}
}
