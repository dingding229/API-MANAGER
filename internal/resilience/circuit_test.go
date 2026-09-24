package resilience

import (
	"testing"
	"time"
)

func TestCircuitBreakerOpensAndAllowsProbeAfterReset(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	breaker := NewCircuitBreaker()
	breaker.now = func() time.Time { return now }
	if !breaker.Allow("upstream", 2) {
		t.Fatal("fresh circuit must allow")
	}
	breaker.Failure("upstream", 2, 5)
	if !breaker.Allow("upstream", 2) {
		t.Fatal("circuit should stay closed before threshold")
	}
	breaker.Failure("upstream", 2, 5)
	if breaker.Allow("upstream", 2) {
		t.Fatal("circuit must open at threshold")
	}
	now = now.Add(5 * time.Second)
	if !breaker.Allow("upstream", 2) {
		t.Fatal("expired circuit should allow a probe")
	}
	breaker.Success("upstream")
	if !breaker.Allow("upstream", 2) {
		t.Fatal("successful probe should close circuit")
	}
}
