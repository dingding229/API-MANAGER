package store

import (
	"api-manager/internal/auth"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresPasskeyOwnershipCounterCASLimitAndDeletion(t *testing.T) {
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
	u := model.User{ID: ids.NewUUID(), Username: "pkey" + ids.NewUUID()[:8], PasswordHash: "test-password-hash", Role: "member", Roles: []string{"member"}, Status: "active"}
	if e = p.CreateUser(u); e != nil {
		t.Fatal(e)
	}
	u, e = p.GetUserByID(u.ID)
	if e != nil {
		t.Fatal(e)
	}
	raw := json.RawMessage(`{"id":"Y3JlZA","publicKey":"cHVibGljLWtleQ","flags":{"userPresent":true,"userVerified":true},"authenticator":{"signCount":1}}`)
	key := model.Passkey{ID: ids.NewUUID(), UserID: u.ID, RPID: "example.test", Name: "<svg onload=alert(1)>", Credential: raw}
	if e = p.AddPasskey(ctx, key, u.AuthRevision); e != nil {
		t.Fatal(e)
	}
	if e = p.AddPasskey(ctx, key, u.AuthRevision); e == nil {
		t.Fatal("duplicate credential accepted")
	}
	list, e := p.Passkeys(ctx, u.ID, "example.test")
	if e != nil || len(list) != 1 {
		t.Fatal("owner listing", e)
	}
	record, e := p.Passkey(ctx, key.ID, "example.test")
	if e != nil {
		t.Fatal(e)
	}
	if e = p.UsePasskey(ctx, record, u.AuthRevision+1); !errors.Is(e, ErrConflict) {
		t.Fatal("stale user state accepted", e)
	}
	if e = p.UsePasskey(ctx, record, u.AuthRevision); e != nil {
		t.Fatal(e)
	}
	if e = p.UsePasskey(ctx, record, u.AuthRevision); !errors.Is(e, ErrConflict) {
		t.Fatal("stale credential counter accepted", e)
	}
	if e = p.DeletePasskey(ctx, ids.NewUUID(), key.ID); e == nil {
		t.Fatal("other owner deleted credential")
	}
	for i := 1; i < 20; i++ {
		k := key
		k.ID = ids.NewUUID()
		if e = p.AddPasskey(ctx, k, u.AuthRevision); e != nil {
			t.Fatal(e)
		}
	}
	key.ID = ids.NewUUID()
	if e = p.AddPasskey(ctx, key, u.AuthRevision); !errors.Is(e, ErrConflict) {
		t.Fatal("credential limit not enforced", e)
	}
	// Persistence paths revoke all active sessions when an authenticator is removed.
	token := "session-before-passkey-deletion"
	if e = p.CreateSession(model.Session{Hash: auth.HashAPIKey(token), UserID: u.ID, AuthRevision: u.AuthRevision, ExpiresAt: time.Now().Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
	if e = p.DeletePasskey(ctx, u.ID, list[0].ID); e != nil {
		t.Fatal(e)
	}
	after, e := p.GetUserByID(u.ID)
	if e != nil || after.AuthRevision <= u.AuthRevision {
		t.Fatal("deletion did not change auth revision")
	}
	if _, e = p.Passkey(ctx, list[0].ID, "example.test"); e == nil {
		t.Fatal("deleted credential still usable")
	}
}
func TestDatabaseHiddenExtractionPreservesMaskedFieldsAndDoesNotMutateRows(t *testing.T) {
	row := map[string]any{"id": "public-id", "password_hash": "one-way-hash", "settings": map[string]any{"password": "secret-html-<svg>", "title": "public"}, "encrypted_key": "ciphertext", "token_hash": "hash"}
	fields := DatabaseHiddenValues(row)
	if fields["password_hash"] != "one-way-hash" || fields["settings.password"] != "secret-html-<svg>" || fields["encrypted_key"] != "ciphertext" || fields["token_hash"] != "hash" {
		t.Fatal("hidden values omitted")
	}
	if _, ok := fields["id"]; ok {
		t.Fatal("unrelated field disclosed")
	}
	if row["password_hash"] != "one-way-hash" {
		t.Fatal("record mutated")
	}
	redactDatabaseValue(row)
	if row["password_hash"] != "[已隐藏]" {
		t.Fatal("ordinary browse no longer redacts secrets")
	}
}
