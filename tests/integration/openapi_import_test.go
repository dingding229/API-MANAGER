package integration

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	adminapi "api-manager/internal/api"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
)

func TestOpenAPIYAMLImport(t *testing.T) {
	memory := store.NewMemory()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	admin := adminapi.NewAdmin(memory, plugin.NewRegistry(), "admin-secret", logger)
	yamlDocument := `openapi: 3.0.3
servers:
  - url: https://upstream.example.test
paths:
  /widgets:
    get:
      summary: List widgets
      responses:
        "200":
          description: OK
`
	body := bytes.NewBufferString(`{"document_yaml":` + strconv.Quote(yamlDocument) + `}`)
	request := httptest.NewRequest(http.MethodPost, "/admin/v1/openapi/import", body)
	request.Header.Set("X-Admin-Token", "admin-secret")
	response := httptest.NewRecorder()
	admin.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("YAML import returned %d: %s", response.Code, response.Body.String())
	}
	if len(memory.ListAPIs()) != 1 || memory.ListAPIs()[0].Path != "/widgets" {
		t.Fatalf("unexpected imported APIs: %#v", memory.ListAPIs())
	}
}
