package store

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"context"
	"os"
	"strings"
	"testing"
)

func TestPostgresConfirmationPreferenceRequiresOwnedKeyAndRevision(t *testing.T) {
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
	u := model.User{ID: ids.NewUUID(), Username: "confirm" + ids.NewUUID()[:8], PasswordHash: "fixture-password-digest", Role: "member", Roles: []string{"member"}, Status: "active"}
	if e = p.CreateUser(u); e != nil {
		t.Fatal(e)
	}
	u, _ = p.GetUserByID(u.ID)
	method, e := p.ConfirmationMethod(ctx, u.ID)
	if e != nil || method != "password" {
		t.Fatal("default", e, method)
	}
	if e = p.SetConfirmationMethod(ctx, u.ID, "passkey", u.AuthRevision, "example.test"); e == nil {
		t.Fatal("passkey preference without key")
	}
	key := model.Passkey{ID: ids.NewUUID(), UserID: u.ID, RPID: "example.test", Name: "fixture", Credential: []byte(`{}`)}
	if e = p.AddPasskey(ctx, key, u.AuthRevision); e != nil {
		t.Fatal(e)
	}
	if e = p.SetConfirmationMethod(ctx, u.ID, "passkey", u.AuthRevision, "example.test"); e != nil {
		t.Fatal(e)
	}
	if e = p.SetConfirmationMethod(ctx, u.ID, "password", u.AuthRevision+1, "example.test"); e == nil {
		t.Fatal("stale state changed preference")
	}
	if e = p.DeletePasskey(ctx, u.ID, key.ID); e != nil {
		t.Fatal(e)
	}
	method, _ = p.ConfirmationMethod(ctx, u.ID)
	if method != "password" {
		t.Fatal("last key removal left unusable confirmation preference")
	}
}
