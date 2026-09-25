// Package resilience contains bounded, in-process resilience primitives for
// upstream proxy calls. State is intentionally local to a gateway process;
// deployments requiring fleet-wide circuit state should use a dedicated
// service-mesh or shared policy engine.
package resilience

import (
	"errors"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("upstream circuit breaker is open")

type CircuitBreaker struct {
	mu     sync.Mutex
	states map[string]state
	now    func() time.Time
}

type state struct {
	failures  int
	openUntil time.Time
}

func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{states: make(map[string]state), now: time.Now}
}

// Allow permits a request unless the circuit is open. Threshold values less
// than one disable the breaker. On expiry the next request is a half-open
// probe; its succeeding Record call closes or reopens the circuit.
func (b *CircuitBreaker) Allow(key string, threshold int) bool {
	if threshold < 1 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	current := b.states[key]
	if current.openUntil.IsZero() || !b.now().Before(current.openUntil) {
		if !current.openUntil.IsZero() {
			current.openUntil = time.Time{}
			current.failures = 0
			b.states[key] = current
		}
		return true
	}
	return false
}

func (b *CircuitBreaker) Success(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.states, key)
}

func (b *CircuitBreaker) Failure(key string, threshold, resetSeconds int) {
	if threshold < 1 {
		return
	}
	if resetSeconds < 1 {
		resetSeconds = 30
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	current := b.states[key]
	current.failures++
	if current.failures >= threshold {
		current.openUntil = b.now().Add(time.Duration(resetSeconds) * time.Second)
	}
	b.states[key] = current
}
