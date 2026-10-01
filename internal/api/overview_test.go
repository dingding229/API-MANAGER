package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"api-manager/internal/observability"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
)

func TestOverviewCountsBusinessRequestsAndHonorsPermissions(t *testing.T) {
	m := store.NewMemory()
	s := user.NewService(m)
	if err := s.EnsureInitialAdmin("admin", "Admin888"); err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Authenticate("admin", "Admin888")
	if err != nil {
		t.Fatal(err)
	}
	metrics := observability.NewMetrics()
	metrics.ObserveGateway("GET", 200, time.Millisecond)
	metrics.ObserveGateway("POST", 500, 2*time.Millisecond)
	metrics.ObserveHTTP("health", "GET", 200, time.Millisecond)
	a := NewAdminWithUserManagement(m, plugin.NewRegistry(), s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.SetObservability(nil, metrics)
	r := httptest.NewRequest("GET", "/admin/v1/overview", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var body struct {
		Metrics   observability.MetricsSnapshot
		Resources map[string]int
		Scope     string `json:"counter_scope"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Metrics.GatewayRequests != 2 || body.Metrics.GatewayErrors != 1 || body.Metrics.HTTPRequestsTotal != 1 || body.Resources["users"] != 1 || body.Scope != "process_lifetime" {
		t.Fatal("overview counts incorrect")
	}
	if _, err = s.CreateRole("minimal", "", []string{"api.read"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create("minimal-user", "ReadMe88", "minimal"); err != nil {
		t.Fatal(err)
	}
	_, token, err = s.Authenticate("minimal-user", "ReadMe88")
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	var raw map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	if _, ok := raw["metrics"]; ok {
		t.Fatal("stats exposed without observability permission")
	}
}
