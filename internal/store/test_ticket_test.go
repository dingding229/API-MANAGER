package store

import (
	"api-manager/internal/model"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTicketConsumeIsAtomicSessionBoundAndExpires(t *testing.T) {
	m := NewMemory()
	_ = m.CreateUser(model.User{ID: "u", Username: "u", Status: "active"})
	_ = m.CreateSession(model.Session{Hash: "session", UserID: "u", ExpiresAt: time.Now().Add(time.Hour)})
	ticket := model.APITestTicket{Hash: "ticket", SessionHash: "session", APIID: "api", Digest: "digest", ClientIP: "ip", UserAgent: "ua", ExpiresAt: time.Now().Add(time.Minute)}
	if err := m.PutTestTicket(context.Background(), ticket); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ConsumeTestTicket(context.Background(), "ticket", "wrong", "digest", "ip", "ua", time.Now()); err == nil {
		t.Fatal("wrong session allowed")
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.ConsumeTestTicket(context.Background(), "ticket", "session", "digest", "ip", "ua", time.Now()); err == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("multiple uses", accepted.Load())
	}
	ticket.Hash = "expired"
	ticket.ExpiresAt = time.Now().Add(-time.Second)
	_ = m.PutTestTicket(context.Background(), ticket)
	if _, err := m.ConsumeTestTicket(context.Background(), "expired", "session", "digest", "ip", "ua", time.Now()); err == nil {
		t.Fatal("expired allowed")
	}
	ticket.Hash = "revoked"
	ticket.ExpiresAt = time.Now().Add(time.Minute)
	_ = m.PutTestTicket(context.Background(), ticket)
	_ = m.DeleteSession("session")
	if _, err := m.ConsumeTestTicket(context.Background(), "revoked", "session", "digest", "ip", "ua", time.Now()); err == nil {
		t.Fatal("revoked session used ticket")
	}
}
