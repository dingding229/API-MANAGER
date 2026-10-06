package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionCookiesDoNotOverrideInvalidExplicitCredentials(t *testing.T) {
	r := httptest.NewRequest("GET", "/admin/v1/apis", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookie, Value: "us_valid"})
	if token, cookie := SessionToken(r); token != "us_valid" || !cookie {
		t.Fatal(token, cookie)
	}
	r.Header.Set("Authorization", "invalid")
	if token, _ := SessionToken(r); token != "" {
		t.Fatal("invalid header fell back to cookie")
	}
	r.Header.Del("Authorization")
	r.Header.Set("X-API-Key", "business-key")
	if token, _ := SessionToken(r); token != "" {
		t.Fatal("business key accepted")
	}
	r.Header.Del("X-API-Key")
	r.AddCookie(&http.Cookie{Name: SessionCookie, Value: "us_other"})
	if token, _ := SessionToken(r); token != "" {
		t.Fatal("ambiguous cookie accepted")
	}
	StripUserSessionCookies(r)
	if r.Header.Get("Cookie") != "" {
		t.Fatal("session cookie exposed to plugin")
	}
	r.Header.Set("Cookie", SessionCookie+"=hidden; business=kept; api_manager_test_session=hidden")
	StripUserSessionCookies(r)
	if r.Header.Get("Cookie") != "business=kept" {
		t.Fatal(r.Header)
	}
}

func TestBusinessResponseCannotOverwriteLoginCookie(t *testing.T) {
	h := http.Header{}
	h.Add("Set-Cookie", SessionCookie+"=us_attacker; Path=/; HttpOnly")
	h.Add("Set-Cookie", "business=kept; Path=/api/")
	StripSessionSetCookies(h)
	if values := h.Values("Set-Cookie"); len(values) != 1 || values[0] != "business=kept; Path=/api/" {
		t.Fatal(values)
	}
}

func TestOAuthAndMFAStateNeverReachBusinessPlugins(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/test", nil)
	r.Header.Set("Cookie", "api_manager_mfa=hidden; api_manager_oauth_google=hidden; business=kept")
	StripUserSessionCookies(r)
	if r.Header.Get("Cookie") != "business=kept" {
		t.Fatal(r.Header)
	}
	h := http.Header{}
	for _, name := range []string{"api_manager_mfa", "api_manager_oauth_google", SessionCookie} {
		h.Add("Set-Cookie", name+"=injected; Path=/")
	}
	h.Add("Set-Cookie", "business=kept")
	StripSessionSetCookies(h)
	if v := h.Values("Set-Cookie"); len(v) != 1 || v[0] != "business=kept" {
		t.Fatal(v)
	}
}
