package store

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func accountPG(t *testing.T) *Postgres {
	t.Helper()
	dsn := os.Getenv("TEST_ACCOUNT_DSN")
	if dsn == "" {
		t.Skip("isolated account database not configured")
	}
	if !strings.Contains(dsn, "/api_account_review_") {
		t.Fatal("account tests require disposable database")
	}
	p, e := NewPostgres(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(p.Close)
	return p
}
func accountUser(t *testing.T, p *Postgres) model.User {
	t.Helper()
	n := time.Now().UTC()
	id := ids.NewUUID()
	u := model.User{ID: id, UID: id, Username: "u-" + id, Email: id + "@example.test", Nickname: "会员", Role: "member", Roles: []string{"member"}, Status: "active", PasswordHash: "test-hash", CreatedAt: n, UpdatedAt: n}
	if e := p.CreateUser(u); e != nil {
		t.Fatal(e)
	}
	return u
}
func TestAccountBillingConcurrentReservations(t *testing.T) {
	p := accountPG(t)
	u := accountUser(t, p)
	ctx := context.Background()
	if _, e := p.AdjustBalance(ctx, u.ID, 100, "credit:"+u.ID, "test"); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var accepted atomic.Int64
	charges := make(chan model.Charge, 40)
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := p.BeginCharge(ctx, u.ID, "test-api", 10, ids.NewUUID(), time.Now())
			if e == nil {
				accepted.Add(1)
				charges <- c
			} else if !errors.Is(e, ErrFunds) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	close(charges)
	if accepted.Load() != 10 {
		t.Fatalf("overspend or lost reservations: %d", accepted.Load())
	}
	w, e := p.Wallet(ctx, u.ID)
	if e != nil || w.BalanceMicros != 100 || w.HeldMicros != 100 {
		t.Fatal(w, e)
	}
	for c := range charges {
		if e = p.FinishCharge(ctx, c, true); e != nil {
			t.Fatal(e)
		}
		if e = p.FinishCharge(ctx, c, false); e != nil {
			t.Fatal(e)
		}
	}
	w, e = p.Wallet(ctx, u.ID)
	if e != nil || w.BalanceMicros != 0 || w.HeldMicros != 0 {
		t.Fatal(w, e)
	}
	ledger, e := p.Ledger(ctx, u.ID)
	if e != nil || len(ledger) != 11 {
		t.Fatal(len(ledger), e)
	}
	if _, e = p.AdjustBalance(ctx, u.ID, 100, "credit:"+u.ID, "retry"); e != nil {
		t.Fatal(e)
	}
	if _, e = p.AdjustBalance(ctx, u.ID, 200, "credit:"+u.ID, "collision"); !errors.Is(e, ErrConflict) {
		t.Fatal("idempotency payload collision", e)
	}
}
func TestAccountPlansQuotaRefundSnapshotAndRenewal(t *testing.T) {
	p := accountPG(t)
	u := accountUser(t, p)
	ctx := context.Background()
	plan := model.Plan{ID: ids.NewUUID(), Name: "基础", PriceMicros: 100, Days: 30, Hourly: 2, Daily: 3, Monthly: 4, Enabled: true}
	if e := p.SavePlan(ctx, plan); e != nil {
		t.Fatal(e)
	}
	if _, e := p.AdjustBalance(ctx, u.ID, 1000, "credit:"+u.ID, "test"); e != nil {
		t.Fatal(e)
	}
	sub, e := p.PurchasePlan(ctx, u.ID, plan.ID, "purchase:"+u.ID)
	if e != nil {
		t.Fatal(e)
	}
	retry, e := p.PurchasePlan(ctx, u.ID, plan.ID, "purchase:"+u.ID)
	if e != nil || retry.ID != sub.ID {
		t.Fatal(retry, e)
	}
	other, e := p.PurchasePlan(ctx, u.ID, plan.ID, "renew:"+u.ID)
	if e != nil || !other.StartsAt.Equal(sub.ExpiresAt) {
		t.Fatal("renewal overlaps", other, e)
	}
	plan.Hourly = 100
	if e = p.SavePlan(ctx, plan); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	c1, e := p.BeginCharge(ctx, u.ID, "api", 999, ids.NewUUID(), now)
	if e != nil || c1.PriceMicros != 0 {
		t.Fatal(c1, e)
	}
	c2, e := p.BeginCharge(ctx, u.ID, "api", 999, ids.NewUUID(), now)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.BeginCharge(ctx, u.ID, "api", 999, ids.NewUUID(), now); !errors.Is(e, ErrPlanQuota) {
		t.Fatal("snapshot/hour quota", e)
	}
	if e = p.FinishCharge(ctx, c1, false); e != nil {
		t.Fatal(e)
	}
	if e = p.FinishCharge(ctx, c1, false); e != nil {
		t.Fatal(e)
	}
	if _, e = p.BeginCharge(ctx, u.ID, "api", 999, ids.NewUUID(), now); e != nil {
		t.Fatal("failed call quota not released", e)
	}
	if e = p.FinishCharge(ctx, c2, true); e != nil {
		t.Fatal(e)
	}
	w, e := p.Wallet(ctx, u.ID)
	if e != nil || w.BalanceMicros != 800 || w.HeldMicros != 0 {
		t.Fatal(w, e)
	}
}
func TestAccountUncertainChargesRetainHold(t *testing.T) {
	p := accountPG(t)
	u := accountUser(t, p)
	ctx := context.Background()
	if _, e := p.AdjustBalance(ctx, u.ID, 100, "credit:"+u.ID, "test"); e != nil {
		t.Fatal(e)
	}
	c, e := p.BeginCharge(ctx, u.ID, "api", 30, ids.NewUUID(), time.Now().Add(-time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	if e = p.ReconcileCharges(ctx); e != nil {
		t.Fatal(e)
	}
	w, e := p.Wallet(ctx, u.ID)
	if e != nil || w.HeldMicros != 30 || w.BalanceMicros != 100 {
		t.Fatal("uncertain result automatically refunded", w, e)
	}
	if e = p.FinishCharge(ctx, c, true); e != nil {
		t.Fatal(e)
	}
	w, _ = p.Wallet(ctx, u.ID)
	if w.BalanceMicros != 70 || w.HeldMicros != 0 {
		t.Fatal(w)
	}
}
func TestAccountTOTPAtomicAndSessionRevision(t *testing.T) {
	p := accountPG(t)
	u := accountUser(t, p)
	ctx := context.Background()
	sess := model.Session{ID: ids.NewUUID(), Hash: "session-" + u.ID, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour), AuthenticatedUsername: u.Username, AuthenticatedEmail: u.Email, AuthenticatedPasswordHash: u.PasswordHash}
	if e := p.CreateSession(sess); e != nil {
		t.Fatal(e)
	}
	if e := p.BeginTOTP(ctx, u.ID, "encrypted-test"); e != nil {
		t.Fatal(e)
	}
	if e := p.EnableTOTP(ctx, u.ID, "wrong", 10, []string{"recovery-hash"}); !errors.Is(e, ErrConflict) {
		t.Fatal("stale pending secret enabled", e)
	}
	if e := p.EnableTOTP(ctx, u.ID, "encrypted-test", 10, []string{"recovery-hash"}); e != nil {
		t.Fatal(e)
	}
	if _, e := p.GetSession(sess.Hash); !errors.Is(e, ErrNotFound) {
		t.Fatal("security change did not revoke session", e)
	}
	if e := p.CreateSession(sess); !errors.Is(e, ErrConflict) {
		t.Fatal("pre-security-change login creates session", e)
	}
	if e := p.AcceptTOTP(ctx, u.ID, "encrypted-test", 10, ""); !errors.Is(e, ErrConflict) {
		t.Fatal("replayed step accepted", e)
	}
	var wg sync.WaitGroup
	var used atomic.Int64
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p.AcceptTOTP(ctx, u.ID, "encrypted-test", 0, "recovery-hash") == nil {
				used.Add(1)
			}
		}()
	}
	wg.Wait()
	if used.Load() != 1 {
		t.Fatal("recovery code not single-use", used.Load())
	}
	if e := p.DisableTOTP(ctx, u.ID, "stale"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e := p.DisableTOTP(ctx, u.ID, "encrypted-test"); e != nil {
		t.Fatal(e)
	}
}
func TestAccountVerificationSingleUseAttemptsAndOwnership(t *testing.T) {
	p := accountPG(t)
	ctx := context.Background()
	id := ids.NewUUID()
	v := model.Verification{ID: id, Purpose: "login", Subject: id, CodeHash: "correct", Binding: "device", ExpiresAt: time.Now().Add(time.Minute)}
	if e := p.PutVerification(ctx, v); e != nil {
		t.Fatal(e)
	}
	for range 5 {
		if _, e := p.ConsumeVerification(ctx, id, "login", "wrong", "device"); !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
	}
	if _, e := p.ConsumeVerification(ctx, id, "login", "correct", "device"); !errors.Is(e, ErrConflict) {
		t.Fatal("attempt lockout ignored", e)
	}
	v.ID = ids.NewUUID()
	if e := p.PutVerification(ctx, v); e != nil {
		t.Fatal(e)
	}
	if _, e := p.ConsumeVerification(ctx, v.ID, "login", "correct", "other"); !errors.Is(e, ErrConflict) {
		t.Fatal("device binding ignored", e)
	}
	if _, e := p.ConsumeVerification(ctx, v.ID, "login", "correct", "device"); e != nil {
		t.Fatal(e)
	}
	if _, e := p.ConsumeVerification(ctx, v.ID, "login", "correct", "device"); e == nil {
		t.Fatal("verification replay accepted")
	}
	u := accountUser(t, p)
	other := accountUser(t, p)
	key := model.Credential{ID: ids.NewUUID(), Name: "owned", OwnerUserID: u.ID, Hash: ids.NewUUID(), Prefix: "ak_test", CreatedAt: time.Now()}
	if e := p.CreateCredential(key); e != nil {
		t.Fatal(e)
	}
	own, e := p.OwnCredentials(ctx, u.ID)
	if e != nil || len(own) != 1 || own[0].Hash != "" || own[0].EncryptedKey != "" {
		t.Fatal(own, e)
	}
	none, e := p.OwnCredentials(ctx, other.ID)
	if e != nil || len(none) != 0 {
		t.Fatal("cross-account keys", none, e)
	}
}
func TestAccountBillingWindowTimezone(t *testing.T) {
	s := windowStarts(time.Date(2026, 10, 6, 18, 25, 0, 0, time.UTC))
	if s[1].UTC().Hour() != 16 || s[1].Day() != 7 || s[2].Day() != 1 {
		t.Fatal(s)
	}
}

func TestAccountAPIPriceCredentialRotationAndSettingsCAS(t *testing.T) {
	p := accountPG(t)
	u := accountUser(t, p)
	ctx := context.Background()
	now := time.Now().UTC()
	a := model.API{ID: ids.NewUUID(), Name: "priced", Method: "GET", Methods: []string{"GET", "POST"}, Path: "/api/price-test", AuthMode: "api_key", PriceMicros: 12345, CreatedAt: now, UpdatedAt: now}
	if e := p.CreateAPI(a); e != nil {
		t.Fatal(e)
	}
	stored, e := p.GetAPI(a.ID)
	if e != nil || stored.PriceMicros != 12345 || len(stored.HTTPMethods()) != 2 {
		t.Fatal(stored, e)
	}
	stored.PriceMicros = 678
	if e = p.UpdateAPI(stored); e != nil {
		t.Fatal(e)
	}
	stored, e = p.GetAPI(a.ID)
	if e != nil || stored.PriceMicros != 678 {
		t.Fatal(stored, e)
	}
	c := model.Credential{ID: ids.NewUUID(), OwnerUserID: u.ID, Name: "owner", Prefix: "ak_old", Hash: ids.NewUUID(), CreatedAt: now}
	if e = p.CreateCredential(c); e != nil {
		t.Fatal(e)
	}
	rotated, e := p.RotateCredential(c.ID, "ak_new", ids.NewUUID(), "encrypted")
	if e != nil || rotated.OwnerUserID != u.ID {
		t.Fatal(rotated, e)
	}
	settings, _, e := p.SecuritySettings(ctx)
	if e != nil {
		t.Fatal(e)
	}
	settings.DefaultRole = "member"
	if e = p.SaveSecuritySettings(ctx, settings, "encrypted-test"); e != nil {
		t.Fatal(e)
	}
	if e = p.SaveSecuritySettings(ctx, settings, "overwritten"); !errors.Is(e, ErrConflict) {
		t.Fatal("concurrent setting overwrote secrets", e)
	}
}
