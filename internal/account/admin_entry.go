package account

import (
	"api-manager/internal/auth"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"net/http"
	"strings"
	"time"
)

const adminEntryCookie = "api_manager_admin_entry"

func (s *Service) canEnterAdministration(id string) bool {
	for _, permission := range s.store.GetUserPermissions(id) {
		if permission == "*" || (permission != "api.test" && !strings.HasPrefix(permission, "account.")) {
			return true
		}
	}
	return false
}

func (s *Service) createAdminEntry(w http.ResponseWriter, r *http.Request, u model.User) {
	token, cookieSession := auth.SessionToken(r)
	if !cookieSession || !auth.CookieMutationAllowed(r) || !s.canEnterAdministration(u.ID) {
		write(w, http.StatusForbidden, map[string]string{"error": "没有访问管理后台的权限"})
		return
	}
	secret := randomHex(32)
	if secret == "" || s.accounts == nil {
		write(w, 503, nil)
		return
	}
	id := ids.NewUUID()
	expires := time.Now().Add(time.Minute)
	// Bind the one-use entry to the authenticated session, not merely to a user ID.
	if err := s.accounts.PutVerification(r.Context(), model.Verification{ID: id, Purpose: "admin-entry", Subject: u.ID, UserID: u.ID, Binding: auth.HashAPIKey(token), CodeHash: auth.HashAPIKey(secret), ExpiresAt: expires}); err != nil {
		write(w, 503, nil)
		return
	}
	path := s.adminPath
	if path == "" {
		path = "/admin"
	}
	// #nosec G124 -- Production always forces Secure. Only explicit non-production previews permit HTTP; this 60-second proof is session-bound, HttpOnly, SameSite=Strict and single-use.
	http.SetCookie(w, &http.Cookie{Name: adminEntryCookie, Value: id + ":" + secret, Path: path + "/", Expires: expires, MaxAge: 60, HttpOnly: true, Secure: s.production || r.TLS != nil, SameSite: http.SameSiteStrictMode})
	write(w, 200, map[string]string{"url": path + "/"})
}

// ProtectConsole requires a fresh, session-bound entry issued by the user center.
// Assets contain no private data; administrative APIs enforce RBAC independently.
func (s *Service) ProtectConsole(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := s.adminPath
		if base == "" {
			base = "/admin"
		}
		isDocument := r.URL.Path == base || r.URL.Path == base+"/" || r.URL.Path == base+"/index.html"
		if !isDocument {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		// Preserve the one-time initial administrator registration, never a second login.
		if available, err := s.users.SetupAvailable(); err == nil && available {
			next.ServeHTTP(w, r)
			return
		}
		token, cookieSession := auth.SessionToken(r)
		u, err := s.users.ValidateSession(token)
		destination := "/account"
		if err != nil || !cookieSession {
			destination = "/login?return=admin"
		}
		if err != nil || !cookieSession || !s.canEnterAdministration(u.ID) || r.Method != http.MethodGet || s.accounts == nil {
			http.Redirect(w, r, destination, http.StatusSeeOther)
			return
		}
		cookies := r.CookiesNamed(adminEntryCookie)
		if len(cookies) != 1 {
			http.Redirect(w, r, "/account", http.StatusSeeOther)
			return
		}
		// #nosec G124 -- Deletes the short-lived entry cookie with the same flags; Secure is always true in production.
		http.SetCookie(w, &http.Cookie{Name: adminEntryCookie, Path: base + "/", MaxAge: -1, HttpOnly: true, Secure: s.production || r.TLS != nil, SameSite: http.SameSiteStrictMode})
		proof, err := s.consume(r.Context(), cookies[0].Value, "admin-entry", auth.HashAPIKey(token))
		if err != nil || proof.UserID != u.ID {
			http.Redirect(w, r, "/account", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}
