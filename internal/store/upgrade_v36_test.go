package store

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"context"
	"errors"
	"testing"
	"time"
)

func TestPostgresKeyPolicyRotationAndDeletedUserIntegrity(t *testing.T) {
	p := accountPG(t)
	u := accountUser(t, p)
	ctx := context.Background()
	key := model.Credential{ID: ids.NewUUID(), OwnerUserID: u.ID, Name: "network", Prefix: "ak_test", Hash: ids.NewUUID(), EncryptedKey: "test-secret", AllowedIPRanges: []string{"8.8.8.8/32"}, CreatedAt: time.Now()}
	if e := p.CreateOwnedCredential(ctx, key); e != nil {
		t.Fatal(e)
	}
	a, e := p.GetCredential(key.ID)
	if e != nil || len(a.AllowedIPRanges) != 1 {
		t.Fatal(a, e)
	}
	rotated, e := p.RotateCredential(key.ID, "ak_next", ids.NewUUID(), "next-secret")
	if e != nil || len(rotated.AllowedIPRanges) != 1 {
		t.Fatal(rotated, e)
	}
	list, e := p.OwnCredentials(ctx, u.ID)
	if e != nil || len(list) != 1 || len(list[0].AllowedIPRanges) != 1 {
		t.Fatal(list, e)
	}
	if _, e = p.AdjustBalance(ctx, u.ID, 100, "delete-test:"+u.ID, "test"); e != nil {
		t.Fatal(e)
	}
	session := model.Session{Hash: ids.NewUUID(), UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	if e = p.CreateSession(session); e != nil {
		t.Fatal(e)
	}
	if e = p.DeleteUser(ctx, u.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = p.GetSession(session.Hash); !errors.Is(e, ErrNotFound) {
		t.Fatal("session survived", e)
	}
	a, _ = p.GetCredential(key.ID)
	if !a.Revoked {
		t.Fatal("key survived")
	}
	wallet, e := p.Wallet(ctx, u.ID)
	if e != nil || wallet.BalanceMicros != 100 {
		t.Fatal("financial data lost", wallet, e)
	}
	for _, v := range p.ListUsers() {
		if v.ID == u.ID {
			t.Fatal("deleted account still listed")
		}
	}
	if e = p.UpdateUserStatus(u.ID, "active"); !errors.Is(e, ErrNotFound) {
		t.Fatal("deleted user reactivated", e)
	}
}
func TestPostgresVersionSettingsOptimisticAndEncrypted(t *testing.T) {
	p := accountPG(t)
	ctx := context.Background()
	v, e := p.VersionCheckSettings(ctx)
	if e != nil && !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	saved, e := p.SaveVersionCheckSettings(ctx, v.Version, "ciphertext")
	if e != nil || saved.EncryptedToken != "ciphertext" {
		t.Fatal(saved, e)
	}
	if _, e = p.SaveVersionCheckSettings(ctx, v.Version, "other"); !errors.Is(e, ErrConflict) {
		t.Fatal("stale update accepted", e)
	}
	read, e := p.VersionCheckSettings(ctx)
	if e != nil || read.Version != saved.Version {
		t.Fatal(read, e)
	}
}

func TestPostgresAPIOwnerSurvivesEditAndRelease(t *testing.T) {
	p := accountPG(t)
	u := accountUser(t, p)
	n := time.Now().UTC()
	a := model.API{ID: ids.NewUUID(), OwnerUserID: u.ID, Name: "owner", Method: "GET", Path: "/api/test-" + ids.NewUUID(), AuthMode: "none", ResponseStatus: 200, CreatedAt: n, UpdatedAt: n}
	if e := p.CreateAPI(a); e != nil {
		t.Fatal(e)
	}
	a.Name = "changed"
	a.OwnerUserID = "forged"
	if e := p.UpdateAPI(a); e != nil {
		t.Fatal(e)
	}
	read, e := p.GetAPI(a.ID)
	if e != nil || read.OwnerUserID != u.ID {
		t.Fatal(read, e)
	}
	if _, e = p.UpdateAndRelease(read); e != nil {
		t.Fatal(e)
	}
	read, _ = p.GetAPI(a.ID)
	if read.OwnerUserID != u.ID {
		t.Fatal("ownership changed")
	}
}
