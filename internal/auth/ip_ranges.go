package auth

import (
	"api-manager/internal/clientip"
	"api-manager/internal/model"
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
)

// NormalizeIPRanges accepts single addresses or CIDR prefixes, with bounded policy size.
func NormalizeIPRanges(values []string) ([]string, error) {
	if len(values) > 32 {
		return nil, errors.New("最多设置 32 个 IP 地址或地址段")
	}
	result := []string{}
	seen := map[string]bool{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" || len(value) > 128 {
			return nil, errors.New("请填写有效的 IPv4/IPv6 地址或 CIDR")
		}
		p, err := netip.ParsePrefix(value)
		if err != nil {
			if ip, e := netip.ParseAddr(value); e == nil && ip.Zone() == "" {
				ip = ip.Unmap()
				p = netip.PrefixFrom(ip, ip.BitLen())
				err = nil
			}
		}
		if err != nil || p.Addr().Is4In6() || p.Addr().Zone() != "" {
			return nil, errors.New("请填写有效的 IPv4/IPv6 地址或 CIDR")
		}
		canonical := p.Masked().String()
		if !seen[canonical] {
			result = append(result, canonical)
			seen[canonical] = true
		}
	}
	return result, nil
}
func CredentialIPAllowed(c model.Credential, r *http.Request) bool {
	if len(c.AllowedIPRanges) == 0 {
		return true
	}
	ip, err := netip.ParseAddr(clientip.Client(r).IP)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	matched := false
	for _, value := range c.AllowedIPRanges {
		p, e := netip.ParsePrefix(value)
		if e != nil || p.Addr().Is4In6() {
			return false
		}
		if p.Contains(ip) {
			matched = true
		}
	}
	return matched
}

type resolvedCredential struct {
	value model.Credential
	valid bool
}
type requestCredentialKey struct{}

// Cache a lookup within one request only. Revocations/policy edits affect the next request immediately.
func ResolveRequestCredential(r *http.Request, s interface {
	FindCredentialByHash(string) (model.Credential, bool)
}) *http.Request {
	c, ok := ValidateAPIKey(s, RequestKey(r))
	return r.WithContext(context.WithValue(r.Context(), requestCredentialKey{}, resolvedCredential{c, ok}))
}
func RequestCredential(r *http.Request, s interface {
	FindCredentialByHash(string) (model.Credential, bool)
}) (model.Credential, bool) {
	if v, ok := r.Context().Value(requestCredentialKey{}).(resolvedCredential); ok {
		return v.value, v.valid
	}
	return ValidateAPIKey(s, RequestKey(r))
}
