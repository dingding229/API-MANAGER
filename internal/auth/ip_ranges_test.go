package auth

import (
	"api-manager/internal/clientip"
	"api-manager/internal/model"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestIPRangesCanonicalAndTrustedSource(t *testing.T) {
	values, e := NormalizeIPRanges([]string{"8.8.8.8", "1.1.1.7/24", "2001:4860:4860::8888", "8.8.8.8/32"})
	if e != nil || len(values) != 3 || values[1] != "1.1.1.0/24" {
		t.Fatal(values, e)
	}
	for _, v := range [][]string{{""}, {"bad"}, {"8.8.8.8/33"}, {"::ffff:8.8.8.8/120"}, {"fe80::1%eth0"}, make([]string, 33)} {
		if _, e = NormalizeIPRanges(v); e == nil {
			t.Fatal("bad policy accepted", v)
		}
	}
	c := model.Credential{AllowedIPRanges: []string{"8.8.8.0/24", "2001:4860::/32"}}
	for _, tc := range []struct {
		peer string
		want bool
	}{{"8.8.8.8:443", true}, {"[2001:4860:4860::8888]:443", true}, {"1.1.1.1:443", false}, {"garbage", false}} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", "8.8.8.8")
		if CredentialIPAllowed(c, r) != tc.want {
			t.Fatal(tc)
		}
	}
	h := clientip.ClientIdentity([]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !CredentialIPAllowed(c, r) {
			t.Fatal("trusted chain not used")
		}
	}))
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:8000"
	r.Header.Set("X-Forwarded-For", "8.8.8.8")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if _, e = NormalizeIPRanges([]string{strings.Repeat("x", 129)}); e == nil {
		t.Fatal("long IP policy")
	}
}

type countedLookup struct {
	calls int
	key   model.Credential
}

func (s *countedLookup) FindCredentialByHash(hash string) (model.Credential, bool) {
	s.calls++
	return s.key, hash == s.key.Hash
}
func TestCredentialLookupIsOncePerRequestNotGlobal(t *testing.T) {
	s := &countedLookup{key: model.Credential{Hash: HashAPIKey("secret"), AllowedIPRanges: []string{"8.8.8.8/32"}}}
	r := httptest.NewRequest("GET", "/api/a", nil)
	r.RemoteAddr = "8.8.8.8:80"
	r.Header.Set("X-API-Key", "secret")
	r = ResolveRequestCredential(r, s)
	for i := 0; i < 3; i++ {
		if Authorize(model.API{AuthMode: "api_key"}, s, r) != nil {
			t.Fatal("key denied")
		}
		RequestCredential(r, s)
	}
	if s.calls != 1 {
		t.Fatal(s.calls)
	}
	s.key.Revoked = true
	next := ResolveRequestCredential(r.Clone(r.Context()), s)
	if Authorize(model.API{AuthMode: "api_key"}, s, next) == nil {
		t.Fatal("stale credential cached across request")
	}
	if s.calls != 2 {
		t.Fatal(s.calls)
	}
}
func BenchmarkRequestCredentialReuse(b *testing.B) {
	s := &countedLookup{key: model.Credential{Hash: HashAPIKey("secret")}}
	base := httptest.NewRequest("GET", "/api/test", nil)
	base.Header.Set("X-API-Key", "secret")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r := ResolveRequestCredential(base, s)
		RequestCredential(r, s)
		Authorize(model.API{AuthMode: "api_key"}, s, r)
		RequestCredential(r, s)
	}
	b.ReportMetric(float64(s.calls)/float64(b.N), "lookups/op")
}
