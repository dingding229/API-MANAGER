package sitesettings

import (
	"encoding/json"
	"errors"
	"golang.org/x/net/idna"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// NormalizeAPIDomain accepts a hostname or a full origin, never credentials,
// wildcards, a path prefix, a query, a fragment or an ambiguous port.
func NormalizeAPIDomain(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(raw, "\\\r\n\t \x00") || (u.Scheme != "https" && u.Scheme != "http") {
		return "", ErrInvalid
	}
	authority, ok := canonicalAuthority(u.Host, u.Scheme)
	if !ok {
		return "", ErrInvalid
	}
	return u.Scheme + "://" + authority, nil
}
func canonicalAuthority(raw, scheme string) (string, bool) {
	if raw == "" || strings.ContainsAny(raw, "/\\@?#\r\n\t \x00") {
		return "", false
	}
	parsed, err := url.Parse("http://" + raw)
	if err != nil || parsed.Host != raw || parsed.User != nil {
		return "", false
	}
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if _, err := netip.ParseAddr(hostname); err != nil {
		var e error
		hostname, e = idna.Lookup.ToASCII(hostname)
		if e != nil {
			return "", false
		}
	}
	if hostname == "" {
		return "", false
	}
	if ip, err := netip.ParseAddr(hostname); err == nil {
		hostname = ip.String()
	} else {
		if len(hostname) > 253 {
			return "", false
		}
		for _, label := range strings.Split(hostname, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", false
			}
			for _, c := range label {
				if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
					return "", false
				}
			}
		}
	}
	port := parsed.Port()
	if strings.HasSuffix(raw, ":") {
		return "", false
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", false
		}
		port = strconv.Itoa(n)
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		return net.JoinHostPort(hostname, port), true
	}
	if strings.Contains(hostname, ":") {
		return "[" + hostname + "]", true
	}
	return hostname, true
}
func SameHostname(origin, authority string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	left, ok := canonicalAuthority(u.Host, "http")
	if !ok {
		return false
	}
	right, ok := canonicalAuthority(authority, "http")
	if !ok {
		return false
	}
	first, _ := url.Parse("http://" + left)
	second, _ := url.Parse("http://" + right)
	return first.Hostname() == second.Hostname()
}

func (s *Service) AllowsAPIHost(host string) bool {
	snap := s.current.Load()
	if snap.apiAuthority == "" {
		return true
	}
	actual, ok := canonicalAuthority(host, snap.apiScheme)
	return ok && actual == snap.apiAuthority
}

// Ignore X-Forwarded-Host/Forwarded: deployments must preserve the original Host
// at their trusted ingress. Untrusted headers cannot bypass domain isolation.
func (s *Service) APIDomainGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/")) && (!s.AllowsAPIHost(r.Host) || (r.URL.Host != "" && !s.AllowsAPIHost(r.URL.Host)) || !s.AllowsAPITLSName(r)) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusMisdirectedRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "请使用设置的接口调用地址。"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

var ErrSameSiteDomain = errors.New("接口专用地址不能与网站地址相同")

func (s *Service) AllowsAPITLSName(r *http.Request) bool {
	snap := s.current.Load()
	if snap.apiAuthority == "" || r.TLS == nil || r.TLS.ServerName == "" {
		return true
	}
	return SameHostname(snap.apiScheme+"://"+snap.apiAuthority, r.TLS.ServerName)
}
