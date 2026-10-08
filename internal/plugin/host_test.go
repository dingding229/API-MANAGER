package plugin

import (
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type runtimeTestStore struct {
	mu       sync.Mutex
	policies map[string]model.PluginRuntimePolicy
	records  map[string]model.PluginSession
}

func (s *runtimeTestStore) PluginRuntimePolicy(ctx context.Context, id string) (model.PluginRuntimePolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.policies[id], nil
}
func (s *runtimeTestStore) SavePluginRuntimePolicy(ctx context.Context, id string, p model.PluginRuntimePolicy, version int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.policies[id].Version != version {
		return 0, store.ErrConflict
	}
	p.Version = version + 1
	s.policies[id] = p
	return p.Version, nil
}
func (s *runtimeTestStore) PluginSession(ctx context.Context, id, key string) (model.PluginSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.records[id+":"+key]
	if !ok || (v.ExpiresAt != nil && !time.Now().Before(*v.ExpiresAt)) {
		return v, store.ErrNotFound
	}
	return v, nil
}
func (s *runtimeTestStore) SavePluginSession(ctx context.Context, v model.PluginSession, version int64, limit int, policyVersion int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records[v.PluginID+":"+v.KeyHash].Version != version || s.policies[v.PluginID].Version != policyVersion || !s.policies[v.PluginID].SessionPersistence {
		return 0, store.ErrConflict
	}
	v.Version = version + 1
	s.records[v.PluginID+":"+v.KeyHash] = v
	return v.Version, nil
}
func (s *runtimeTestStore) DeletePluginSession(ctx context.Context, id, key string, version int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records[id+":"+key].Version != version {
		return store.ErrConflict
	}
	delete(s.records, id+":"+key)
	return nil
}
func testHostServices() (*pluginRuntimeServices, *runtimeTestStore) {
	st := &runtimeTestStore{policies: map[string]model.PluginRuntimePolicy{}, records: map[string]model.PluginSession{}}
	return &pluginRuntimeServices{store: st, key: "host-fixture-encryption-key-0123456789", policies: map[string]runtimeSnapshot{}, sessions: map[string]map[string]cachedPluginSession{}}, st
}
func TestRuntimePermissionsDefaultDenyAndHTTPSOrigins(t *testing.T) {
	p := defaultRuntimePolicy(model.PluginRuntimePolicy{})
	if p.NetworkEnabled || p.SessionCache || p.SessionPersistence {
		t.Fatal("permissions enabled by default")
	}
	for _, origin := range []string{"http://example.com", "https://example.com/path", "https://user:pass@example.com", "https://*.example.com", "https://example.com?", "https://example.com#x"} {
		p := defaultRuntimePolicy(model.PluginRuntimePolicy{NetworkEnabled: true, AllowedOrigins: []string{origin}})
		if validateRuntimePolicy(&p) == nil {
			t.Fatal("unsafe origin accepted", origin)
		}
	}
	services, _ := testHostServices()
	services.setPolicy("fixture", "owner", defaultRuntimePolicy(model.PluginRuntimePolicy{}))
	if _, e := services.httpRequest(context.Background(), "fixture", hostHTTPRequest{URL: "https://example.com", Method: "GET"}); e == nil {
		t.Fatal("network enabled without grant")
	}
	if _, e := services.session(context.Background(), "fixture", "session_put", hostSessionRequest{Key: "x", Value: json.RawMessage(`{}`)}); e == nil {
		t.Fatal("session write accepted without grant")
	}
}
func TestPluginPersistentAndCachedSessionsIsolationCASExpiryAndDisable(t *testing.T) {
	s, st := testHostServices()
	policy := defaultRuntimePolicy(model.PluginRuntimePolicy{Version: 1, SessionPersistence: true, SessionCache: true, AllowNonexpiring: true, MaxEntries: 2, SessionTTLSeconds: 60})
	s.setPolicy("first", "plugin-first", policy)
	s.setPolicy("second", "plugin-second", policy)
	st.policies["plugin-first"] = policy
	st.policies["plugin-second"] = policy
	persist, cache := true, false
	ttl := 0
	secret := json.RawMessage(`{"cookie":"external-session-secret","body":"<svg onload=alert(1)>"}`)
	result, e := s.session(context.Background(), "first", "session_put", hostSessionRequest{Key: "external.cookie", Value: secret, Persist: &persist, TTLSeconds: &ttl})
	if e != nil || result.Version != 1 {
		t.Fatal("persist put", e)
	}
	stored := st.records["plugin-first:"+auth.HashAPIKey("external.cookie")]
	if strings.Contains(stored.EncryptedValue, "external-session-secret") {
		t.Fatal("session not encrypted")
	}
	s.sessions = map[string]map[string]cachedPluginSession{}
	read, e := s.session(context.Background(), "first", "session_get", hostSessionRequest{Key: "external.cookie", Persist: &persist})
	if e != nil || !read.Found || string(read.Value) != string(secret) {
		t.Fatal("persistent session did not survive process cache loss", e)
	}
	other, e := s.session(context.Background(), "second", "session_get", hostSessionRequest{Key: "external.cookie", Persist: &persist})
	if e != nil || other.Found {
		t.Fatal("session crossed plugin identities")
	}
	if _, e = s.session(context.Background(), "first", "session_put", hostSessionRequest{Key: "external.cookie", Value: secret, Persist: &persist}); !errors.Is(e, store.ErrConflict) {
		t.Fatal("stale session update accepted", e)
	}
	result, e = s.session(context.Background(), "first", "session_put", hostSessionRequest{Key: "external.cookie", Value: json.RawMessage(`{"cache":"isolated"}`), Persist: &cache})
	if e != nil || result.Version < 1 {
		t.Fatal("cache-only key did not have an independent version", e)
	}
	for i := 0; i < 2; i++ {
		read, e = s.session(context.Background(), "first", "session_get", hostSessionRequest{Key: "external.cookie", Persist: &cache})
		if e != nil || !read.Found || !strings.Contains(string(read.Value), "isolated") {
			t.Fatal("cache session changed storage mode", e)
		}
	}
	p := policy
	p.Version++
	p.SessionPersistence = false
	p.AllowNonexpiring = false
	p.SessionCache = false
	s.setPolicy("first", "plugin-first", p)
	if _, e = s.session(context.Background(), "first", "session_get", hostSessionRequest{Key: "external.cookie"}); e == nil {
		t.Fatal("disabled session permissions not enforced")
	}
	// Cached values are intentionally ephemeral, including their encryption envelope.
	s.setPolicy("second", "plugin-second", policy)
	if _, e = s.session(context.Background(), "second", "session_put", hostSessionRequest{Key: "cache", Value: secret, Persist: &cache}); e != nil {
		t.Fatal(e)
	}
	s.sessions = map[string]map[string]cachedPluginSession{}
	read, e = s.session(context.Background(), "second", "session_get", hostSessionRequest{Key: "cache", Persist: &cache})
	if e != nil || read.Found {
		t.Fatal("cache-only value unexpectedly persisted")
	}
}
func TestPluginNetworkDenyPrivateSpecialAndUnapprovedOrigins(t *testing.T) {
	s, _ := testHostServices()
	for _, origin := range []string{"https://127.0.0.1", "https://169.254.169.254", "https://localhost", "https://[::1]", "https://[64:ff9b::a9fe:a9fe]"} {
		p := defaultRuntimePolicy(model.PluginRuntimePolicy{Version: 1, NetworkEnabled: true, AllowedOrigins: []string{origin}, TimeoutMS: 100})
		s.setPolicy("fixture", "owner", p)
		if _, e := s.httpRequest(context.Background(), "fixture", hostHTTPRequest{URL: origin + "/latest/meta-data", Method: "GET"}); e == nil {
			t.Fatal("private or translated internal address accepted", origin)
		}
	}
	s.setPolicy("fixture", "owner", defaultRuntimePolicy(model.PluginRuntimePolicy{NetworkEnabled: true, AllowedOrigins: []string{"https://example.com"}}))
	if _, e := s.httpRequest(context.Background(), "fixture", hostHTTPRequest{URL: "https://attacker.test", Method: "GET"}); e == nil {
		t.Fatal("allowlist escaped")
	}
	if _, e := s.httpRequest(context.Background(), "fixture", hostHTTPRequest{URL: "https://example.com", Method: "GET", Headers: map[string][]string{"Host": {"localhost"}}}); e == nil {
		t.Fatal("Host header override accepted")
	}
}
func TestPluginHostCapabilityDeclarationsAndInvalidInput(t *testing.T) {
	s, _ := testHostServices()
	r := NewRegistry()
	r.services = s
	manifest := Manifest{Name: "fixture"}
	for _, input := range []string{`{"operation":"http_request","http":{"url":"https://example.com","method":"GET"}}`, `{"operation":"session_get","session":{"key":"test"}}`, `{"operation":"session_put","session":{"key":"test","plugin_id":"other"}}`, `{} {}`} {
		if _, e := r.dispatchHost(context.Background(), manifest, []byte(input)); e == nil {
			t.Fatal("undeclared or malformed host call accepted")
		}
	}
}
func TestWASMHostCallActualGoImportAndSessionRoundTrip(t *testing.T) {
	if os.Getenv("TEST_PLUGIN_HOST_WASM") != "1" {
		t.Skip("set TEST_PLUGIN_HOST_WASM=1 for compiled WASM host integration")
	}
	program := `package main
import("encoding/json";"unsafe")
var heaps [][]byte
//go:wasmimport api_manager host_call
func hostCall(inPtr,inLen,outPtr,outCap uint32)uint64
func call(raw string)string{in:=[]byte(raw);out:=make([]byte,2<<20);code:=hostCall(uint32(uintptr(unsafe.Pointer(&in[0]))),uint32(len(in)),uint32(uintptr(unsafe.Pointer(&out[0]))),uint32(len(out)));if uint32(code)!=0{panic("host status")};return string(out[:uint32(code>>32)])}
//go:wasmexport alloc
func alloc(size uint32)uint32{v:=make([]byte,size);heaps=append(heaps,v);return uint32(uintptr(unsafe.Pointer(&v[0])))}
//go:wasmexport handle
func handle(ptr,size uint32)uint64{first:=call("{\"operation\":\"session_put\",\"session\":{\"key\":\"test\",\"persist\":false,\"version\":0,\"value\":{\"message\":\"host-session-ok\"}}}");second:=call("{\"operation\":\"session_get\",\"session\":{\"key\":\"test\",\"persist\":false}}");body,_:=json.Marshal(map[string]string{"first":first,"second":second});response,_:=json.Marshal(map[string]any{"status":200,"body_base64":body});out:=alloc(uint32(len(response)));copy(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(out))),len(response)),response);return uint64(out)<<32|uint64(len(response))}
func main(){}
`
	dir := t.TempDir()
	file := filepath.Join(dir, "plugin.go")
	if e := os.WriteFile(file, []byte(program), 0600); e != nil {
		t.Fatal(e)
	}
	command := exec.Command("go", "build", "-buildmode=c-shared", "-o", filepath.Join(dir, "plugin.wasm"), file)
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, e := command.CombinedOutput(); e != nil {
		t.Fatalf("compile: %s %v", out, e)
	}
	wasm, e := os.ReadFile(filepath.Join(dir, "plugin.wasm"))
	if e != nil {
		t.Fatal(e)
	}
	r := NewRegistry()
	r.services, _ = testHostServices()
	r.services.setPolicy("hostfixture", "plugin-hostfixture", defaultRuntimePolicy(model.PluginRuntimePolicy{Version: 1, SessionCache: true}))
	manifest := []byte("name: hostfixture\nversion: 1.0.0\nruntime: wasm\nentrypoint: plugin.wasm\ncapabilities: [session_storage]\n")
	if e = r.LoadWASMBytes(context.Background(), manifest, wasm); e != nil {
		t.Fatal(e)
	}
	defer r.Close(context.Background())
	handler, _ := r.Get("hostfixture")
	w := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "https://example.test/api/test", nil)
	if e = handler.Handle(context.Background(), w, request, model.API{ID: "test"}); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(w.Body.String(), "host-session-ok") || !strings.Contains(w.Body.String(), `\"ok\":true`) {
		t.Fatal("host roundtrip failed", w.Body.String())
	}
}

func TestPluginNetworkPublicHTTPSAndRedirectBoundary(t *testing.T) {
	if os.Getenv("TEST_PLUGIN_NETWORK") != "1" {
		t.Skip("set TEST_PLUGIN_NETWORK=1 for live public HTTPS verification")
	}
	s, _ := testHostServices()
	p := defaultRuntimePolicy(model.PluginRuntimePolicy{Version: 1, NetworkEnabled: true, AllowedOrigins: []string{"https://example.com", "https://httpbin.org"}, TimeoutMS: 10000})
	s.setPolicy("fixture", "owner", p)
	response, e := s.httpRequest(context.Background(), "fixture", hostHTTPRequest{URL: "https://example.com/", Method: "GET"})
	if e != nil || response.Status != 200 || response.Body == "" {
		t.Fatal("public HTTPS failed", e, response.Status)
	}
	response, e = s.httpRequest(context.Background(), "fixture", hostHTTPRequest{URL: "https://httpbin.org/redirect-to?url=https%3A%2F%2F127.0.0.1%2Fmetadata", Method: "GET"})
	if e != nil || response.Status < 300 || response.Status >= 400 {
		t.Fatal("redirect was followed or not returned", e, response.Status)
	}
}

func TestPluginRuntimeDoesNotLetOldModuleAdoptAnotherInstalledIdentity(t *testing.T) {
	s, _ := testHostServices()
	p := defaultRuntimePolicy(model.PluginRuntimePolicy{Version: 1, SessionCache: true})
	s.setPolicy("same-name", "old-install", p)
	ctx := context.WithValue(context.Background(), hostIdentityKey{}, "old-install")
	if _, e := s.session(ctx, "same-name", "session_put", hostSessionRequest{Key: "test", Value: json.RawMessage(`{"v":1}`)}); e != nil {
		t.Fatal(e)
	}
	s.setPolicy("same-name", "new-install", p)
	if _, e := s.session(ctx, "same-name", "session_get", hostSessionRequest{Key: "test"}); e == nil {
		t.Fatal("old module adopted new installed identity")
	}
}
