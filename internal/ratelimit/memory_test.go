package ratelimit

import (
	"testing"
	"time"
)

func TestMemoryAllow(t *testing.T) {
	limiter := NewMemory()
	now := time.Now()
	for i := 0; i < 2; i++ {
		if !limiter.Allow("client", 2, time.Minute, now) {
			t.Fatalf("request %d should pass", i+1)
		}
	}
	if limiter.Allow("client", 2, time.Minute, now) {
		t.Fatal("third request should be rejected")
	}
	if !limiter.Allow("client", 2, time.Minute, now.Add(time.Minute)) {
		t.Fatal("request in a new window should pass")
	}
}
