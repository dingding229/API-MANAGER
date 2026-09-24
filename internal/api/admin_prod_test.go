package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"api-manager/internal/store"
)

func TestAdminTokenCanBeDisabledForProduction(t *testing.T) {
	admin := NewAdmin(store.NewMemory(), nil, "a-long-admin-token", nil)
	request := httptest.NewRequest(http.MethodGet, "/admin/v1/apis", nil)
	request.Header.Set("X-Admin-Token", "a-long-admin-token")
	if _, allowed := admin.requestActor(request); !allowed {
		t.Fatal("development admin token should be accepted")
	}
	admin.SetAdminTokenAPIEnabled(false)
	if _, allowed := admin.requestActor(request); allowed {
		t.Fatal("bootstrap token must not authorize production admin endpoints")
	}
}
