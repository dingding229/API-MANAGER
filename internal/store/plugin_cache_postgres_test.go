package store

import (
	"api-manager/internal/model"
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// This test uses a separately-created database, not the production database.
func TestPostgresPluginCache(t *testing.T) {
	dsn := os.Getenv("TEST_PLUGIN_CACHE_DSN")
	if dsn == "" {
		t.Skip("isolated cache database not configured")
	}
	p, err := NewPostgres(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	now := time.Now().UTC()
	a := model.API{ID: "cache-pg-test", Name: "test-cache", Method: "GET", Path: "/api/cache-test", AuthMode: "none", Plugin: "fixture", Enabled: true, PublishedAt: &now, CreatedAt: now, UpdatedAt: now, PluginCache: model.PluginCacheConfig{Enabled: true, TTLSeconds: 30, MaxEntries: 2}}
	if err := p.CreateAPI(a); err != nil {
		t.Fatal(err)
	}
	defer p.DeleteAPI(a.ID)
	a, err = p.GetAPI(a.ID)
	if err != nil || !a.PluginCache.Enabled || a.PluginCache.TTLSeconds != 30 {
		t.Fatal("settings not persisted", err, a.PluginCache)
	}
	for _, key := range []string{"one", "two", "three"} {
		e := model.PluginCacheEntry{APIID: a.ID, Key: key, Ciphertext: "ciphertext", APIUpdatedAt: a.UpdatedAt, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Minute)}
		if err := p.PutPluginCache(context.Background(), e, 2); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := p.PluginCacheStats(context.Background(), a.ID, time.Now())
	if err != nil || stats.Entries != 2 {
		t.Fatal("capacity not enforced", stats, err)
	}
	second, openErr := NewPostgres(context.Background(), dsn)
	if openErr != nil {
		t.Fatal(openErr)
	}
	reopened, reopenErr := second.GetPluginCache(context.Background(), a.ID, "three", time.Now())
	second.Close()
	if reopenErr != nil || reopened.Ciphertext != "ciphertext" {
		t.Fatal("cache did not survive store restart", reopenErr)
	}
	e, err := p.GetPluginCache(context.Background(), a.ID, "three", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.GetPluginCache(context.Background(), a.ID, "three", time.Now().Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired item read", err)
	}
	if err = p.ClearPluginCache(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(p.PutPluginCache(context.Background(), e, 2), ErrConflict) {
		t.Fatal("inflight stale writer repopulated cleared cache")
	}
	// Updating a route retains all settings and invalidates the current key generation.
	a, _ = p.GetAPI(a.ID)
	a.PluginCache.TTLSeconds = 90
	a.UpdatedAt = time.Now().UTC()
	if err = p.UpdateAPI(a); err != nil {
		t.Fatal(err)
	}
	a, _ = p.GetAPI(a.ID)
	if a.PluginCache.TTLSeconds != 90 {
		t.Fatal("updated TTL lost")
	}
	e.APIUpdatedAt = a.UpdatedAt
	e.Key = "after-clear"
	e.ExpiresAt = time.Now().Add(-time.Minute)
	if err = p.PutPluginCache(context.Background(), e, 2); err != nil {
		t.Fatal(err)
	}
	if err = p.PrunePluginCache(context.Background(), time.Now(), 1000); err != nil {
		t.Fatal(err)
	}
	stats, err = p.PluginCacheStats(context.Background(), a.ID, time.Now())
	if err != nil || stats.Entries+stats.Expired != 0 {
		t.Fatal("expired entries not pruned", stats, err)
	}
}

func TestPostgresSessionsAndOneUseTickets(t *testing.T) {
	dsn := os.Getenv("TEST_PLUGIN_CACHE_DSN")
	if dsn == "" {
		t.Skip("isolated session database not configured")
	}
	p, err := NewPostgres(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	n := time.Now().UTC()
	userID := "12345678-1234-4234-8234-123456789abc"
	if err = p.CreateUser(model.User{ID: userID, Username: "session-integration", Email: "session-integration@example.test", Status: "active", CreatedAt: n, UpdatedAt: n}); err != nil {
		t.Fatal(err)
	}
	a := model.API{ID: "ticket-api", Name: "ticket", Method: "GET", Path: "/api/ticket", AuthMode: "api_key", CreatedAt: n, UpdatedAt: n}
	if err = p.CreateAPI(a); err != nil {
		t.Fatal(err)
	}
	session := model.Session{Hash: "fixture-session", UserID: userID, ExpiresAt: n.Add(time.Hour), LoginIP: "192.0.2.1", LastIP: "192.0.2.1", PeerIP: "10.0.0.1", IPSource: "trusted_proxy", UserAgent: "TestBrowser", Device: "TestBrowser / test"}
	if err = p.CreateSession(session); err != nil {
		t.Fatal(err)
	}
	stored, err := p.GetSession(session.Hash)
	if err != nil || stored.ID == "" || stored.LoginIP != "192.0.2.1" || stored.UserAgent != "TestBrowser" {
		t.Fatal(stored, err)
	}
	ticket := model.APITestTicket{Hash: "ticket", SessionHash: session.Hash, APIID: a.ID, Digest: "request", ClientIP: "192.0.2.1", UserAgent: "TestBrowser", ExpiresAt: n.Add(time.Minute)}
	if err = p.PutTestTicket(context.Background(), ticket); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ConsumeTestTicket(context.Background(), ticket.Hash, session.Hash, "request", ticket.ClientIP, ticket.UserAgent, n); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ConsumeTestTicket(context.Background(), ticket.Hash, session.Hash, "request", ticket.ClientIP, ticket.UserAgent, n); !errors.Is(err, ErrNotFound) {
		t.Fatal("replay allowed", err)
	}
	ticket.Hash = "pending"
	if err = p.PutTestTicket(context.Background(), ticket); err != nil {
		t.Fatal(err)
	}
	if ok, e := p.DeleteUserSession(userID, stored.ID); !ok || e != nil {
		t.Fatal(ok, e)
	}
	if _, err = p.ConsumeTestTicket(context.Background(), ticket.Hash, session.Hash, "request", ticket.ClientIP, ticket.UserAgent, n); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked session authorized", err)
	}
}
