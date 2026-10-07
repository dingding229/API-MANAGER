package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api-manager/internal/model"
	"api-manager/internal/observability"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
)

func TestLogCleanupRequiresDedicatedPermissionAndConfirmation(t *testing.T) {
	memory := store.NewMemory()
	users := user.NewService(memory)
	sessions := map[string]string{}
	for _, role := range []string{"super_admin", "api_developer", "member"} {
		if _, err := users.Create(role, "Password88", role); err != nil {
			t.Fatal(err)
		}
		_, token, err := users.Authenticate(role, "Password88")
		if err != nil {
			t.Fatal(err)
		}
		sessions[role] = token
	}
	hub, err := observability.NewHub(observability.HubOptions{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	_, _ = hub.Write([]byte("{\"level\":\"INFO\",\"msg\":\"one\"}\n{\"level\":\"INFO\",\"msg\":\"two\"}\n"))
	hub.RecordTrace(observability.TraceEntry{TraceID: "keep-trace", Name: "request", Attributes: map[string]string{"url.path": "/api/example"}})
	metrics := observability.NewMetrics()
	metrics.ObserveGateway("GET", 200, time.Millisecond)
	admin := NewAdminWithUserManagement(memory, plugin.NewRegistry(), users, slog.New(slog.NewTextHandler(io.Discard, nil)))
	admin.SetObservability(hub, metrics)
	call := func(token, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("DELETE", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		token, body string
		status      int
	}{
		{"", `{"confirm":true}`, 401}, {"ak_not_a_management_session", `{"confirm":true}`, 401},
		{sessions["api_developer"], `{"confirm":true}`, 403}, {sessions["member"], `{"confirm":true}`, 403},
		{sessions["super_admin"], `{}`, 400}, {sessions["super_admin"], `{"confirm":false}`, 400},
		{sessions["super_admin"], `{"confirm":true,"path":"/root"}`, 400},
		{sessions["super_admin"], `{"confirm":true} {}`, 400}, {sessions["super_admin"], `not json`, 400},
	} {
		w := call(tc.token, "/admin/v1/observability/logs", tc.body)
		if w.Code != tc.status {
			t.Fatalf("cleanup status=%d want %d", w.Code, tc.status)
		}
		if hub.QueryLogs(observability.LogQuery{}).Total != 2 {
			t.Fatal("unconfirmed/unauthorized request cleared logs")
		}
	}
	if w := call(sessions["super_admin"], "/admin/v1/observability/logs?level=ERROR", `{"confirm":true}`); w.Code != 400 {
		t.Fatal("filtered deletion silently cleared all logs")
	}
	if err := memory.CreateAuditLog(model.AuditLog{Action: "retain.original.audit", Details: map[string]any{"keep": true}}); err != nil {
		t.Fatal(err)
	}
	w := call(sessions["super_admin"], "/admin/v1/observability/logs", `{"confirm":true}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var result struct {
		Cleared        bool
		IndexedEntries int `json:"indexed_entries"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Cleared || result.IndexedEntries != 2 || hub.QueryLogs(observability.LogQuery{}).Total != 0 {
		t.Fatal("cleanup response/index invalid")
	}
	if hub.QueryTraces(observability.TraceQuery{}).Total != 1 || metrics.Snapshot().GatewayRequests != 1 {
		t.Fatal("cleanup reset traces or metrics")
	}
	audit, err := memory.ListAuditLogs(model.AuditLogQuery{Action: "observability.logs.clear"})
	if err != nil || audit.Total != 1 || audit.Items[0].StatusCode != 200 {
		t.Fatal("cleanup was not audited")
	}
	kept, err := memory.ListAuditLogs(model.AuditLogQuery{Action: "retain.original.audit"})
	if err != nil || kept.Total != 1 {
		t.Fatal("original audit history was removed")
	}
	// An explicit grant enables cleanup; existing operator permissions do not.
	if err = users.UpdateRolePermissions("api_developer", []string{"observability.read", "observability.logs.clear"}); err == nil {
		t.Fatal("developer acquired administrative log deletion permission")
	}
	_, _ = hub.Write([]byte("{\"msg\":\"new log\"}\n"))
	if w = call(sessions["api_developer"], "/admin/v1/observability/logs", `{"confirm":true}`); w.Code != 403 {
		t.Fatal("developer bypassed log deletion restriction")
	}
}

func TestLogCleanupUnavailableAndClosedStorageFailSafely(t *testing.T) {
	memory := store.NewMemory()
	users := user.NewService(memory)
	if _, err := users.Create("owner", "Password88", "super_admin"); err != nil {
		t.Fatal(err)
	}
	_, token, err := users.Authenticate("owner", "Password88")
	if err != nil {
		t.Fatal(err)
	}
	admin := NewAdminWithUserManagement(memory, plugin.NewRegistry(), users, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("DELETE", "/admin/v1/observability/logs", strings.NewReader(`{"confirm":true}`))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, r)
		return w
	}
	if w := request(); w.Code != 503 {
		t.Fatal("unavailable log backend did not fail safely")
	}
	directory := t.TempDir()
	hub, err := observability.NewHub(observability.HubOptions{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = hub.Write([]byte("{\"msg\":\"retain on error\"}\n"))
	if err = hub.Close(); err != nil {
		t.Fatal(err)
	}
	admin.SetObservability(hub, observability.NewMetrics())
	w := request()
	if w.Code != 500 || strings.Contains(w.Body.String(), directory) || hub.QueryLogs(observability.LogQuery{}).Total != 1 {
		t.Fatal("storage failure deleted cached history or leaked filesystem details")
	}
}

type unavailableCleanupAuditStore struct{ store.Store }

func (s unavailableCleanupAuditStore) CreateAuditLog(model.AuditLog) error {
	return errors.New("audit unavailable")
}

func TestLogCleanupDoesNotDeleteWhenAuditStorageIsUnavailable(t *testing.T) {
	memory := store.NewMemory()
	users := user.NewService(memory)
	if _, err := users.Create("owner", "Password88", "super_admin"); err != nil {
		t.Fatal(err)
	}
	_, token, err := users.Authenticate("owner", "Password88")
	if err != nil {
		t.Fatal(err)
	}
	hub, err := observability.NewHub(observability.HubOptions{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	_, _ = hub.Write([]byte("{\"msg\":\"preserve when auditing fails\"}\n"))
	admin := NewAdminWithUserManagement(unavailableCleanupAuditStore{Store: memory}, plugin.NewRegistry(), users, slog.New(slog.NewTextHandler(io.Discard, nil)))
	admin.SetObservability(hub, observability.NewMetrics())
	r := httptest.NewRequest("DELETE", "/admin/v1/observability/logs", strings.NewReader(`{"confirm":true}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, r)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "log_cleanup_audit_unavailable") || hub.QueryLogs(observability.LogQuery{}).Total != 1 {
		t.Fatal("cleanup bypassed failed audit storage")
	}
}

func TestLogCleanupRoleCannotBeGrantedByAnOrdinaryUserManager(t *testing.T) {
	memory := store.NewMemory()
	users := user.NewService(memory)
	if _, err := users.Create("owner", "Password88", "super_admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Create("manager", "Password88", "api_developer"); err != nil {
		t.Fatal(err)
	}
	if _, err := users.CreateRole("log-cleaner", "", []string{"observability.read", "observability.logs.clear"}); err == nil {
		t.Fatal("fourth role was created")
	}
	_, token, err := users.Authenticate("manager", "Password88")
	if err != nil {
		t.Fatal(err)
	}
	admin := NewAdminWithUserManagement(memory, plugin.NewRegistry(), users, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := httptest.NewRequest("POST", "/admin/v1/users", strings.NewReader(`{"username":"unapproved-cleaner","password":"Password88","role":"log-cleaner"}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, r)
	if w.Code != 403 || memory.CountUsers() != 2 {
		t.Fatal("ordinary user manager escalated cleanup privilege")
	}
}
