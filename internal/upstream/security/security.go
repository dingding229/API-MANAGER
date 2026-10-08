// Package security enforces a network boundary for configurable upstreams.
package security

import (
	"api-manager/internal/upstream"
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

func SafeOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "invalid"
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

func DialContext(credentials upstream.Credentials, ref string, target *url.URL) func(context.Context, string, string) (net.Conn, error) {
	approvedPrivate := false
	origin := SafeOrigin(target.String())
	if ref != "" {
		item, ok := credentials[ref]
		approvedPrivate = ok && item.Origin == origin
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		// Do not allow a redirect, custom proxy or late DNS change to escape the pinned destination.
		expectedPort := target.Port()
		if expectedPort == "" {
			if target.Scheme == "https" {
				expectedPort = "443"
			} else {
				expectedPort = "80"
			}
		}
		if port != expectedPort || !strings.EqualFold(host, target.Hostname()) {
			return nil, errors.New("upstream host changed")
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("upstream has no addresses")
		}
		for _, ip := range ips {
			if !publicIP(ip) && !approvedPrivate {
				return nil, errors.New("private upstream address not approved")
			}
		}
		// Dial a validated numeric address; re-resolution at connect time would permit DNS rebinding.
		var lastErr error
		d := &net.Dialer{}
		for _, ip := range ips {
			conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.Is4In6() && !blockedSpecial(ip)
}

func blockedSpecial(ip netip.Addr) bool {
	for _, raw := range []string{"0.0.0.0/8", "100.64.0.0/10", "169.254.0.0/16", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "::/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/32", "2001:10::/28", "2001:20::/28", "2002::/16", "2001:db8::/32", "3fff::/20", "5f00::/16", "fec0::/10", "fc00::/7", "fe80::/10"} {
		if netip.MustParsePrefix(raw).Contains(ip) {
			return true
		}
	}
	return false
}
