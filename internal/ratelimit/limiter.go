package ratelimit

import "time"

type Limiter interface {
	Allow(key string, limit int, window time.Duration, now time.Time) bool
	Close() error
}

// NonceStore records a one-use token across the entire timestamp validity window.
type NonceStore interface {
	UseNonce(key string, ttl time.Duration) bool
}
