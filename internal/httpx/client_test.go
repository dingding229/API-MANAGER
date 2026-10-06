package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestOnlyTrustedProxyChainSuppliesClientIP(t *testing.T) {
	for _, tc := range []struct {
		peer, forwarded, want string
		trusted               bool
	}{{"203.0.113.1:1234", "1.2.3.4", "203.0.113.1", false}, {"10.0.0.2:1234", "203.0.113.10", "203.0.113.10", true}, {"10.0.0.2:1234", "1.1.1.1, 203.0.113.10", "203.0.113.10", true}, {"10.0.0.2:1234", "invalid", "10.0.0.2", true}} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.forwarded)
		var proxies []netip.Prefix
		if tc.trusted {
			proxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}
		}
		ClientIdentity(proxies, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := Client(r).IP; got != tc.want {
				t.Errorf("%+v got=%s", tc, got)
			}
		})).ServeHTTP(httptest.NewRecorder(), r)
	}
}
