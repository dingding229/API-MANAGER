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
	"api-manager/internal/auth"
	"api-manager/internal/gateway"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/ratelimit"
	"api-manager/internal/store"
)

func TestAdminToGatewayFlow(t *testing.T) {
	memory := store.NewMemory()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	admin := adminapi.NewAdmin(memory, plugin.NewRegistry(), "admin-secret", logger)
	gatewayHandler := gateway.New(memory, plugin.NewRegistry(), ratelimit.NewMemory(), logger)

	credential := model.Credential{ID: "cred_1", Name: "client", Prefix: "ak_test", Hash: auth.HashAPIKey("ak_test-key")}
	if err := memory.CreateCredential(credential); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"name":"hello","method":"GET","path":"/api/hello","auth_mode":"api_key","response_body":"{\"message\":\"ok\"}"}`)
	createRequest := httptest.NewRequest(http.MethodPost, "/admin/v1/apis", bytes.NewReader(body))
	createRequest.Header.Set("X-Admin-Token", "admin-secret")
	createResponse := httptest.NewRecorder()
	admin.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create API returned %d: %s", createResponse.Code, createResponse.Body.String())
	}

	var created model.API
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	publishRequest := httptest.NewRequest(http.MethodPost, "/admin/v1/apis/"+created.ID+"/publish", nil)
	publishRequest.Header.Set("X-Admin-Token", "admin-secret")
	publishResponse := httptest.NewRecorder()
	admin.ServeHTTP(publishResponse, publishRequest)
	if publishResponse.Code != http.StatusOK {
		t.Fatalf("publish API returned %d: %s", publishResponse.Code, publishResponse.Body.String())
	}

	callRequest := httptest.NewRequest(http.MethodGet, "/api/hello", nil)
	callRequest.Header.Set("X-API-Key", "ak_test-key")
	callResponse := httptest.NewRecorder()
	gatewayHandler.ServeHTTP(callResponse, callRequest)
	if callResponse.Code != http.StatusOK {
		t.Fatalf("gateway returned %d: %s", callResponse.Code, callResponse.Body.String())
	}
	if got := callResponse.Body.String(); got != `{"message":"ok"}` {
		t.Fatalf("unexpected body: %s", got)
	}
}
