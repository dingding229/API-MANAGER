package ratelimit

import "time"

type Limiter interface {
	Allow(key string, limit int, window time.Duration, now time.Time) bool
	Close() error
}
