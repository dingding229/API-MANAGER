package integration

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	adminapi "api-manager/internal/api"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"api-manager/internal/web"
)

func TestRBACBlocksUnauthorizedMutation(t *testing.T) {
	memory := store.NewMemory()
	service := user.NewService(memory, "test-secret", 0)
	viewer, err := service.Create("viewer@example.com", "password123", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := service.Authenticate(viewer.Email, "password123")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	admin := adminapi.NewAdminWithUserAuth(memory, plugin.NewRegistry(), "root-token", service, logger)

	read := httptest.NewRequest(http.MethodGet, "/admin/v1/apis", nil)
	read.Header.Set("Authorization", "Bearer "+token)
	readResponse := httptest.NewRecorder()
	admin.ServeHTTP(readResponse, read)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("expected api.read to succeed, got %d", readResponse.Code)
	}

	write := httptest.NewRequest(http.MethodPost, "/admin/v1/apis", bytes.NewBufferString(`{"name":"blocked","method":"GET","path":"/api/blocked","response_body":"{}"}`))
	write.Header.Set("Authorization", "Bearer "+token)
	writeResponse := httptest.NewRecorder()
	admin.ServeHTTP(writeResponse, write)
	if writeResponse.Code != http.StatusForbidden {
		t.Fatalf("expected api.write to be forbidden, got %d: %s", writeResponse.Code, writeResponse.Body.String())
	}
}

func TestConsoleServesIndex(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/console/", nil)
	response := httptest.NewRecorder()
	web.Console().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte("API Manager Console")) {
		t.Fatal("console index was not served")
	}
}
