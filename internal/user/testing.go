package user

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Both UIs use one host-only HttpOnly session. Business KEY authentication never
// reads cookies; public DTOs still contain no management profile or credentials.
const TestingCookie = "api_manager_test_session"

func testingSecure(r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	return r.TLS != nil || (err == nil && origin.Scheme == "https" && strings.EqualFold(origin.Host, r.Host))
}
func setTestingCookie(w http.ResponseWriter, r *http.Request, token string, production bool) {
	clearLegacyTestingCookie(w, r, production)
	// #nosec G124 -- Secure is always true in production; plaintext cookies are permitted only for explicitly non-production HTTP previews. HttpOnly and SameSiteStrict are mandatory.
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: production || testingSecure(r), SameSite: http.SameSiteStrictMode})
}
func clearTestingCookie(w http.ResponseWriter, r *http.Request, production bool) {
	clearLegacyTestingCookie(w, r, production)
	// #nosec G124 -- Secure is always true in production; plaintext cookies are permitted only for explicitly non-production HTTP previews. HttpOnly and SameSiteStrict are mandatory.
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: production || testingSecure(r), SameSite: http.SameSiteStrictMode})
}
func testingToken(r *http.Request) string {
	if _, cookieAuth := auth.SessionToken(r); cookieAuth {
		token, _ := auth.SessionToken(r)
		return token
	}
	if len(r.CookiesNamed(auth.SessionCookie)) > 0 || len(r.Header.Values("Authorization")) > 0 || len(r.Header.Values("X-API-Key")) > 0 {
		return ""
	}
	cookies := r.CookiesNamed(TestingCookie)
	if len(cookies) != 1 {
		return ""
	}
	return cookies[0].Value
}
func testingSameOrigin(r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	return auth.CookieMutationAllowed(r) || (err == nil && (origin.Scheme == "http" || origin.Scheme == "https") && origin.User == nil && origin.RawQuery == "" && origin.Fragment == "" && origin.Path == "" && strings.EqualFold(origin.Host, r.Host) && (r.TLS == nil || origin.Scheme == "https") && r.Header.Get("X-API-Test") == "1" && (r.Header.Get("Sec-Fetch-Site") == "" || r.Header.Get("Sec-Fetch-Site") == "same-origin"))
}
func (h *HTTP) testing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodGet && !testingSameOrigin(r) {
		writeJSON(w, 403, map[string]string{"error": "请在本站完成登录操作。"})
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/test/v1/session":
		u, err := h.service.ValidateSession(testingToken(r))
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": "请先登录。"})
			return
		}
		if len(r.CookiesNamed(auth.SessionCookie)) == 0 {
			setTestingCookie(w, r, testingToken(r), h.production)
		}
		h.service.TouchSession(testingToken(r), r)
		writeJSON(w, 200, map[string]any{"username": u.Username, "can_test": h.service.Can(u.ID, "api.test"), "can_test_write": (h.service.Can(u.ID, "api.test.write") || h.service.Can(u.ID, "api.write"))})
	case r.Method == http.MethodPost && r.URL.Path == "/test/v1/login":
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&req) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			writeJSON(w, 400, map[string]string{"error": "请检查用户名和密码。"})
			return
		}
		u, token, err := h.service.AuthenticateRequest(req.Username, req.Password, r)
		if err != nil {
			status := 503
			message := "登录暂时不可用。"
			if errors.Is(err, ErrInvalidCredentials) {
				status = 401
				message = "用户名或密码不正确。"
			}
			h.service.RecordAudit(audit.Actor{Type: "anonymous"}, r, "auth.login.denied", "user", "", status, nil)
			writeJSON(w, status, map[string]string{"error": message})
			return
		}
		// Revoke only the previous testing session on a successful account switch.
		if previous := testingToken(r); previous != "" {
			_ = h.service.Logout(previous)
		}
		setTestingCookie(w, r, token, h.production)
		h.service.RecordAudit(audit.Actor{ID: u.ID, Type: "user", Email: u.Username}, r, "auth.login", "user", u.ID, 200, nil)
		writeJSON(w, 200, map[string]string{"username": u.Username})
	case r.Method == http.MethodPost && r.URL.Path == "/test/v1/logout":
		if token := testingToken(r); token != "" {
			if err := h.service.Logout(token); err != nil {
				writeJSON(w, 503, map[string]string{"error": "退出暂时不可用。"})
				return
			}
		}
		clearTestingCookie(w, r, h.production)
		w.WriteHeader(204)
	default:
		writeJSON(w, 404, map[string]string{"error": "not found"})
	}
}

func clearLegacyTestingCookie(w http.ResponseWriter, r *http.Request, production bool) {
	// #nosec G124 -- Secure is mandatory in production and TLS; non-production HTTP tests are the only exception.
	http.SetCookie(w, &http.Cookie{Name: TestingCookie, Path: "/test/v1", MaxAge: -1, HttpOnly: true, Secure: production || testingSecure(r), SameSite: http.SameSiteStrictMode})
}

func SetSessionCookie(w http.ResponseWriter, r *http.Request, token string, production bool) {
	setTestingCookie(w, r, token, production)
}
func ClearSessionCookie(w http.ResponseWriter, r *http.Request, production bool) {
	clearTestingCookie(w, r, production)
}
