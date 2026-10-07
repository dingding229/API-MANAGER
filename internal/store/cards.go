package store

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"context"
	"encoding/json"
	"time"
)

func (p *Postgres) SaveCardBatch(ctx context.Context, b model.CardBatch) (model.CardBatch, error) {
	if b.Count < 1 || b.Count > 100 || len(b.Cards) != b.Count || (b.Kind != "balance" && b.Kind != "plan") || (b.Kind == "balance" && (b.AmountMicros <= 0 || b.AmountMicros > 1000000000000000)) || !b.ExpiresAt.After(time.Now()) {
		return b, ErrConflict
	}
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		return b, e
	}
	defer tx.Rollback(ctx)
	result, e := tx.Exec(ctx, `INSERT INTO card_batches(id,creator_id,operation_id,request_hash,kind,amount_micros,plan_snapshot,count,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(creator_id,operation_id) DO NOTHING`, b.ID, b.CreatorID, b.OperationID, b.RequestHash, b.Kind, b.AmountMicros, b.Plan, b.Count, b.ExpiresAt)
	if e != nil {
		return b, e
	}
	if result.RowsAffected() == 0 {
		var id, hash string
		e = tx.QueryRow(ctx, `SELECT id,request_hash FROM card_batches WHERE creator_id=$1 AND operation_id=$2`, b.CreatorID, b.OperationID).Scan(&id, &hash)
		if e != nil {
			return b, e
		}
		if hash != b.RequestHash {
			return b, ErrConflict
		}
		if e = tx.Commit(ctx); e != nil {
			return b, e
		}
		return p.CardBatch(ctx, id)
	}
	for _, v := range b.Cards {
		if _, e = tx.Exec(ctx, `INSERT INTO redeem_cards(id,batch_id,code_hash,prefix,encrypted_code) VALUES($1,$2,$3,$4,$5)`, v.ID, b.ID, v.CodeHash, v.Prefix, v.EncryptedCode); e != nil {
			return b, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return b, e
	}
	return p.CardBatch(ctx, b.ID)
}
func (p *Postgres) CardBatch(ctx context.Context, id string) (model.CardBatch, error) {
	var b model.CardBatch
	e := p.pool.QueryRow(ctx, `SELECT id,creator_id,operation_id,request_hash,kind,amount_micros,plan_snapshot,count,expires_at,created_at FROM card_batches WHERE id=$1`, id).Scan(&b.ID, &b.CreatorID, &b.OperationID, &b.RequestHash, &b.Kind, &b.AmountMicros, &b.Plan, &b.Count, &b.ExpiresAt, &b.CreatedAt)
	if e != nil {
		return b, e
	}
	rows, e := p.pool.Query(ctx, `SELECT id,batch_id,code_hash,prefix,encrypted_code,revoked,redeemed_by,redeemed_at FROM redeem_cards WHERE batch_id=$1 ORDER BY id`, id)
	if e != nil {
		return b, e
	}
	defer rows.Close()
	b.Cards = []model.RedeemCard{}
	for rows.Next() {
		var v model.RedeemCard
		if e = rows.Scan(&v.ID, &v.BatchID, &v.CodeHash, &v.Prefix, &v.EncryptedCode, &v.Revoked, &v.RedeemedBy, &v.RedeemedAt); e != nil {
			return b, e
		}
		b.Cards = append(b.Cards, v)
	}
	return b, rows.Err()
}
func (p *Postgres) CardBatches(ctx context.Context) ([]model.CardBatch, error) {
	rows, e := p.pool.Query(ctx, `SELECT id,creator_id,kind,amount_micros,plan_snapshot,count,expires_at,created_at FROM card_batches ORDER BY created_at DESC LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.CardBatch{}
	for rows.Next() {
		var b model.CardBatch
		if e = rows.Scan(&b.ID, &b.CreatorID, &b.Kind, &b.AmountMicros, &b.Plan, &b.Count, &b.ExpiresAt, &b.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (p *Postgres) RevokeCardBatch(ctx context.Context, id string) error {
	r, e := p.pool.Exec(ctx, `UPDATE redeem_cards SET revoked=TRUE WHERE batch_id=$1 AND redeemed_by IS NULL`, id)
	if e == nil && r.RowsAffected() == 0 {
		return ErrNotFound
	}
	return e
}
func (p *Postgres) RedeemCard(ctx context.Context, userID, hash string) (string, error) {
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	wallet, e := lockWallet(ctx, tx, userID)
	if e != nil {
		return "", e
	}
	var status string
	if e = tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR SHARE`, userID).Scan(&status); e != nil || status != "active" {
		return "", ErrNotFound
	}
	var id, kind string
	var amount int64
	var snapshot []byte
	var expiry time.Time
	var revoked bool
	var redeemed *string
	e = tx.QueryRow(ctx, `SELECT c.id,b.kind,b.amount_micros,b.plan_snapshot,b.expires_at,c.revoked,c.redeemed_by FROM redeem_cards c JOIN card_batches b ON b.id=c.batch_id WHERE c.code_hash=$1 FOR UPDATE OF c`, hash).Scan(&id, &kind, &amount, &snapshot, &expiry, &revoked, &redeemed)
	if e != nil {
		return "", ErrNotFound
	}
	if redeemed != nil {
		if *redeemed == userID {
			return kind, tx.Commit(ctx)
		}
		return "", ErrConflict
	}
	if revoked || !expiry.After(time.Now()) {
		return "", ErrNotFound
	}
	if kind == "balance" {
		if amount <= 0 || amount > 1000000000000000 || wallet.BalanceMicros > 1000000000000000-amount {
			return "", ErrConflict
		}
		wallet.BalanceMicros += amount
		if _, e = tx.Exec(ctx, `UPDATE wallets SET balance_micros=$2 WHERE user_id=$1`, userID, wallet.BalanceMicros); e != nil {
			return "", e
		}
	} else {
		var plan model.Plan
		if e = json.Unmarshal(snapshot, &plan); e != nil || plan.Days < 1 || plan.Days > 3650 {
			return "", ErrConflict
		}
		var count int
		var start time.Time
		if e = tx.QueryRow(ctx, `SELECT COUNT(*),COALESCE(MAX(expires_at),NOW()) FROM subscriptions WHERE user_id=$1 AND expires_at>NOW()`, userID).Scan(&count, &start); e != nil {
			return "", e
		}
		if count >= 2 {
			return "", ErrConflict
		}
		zone, e := siteBillingZone(ctx, tx)
		if e != nil {
			return "", e
		}
		loc, _ := time.LoadLocation(zone)
		end := start.In(loc).AddDate(0, 0, plan.Days).UTC()
		if _, e = tx.Exec(ctx, `INSERT INTO subscriptions(id,user_id,plan_id,plan_name,hourly,daily,monthly,starts_at,expires_at,time_zone) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, "card:"+id, userID, plan.ID, plan.Name, plan.Hourly, plan.Daily, plan.Monthly, start, end, zone); e != nil {
			return "", e
		}
	}
	if _, e = tx.Exec(ctx, `INSERT INTO wallet_ledger(id,user_id,kind,reference,note,amount_micros,balance_micros) VALUES($1,$2,$3,$4,$5,$6,$7)`, ids.NewUUID(), userID, "card_"+kind, "card:"+id, "卡密兑换", amount, wallet.BalanceMicros); e != nil {
		return "", e
	}
	if _, e = tx.Exec(ctx, `UPDATE redeem_cards SET redeemed_by=$2,redeemed_at=NOW() WHERE id=$1`, id, userID); e != nil {
		return "", e
	}
	return kind, tx.Commit(ctx)
}
func (p *Postgres) Subscriptions(ctx context.Context, id string) ([]model.Subscription, error) {
	rows, e := p.pool.Query(ctx, `SELECT id,user_id,plan_id,plan_name,hourly,daily,monthly,starts_at,expires_at,time_zone FROM subscriptions WHERE user_id=$1 AND expires_at>NOW() ORDER BY starts_at`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Subscription{}
	for rows.Next() {
		v, e := subscriptionRow(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
