// Package grafanaproxy exposes only the loopback Grafana through an authenticated
// administration subpath. Business API keys and anonymous users cannot enter it.
package grafanaproxy

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"api-manager/internal/model"
)

const cookieName = "api_manager_grafana"

type Users interface {
	ValidateSession(string) (model.User, error)
	Can(string, string) bool
}
type Proxy struct {
	users         Users
	prefix        string
	enabled       bool
	secureCookies bool
	upstream      *httputil.ReverseProxy
}

func New(users Users, adminPath string, enabled bool, logger *slog.Logger) *Proxy {
	p := &Proxy{users: users, prefix: adminPath + "/grafana/", enabled: enabled}
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:3000"}
	p.upstream = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			// Client supplied cookies and auth-proxy identities are never trusted.
			r.Out.Header.Del("Cookie")
			r.Out.Header.Del("Authorization")
			for name := range r.Out.Header {
				if strings.HasPrefix(strings.ToLower(name), "x-webauth-") {
					r.Out.Header.Del(name)
				}
			}
			identity := r.In.Context().Value(identityKey{}).(model.User)
			r.Out.Header.Set("X-WEBAUTH-USER", "manager-"+identity.ID)
			r.Out.Header.Set("X-WEBAUTH-NAME", identity.Email)
			role := "Viewer"
			if p.users.Can(identity.ID, "*") {
				role = "Admin"
			} else if p.users.Can(identity.ID, "observability.manage") {
				role = "Editor"
			}
			r.Out.Header.Set("X-WEBAUTH-ROLE", role)
			r.Out.Header.Set("X-Forwarded-Host", r.In.Host)
			proto := "http"
			if r.In.TLS != nil || p.secureCookies {
				proto = "https"
			}
			r.Out.Header.Set("X-Forwarded-Proto", proto)
		},
		ModifyResponse: func(r *http.Response) error {
			// Never allow Grafana to create an independent browser session: every
			// subsequent request must revalidate the API Manager account and permissions.
			r.Header.Del("Set-Cookie")
			r.Header.Set("X-Frame-Options", "SAMEORIGIN")
			r.Header.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; font-src 'self' data:; worker-src 'self' blob:; object-src 'none'; base-uri 'self'; frame-ancestors 'self'; form-action 'self'")
			r.Header.Set("Cache-Control", "no-store")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Warn("Grafana unavailable", "error", err)
			fail(w, 503, "Grafana is starting or unavailable")
		},
	}
	return p
}

func (p *Proxy) SetSecureCookies(enabled bool) { p.secureCookies = enabled }

type identityKey struct{}

func withIdentity(ctx context.Context, u model.User) context.Context {
	return context.WithValue(ctx, identityKey{}, u)
}
func (p *Proxy) Grant(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		fail(w, 405, "method not allowed")
		return
	}
	if !sameOrigin(r) {
		fail(w, 403, "cross-origin request denied")
		return
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || r.Header.Get("X-API-Key") != "" {
		fail(w, 401, "login required")
		return
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	user, err := p.users.ValidateSession(token)
	if err != nil {
		fail(w, 401, "login required")
		return
	}
	if !p.users.Can(user.ID, "observability.read") {
		fail(w, 403, "observability.read permission required")
		return
	}
	if !p.enabled {
		fail(w, 503, "enable OBSERVABILITY_STACK_ENABLED to use Grafana")
		return
	}
	// #nosec G124 -- Secure is enabled for TLS or the configured HTTPS origin; explicit HTTP deployments still require HttpOnly + SameSite=Strict.
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: p.prefix, HttpOnly: true, Secure: r.TLS != nil || p.secureCookies, SameSite: http.SameSiteStrictMode, MaxAge: 900, Expires: time.Now().Add(15 * time.Minute)})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !strings.HasPrefix(r.URL.Path, p.prefix) {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) {
		fail(w, 403, "cross-origin request denied")
		return
	}
	cookies := r.CookiesNamed(cookieName)
	if len(cookies) != 1 {
		fail(w, 401, "login required")
		return
	}
	user, err := p.users.ValidateSession(cookies[0].Value)
	if err != nil {
		fail(w, 401, "login required")
		return
	}
	if !p.users.Can(user.ID, "observability.read") {
		fail(w, 403, "observability.read permission required")
		return
	}
	if !p.enabled {
		fail(w, 503, "Grafana is disabled")
		return
	}
	// ReverseProxy adds response headers rather than replacing existing values.
	// Remove the outer DENY/CSP only for authorized Grafana responses so the
	// single SAMEORIGIN/frame-ancestors policy returned upstream takes effect.
	w.Header().Del("X-Frame-Options")
	w.Header().Del("Content-Security-Policy")
	p.upstream.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), user)))
}
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.User != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
			return false
		}
	} else if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
		// The iframe uses this header on Grafana XHRs; form submissions without an
		// Origin or a trusted same-origin fetch context are not accepted.
		if r.Header.Get("Sec-Fetch-Site") != "same-origin" {
			return false
		}
	}
	return true
}
func fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
