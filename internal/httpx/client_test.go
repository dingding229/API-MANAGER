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

func TestPublicAddressDoesNotInventHistoricalPublicIP(t *testing.T) {
	for _, raw := range []string{"192.168.32.1", "172.18.0.2", "10.0.0.1", "127.0.0.1", "::1", "100.64.1.1", "169.254.1.1", "garbage"} {
		if PublicAddress(raw) != "" {
			t.Fatal("private address shown as public", raw)
		}
	}
	if PublicAddress("8.8.8.8") != "8.8.8.8" {
		t.Fatal("valid public address hidden")
	}
}

func TestTrustedIngressSkipsCloudflareEdgeButRejectsForgedDirectHeaders(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("192.168.32.1/32"), netip.MustParsePrefix("173.245.48.0/20")}
	for _, tc := range []struct{ peer, xff, want string }{{"192.168.32.1:80", "8.8.8.8, 173.245.48.10", "8.8.8.8"}, {"8.8.4.4:80", "1.2.3.4", "8.8.4.4"}, {"192.168.32.1:80", "1.2.3.4, 8.8.4.4", "8.8.4.4"}} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.xff)
		r.Header.Set("CF-Connecting-IP", "9.9.9.9")
		ClientIdentity(proxies, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if Client(r).IP != tc.want {
				t.Fatal(tc, Client(r))
			}
		})).ServeHTTP(httptest.NewRecorder(), r)
	}
}
