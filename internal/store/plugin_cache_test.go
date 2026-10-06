package store

import (
	"api-manager/internal/model"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestMemoryCacheCapacityExpiryAndClearGeneration(t *testing.T) {
	m := NewMemory()
	now := time.Now().UTC()
	a := model.API{ID: "cache-test", Name: "cache", Method: "GET", Path: "/api/cache", Enabled: true, PublishedAt: &now, UpdatedAt: now, PluginCache: model.PluginCacheConfig{Enabled: true}}
	if err := m.CreateAPI(a); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		err := m.PutPluginCache(context.Background(), model.PluginCacheEntry{APIID: a.ID, Key: fmt.Sprint(i), Ciphertext: "encrypted", APIUpdatedAt: now, CreatedAt: now.Add(time.Duration(i) * time.Millisecond), ExpiresAt: now.Add(time.Hour)}, 2)
		if err != nil {
			t.Fatal(err)
		}
	}
	stats, _ := m.PluginCacheStats(context.Background(), a.ID, now)
	if stats.Entries != 2 {
		t.Fatal(stats)
	}
	if err := m.ClearPluginCache(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	stats, _ = m.PluginCacheStats(context.Background(), a.ID, now)
	if stats.Entries != 0 {
		t.Fatal(stats)
	}
	if !errors.Is(m.PutPluginCache(context.Background(), model.PluginCacheEntry{APIID: a.ID, Key: "stale", APIUpdatedAt: now}, 2), ErrConflict) {
		t.Fatal("stale generation accepted")
	}
}
