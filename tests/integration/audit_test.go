package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	adminapi "api-manager/internal/api"
	"api-manager/internal/httpx"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
)

func TestAdminMutationCreatesAuditableRecordAndRBACProtectsIt(t *testing.T) {
	memory := store.NewMemory()
	service := user.NewService(memory, "test-secret", 0)
	adminUser, err := service.Create("admin@example.com", "password123", "super_admin")
	if err != nil {
		t.Fatal(err)
	}
	_, adminToken, err := service.Authenticate(adminUser.Email, "password123")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := service.Create("viewer@example.com", "password123", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	_, viewerToken, err := service.Authenticate(viewer.Email, "password123")
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	admin := httpx.RequestID(adminapi.NewAdminWithUserAuth(memory, plugin.NewRegistry(), "root-token", service, logger))
	body := []byte(`{"name":"audited","method":"GET","path":"/api/audited","auth_mode":"none","response_body":"{}"}`)
	create := httptest.NewRequest(http.MethodPost, "/admin/v1/apis", bytes.NewReader(body))
	create.Header.Set("Authorization", "Bearer "+adminToken)
	create.Header.Set(httpx.RequestIDHeader, "audit-request-1")
	created := httptest.NewRecorder()
	admin.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create returned %d: %s", created.Code, created.Body.String())
	}

	query := httptest.NewRequest(http.MethodGet, "/admin/v1/audit-logs?action=api.create", nil)
	query.Header.Set("Authorization", "Bearer "+adminToken)
	listed := httptest.NewRecorder()
	admin.ServeHTTP(listed, query)
	if listed.Code != http.StatusOK {
		t.Fatalf("list returned %d: %s", listed.Code, listed.Body.String())
	}
	var page model.AuditLogPage
	if err := json.Unmarshal(listed.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("expected one record: %#v", page)
	}
	log := page.Items[0]
	if log.ActorID != adminUser.ID || log.ActorEmail != adminUser.Email || log.RequestID != "audit-request-1" || log.Action != "api.create" {
		t.Fatalf("wrong audit record: %#v", log)
	}

	forbidden := httptest.NewRequest(http.MethodGet, "/admin/v1/audit-logs", nil)
	forbidden.Header.Set("Authorization", "Bearer "+viewerToken)
	response := httptest.NewRecorder()
	admin.ServeHTTP(response, forbidden)
	if response.Code != http.StatusForbidden {
		t.Fatalf("viewer audit access returned %d: %s", response.Code, response.Body.String())
	}
}
