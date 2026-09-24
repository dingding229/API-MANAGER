package security

import (
	"api-manager/internal/upstream"
	"context"
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestPrivateUpstreamRequiresPinnedCredentialAndPort(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:18080")
	for _, tc := range []struct {
		ref, port string
		approved  bool
	}{
		{"", "18080", false}, {"unknown", "18080", false}, {"approved", "18080", true}, {"approved", "8080", false},
	} {
		dial := DialContext(upstream.Credentials{"approved": {Origin: u.Scheme + "://" + u.Host, APIKey: "test"}}, tc.ref, u)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		conn, err := dial(ctx, "tcp", net.JoinHostPort("127.0.0.1", tc.port))
		if conn != nil {
			conn.Close()
		}
		if !tc.approved && err == nil {
			t.Fatalf("unapproved private target accepted: %+v", tc)
		}
		if tc.approved && (err == nil || !strings.Contains(err.Error(), "canceled")) {
			t.Fatalf("approved target did not reach dial: %v", err)
		}
	}
}
