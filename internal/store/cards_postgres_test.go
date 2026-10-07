package store

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sampleCards(t *testing.T, p *Postgres, creator model.User, kind string, amount int64, plan *model.Plan) model.CardBatch {
	t.Helper()
	b := model.CardBatch{ID: ids.NewUUID(), CreatorID: creator.ID, OperationID: ids.NewUUID(), RequestHash: "request", Kind: kind, AmountMicros: amount, Count: 1, ExpiresAt: time.Now().Add(time.Hour), Cards: []model.RedeemCard{{ID: ids.NewUUID(), CodeHash: ids.NewUUID(), Prefix: "cd_test", EncryptedCode: "encrypted"}}}
	if plan != nil {
		b.Plan, _ = json.Marshal(plan)
	}
	b, e := p.SaveCardBatch(context.Background(), b)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestCardRedemptionIsConcurrentAtomicAndIdempotent(t *testing.T) {
	p := accountPG(t)
	ctx := context.Background()
	creator := accountUser(t, p)
	one := accountUser(t, p)
	two := accountUser(t, p)
	b := sampleCards(t, p, creator, "balance", 1000, nil)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for _, u := range []model.User{one, two} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, e := p.RedeemCard(ctx, id, b.Cards[0].CodeHash); e == nil {
				wins.Add(1)
			}
		}(u.ID)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("double redemption", wins.Load())
	}
	card, e := p.CardBatch(ctx, b.ID)
	if e != nil || card.Cards[0].RedeemedBy == nil {
		t.Fatal(card, e)
	}
	winner := *card.Cards[0].RedeemedBy
	before, e := p.Wallet(ctx, winner)
	if e != nil || before.BalanceMicros != 1000 {
		t.Fatal(before, e)
	}
	if _, e = p.RedeemCard(ctx, winner, b.Cards[0].CodeHash); e != nil {
		t.Fatal(e)
	}
	after, e := p.Wallet(ctx, winner)
	if e != nil || after.BalanceMicros != before.BalanceMicros {
		t.Fatal("retry credited twice", after, e)
	}
	ledger, e := p.Ledger(ctx, winner)
	if e != nil || len(ledger) != 1 {
		t.Fatal(ledger, e)
	}
	raw, _ := json.Marshal(card)
	if string(raw) == "" || containsCardSecret(string(raw), b.Cards[0].CodeHash) {
		t.Fatal("list exposed secret")
	}
}
func containsCardSecret(raw, secret string) bool {
	for i := 0; i+len(secret) <= len(raw); i++ {
		if raw[i:i+len(secret)] == secret {
			return true
		}
	}
	return false
}
func TestCardExpiryRevocationCreationRetryAndPlanRenewal(t *testing.T) {
	p := accountPG(t)
	ctx := context.Background()
	u := accountUser(t, p)
	b := sampleCards(t, p, u, "balance", 10, nil)
	retry, e := p.SaveCardBatch(ctx, b)
	if e != nil || retry.ID != b.ID {
		t.Fatal(retry, e)
	}
	if e = p.RevokeCardBatch(ctx, b.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = p.RedeemCard(ctx, u.ID, b.Cards[0].CodeHash); e == nil {
		t.Fatal("revoked card redeemed")
	}
	plan := model.Plan{ID: ids.NewUUID(), Name: "card plan", Days: 30, Daily: 10, Enabled: true}
	if e = p.SavePlan(ctx, plan); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		card := sampleCards(t, p, u, "plan", 0, &plan)
		if _, e = p.RedeemCard(ctx, u.ID, card.Cards[0].CodeHash); e != nil {
			t.Fatal(e)
		}
	}
	subs, e := p.Subscriptions(ctx, u.ID)
	if e != nil || len(subs) != 2 || !subs[1].StartsAt.Equal(subs[0].ExpiresAt) {
		t.Fatal("renewal overlaps or lost", subs, e)
	}
	third := sampleCards(t, p, u, "plan", 0, &plan)
	if _, e = p.RedeemCard(ctx, u.ID, third.Cards[0].CodeHash); e == nil {
		t.Fatal("unbounded queued renewals")
	}
	check, e := p.CardBatch(ctx, third.ID)
	if e != nil || check.Cards[0].RedeemedBy != nil {
		t.Fatal("failed redemption burned card", check, e)
	}
	plan.Daily = 999
	if e = p.SavePlan(ctx, plan); e != nil {
		t.Fatal(e)
	}
	if subs[0].Daily != 10 {
		t.Fatal("issued plan snapshot changed")
	}
	if _, e = p.pool.Exec(ctx, `UPDATE card_batches SET expires_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, third.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = p.RedeemCard(ctx, u.ID, third.Cards[0].CodeHash); e == nil {
		t.Fatal("expired card redeemed")
	}
}

func TestPluginSettingsStoreCASAndVersionIsolation(t *testing.T) {
	p := accountPG(t)
	ctx := context.Background()
	one := model.Plugin{ID: ids.NewUUID(), Name: "settings-test", Version: ids.NewUUID(), Runtime: "wasm", Manifest: json.RawMessage(`{}`), CreatedAt: time.Now()}
	two := one
	two.ID = ids.NewUUID()
	two.Version = ids.NewUUID()
	for _, v := range []model.Plugin{one, two} {
		if e := p.CreatePlugin(v); e != nil {
			t.Fatal(e)
		}
	}
	version, e := p.SavePluginSettings(ctx, one.ID, "cipher-not-plaintext", 0)
	if e != nil || version != 1 {
		t.Fatal(version, e)
	}
	raw, other, e := p.PluginSettings(ctx, two.ID)
	if e != nil || other != 0 || raw != "" {
		t.Fatal("settings crossed versions", raw, other, e)
	}
	if _, e = p.SavePluginSettings(ctx, one.ID, "overwrite", 0); e == nil {
		t.Fatal("stale configuration overwrote")
	}
	version, e = p.SavePluginSettings(ctx, one.ID, "new-cipher", 1)
	if e != nil || version != 2 {
		t.Fatal(version, e)
	}
	if e = p.DeletePlugin(one.ID); e != nil {
		t.Fatal(e)
	}
	raw, version, e = p.PluginSettings(ctx, one.ID)
	if e != nil || version != 0 || raw != "" {
		t.Fatal("deleted plugin retained settings", version, e)
	}
}
