package auth

import (
	"net/http"
	"net/url"
	"strings"
)

const SessionCookie = "api_manager_session"

// SessionToken never interprets business KEY headers as user authentication.
// Explicit malformed/ambiguous Authorization does not fall back to a cookie.
func SessionToken(r *http.Request) (string, bool) {
	if len(r.Header.Values("X-API-Key")) != 0 {
		return "", false
	}
	authorization := r.Header.Values("Authorization")
	if len(authorization) > 0 {
		if len(authorization) != 1 || !strings.HasPrefix(authorization[0], "Bearer ") {
			return "", false
		}
		return ExtractAPIKey(authorization[0]), false
	}
	cookies := r.CookiesNamed(SessionCookie)
	if len(cookies) != 1 {
		return "", false
	}
	return cookies[0].Value, true
}
func CookieMutationAllowed(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) != 1 || len(r.Header.Values("X-API-Request")) != 1 || r.Header.Get("X-API-Request") != "1" {
		return false
	}
	origin, err := url.Parse(origins[0])
	return err == nil && (origin.Scheme == "http" || origin.Scheme == "https") && origin.User == nil && origin.Path == "" && origin.RawQuery == "" && origin.Fragment == "" && strings.EqualFold(origin.Host, r.Host) && (r.TLS == nil || origin.Scheme == "https") && (r.Header.Get("Sec-Fetch-Site") == "" || r.Header.Get("Sec-Fetch-Site") == "same-origin")
}
func UnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// StripUserSessionCookies preserves business cookies, but never gives plugins
// or upstreams the web console's login session, even on the shared origin.
func StripUserSessionCookies(r *http.Request) {
	var kept []string
	for _, cookie := range r.Cookies() {
		if !managementCookie(cookie.Name) {
			kept = append(kept, cookie.String())
		}
	}
	r.Header.Del("Cookie")
	if len(kept) > 0 {
		r.Header.Set("Cookie", strings.Join(kept, "; "))
	}
}

// Business responses may set their own cookies, never the UI login cookie.
func StripSessionSetCookies(headers http.Header) {
	values := headers.Values("Set-Cookie")
	headers.Del("Set-Cookie")
	for _, value := range values {
		pair := strings.SplitN(value, ";", 2)[0]
		name, _, ok := strings.Cut(pair, "=")
		if ok && managementCookie(strings.TrimSpace(name)) {
			continue
		}
		headers.Add("Set-Cookie", value)
	}
}

func managementCookie(name string) bool {
	return name == "api_manager_admin_entry" || name == SessionCookie || name == DeviceCookie || name == "api_manager_test_session" || name == "api_manager_mfa" || strings.HasPrefix(name, "api_manager_oauth_") || strings.HasPrefix(name, "api_manager_passkey_")
}
