package version

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func versionSource(body string, status int) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func TestStableSemanticComparisonAndCachedChecks(t *testing.T) {
	oldVersion, oldRevision := Version, Revision
	defer func() { Version = oldVersion; Revision = oldRevision }()
	Version = "v0.3.25"
	Revision = "release-sha"
	c := NewChecker("secret-readonly")
	var calls atomic.Int32
	c.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.Host != "api.github.com" || !strings.HasPrefix(r.URL.Path, "/repos/dingding229/API-MANAGER/tags") || r.Header.Get("Authorization") != "Bearer secret-readonly" {
			t.Error("untrusted version destination")
		}
		return versionSource(`[{"name":"v0.3.9"},{"name":"v0.3.26-rc.1"},{"name":"v0.3.26"},{"name":"v0.3.25"}]`, 200), nil
	})
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			info := c.Check(context.Background())
			if info.Status != "update_available" || info.Latest != "0.3.26" {
				t.Error(info)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("checks not coalesced", calls.Load())
	}
	data, _ := json.Marshal(c.cached)
	if strings.Contains(string(data), "secret-readonly") {
		t.Fatal("credential leaked")
	}
}
func TestUnavailableNeverMeansLatest(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{{404, `{}`}, {429, `{}`}, {200, `not-json`}, {200, `[]`}, {200, strings.Repeat("x", 256<<10+1)}} {
		c := NewChecker("")
		c.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { return versionSource(tc.body, tc.status), nil })
		info := c.Check(context.Background())
		if (info.Status != "credential_required" && info.Status != "rate_limited" && info.Status != "unavailable") || info.Latest != "" {
			t.Fatal(info)
		}
	}
}
func TestBoundedPaginationFindsHighestStableAndFailsClosed(t *testing.T) {
	first := `[` + strings.Repeat(`{"name":"v0.3.2"},`, 99) + `{"name":"v0.3.3"}]`
	c := NewChecker("")
	calls := 0
	c.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("page") == "1" {
			return versionSource(first, 200), nil
		}
		return versionSource(`[{"name":"v0.3.100"}]`, 200), nil
	})
	info := c.Check(context.Background())
	if info.Latest != "0.3.100" || calls != 2 {
		t.Fatal(info, calls)
	}
	c = NewChecker("")
	c.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { return versionSource(first, 200), nil })
	if c.Check(context.Background()).Status != "unavailable" {
		t.Fatal("incomplete result called latest")
	}
	for _, v := range []string{"v1.2", "main", "v1.2.3-rc", "v999999999999999.1.1", "v1.2.3; evil"} {
		if _, ok := parts(v); ok {
			t.Fatal(v)
		}
	}
}

func TestVersionStatesDistinguishReleaseAndDevelopment(t *testing.T) {
	oldVersion, oldRevision := Version, Revision
	defer func() { Version = oldVersion; Revision = oldRevision }()
	for _, tc := range []struct{ current, revision, want string }{{"v0.3.25", "release-sha", "current"}, {"0.3.26", "release-sha", "ahead"}, {"0.3.25-dev", "development", "development"}, {"0.3.25", "development", "development"}} {
		Version, Revision = tc.current, tc.revision
		c := NewChecker("")
		c.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { return versionSource(`[{"name":"v0.3.25"}]`, 200), nil })
		if got := c.Check(context.Background()).Status; got != tc.want {
			t.Fatalf("%s %s = %s", tc.current, tc.revision, got)
		}
	}
	c := NewChecker("read-token")
	calls := 0
	c.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		resp := versionSource("", 302)
		resp.Header.Set("Location", "https://evil.example/token")
		return resp, nil
	})
	if c.Check(context.Background()).Status != "unavailable" || calls != 1 {
		t.Fatal("redirect followed")
	}
}

func TestVersionCredentialUpdatesInvalidateCacheWithoutDisclosure(t *testing.T) {
	c := NewChecker("")
	c.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") == "Bearer read-only-token" {
			return versionSource(`[{"name":"v9.9.9"}]`, 200), nil
		}
		return versionSource(`{}`, 404), nil
	})
	if c.Check(context.Background()).Status != "credential_required" {
		t.Fatal("missing credential not explained")
	}
	c.SetToken("read-only-token")
	v := c.Check(context.Background())
	if v.Latest != "9.9.9" {
		t.Fatal(v)
	}
	data, _ := json.Marshal(v)
	if strings.Contains(string(data), "read-only-token") {
		t.Fatal("credential leaked")
	}
	c.SetToken("")
	if c.Check(context.Background()).Latest != "" {
		t.Fatal("stale credential check retained")
	}
}
