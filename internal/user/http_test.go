package user

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleRequiresAdminKeyAndHasNoAccountAuth(t *testing.T) {
	key := strings.Repeat("a", 64)
	h := NewHTTP(key)
	for _, c := range []struct {
		method, path, key string
		status            int
	}{
		{"GET", "/auth/v1/me", key, 200}, {"GET", "/auth/v1/me", "", 401}, {"GET", "/auth/v1/me", "bad", 401},
		{"POST", "/auth/v1/login", key, 404}, {"POST", "/auth/v1/bootstrap", key, 404}, {"GET", "/auth/v1/bootstrap/status", "", 404},
	} {
		r := httptest.NewRequest(c.method, "http://example.com"+c.path, nil)
		if c.key != "" {
			r.Header.Set("X-API-Key", c.key)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.status {
			t.Fatalf("%s: status=%d want=%d", c.path, w.Code, c.status)
		}
	}
}
