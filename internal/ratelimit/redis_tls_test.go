package ratelimit

import (
	"context"
	"testing"
)

func TestRedisTLSRejectsInvalidAddressAndCA(t *testing.T) {
	if _, err := NewRedisWithTLS(context.Background(), "missing-port", "", "password", 0, true, ""); err == nil {
		t.Fatal("TLS requires host:port for server name validation")
	}
	if _, err := NewRedisWithTLS(context.Background(), "redis.example:6380", "", "password", 0, true, t.TempDir()+"/missing.pem"); err == nil {
		t.Fatal("missing CA file must fail before dialing")
	}
}
