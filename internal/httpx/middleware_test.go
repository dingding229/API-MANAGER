package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLimitRequestBodyUsesSeparatePluginLimit(t *testing.T) {
	handler := LimitRequestBody(4, 16, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	normal := httptest.NewRecorder()
	handler.ServeHTTP(normal, httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader("12345")))
	if normal.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("normal request status = %d", normal.Code)
	}

	plugin := httptest.NewRecorder()
	handler.ServeHTTP(plugin, httptest.NewRequest(http.MethodPost, "/admin/v1/plugins", strings.NewReader("12345")))
	if plugin.Code != http.StatusNoContent {
		t.Fatalf("plugin request status = %d", plugin.Code)
	}
}
