package apitest

import (
	"api-manager/internal/auth"
	"api-manager/internal/gateway"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/ratelimit"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var testingDevices sync.Map

func fixture(t *testing.T) (*Handler, *store.Memory, *user.Service, string, Request, *gateway.Gateway) {
	t.Helper()
	m := store.NewMemory()
	u := user.NewService(m)
	_ = u.EnsureInitialAdmin("admin", "a-long-initial-password")
	r := req("/test/v1/login", "", input{})
	_, token, err := u.AuthenticateRequest("admin", "a-long-initial-password", r)
	if err != nil {
		t.Fatal(err)
	}
	testingDevices.Store(token, auth.NewDeviceToken(r))
	n := time.Now()
	a := model.API{ID: "fixture", Name: "test", Method: "GET", Path: "/api/demo", AuthMode: "api_key", PublicVisible: true, PublicTestEnabled: true, Enabled: true, PublishedAt: &n, UpdatedAt: n, ResponseStatus: 200, ResponseBody: `{"ok":true}`}
	_ = m.CreateAPI(a)
	g := gateway.New(m, plugin.NewRegistry(), ratelimit.NewMemory(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := New(m, u, g, ratelimit.NewMemory(), nil)
	sum := sha256.Sum256([]byte(a.Path))
	payload := Request{APIID: hex.EncodeToString(sum[:8]), Method: "GET", Path: a.Path, Headers: map[string]string{}}
	return h, m, u, token, payload, g
}
func req(path, token string, input input) *http.Request {
	body, _ := json.Marshal(input)
	r := httptest.NewRequest("POST", "https://site.example"+path, strings.NewReader(string(body)))
	r.Header.Set("Origin", "https://site.example")
	r.Header.Set("X-API-Request", "1")
	r.Header.Set("User-Agent", "TestBrowser/1.0")
	r.RemoteAddr = "192.0.2.1:1111"
	if token != "" {
		r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
		if device, ok := testingDevices.Load(token); ok {
			r.AddCookie(&http.Cookie{Name: auth.DeviceCookie, Value: device.(string)})
		}
	}
	return r
}
func prepare(t *testing.T, h *Handler, token string, payload Request) string {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("/test/v1/prepare", token, input{Request: payload}))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var data map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &data)
	return data["ticket"].(string)
}
func TestNoKEYTestingRequiresOneUseSessionBoundAuthorization(t *testing.T) {
	h, _, _, token, payload, g := fixture(t)
	key := prepare(t, h, token, payload)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("/test/v1/invoke", token, input{Request: payload, Ticket: key}))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `\"ok\":true`) {
		t.Fatal(w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req("/test/v1/invoke", token, input{Request: payload, Ticket: key}))
	if w.Code != 403 {
		t.Fatal("ticket replayed", w.Code)
	}
	r := httptest.NewRequest("GET", "/api/demo", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("cookie accepted as API KEY", w.Code)
	}
}
func TestTicketBlocksDifferentSessionPathIPAndDeviceAndClosedAPI(t *testing.T) {
	for _, kind := range []string{"cookie_only", "different_session", "changed_body", "ip", "ua", "hidden", "unpublished", "test_disabled", "changed_config", "revoked"} {
		h, m, u, token, payload, _ := fixture(t)
		ticket := prepare(t, h, token, payload)
		request := req("/test/v1/invoke", token, input{Request: payload, Ticket: ticket})
		want := 403
		switch kind {
		case "cookie_only":
			request.Header.Del("Origin")
			request.Header.Del("X-API-Request")
		case "different_session":
			_, other, _ := u.AuthenticateRequest("admin", "a-long-initial-password", request)
			request.Header.Set("Cookie", auth.SessionCookie+"="+other)
		case "changed_body":
			payload.Path = "/api/demo?other=1"
			request = req("/test/v1/invoke", token, input{Request: payload, Ticket: ticket})
		case "ip":
			request.RemoteAddr = "192.0.2.2:1111"
		case "ua":
			request.Header.Set("User-Agent", "OtherBrowser")
		case "hidden", "unpublished", "test_disabled", "changed_config":
			a, _ := m.GetAPI("fixture")
			if kind == "hidden" {
				a.PublicVisible = false
			}
			if kind == "unpublished" {
				a.PublishedAt = nil
			}
			if kind == "test_disabled" {
				a.PublicVisible = false
			}
			if kind == "changed_config" {
				a.UpdatedAt = time.Now().Add(time.Second)
			}
			_ = m.UpdateAPI(a)
		case "revoked":
			_ = u.Logout(token)
			want = 401
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request)
		if w.Code != want {
			t.Fatalf("%s %d %s", kind, w.Code, w.Body)
		}
	}
}
func TestPrepareRejectsAdminURLsAndInjectedCredentials(t *testing.T) {
	h, _, _, token, payload, _ := fixture(t)
	for _, path := range []string{"/admin/v1/users", "https://attacker.invalid/api/demo", "//attacker.invalid/api/demo", "/api/demo/../../admin/v1/users"} {
		payload.Path = path
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req("/test/v1/prepare", token, input{Request: payload}))
		if w.Code != 403 {
			t.Fatal(path, w.Code)
		}
	}
	payload.Path = "/api/demo"
	payload.Headers["X-API-Key"] = "attempt-to-inject"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("/test/v1/prepare", token, input{Request: payload}))
	if w.Code != 400 {
		t.Fatal("KEY injection allowed", w.Code)
	}
}
func TestResponseCaptureIsBounded(t *testing.T) {
	b := &bounded{headers: http.Header{}}
	n, _ := b.Write([]byte(strings.Repeat("x", 200<<10)))
	if n != 200<<10 || b.body.Len() != 128<<10 || !b.truncated {
		t.Fatal("response bound", n, b.body.Len())
	}
}

func TestTestPermissionCoversDisplayedMethodsAndExpiredTicketIsRejected(t *testing.T) {
	h, m, u, _, payload, _ := fixture(t)
	_, err := u.Create("readonly", "ViewerPass888", "member")
	if err != nil {
		t.Fatal(err)
	}
	r := req("/test/v1/login", "", input{})
	_, token, err := u.AuthenticateRequest("readonly", "ViewerPass888", r)
	if err != nil {
		t.Fatal(err)
	}
	testingDevices.Store(token, auth.NewDeviceToken(r))
	a, _ := m.GetAPI("fixture")
	a.Methods = []string{"GET", "POST"}
	_ = m.UpdateAPI(a)
	payload.Method = "POST"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("/test/v1/prepare", token, input{Request: payload}))
	if w.Code != 200 {
		t.Fatal("visible POST method unavailable to testing user", w.Code)
	}
	payload.Method = "GET"
	ticket := prepare(t, h, token, payload)
	active, _ := u.CurrentSession(token)
	_ = m.PutTestTicket(r.Context(), model.APITestTicket{Hash: auth.HashAPIKey(ticket), SessionHash: active.Hash, APIID: a.ID, Digest: func() string {
		raw, _ := json.Marshal(struct {
			Request   Request
			UpdatedAt time.Time
		}{payload, a.UpdatedAt})
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}(), ClientIP: active.LoginIP, UserAgent: active.UserAgent, ExpiresAt: time.Now().Add(-time.Second)})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req("/test/v1/invoke", token, input{Request: payload, Ticket: ticket}))
	if w.Code != 403 {
		t.Fatal("expired grant used", w.Code)
	}
}

func TestOnlineTestUsesBoundDeviceRatherThanChangingLoginIP(t *testing.T) {
	h, _, _, token, payload, _ := fixture(t)
	r := req("/test/v1/prepare", token, input{Request: payload})
	r.RemoteAddr = "198.51.100.99:4242"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("same bound browser rejected after source IP changed", w.Code, w.Body.String())
	}
	r = req("/test/v1/prepare", token, input{Request: payload})
	r.Header.Set("Cookie", auth.SessionCookie+"="+token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("session cookie alone bypassed device binding", w.Code)
	}
}
func TestDeviceCookieNeverReachesBusinessPlugins(t *testing.T) {
	r := httptest.NewRequest("GET", "https://example.test/api/demo", nil)
	r.AddCookie(&http.Cookie{Name: auth.DeviceCookie, Value: "private-device-token"})
	r.AddCookie(&http.Cookie{Name: "business", Value: "ok"})
	auth.StripUserSessionCookies(r)
	if strings.Contains(r.Header.Get("Cookie"), "private-device-token") || !strings.Contains(r.Header.Get("Cookie"), "business=ok") {
		t.Fatal("device cookie leaked or business cookie lost")
	}
}

func TestLegacyBrowserSessionCanBindDeviceOnceFromItsExistingTrustedSource(t *testing.T) {
	h, m, u, token, payload, _ := fixture(t)
	session, e := u.CurrentSession(token)
	if e != nil {
		t.Fatal(e)
	}
	session.DeviceBindingHash = ""
	session.LoginIP = "198.51.100.40"
	if e = m.CreateSession(session); e != nil {
		t.Fatal(e)
	}
	r := req("/test/v1/prepare", token, input{Request: payload})
	r.Header.Set("Cookie", auth.SessionCookie+"="+token)
	r.RemoteAddr = "198.51.100.40:4242"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("legacy browser could not bind device", w.Code, w.Body.String())
	}
	bound, _ := u.CurrentSession(token)
	if bound.DeviceBindingHash == "" {
		t.Fatal("device binding not saved")
	}
	found := false
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.DeviceCookie {
			found = true
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
				t.Fatal("unsafe device cookie")
			}
		}
	}
	if !found {
		t.Fatal("device cookie not issued")
	}
	r = req("/test/v1/prepare", token, input{Request: payload})
	r.Header.Set("Cookie", auth.SessionCookie+"="+token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("legacy device binding could be replaced by cookie alone")
	}
}
