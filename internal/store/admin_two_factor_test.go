package store

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"context"
	"errors"
	"testing"
	"time"
)

func TestPostgresAdminResetTwoFactorClearsRecoveryAndRevokesSessions(t *testing.T) {
	p := accountPG(t)
	u := accountUser(t, p)
	ctx := context.Background()
	secret := "encrypted-test-secret"
	if e := p.BeginTOTP(ctx, u.ID, secret); e != nil {
		t.Fatal(e)
	}
	if e := p.EnableTOTP(ctx, u.ID, secret, 123, []string{"recovery-hash"}); e != nil {
		t.Fatal(e)
	}
	before, e := p.GetUserByID(u.ID)
	if e != nil {
		t.Fatal(e)
	}
	session := model.Session{Hash: ids.NewUUID(), UserID: u.ID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if e = p.CreateSession(session); e != nil {
		t.Fatal(e)
	}
	if e = p.ResetUserTwoFactor(ctx, u.ID, before.AuthRevision-1); !errors.Is(e, ErrConflict) {
		t.Fatal("stale reset accepted", e)
	}
	record, _ := p.Account(ctx, u.ID)
	if record.TOTPSecret == "" {
		t.Fatal("stale reset cleared MFA")
	}
	if e = p.ResetUserTwoFactor(ctx, u.ID, before.AuthRevision); e != nil {
		t.Fatal(e)
	}
	record, _ = p.Account(ctx, u.ID)
	if record.TOTPSecret != "" || record.TOTPPending != "" || len(record.RecoveryHashes) != 0 {
		t.Fatal("MFA data survived reset")
	}
	if _, e = p.GetSession(session.Hash); !errors.Is(e, ErrNotFound) {
		t.Fatal("session survived", e)
	}
	after, _ := p.GetUserByID(u.ID)
	if after.AuthRevision != before.AuthRevision+1 {
		t.Fatal("auth challenges not invalidated")
	}
	if e = p.ResetUserTwoFactor(ctx, u.ID, before.AuthRevision); !errors.Is(e, ErrConflict) {
		t.Fatal("reset replay accepted", e)
	}
}
