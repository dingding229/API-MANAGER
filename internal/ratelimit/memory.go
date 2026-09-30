package ratelimit

import (
	"context"
	"sync"
	"time"
)

type Memory struct {
	mu      sync.Mutex
	windows map[string]window
}

type window struct {
	started time.Time
	count   int
	expires time.Time
}

func NewMemory() *Memory {
	return &Memory{windows: make(map[string]window)}
}

func (m *Memory) Close() error               { return nil }
func (m *Memory) Ping(context.Context) error { return nil }

func (m *Memory) Allow(key string, limit int, windowSize time.Duration, now time.Time) bool {
	if limit <= 0 {
		return true
	}
	if windowSize <= 0 {
		windowSize = time.Minute
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.windows) > 10000 {
		for k, window := range m.windows {
			if !now.Before(window.expires) {
				delete(m.windows, k)
			}
		}
		if len(m.windows) > 20000 {
			return false
		} // fail closed under unbounded identities
	}
	current, ok := m.windows[key]
	if !ok || now.Sub(current.started) >= windowSize {
		m.windows[key] = window{started: now, count: 1, expires: now.Add(windowSize)}
		return true
	}
	if current.count >= limit {
		return false
	}
	current.count++
	m.windows[key] = current
	return true
}
