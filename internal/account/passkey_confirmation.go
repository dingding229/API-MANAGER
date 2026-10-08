package account

import (
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func ioBody(raw []byte) io.ReadCloser { return io.NopCloser(bytes.NewReader(raw)) }
func validPasskeyTarget(method, path, digest string) bool {
	if method != "POST" && method != "PUT" && method != "PATCH" && method != "DELETE" {
		return false
	}
	u, e := url.ParseRequestURI(path)
	if e != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || len(path) > 2048 || strings.ContainsAny(path, "\r\n") {
		return false
	}
	if !(strings.HasPrefix(u.Path, "/admin/v1/") || strings.HasPrefix(u.Path, "/account/v1/") || u.Path == "/auth/v1/me") {
		return false
	}
	if strings.HasPrefix(u.Path, "/account/v1/passkeys/") && !strings.HasPrefix(u.Path, "/account/v1/passkeys/register/") && !(!strings.Contains(strings.TrimPrefix(u.Path, "/account/v1/passkeys/"), "/") && method == "DELETE") {
		return false
	}
	decoded, e := hex.DecodeString(digest)
	return e == nil && len(decoded) == 32 && digest == strings.ToLower(digest)
}
func (s *Service) savePasskeyConfirmation(r *http.Request, u model.User, state passkeyState, keyID string) (string, error) {
	state.Name = keyID
	state.Session.Expires = time.Now().Add(time.Minute)
	// 'confirmation' is not accepted as an assertion challenge; separate purpose
	// prevents exchanging one kind of verification for another.
	raw, e := json.Marshal(state)
	if e != nil {
		return "", e
	}
	encrypted, e := auth.EncryptSecret(s.key+":passkey", string(raw))
	if e != nil {
		return "", e
	}
	return s.putChallenge(r.Context(), "passkey-confirmation", u.ID, u.ID, auth.HashAPIKey(state.SessionHash), encrypted)
}
func (s *Service) ConfirmationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values("X-Passkey-Confirmation")
		if len(values) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		if len(values) != 1 || len(values[0]) > 160 || !auth.CookieMutationAllowed(r) {
			write(w, 403, map[string]string{"error": "请重新确认此操作"})
			return
		}
		token, _ := auth.SessionToken(r)
		u, e := s.users.ValidateSession(token)
		if e != nil {
			write(w, 401, nil)
			return
		}
		if s.accounts == nil {
			write(w, 503, nil)
			return
		}
		v, e := s.consume(r.Context(), values[0], "passkey-confirmation", auth.HashAPIKey(auth.HashAPIKey(token)))
		if e != nil || v.UserID != u.ID {
			write(w, 403, map[string]string{"error": "通行密钥确认已失效，请重新确认"})
			return
		}
		plain, e := auth.DecryptSecret(s.key+":passkey", v.Payload)
		var saved passkeyState
		if e != nil || json.Unmarshal([]byte(plain), &saved) != nil || saved.UserID != u.ID || saved.SessionHash != auth.HashAPIKey(token) || saved.Fingerprint != loginFingerprint(u) || saved.Session.Expires.Before(time.Now()) || saved.Method != r.Method || saved.Path != r.URL.RequestURI() {
			write(w, 403, map[string]string{"error": "此确认不适用于当前操作"})
			return
		}
		cfg, e := s.settings(r.Context())
		wa, waErr := s.passkeyClient(cfg)
		if e != nil || waErr != nil || wa.Config.RPOrigins[0] != saved.Origin {
			write(w, 403, nil)
			return
		}
		st, ok := s.store.(store.PasskeyStore)
		if !ok {
			write(w, 503, nil)
			return
		}
		record, e := st.Passkey(r.Context(), saved.Name, wa.Config.RPID)
		if e != nil || record.UserID != u.ID {
			write(w, 403, nil)
			return
		}
		raw, e := io.ReadAll(io.LimitReader(r.Body, (25<<20)+1))
		if e != nil || len(raw) > 25<<20 {
			write(w, 400, nil)
			return
		}
		digest := sha256.Sum256(raw)
		if saved.Digest != hex.EncodeToString(digest[:]) {
			write(w, 403, map[string]string{"error": "操作内容已变化，请重新确认"})
			return
		}
		r.Body = ioBody(raw)
		r.Header.Del("X-Passkey-Confirmation")
		// Downstream code must never accept headers or client JSON as this marker.
		r = r.WithContext(auth.WithPasskeyConfirmation(r.Context(), u.ID))
		next.ServeHTTP(w, r)
	})
}
