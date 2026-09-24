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
	"api-manager/internal/gateway"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/ratelimit"
	"api-manager/internal/store"
)

func TestGatewayValidatesRequestAndResponseSchemas(t *testing.T) {
	memory := store.NewMemory()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	admin := adminapi.NewAdmin(memory, plugin.NewRegistry(), "root-token", logger)
	gatewayHandler := gateway.New(memory, plugin.NewRegistry(), ratelimit.NewMemory(), logger)

	requestSchema := json.RawMessage(`{"type":"object","required":["id"],"properties":{"id":{"type":"integer"}},"additionalProperties":false}`)
	responseSchema := json.RawMessage(`{"type":"object","required":["message"],"properties":{"message":{"type":"string"}}}`)
	createBody, _ := json.Marshal(model.CreateAPIRequest{Name: "schema", Method: "POST", Path: "/api/schema", AuthMode: "none", ResponseStatus: 200, ResponseBody: `{"message":"ok"}`, RequestSchema: requestSchema, ResponseSchema: responseSchema})
	create := httptest.NewRequest(http.MethodPost, "/admin/v1/apis", bytes.NewReader(createBody))
	create.Header.Set("X-Admin-Token", "root-token")
	created := httptest.NewRecorder()
	admin.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create returned %d: %s", created.Code, created.Body.String())
	}
	var api model.API
	if err := json.Unmarshal(created.Body.Bytes(), &api); err != nil {
		t.Fatal(err)
	}
	publish := httptest.NewRequest(http.MethodPost, "/admin/v1/apis/"+api.ID+"/publish", nil)
	publish.Header.Set("X-Admin-Token", "root-token")
	published := httptest.NewRecorder()
	admin.ServeHTTP(published, publish)
	if published.Code != http.StatusOK {
		t.Fatalf("publish returned %d: %s", published.Code, published.Body.String())
	}

	invalid := httptest.NewRequest(http.MethodPost, "/api/schema", bytes.NewBufferString(`{"id":"not-an-int"}`))
	invalid.Header.Set("Content-Type", "application/json")
	invalidResult := httptest.NewRecorder()
	gatewayHandler.ServeHTTP(invalidResult, invalid)
	if invalidResult.Code != http.StatusBadRequest {
		t.Fatalf("invalid request returned %d: %s", invalidResult.Code, invalidResult.Body.String())
	}

	valid := httptest.NewRequest(http.MethodPost, "/api/schema", bytes.NewBufferString(`{"id":7}`))
	valid.Header.Set("Content-Type", "application/json")
	validResult := httptest.NewRecorder()
	gatewayHandler.ServeHTTP(validResult, valid)
	if validResult.Code != http.StatusOK || validResult.Body.String() != `{"message":"ok"}` {
		t.Fatalf("valid request failed %d: %s", validResult.Code, validResult.Body.String())
	}
}

func TestOpenAPIImportCreatesDraftsWithSchemas(t *testing.T) {
	memory := store.NewMemory()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	admin := adminapi.NewAdmin(memory, plugin.NewRegistry(), "root-token", logger)
	openAPIDocument := map[string]any{
		"openapi":    "3.0.3",
		"servers":    []any{map[string]any{"url": "https://upstream.example.test"}},
		"components": map[string]any{"schemas": map[string]any{"Order": map[string]any{"type": "object", "required": []any{"id"}, "properties": map[string]any{"id": map[string]any{"type": "integer"}}}}},
		"paths": map[string]any{"/orders": map[string]any{"post": map[string]any{
			"operationId": "createOrder", "requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Order"}}}},
			"responses": map[string]any{"201": map[string]any{"description": "created", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Order"}}}}},
		}}},
	}
	body, _ := json.Marshal(map[string]any{"document": openAPIDocument, "path_prefix": "/api"})
	request := httptest.NewRequest(http.MethodPost, "/admin/v1/openapi/import", bytes.NewReader(body))
	request.Header.Set("X-Admin-Token", "root-token")
	response := httptest.NewRecorder()
	admin.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("import returned %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Created      []model.API `json:"created"`
		CreatedCount int         `json:"created_count"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.CreatedCount != 1 || len(result.Created) != 1 {
		t.Fatalf("unexpected import result: %s", response.Body.String())
	}
	api := result.Created[0]
	if api.Path != "/api/orders" || api.Method != http.MethodPost || api.UpstreamURL != "https://upstream.example.test" || len(api.RequestSchema) == 0 || len(api.ResponseSchema) == 0 {
		t.Fatalf("imported API is incomplete: %#v", api)
	}
	if api.Enabled {
		t.Fatal("imported API must be a draft")
	}
}
