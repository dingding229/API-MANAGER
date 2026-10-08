package store

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresPluginPolicyAndSessionsIsolationCASQuotaExpiry(t *testing.T) {
	dsn := os.Getenv("TEST_ACCOUNT_DSN")
	if dsn == "" {
		t.Skip("isolated database required")
	}
	if !strings.Contains(dsn, "/api_account_review_") {
		t.Fatal("disposable database required")
	}
	ctx := context.Background()
	p, e := NewPostgres(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	id := "plugin_" + ids.NewUUID()
	other := "plugin_" + ids.NewUUID()
	for _, v := range []string{id, other} {
		if e = p.CreatePlugin(model.Plugin{ID: v, Name: v, Version: "1.0.0", Runtime: "wasm", Manifest: []byte(`{}`)}); e != nil {
			t.Fatal(e)
		}
	}
	policy := model.PluginRuntimePolicy{SessionPersistence: true}
	version, e := p.SavePluginRuntimePolicy(ctx, id, policy, 0)
	if e != nil || version != 1 {
		t.Fatal("policy init", e)
	}
	if _, e = p.SavePluginRuntimePolicy(ctx, id, policy, 0); !errors.Is(e, ErrConflict) {
		t.Fatal("policy CAS", e)
	}
	future := time.Now().Add(time.Hour)
	entry := model.PluginSession{PluginID: id, KeyHash: strings.Repeat("a", 64), EncryptedValue: "fixture-ciphertext-not-a-real-secret", ExpiresAt: &future}
	ver, e := p.SavePluginSession(ctx, entry, 0, 1, version)
	if e != nil || ver < 1 {
		t.Fatal("session put", e)
	}
	if _, e = p.SavePluginSession(ctx, entry, 0, 1, version); !errors.Is(e, ErrConflict) {
		t.Fatal("session overwrite omitted version", e)
	}
	if _, e = p.PluginSession(ctx, other, entry.KeyHash); !errors.Is(e, ErrNotFound) {
		t.Fatal("session crossed plugin owner", e)
	}
	extra := entry
	extra.KeyHash = strings.Repeat("b", 64)
	if _, e = p.SavePluginSession(ctx, extra, 0, 1, version); !errors.Is(e, ErrConflict) {
		t.Fatal("session quota bypass", e)
	}
	previousVersion := ver
	ver, e = p.SavePluginSession(ctx, entry, ver, 1, version)
	if e != nil || ver <= previousVersion {
		t.Fatal("session CAS update", e)
	}
	if e = p.DeletePluginSession(ctx, id, entry.KeyHash, previousVersion); !errors.Is(e, ErrConflict) {
		t.Fatal("delete stale version", e)
	}
	if e = p.DeletePluginSession(ctx, id, entry.KeyHash, ver); e != nil {
		t.Fatal(e)
	}
	past := time.Now().Add(-time.Second)
	entry.ExpiresAt = &past
	if _, e = p.SavePluginSession(ctx, entry, 0, 1, version); e != nil {
		t.Fatal(e)
	}
	if _, e = p.PluginSession(ctx, id, entry.KeyHash); !errors.Is(e, ErrNotFound) {
		t.Fatal("expired session visible", e)
	}
	policy.SessionPersistence = false
	if _, e = p.SavePluginRuntimePolicy(ctx, id, policy, 1); e != nil {
		t.Fatal(e)
	}
	if _, e = p.SavePluginSession(ctx, extra, 0, 1, 1); !errors.Is(e, ErrConflict) {
		t.Fatal("revoked policy permitted persistence", e)
	}
}
