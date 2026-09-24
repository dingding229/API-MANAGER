package httpx

import (
	"api-manager/internal/ratelimit"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestThrottleAdminCannotBypassWithForwardedIP(t *testing.T) {
	calls := 0
	handler := ThrottleAdmin(ratelimit.NewMemory(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(http.StatusOK) }))
	for i := 0; i < 11; i++ {
		r := httptest.NewRequest("POST", "/auth/v1/login", nil)
		r.RemoteAddr = "192.0.2.10:1234"
		r.Header.Set("X-Forwarded-For", "attacker-"+string(rune('a'+i)))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if i == 10 && w.Code != http.StatusTooManyRequests {
			t.Fatalf("login brute force not limited: %d", w.Code)
		}
	}
	if calls != 10 {
		t.Fatalf("expected 10 attempts; got %d", calls)
	}
}
