package gateway

import (
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/ratelimit"
	"api-manager/internal/store"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type cacheFixturePlugin struct {
	calls    atomic.Int32
	status   int
	response string
	cookie   bool
	failure  bool
}

func (p *cacheFixturePlugin) Name() string          { return "cache-fixture" }
func (p *cacheFixturePlugin) CacheRevision() string { return "fixture-v1" }
func (p *cacheFixturePlugin) Handle(ctx context.Context, w http.ResponseWriter, r *http.Request, a model.API) error {
	p.calls.Add(1)
	if strings.Contains(r.Header.Get("Cookie"), "api_manager_session") {
		return errors.New("session leaked")
	}
	if p.failure {
		return errors.New("fixture failure")
	}
	if p.cookie {
		w.Header().Set("Set-Cookie", "business=value")
	}
	w.Header().Set("Content-Type", "application/json")
	status := p.status
	if status == 0 {
		status = 200
	}
	w.WriteHeader(status)
	_, err := w.Write([]byte(p.response))
	return err
}
func cachedGateway(t *testing.T) (*Gateway, *store.Memory, *cacheFixturePlugin, model.API) {
	t.Helper()
	m := store.NewMemory()
	p := &cacheFixturePlugin{response: `{"value":"sensitive-response"}`}
	registry := plugin.NewRegistry()
	registry.Register(p)
	g := New(m, registry, ratelimit.NewMemory(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	g.SetCacheEncryptionKey("private-test-cache-encryption-key")
	now := time.Now().UTC()
	a := model.API{ID: "cache-api", Name: "cache-test", Method: "GET", Methods: []string{"GET", "POST", "PUT"}, Path: "/api/cache", AuthMode: "none", Plugin: p.Name(), Enabled: true, PublishedAt: &now, UpdatedAt: now, PluginCache: model.PluginCacheConfig{Enabled: true, TTLSeconds: 30, MaxEntries: 10}}
	if err := m.CreateAPI(a); err != nil {
		t.Fatal(err)
	}
	return g, m, p, a
}
func cacheRequest(g *Gateway, method, path, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		r.Header.Set("X-API-Key", key)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}
func TestCacheHitExpirySettingsAndEncryption(t *testing.T) {
	g, m, p, a := cachedGateway(t)
	for i := 0; i < 2; i++ {
		w := cacheRequest(g, "GET", "/api/cache?q=1", "", "")
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		want := "MISS"
		if i == 1 {
			want = "HIT"
		}
		if w.Header().Get("X-Plugin-Cache") != want {
			t.Fatal(w.Header())
		}
	}
	if p.calls.Load() != 1 {
		t.Fatal("cache not used")
	}
	rStored := httptest.NewRequest("GET", "/api/cache?q=1", nil)
	kStored, _ := pluginCacheKey(rStored, a, p)
	stored, err := m.GetPluginCache(context.Background(), a.ID, kStored, time.Now())
	if err != nil || strings.Contains(stored.Ciphertext, "sensitive-response") {
		t.Fatal("plaintext/missing response", err)
	}
	// Update timestamp changes the key. Clear also changes the timestamp and must
	// reject an old in-flight writer that attempts to repopulate stale results.
	oldKey, _ := pluginCacheKey(httptest.NewRequest("GET", "/api/cache", nil), a, p)
	if err := m.ClearPluginCache(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	stale := model.PluginCacheEntry{APIID: a.ID, Key: oldKey, APIUpdatedAt: a.UpdatedAt, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if !errors.Is(m.PutPluginCache(context.Background(), stale, 10), store.ErrConflict) {
		t.Fatal("stale writer accepted")
	}
	cacheRequest(g, "GET", "/api/cache?q=1", "", "")
	if p.calls.Load() != 2 {
		t.Fatal("cleared data returned")
	}
	current, _ := m.GetAPI(a.ID)
	r := httptest.NewRequest("GET", "/api/cache?q=1", nil)
	key, _ := pluginCacheKey(r, current, p)
	e, err := m.GetPluginCache(context.Background(), a.ID, key, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e.ExpiresAt = time.Now().Add(-time.Second)
	_ = m.PutPluginCache(context.Background(), e, 10)
	cacheRequest(g, "GET", "/api/cache?q=1", "", "")
	if p.calls.Load() != 3 {
		t.Fatal("expired response used")
	}
}
func TestCacheAuthQuotasMethodsParametersAndCookies(t *testing.T) {
	g, m, p, a := cachedGateway(t)
	a.AuthMode = "api_key"
	a.RateLimitPerMinute = 3
	_ = m.UpdateAPI(a)
	for _, key := range []string{"first-key", "second-key"} {
		_ = m.CreateCredential(model.Credential{ID: key, Hash: auth.HashAPIKey(key)})
	}
	if cacheRequest(g, "GET", "/api/cache", "", "").Code != 401 {
		t.Fatal("cache bypassed auth")
	}
	for i := 0; i < 3; i++ {
		if cacheRequest(g, "GET", "/api/cache", "first-key", "").Code != 200 {
			t.Fatal("request rejected")
		}
	}
	if cacheRequest(g, "GET", "/api/cache", "first-key", "").Code != 429 {
		t.Fatal("hit did not consume quota")
	}
	if p.calls.Load() != 1 {
		t.Fatal(p.calls.Load())
	}
	if cacheRequest(g, "GET", "/api/cache", "second-key", "").Header().Get("X-Plugin-Cache") != "MISS" {
		t.Fatal("keys shared cache")
	}
	_ = m.UpdateCredential(model.Credential{ID: "second-key", Hash: auth.HashAPIKey("second-key"), Revoked: true})
	if cacheRequest(g, "GET", "/api/cache", "second-key", "").Code != 401 {
		t.Fatal("revoked key used cache")
	}
	// A common UI session never replaces a business KEY and never crosses into WASM.
	r := httptest.NewRequest("GET", "/api/cache", nil)
	r.Header.Set("Cookie", auth.SessionCookie+"=us_secret")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("session authorized business API")
	}
	g, m, p, a = cachedGateway(t)
	cacheRequest(g, "GET", "/api/cache?q=1", "", "")
	cacheRequest(g, "GET", "/api/cache?q=2", "", "")
	if p.calls.Load() != 2 {
		t.Fatal("query variants shared")
	}
	for i := 0; i < 2; i++ {
		cacheRequest(g, "POST", "/api/cache", "", `{"q":1}`)
	}
	if p.calls.Load() != 4 {
		t.Fatal("POST cached without opt-in")
	}
	a.PluginCache.CachePOST = true
	_ = m.UpdateAPI(a)
	for i := 0; i < 2; i++ {
		cacheRequest(g, "POST", "/api/cache", "", `{"q":1}`)
	}
	cacheRequest(g, "POST", "/api/cache", "", `{"q":2}`)
	if p.calls.Load() != 6 {
		t.Fatal("POST variant handling", p.calls.Load())
	}
	for i := 0; i < 2; i++ {
		cacheRequest(g, "PUT", "/api/cache", "", `{}`)
	}
	if p.calls.Load() != 8 {
		t.Fatal("PUT cached")
	}
	r = httptest.NewRequest("GET", "/api/cache", nil)
	r.Header.Set("Cookie", auth.SessionCookie+"=us_secret")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("session leaked to plugin")
	}
}
func TestCacheRejectsErrorsOversizeSchemaAndParallelColdCalls(t *testing.T) {
	for _, kind := range []string{"status", "cookie", "failure", "oversize", "schema"} {
		g, m, p, a := cachedGateway(t)
		switch kind {
		case "status":
			p.status = 500
		case "cookie":
			p.cookie = true
		case "failure":
			p.failure = true
		case "oversize":
			p.response = strings.Repeat("x", model.MaxPluginCacheBody+1)
		case "schema":
			a.ResponseSchema = []byte(`{"type":"object","required":["missing"]}`)
			_ = m.UpdateAPI(a)
		}
		for i := 0; i < 2; i++ {
			cacheRequest(g, "GET", "/api/cache", "", "")
		}
		if p.calls.Load() != 2 {
			t.Fatal("unsafe response cached", kind)
		}
	}
	g, _, p, _ := cachedGateway(t)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); cacheRequest(g, "GET", "/api/cache", "", "") }()
	}
	wg.Wait()
	if p.calls.Load() != 1 {
		t.Fatal("cold cache stampede", p.calls.Load())
	}
}

func TestCacheStoreFailureDoesNotInterruptPluginAndNoCacheRequestsBypass(t *testing.T) {
	g, m, p, a := cachedGateway(t)
	// Corrupt ciphertext is treated as a miss and replaced, not returned.
	cacheRequest(g, "GET", "/api/cache", "", "")
	r := httptest.NewRequest("GET", "/api/cache", nil)
	key, _ := pluginCacheKey(r, a, p)
	e, _ := m.GetPluginCache(context.Background(), a.ID, key, time.Now())
	e.Ciphertext = "not-authentic-ciphertext"
	_ = m.PutPluginCache(context.Background(), e, 10)
	if cacheRequest(g, "GET", "/api/cache", "", "").Code != 200 || p.calls.Load() != 2 {
		t.Fatal("corrupt ciphertext not handled")
	}
	r = httptest.NewRequest("GET", "/api/cache", nil)
	r.Header.Set("Cache-Control", "no-cache")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if p.calls.Load() != 3 {
		t.Fatal("request freshness ignored")
	}
	// Headers that select user data must not share entries, including forwarded
	// client identity when several clients use a common integration KEY.
	for _, ip := range []string{"192.0.2.1", "192.0.2.2"} {
		r = httptest.NewRequest("GET", "/api/cache", nil)
		r.Header.Set("X-Forwarded-For", ip)
		g.ServeHTTP(httptest.NewRecorder(), r)
	}
	if p.calls.Load() != 5 {
		t.Fatal("forwarded identities mixed")
	}
}

func TestResponseCacheUsesSavedSchemaAndDoesNotStoreFreshnessOptOut(t *testing.T) {
	g, _, p, _ := cachedGateway(t)
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("GET", "/api/cache", nil)
		r.Header.Set("Cache-Control", "no-store")
		g.ServeHTTP(httptest.NewRecorder(), r)
	}
	if p.calls.Load() != 2 {
		t.Fatal("no-store request served or populated cache")
	}
}

type unavailableCacheStore struct{ *store.Memory }

func (s unavailableCacheStore) GetPluginCache(context.Context, string, string, time.Time) (model.PluginCacheEntry, error) {
	return model.PluginCacheEntry{}, errors.New("cache unavailable")
}
func (s unavailableCacheStore) PutPluginCache(context.Context, model.PluginCacheEntry, int) error {
	return errors.New("cache unavailable")
}
func TestUnavailableCacheFallsBackAndSchemaChecksRunBeforeCache(t *testing.T) {
	g, m, p, a := cachedGateway(t)
	g.store = unavailableCacheStore{m}
	for range 2 {
		w := cacheRequest(g, "GET", "/api/cache", "", "")
		if w.Code != 200 {
			t.Fatal("storage failure broke API", w.Code)
		}
	}
	if p.calls.Load() != 2 {
		t.Fatal("plugin not executed")
	}
	g.store = m
	a.ParametersSchema = []byte(`{"type":"object","properties":{"query":{"type":"object","required":["q"],"properties":{"q":{"type":"string"}}}}}`)
	_ = m.UpdateAPI(a)
	if cacheRequest(g, "GET", "/api/cache?q=ok", "", "").Code != 200 {
		t.Fatal("valid request rejected")
	}
	count := p.calls.Load()
	if cacheRequest(g, "GET", "/api/cache", "", "").Code != 400 || p.calls.Load() != count {
		t.Fatal("schema verification skipped")
	}
}

func TestFailedRequestBodyReadIsNotSilentlyConsumedByCache(t *testing.T) {
	g, _, p, a := cachedGateway(t)
	a.PluginCache.CachePOST = true
	r := httptest.NewRequest("POST", "/api/cache", nil)
	r.Body = io.NopCloser(io.MultiReader(strings.NewReader("partial"), &cacheBodyReadError{err: io.ErrUnexpectedEOF}))
	if _, ok := pluginCacheKey(r, a, p); ok {
		t.Fatal("broken body cached")
	}
	if _, err := io.ReadAll(r.Body); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("body transport error lost", err)
	}
	_ = g
}

func TestClientHeadersCannotActivateTestPrincipal(t *testing.T) {
	g, m, _, a := cachedGateway(t)
	a.AuthMode = "api_key"
	a.PublicTestEnabled = true
	_ = m.UpdateAPI(a)
	r := httptest.NewRequest("GET", "/api/cache", nil)
	r.Header.Set("X-API-Test", "1")
	r.Header.Set("X-Test-User", "attacker")
	r.Header.Set("X-API-Request", "1")
	r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "us_fake"})
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("header activated privileged test", w.Code)
	}
}
