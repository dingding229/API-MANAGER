package audit

import (
	"net/http/httptest"
	"testing"
	"time"

	"api-manager/internal/httpx"
	"api-manager/internal/model"
	"api-manager/internal/store"
)

func TestRecordRedactsSensitiveDetailsAndCapturesRequestMetadata(t *testing.T) {
	memory := store.NewMemory()
	service := New(memory, nil)
	req := httptest.NewRequest("POST", "/admin/v1/credentials", nil)
	req.RemoteAddr = "203.0.113.20:4567"
	req.Header.Set("User-Agent", "audit-test")
	req = req.WithContext(httpx.WithRequestID(req.Context(), "request-123"))

	service.Record(req.Context(), Actor{ID: "actor-id", Type: "user", Email: "admin@example.com"}, req, "credential.create", "credential", "cred_1", 201, map[string]any{
		"api_key": "ak_plaintext", "password": "plain", "nested": map[string]any{"token": "secret", "safe": "retained"},
	})

	page, err := memory.ListAuditLogs(model.AuditLogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("expected one audit record, got %#v", page)
	}
	log := page.Items[0]
	if log.RequestID != "request-123" || log.RemoteAddr != "203.0.113.20" || log.UserAgent != "audit-test" {
		t.Fatalf("request metadata not captured: %#v", log)
	}
	if log.Details["api_key"] != "[REDACTED]" || log.Details["password"] != "[REDACTED]" {
		t.Fatalf("sensitive values were not redacted: %#v", log.Details)
	}
	nested := log.Details["nested"].(map[string]any)
	if nested["token"] != "[REDACTED]" || nested["safe"] != "retained" {
		t.Fatalf("nested detail redaction failed: %#v", nested)
	}
}

func TestListAuditLogsFiltersAndPaginatesNewestFirst(t *testing.T) {
	memory := store.NewMemory()
	now := time.Now().UTC()
	for i, action := range []string{"api.create", "api.publish", "user.create"} {
		if err := memory.CreateAuditLog(model.AuditLog{Action: action, ResourceType: "api", CreatedAt: now.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := memory.ListAuditLogs(model.AuditLogQuery{Action: "api.create", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].Action != "api.create" {
		t.Fatalf("unexpected filter result: %#v", page)
	}
	page, err = memory.ListAuditLogs(model.AuditLogQuery{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Items) != 2 || page.Items[0].Action != "user.create" {
		t.Fatalf("unexpected pagination order: %#v", page)
	}
}
