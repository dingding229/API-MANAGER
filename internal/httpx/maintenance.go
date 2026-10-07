package httpx

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

// Maintenance drains existing requests and refuses new work during a restore.
type Maintenance struct {
	mu      sync.Mutex
	active  int
	blocked bool
	idle    chan struct{}
}

func (m *Maintenance) Begin(ctx context.Context) (func(), error) {
	m.mu.Lock()
	if m.blocked {
		m.mu.Unlock()
		return nil, errors.New("maintenance busy")
	}
	m.blocked = true
	if m.active == 0 {
		m.mu.Unlock()
		return m.End, nil
	}
	idle := m.idle
	m.mu.Unlock()
	select {
	case <-idle:
		return m.End, nil
	case <-ctx.Done():
		m.End()
		return nil, ctx.Err()
	}
}
func (m *Maintenance) End() { m.mu.Lock(); m.blocked = false; m.mu.Unlock() }
func (m *Maintenance) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/live" || r.URL.Path == "/health/ready" {
			next.ServeHTTP(w, r)
			return
		}
		restore := r.Method == "POST" && r.URL.Path == "/admin/v1/database/restore"
		m.mu.Lock()
		if m.blocked {
			m.mu.Unlock()
			w.Header().Set("Retry-After", "5")
			http.Error(w, "数据库维护中，请稍后再试", http.StatusServiceUnavailable)
			return
		}
		if !restore {
			if m.active == 0 {
				m.idle = make(chan struct{})
			}
			m.active++
		}
		m.mu.Unlock()
		if !restore {
			defer func() {
				m.mu.Lock()
				m.active--
				if m.active == 0 {
					close(m.idle)
				}
				m.mu.Unlock()
			}()
		}
		next.ServeHTTP(w, r)
	})
}

// Work also counts trusted periodic jobs so restores drain them before locking tables.
func (m *Maintenance) Work() (func(), bool) {
	m.mu.Lock()
	if m.blocked {
		m.mu.Unlock()
		return nil, false
	}
	if m.active == 0 {
		m.idle = make(chan struct{})
	}
	m.active++
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		m.active--
		if m.active == 0 {
			close(m.idle)
		}
		m.mu.Unlock()
	}, true
}
