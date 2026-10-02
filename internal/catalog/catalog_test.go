package catalog

import (
	"api-manager/internal/model"
	"api-manager/internal/store"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublicProjectionIncludesOnlyVisiblePublishedAPIs(t *testing.T) {
	m := store.NewMemory()
	now := time.Now()
	for _, a := range []model.API{
		{ID: "private-database-id", Name: "internal name", Description: "private-description-secret", Method: "GET", Path: "/api/public/{id}", AuthMode: "api_key", PublicVisible: true, PublicTitle: "公开资料", PublicSummary: "仅对外的说明", Enabled: true, PublishedAt: &now, UpstreamURL: "https://private.internal/secret", UpstreamAuthRef: "private-credential-reference", ResponseBody: "real-response-secret", RequestSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string","default":"private-default-secret","description":"private-schema-description"}}}`)},
		{ID: "hidden", Name: "hidden-secret", Method: "GET", Path: "/api/hidden", AuthMode: "none", PublicVisible: false, Enabled: true, PublishedAt: &now},
		{ID: "draft", Name: "draft-secret", Method: "GET", Path: "/api/draft", AuthMode: "none", PublicVisible: true, PublicTitle: "draft", Enabled: true},
		{ID: "disabled", Method: "GET", Path: "/api/disabled", AuthMode: "none", PublicVisible: true, PublicTitle: "disabled", PublishedAt: &now},
	} {
		if err := m.CreateAPI(a); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	New(m, "https://api.example.com").ServeHTTP(w, httptest.NewRequest("GET", "/public/v1/catalog", nil))
	var r Response
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.APIs) != 1 || r.APIs[0].Title != "公开资料" || r.APIs[0].ID == "private-database-id" {
		t.Fatalf("unexpected public projection: %+v", r)
	}
	for _, secret := range []string{"private-database-id", "internal name", "private-description-secret", "private.internal", "private-credential-reference", "real-response-secret", "private-default-secret", "private-schema-description", "hidden-secret", "draft-secret", "password_hash", "auth_config", "upstream_url"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("public output contains private field/value: %s", secret)
		}
	}
}
func TestHidingAPIImmediatelyRemovesItFromTheExport(t *testing.T) {
	m := store.NewMemory()
	now := time.Now()
	a := model.API{ID: "id", Method: "GET", Path: "/api/item", AuthMode: "none", Enabled: true, PublishedAt: &now, PublicVisible: true, PublicTitle: "Item"}
	_ = m.CreateAPI(a)
	a.PublicVisible = false
	_ = m.UpdateAPI(a)
	w := httptest.NewRecorder()
	New(m, "").ServeHTTP(w, httptest.NewRequest("GET", "/public/v1/catalog", nil))
	var r Response
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	if len(r.APIs) != 0 {
		t.Fatal("hidden API is still public")
	}
}
func TestCatalogIsReadOnlyAndDoesNotAcceptCredentialOrigins(t *testing.T) {
	m := store.NewMemory()
	c := New(m, "https://user:secret@api.example.com")
	if c.baseURL != "" {
		t.Fatal("credential-bearing public URL accepted")
	}
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("POST", "/public/v1/catalog", nil))
	if w.Code != 405 {
		t.Fatal("public write method accepted")
	}
}

func TestMultipleMethodsProduceOnePublicDocument(t *testing.T) {
	m := store.NewMemory()
	now := time.Now()
	a := model.API{ID: "multi", Method: "GET", Methods: []string{"GET", "POST", "OPTIONS"}, Path: "/api/item/{id}", AuthMode: "api_key", Enabled: true, PublishedAt: &now, PublicVisible: true, PublicTitle: "商品"}
	if err := m.CreateAPI(a); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	New(m, "").ServeHTTP(w, httptest.NewRequest("GET", "/public/v1/catalog", nil))
	var result Response
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.APIs) != 1 || len(result.APIs[0].Methods) != 3 || len(result.APIs[0].Operations) != 3 {
		t.Fatalf("methods were duplicated as interfaces: %+v", result)
	}
}
func TestSamePathDistinctOperationsKeepAuthenticationAndParameters(t *testing.T) {
	m := store.NewMemory()
	now := time.Now()
	for _, a := range []model.API{{ID: "get", Method: "GET", Path: "/api/items", AuthMode: "none", Enabled: true, PublishedAt: &now, PublicVisible: true, PublicTitle: "商品"}, {ID: "post", Method: "POST", Path: "/api/items", AuthMode: "api_key", Enabled: true, PublishedAt: &now, PublicVisible: true, PublicTitle: "商品", RequestSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)}} {
		if err := m.CreateAPI(a); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	New(m, "").ServeHTTP(w, httptest.NewRequest("GET", "/public/v1/catalog", nil))
	var result Response
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.APIs) != 1 {
		t.Fatal("same interface split into separate cards")
	}
	for _, op := range result.APIs[0].Operations {
		if op.Method == "POST" && (op.Authentication != "api_key" || len(op.Body) != 1) {
			t.Fatal("operation-specific settings lost")
		}
	}
}
func TestCatalogUsesDedicatedDomainOrWebsite(t *testing.T) {
	c := New(store.NewMemory(), "https://old-example.example")
	cfg := model.PublicSiteInfo{WebsiteURL: "http://site.example.com:8081", APIDomain: "https://api.example.com"}
	c.SetSiteProvider(func() model.PublicSiteInfo { return cfg })
	for _, expected := range []string{"https://api.example.com", "http://site.example.com:8081"} {
		w := httptest.NewRecorder()
		c.ServeHTTP(w, httptest.NewRequest("GET", "/public/v1/catalog", nil))
		var result Response
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		if result.BaseURL != expected {
			t.Fatal("wrong API example origin")
		}
		cfg.APIDomain = ""
	}
}
