package account

import (
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

// Tests exercise real signed WebAuthn ceremonies; no assertion bypass is used.
type passkeyTestStore struct {
	*store.Memory
	store.AccountStore
	mu            sync.Mutex
	verifications map[string]model.Verification
	keys          map[string]model.Passkey
	cfg           model.SecuritySettings
}

func (p *passkeyTestStore) Account(context.Context, string) (model.AccountRecord, error) {
	return model.AccountRecord{}, nil
}
func (p *passkeyTestStore) SecuritySettings(context.Context) (model.SecuritySettings, string, error) {
	return p.cfg, "", nil
}
func (p *passkeyTestStore) PutVerification(_ context.Context, v model.Verification) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.verifications[v.ID] = v
	return nil
}
func (p *passkeyTestStore) ConsumeVerification(_ context.Context, id, purpose, hash, binding string) (model.Verification, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.verifications[id]
	if !ok || v.Purpose != purpose || v.CodeHash != hash || v.Binding != binding || v.ExpiresAt.Before(time.Now()) {
		return v, store.ErrConflict
	}
	delete(p.verifications, id)
	return v, nil
}
func (p *passkeyTestStore) Passkeys(_ context.Context, id, rp string) (out []model.Passkey, e error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, v := range p.keys {
		if v.UserID == id && v.RPID == rp {
			out = append(out, v)
		}
	}
	return
}
func (p *passkeyTestStore) Passkey(_ context.Context, id, rp string) (v model.Passkey, e error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.keys[id]
	if !ok || v.RPID != rp {
		return v, store.ErrNotFound
	}
	return v, nil
}
func (p *passkeyTestStore) AddPasskey(_ context.Context, v model.Passkey, revision int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.keys[v.ID]; ok {
		return store.ErrConflict
	}
	v.Revision = 1
	p.keys[v.ID] = v
	return nil
}
func (p *passkeyTestStore) UsePasskey(_ context.Context, v model.Passkey, revision int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	old, ok := p.keys[v.ID]
	if !ok || old.Revision != v.Revision {
		return store.ErrConflict
	}
	v.Revision++
	p.keys[v.ID] = v
	return nil
}
func (p *passkeyTestStore) DeletePasskey(_ context.Context, uid, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.keys[id]
	if !ok || v.UserID != uid {
		return store.ErrNotFound
	}
	delete(p.keys, id)
	return nil
}
func passkeyTestService(t *testing.T) (*Service, *passkeyTestStore, model.User, string) {
	t.Helper()
	st := &passkeyTestStore{Memory: store.NewMemory(), keys: map[string]model.Passkey{}, verifications: map[string]model.Verification{}, cfg: model.SecuritySettings{WebsiteURL: "https://example.test"}}
	us := user.NewService(st)
	u, e := us.Create("passkeyuser", "Password888", "member")
	if e != nil {
		t.Fatal(e)
	}
	_, token, e := us.Authenticate(u.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	s := New(st, us, "0123456789abcdefghijklmnopqrstuvwxyz", &fakeMail{}, true)
	return s, st, u, token
}
func passkeyCall(t *testing.T, s *Service, path, token string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "https://example.test/account/v1/passkeys/"+path, bytes.NewReader(raw))
	r.Header.Set("Origin", "https://example.test")
	r.Header.Set("X-API-Request", "1")
	r.Header.Set("User-Agent", "passkey-test")
	if token != "" {
		r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func passkeyBegin(t *testing.T, w *httptest.ResponseRecorder) (string, map[string]any, *http.Cookie) {
	t.Helper()
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	return v["challenge_id"].(string), v["options"].(map[string]any)["publicKey"].(map[string]any), w.Result().Cookies()[0]
}
func testAuthenticator(t *testing.T, private *ecdsa.PrivateKey, id []byte, challenge, origin, rp string, create bool, handle []byte, count uint32, uv bool) map[string]any {
	t.Helper()
	kind := "webauthn.get"
	if create {
		kind = "webauthn.create"
	}
	client, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": origin, "crossOrigin": false})
	rpHash := sha256.Sum256([]byte(rp))
	authData := append([]byte{}, rpHash[:]...)
	flags := byte(1)
	if uv {
		flags |= 4
	}
	if create {
		flags |= 64
	}
	authData = append(authData, flags)
	authData = binary.BigEndian.AppendUint32(authData, count)
	response := map[string]any{"clientDataJSON": base64.RawURLEncoding.EncodeToString(client)}
	if create {
		authData = append(authData, make([]byte, 16)...)
		authData = binary.BigEndian.AppendUint16(authData, uint16(len(id)))
		authData = append(authData, id...)
		key, e := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: private.X.FillBytes(make([]byte, 32)), -3: private.Y.FillBytes(make([]byte, 32))})
		if e != nil {
			t.Fatal(e)
		}
		authData = append(authData, key...)
		attestation, e := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": authData})
		if e != nil {
			t.Fatal(e)
		}
		response["attestationObject"] = base64.RawURLEncoding.EncodeToString(attestation)
	} else {
		h := sha256.Sum256(client)
		message := append(append([]byte{}, authData...), h[:]...)
		digest := sha256.Sum256(message)
		signature, e := ecdsa.SignASN1(rand.Reader, private, digest[:])
		if e != nil {
			t.Fatal(e)
		}
		response["authenticatorData"] = base64.RawURLEncoding.EncodeToString(authData)
		response["signature"] = base64.RawURLEncoding.EncodeToString(signature)
		response["userHandle"] = base64.RawURLEncoding.EncodeToString(handle)
	}
	return map[string]any{"id": base64.RawURLEncoding.EncodeToString(id), "rawId": base64.RawURLEncoding.EncodeToString(id), "type": "public-key", "response": response, "clientExtensionResults": map[string]any{}}
}
func TestPasskeyRegistrationLoginAndOperationBoundConfirmation(t *testing.T) {
	s, _, u, token := passkeyTestService(t)
	passkeyCeremonyFlow(t, s, u, token)
}
func TestPostgresPasskeyRegistrationLoginAndOperationBoundConfirmation(t *testing.T) {
	dsn := os.Getenv("TEST_ACCOUNT_DSN")
	if dsn == "" {
		t.Skip("isolated database required")
	}
	if !strings.Contains(dsn, "/api_account_review_") {
		t.Fatal("disposable database required")
	}
	st, e := store.NewPostgres(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	us := user.NewService(st)
	u, e := us.Create("pkey"+randomHex(4), "Password888", "member")
	if e != nil {
		t.Fatal(e)
	}
	_, token, e := us.Authenticate(u.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	cfg, _, e := st.SecuritySettings(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	cfg.WebsiteURL = "https://example.test"
	if e = st.SaveSecuritySettings(context.Background(), cfg, ""); e != nil {
		t.Fatal(e)
	}
	s := New(st, us, "0123456789abcdefghijklmnopqrstuvwxyz", &fakeMail{}, true)
	passkeyCeremonyFlow(t, s, u, token)
}
func passkeyCeremonyFlow(t *testing.T, s *Service, u model.User, token string) {
	t.Helper()
	private, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := []byte("unique-authenticator-credential-01")
	start := passkeyCall(t, s, "register/begin", token, map[string]any{"name": "我的手机", "current_password": "Password888"}, nil)
	challenge, opts, cookie := passkeyBegin(t, start)
	credential := testAuthenticator(t, private, id, opts["challenge"].(string), "https://example.test", "example.test", true, []byte(u.ID), 0, true)
	finish := map[string]any{"challenge_id": challenge, "credential": credential}
	w := passkeyCall(t, s, "register/finish", token, finish, cookie)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	persisted, e := s.store.(store.PasskeyStore).Passkeys(context.Background(), u.ID, "example.test")
	if e != nil || len(persisted) != 1 {
		t.Fatal("credential not persisted", e)
	}
	if replay := passkeyCall(t, s, "register/finish", token, finish, cookie); replay.Code != 403 {
		t.Fatal("registration replay accepted")
	}
	// A discoverable signed assertion creates the ordinary shared user session.
	start = passkeyCall(t, s, "login/begin", "", map[string]any{}, nil)
	challenge, opts, cookie = passkeyBegin(t, start)
	credential = testAuthenticator(t, private, id, opts["challenge"].(string), "https://example.test", "example.test", false, []byte(u.ID), 1, true)
	w = passkeyCall(t, s, "login/finish", "", map[string]any{"challenge_id": challenge, "credential": credential}, cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	found := false
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			logged, e := s.users.ValidateSession(c.Value)
			if e != nil || logged.ID != u.ID {
				t.Fatal("wrong login user")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing ordinary session")
	}
	operation := []byte(`{"current_password":"","value":"chosen-value"}`)
	digest := sha256.Sum256(operation)
	path := "/admin/v1/database/reveal"
	start = passkeyCall(t, s, "confirm/begin", token, map[string]any{"method": "POST", "path": path, "digest": hex.EncodeToString(digest[:])}, nil)
	challenge, opts, cookie = passkeyBegin(t, start)
	credential = testAuthenticator(t, private, id, opts["challenge"].(string), "https://example.test", "example.test", false, []byte(u.ID), 2, true)
	w = passkeyCall(t, s, "confirm/finish", token, map[string]any{"challenge_id": challenge, "credential": credential}, cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result map[string]string
	json.Unmarshal(w.Body.Bytes(), &result)
	called := 0
	handler := s.ConfirmationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if !auth.PasskeyConfirmed(r.Context(), u.ID) {
			t.Fatal("confirmation context absent")
		}
		if e := s.ReauthenticateAdmin(r, "", ""); e != nil {
			t.Fatal("passkey did not replace password", e)
		}
		w.WriteHeader(204)
	}))
	request := func() *http.Request {
		r := httptest.NewRequest("POST", "https://example.test"+path, bytes.NewReader(operation))
		r.Header.Set("Origin", "https://example.test")
		r.Header.Set("X-API-Request", "1")
		r.Header.Set("X-Passkey-Confirmation", result["confirmation"])
		r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
		return r
	}
	done := httptest.NewRecorder()
	handler.ServeHTTP(done, request())
	if done.Code != 204 || called != 1 {
		t.Fatal("confirmation denied", done.Code)
	}
	replay := httptest.NewRecorder()
	handler.ServeHTTP(replay, request())
	if replay.Code != 403 || called != 1 {
		t.Fatal("confirmation replay reached protected handler")
	}
}
func TestPasskeyRejectsForgedOriginMissingUVAndCrossAccount(t *testing.T) {
	for _, test := range []struct {
		name, origin string
		uv           bool
	}{{"wrong-origin", "https://attacker.test", true}, {"missing-uv", "https://example.test", false}} {
		t.Run(test.name, func(t *testing.T) {
			s, st, _, token := passkeyTestService(t)
			private, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			challenge, opts, cookie := passkeyBegin(t, passkeyCall(t, s, "register/begin", token, map[string]string{"name": "test", "current_password": "Password888"}, nil))
			credential := testAuthenticator(t, private, []byte("testcredential"), opts["challenge"].(string), test.origin, "example.test", true, nil, 0, test.uv)
			w := passkeyCall(t, s, "register/finish", token, map[string]any{"challenge_id": challenge, "credential": credential}, cookie)
			if w.Code != 403 || len(st.keys) != 0 {
				t.Fatal("unverified credential persisted", w.Code)
			}
		})
	}
	s, _, _, token := passkeyTestService(t)
	other, e := s.users.Create("otheruser", "Password888", "member")
	if e != nil {
		t.Fatal(e)
	}
	_, otherToken, _ := s.users.Authenticate(other.Username, "Password888")
	challenge, _, cookie := passkeyBegin(t, passkeyCall(t, s, "register/begin", token, map[string]string{"name": "test", "current_password": "Password888"}, nil))
	w := passkeyCall(t, s, "register/finish", otherToken, map[string]any{"challenge_id": challenge, "credential": map[string]any{}}, cookie)
	if w.Code != 403 {
		t.Fatal("cross-account challenge accepted")
	}
}
func TestPasskeyConfirmationCannotChangeBodyPathSessionOrCredentials(t *testing.T) {
	for _, kind := range []string{"body", "path", "session", "expired", "deleted-key", "auth-revision"} {
		t.Run(kind, func(t *testing.T) {
			s, st, u, token := passkeyTestService(t)
			st.keys["key"] = model.Passkey{ID: "key", UserID: u.ID, RPID: "example.test"}
			body := []byte(`{"value":1}`)
			digest := sha256.Sum256(body)
			saved := passkeyState{UserID: u.ID, SessionHash: auth.HashAPIKey(token), Fingerprint: loginFingerprint(u), Origin: "https://example.test", Method: "POST", Path: "/admin/v1/site", Digest: hex.EncodeToString(digest[:])}
			proof, e := s.savePasskeyConfirmation(httptest.NewRequest("POST", "https://example.test", nil), u, saved, "key")
			if e != nil {
				t.Fatal(e)
			}
			r := httptest.NewRequest("POST", "https://example.test/admin/v1/site", bytes.NewReader(body))
			r.Header.Set("Origin", "https://example.test")
			r.Header.Set("X-API-Request", "1")
			r.Header.Set("X-Passkey-Confirmation", proof)
			r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
			switch kind {
			case "body":
				r.Body = ioBody([]byte(`{"value":2}`))
			case "path":
				r.URL.Path = "/admin/v1/database/reveal"
			case "session":
				r.Header.Set("Cookie", auth.SessionCookie+"=not-the-session")
			case "expired":
				for id, v := range st.verifications {
					v.ExpiresAt = time.Now().Add(-time.Minute)
					st.verifications[id] = v
				}
			case "deleted-key":
				delete(st.keys, "key")
			case "auth-revision":
				fresh := "NewPassword888"
				_, _, e = s.users.UpdateProfile(u.ID, u.ID, model.UpdateUserProfileRequest{Password: &fresh, CurrentPassword: "Password888"})
				if e != nil {
					t.Fatal(e)
				}
			}
			called := false
			w := httptest.NewRecorder()
			s.ConfirmationMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(w, r)
			if called || w.Code < 400 {
				t.Fatal("changed confirmation reached handler", w.Code)
			}
		})
	}
}
func TestPasskeyOriginAndTargetValidation(t *testing.T) {
	s, _, _, _ := passkeyTestService(t)
	for _, origin := range []string{"", "http://example.test", "https://user:pass@example.test", "https://example.test/path", "https://example.test?", "https://example.test#x"} {
		if _, e := s.passkeyClient(model.SecuritySettings{WebsiteURL: origin}); e == nil {
			t.Fatal("unsafe RP origin accepted", origin)
		}
	}
	for _, path := range []string{"https://attacker.test/admin/v1/site", "//attacker.test/admin/v1/site", "/api/business", "/account/v1/passkeys/login/begin"} {
		if validPasskeyTarget("POST", path, strings.Repeat("a", 64)) {
			t.Fatal("unsafe target accepted", path)
		}
	}
}
