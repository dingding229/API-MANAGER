package security

import (
	"net/netip"
	"testing"
)

func TestPublicIPRejectsInternalAndSpecialRanges(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "fc00::1", "::ffff:127.0.0.1"} {
		if publicIP(netip.MustParseAddr(raw)) {
			t.Fatalf("publicIP accepted %s", raw)
		}
	}
	for _, raw := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !publicIP(netip.MustParseAddr(raw)) {
			t.Fatalf("publicIP rejected %s", raw)
		}
	}
}
