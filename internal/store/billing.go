package store

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

var ErrFunds = errors.New("insufficient balance")
var ErrPlanQuota = errors.New("subscription quota exceeded")

type BillingStore interface {
	Wallet(context.Context, string) (model.Wallet, error)
	AdjustBalance(context.Context, string, int64, string, string) (model.Wallet, error)
	Ledger(context.Context, string) ([]model.Ledger, error)
	Plans(context.Context, bool) ([]model.Plan, error)
	SavePlan(context.Context, model.Plan) error
	PurchasePlan(context.Context, string, string, string) (model.Subscription, error)
	Subscription(context.Context, string) (*model.Subscription, error)
	BeginCharge(context.Context, string, string, int64, string, time.Time) (model.Charge, error)
	FinishCharge(context.Context, model.Charge, bool) error
	SaveCallLog(context.Context, model.CallLog) error
	CallLogs(context.Context, string) ([]model.CallLog, error)
}

func windowStarts(now time.Time) []time.Time { return windowStartsIn(now, model.DefaultTimeZone) }
func windowStartsIn(now time.Time, zone string) []time.Time {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.FixedZone("billing", 8*3600)
	}
	n := now.In(loc)
	return []time.Time{time.Date(n.Year(), n.Month(), n.Day(), n.Hour(), 0, 0, 0, loc), time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc), time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc)}
}
func siteBillingZone(ctx context.Context, tx pgx.Tx) (string, error) {
	var zone string
	err := tx.QueryRow(ctx, `SELECT COALESCE(settings->'site'->>'time_zone','Asia/Shanghai') FROM site_settings WHERE id=1`).Scan(&zone)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.DefaultTimeZone, nil
	}
	if err != nil {
		return "", err
	}
	return model.NormalizeTimeZone(zone)
}

func (p *Postgres) Wallet(ctx context.Context, id string) (model.Wallet, error) {
	w := model.Wallet{Currency: "CNY"}
	err := p.pool.QueryRow(ctx, `SELECT balance_micros,held_micros FROM wallets WHERE user_id=$1`, id).Scan(&w.BalanceMicros, &w.HeldMicros)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return w, err
}
func lockWallet(ctx context.Context, tx pgx.Tx, id string) (model.Wallet, error) {
	var w model.Wallet
	_, err := tx.Exec(ctx, `INSERT INTO wallets(user_id) VALUES($1) ON CONFLICT DO NOTHING`, id)
	if err != nil {
		return w, err
	}
	err = tx.QueryRow(ctx, `SELECT balance_micros,held_micros FROM wallets WHERE user_id=$1 FOR UPDATE`, id).Scan(&w.BalanceMicros, &w.HeldMicros)
	w.Currency = "CNY"
	return w, err
}
func (p *Postgres) AdjustBalance(ctx context.Context, id string, amount int64, ref, note string) (model.Wallet, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return model.Wallet{}, err
	}
	defer tx.Rollback(ctx)
	w, err := lockWallet(ctx, tx, id)
	if err != nil {
		return w, err
	}
	var priorID, priorKind string
	var priorAmount int64
	err = tx.QueryRow(ctx, `SELECT user_id,kind,amount_micros FROM wallet_ledger WHERE reference=$1`, ref).Scan(&priorID, &priorKind, &priorAmount)
	if err == nil {
		if priorID != id || priorKind != "adjustment" || priorAmount != amount {
			return w, ErrConflict
		}
		return w, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return w, err
	}
	if amount > 1000000000000000 || amount < -1000000000000000 || w.BalanceMicros > 1000000000000000-amount || w.BalanceMicros+amount < w.HeldMicros {
		return w, ErrFunds
	}
	w.BalanceMicros += amount
	if _, err = tx.Exec(ctx, `UPDATE wallets SET balance_micros=$2 WHERE user_id=$1`, id, w.BalanceMicros); err != nil {
		return w, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wallet_ledger(id,user_id,kind,reference,note,amount_micros,balance_micros) VALUES($1,$2,'adjustment',$3,$4,$5,$6)`, ids.NewUUID(), id, ref, note, amount, w.BalanceMicros); err != nil {
		return w, err
	}
	return w, tx.Commit(ctx)
}
func (p *Postgres) Ledger(ctx context.Context, id string) ([]model.Ledger, error) {
	rows, err := p.pool.Query(ctx, `SELECT id,user_id,kind,reference,note,amount_micros,balance_micros,created_at FROM wallet_ledger WHERE user_id=$1 ORDER BY created_at DESC LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []model.Ledger{}
	for rows.Next() {
		var v model.Ledger
		if err := rows.Scan(&v.ID, &v.UserID, &v.Kind, &v.Reference, &v.Note, &v.AmountMicros, &v.BalanceMicros, &v.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, rows.Err()
}
func (p *Postgres) Plans(ctx context.Context, all bool) ([]model.Plan, error) {
	rows, err := p.pool.Query(ctx, `SELECT id,name,description,price_micros,days,hourly,daily,monthly,enabled,updated_at FROM plans WHERE enabled=TRUE OR $1 ORDER BY price_micros,id`, all)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []model.Plan{}
	for rows.Next() {
		var v model.Plan
		if err := rows.Scan(&v.ID, &v.Name, &v.Description, &v.PriceMicros, &v.Days, &v.Hourly, &v.Daily, &v.Monthly, &v.Enabled, &v.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, rows.Err()
}
func (p *Postgres) SavePlan(ctx context.Context, v model.Plan) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO plans(id,name,description,price_micros,days,hourly,daily,monthly,enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,description=EXCLUDED.description,price_micros=EXCLUDED.price_micros,days=EXCLUDED.days,hourly=EXCLUDED.hourly,daily=EXCLUDED.daily,monthly=EXCLUDED.monthly,enabled=EXCLUDED.enabled,updated_at=NOW()`, v.ID, v.Name, v.Description, v.PriceMicros, v.Days, v.Hourly, v.Daily, v.Monthly, v.Enabled)
	return err
}
func subscriptionRow(row pgx.Row) (model.Subscription, error) {
	var s model.Subscription
	err := row.Scan(&s.ID, &s.UserID, &s.PlanID, &s.PlanName, &s.Hourly, &s.Daily, &s.Monthly, &s.StartsAt, &s.ExpiresAt, &s.TimeZone)
	return s, err
}
func (p *Postgres) Subscription(ctx context.Context, id string) (*model.Subscription, error) {
	v, err := subscriptionRow(p.pool.QueryRow(ctx, `SELECT id,user_id,plan_id,plan_name,hourly,daily,monthly,starts_at,expires_at,time_zone FROM subscriptions WHERE user_id=$1 AND starts_at<=NOW() AND expires_at>NOW() ORDER BY expires_at DESC LIMIT 1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &v, err
}
func (p *Postgres) PurchasePlan(ctx context.Context, id, planID, ref string) (model.Subscription, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return model.Subscription{}, err
	}
	defer tx.Rollback(ctx)
	w, err := lockWallet(ctx, tx, id)
	if err != nil {
		return model.Subscription{}, err
	}
	old, e := subscriptionRow(tx.QueryRow(ctx, `SELECT id,user_id,plan_id,plan_name,hourly,daily,monthly,starts_at,expires_at,time_zone FROM subscriptions WHERE id=$1 AND user_id=$2`, ref, id))
	if e == nil {
		if old.PlanID != planID {
			return old, ErrConflict
		}
		return old, tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return old, e
	}

	var plan model.Plan
	err = tx.QueryRow(ctx, `SELECT id,name,price_micros,days,hourly,daily,monthly FROM plans WHERE id=$1 AND enabled=TRUE FOR SHARE`, planID).Scan(&plan.ID, &plan.Name, &plan.PriceMicros, &plan.Days, &plan.Hourly, &plan.Daily, &plan.Monthly)
	if err != nil {
		return model.Subscription{}, err
	}
	if w.BalanceMicros-w.HeldMicros < plan.PriceMicros {
		return model.Subscription{}, ErrFunds
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM subscriptions WHERE user_id=$1 AND expires_at>NOW()`, id).Scan(&count); err != nil {
		return model.Subscription{}, err
	}
	if count >= 2 {
		return model.Subscription{}, ErrConflict
	}
	n := time.Now().UTC().Truncate(time.Microsecond)
	if count == 1 {
		if err = tx.QueryRow(ctx, `SELECT expires_at FROM subscriptions WHERE user_id=$1 AND expires_at>NOW() ORDER BY expires_at DESC LIMIT 1`, id).Scan(&n); err != nil {
			return model.Subscription{}, err
		}
	}

	zone, e := siteBillingZone(ctx, tx)
	if e != nil {
		return model.Subscription{}, e
	}
	loc, _ := time.LoadLocation(zone)
	s := model.Subscription{TimeZone: zone, ID: ref, UserID: id, PlanID: plan.ID, PlanName: plan.Name, Hourly: plan.Hourly, Daily: plan.Daily, Monthly: plan.Monthly, StartsAt: n, ExpiresAt: n.In(loc).AddDate(0, 0, plan.Days).UTC()}
	_, err = tx.Exec(ctx, `INSERT INTO subscriptions(id,user_id,plan_id,plan_name,hourly,daily,monthly,starts_at,expires_at,time_zone) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, s.ID, id, s.PlanID, s.PlanName, s.Hourly, s.Daily, s.Monthly, s.StartsAt, s.ExpiresAt, s.TimeZone)
	if err != nil {
		return s, err
	}
	w.BalanceMicros -= plan.PriceMicros
	if _, err = tx.Exec(ctx, `UPDATE wallets SET balance_micros=$2 WHERE user_id=$1`, id, w.BalanceMicros); err != nil {
		return s, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wallet_ledger(id,user_id,kind,reference,note,amount_micros,balance_micros) VALUES($1,$2,'plan',$3,$4,$5,$6)`, ids.NewUUID(), id, "plan:"+ref, plan.Name, -plan.PriceMicros, w.BalanceMicros); err != nil {
		return s, err
	}
	return s, tx.Commit(ctx)
}
func (p *Postgres) BeginCharge(ctx context.Context, userID, apiID string, price int64, id string, now time.Time) (model.Charge, error) {
	c := model.Charge{ID: id, UserID: userID, APIID: apiID, PriceMicros: price, CreatedAt: now}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer tx.Rollback(ctx)
	w, err := lockWallet(ctx, tx, userID)
	if err != nil {
		return c, err
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, userID).Scan(&status); err != nil || status != "active" {
		return c, ErrNotFound
	}
	sub, err := subscriptionRow(tx.QueryRow(ctx, `SELECT id,user_id,plan_id,plan_name,hourly,daily,monthly,starts_at,expires_at,time_zone FROM subscriptions WHERE user_id=$1 AND starts_at<=$2 AND expires_at>$2 ORDER BY expires_at DESC LIMIT 1`, userID, now))
	if err == nil {
		c.PlanID = sub.ID
		price = 0
		c.PriceMicros = 0
		starts := windowStartsIn(now, sub.TimeZone)
		limits := []int64{sub.Hourly, sub.Daily, sub.Monthly}
		kinds := []string{"hour", "day", "month"}
		for i, kind := range kinds {
			var used int64
			if _, err = tx.Exec(ctx, `INSERT INTO usage_windows(user_id,subscription_id,window_kind,window_start,used) VALUES($1,$2,$3,$4,0) ON CONFLICT DO NOTHING`, userID, sub.ID, kind, starts[i]); err != nil {
				return c, err
			}
			if err = tx.QueryRow(ctx, `SELECT used FROM usage_windows WHERE user_id=$1 AND subscription_id=$2 AND window_kind=$3 AND window_start=$4`, userID, sub.ID, kind, starts[i]).Scan(&used); err != nil {
				return c, err
			}
			if limits[i] > 0 && used >= limits[i] {
				return c, ErrPlanQuota
			}
		}
		for i, kind := range kinds {
			if _, err = tx.Exec(ctx, `UPDATE usage_windows SET used=used+1 WHERE user_id=$1 AND subscription_id=$2 AND window_kind=$3 AND window_start=$4`, userID, sub.ID, kind, starts[i]); err != nil {
				return c, err
			}
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return c, err
	}
	if price < 0 || price > 1000000000000 || w.BalanceMicros-w.HeldMicros < price {
		return c, ErrFunds
	}
	if _, err = tx.Exec(ctx, `UPDATE wallets SET held_micros=held_micros+$2 WHERE user_id=$1`, userID, price); err != nil {
		return c, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO api_charges(id,user_id,api_id,price_micros,subscription_id,created_at) VALUES($1,$2,$3,$4,$5,$6)`, id, userID, apiID, price, c.PlanID, now); err != nil {
		return c, err
	}
	return c, tx.Commit(ctx)
}
func (p *Postgres) FinishCharge(ctx context.Context, c model.Charge, success bool) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	w, err := lockWallet(ctx, tx, c.UserID)
	if err != nil {
		return err
	}
	stored := c
	zone := model.DefaultTimeZone
	err = tx.QueryRow(ctx, `SELECT outcome,price_micros,subscription_id,created_at,COALESCE((SELECT time_zone FROM subscriptions WHERE id=api_charges.subscription_id),'Asia/Shanghai') FROM api_charges WHERE id=$1 AND user_id=$2 FOR UPDATE`, c.ID, c.UserID).Scan(&stored.Outcome, &stored.PriceMicros, &stored.PlanID, &stored.CreatedAt, &zone)
	if err != nil {
		return err
	}
	if stored.Outcome != "held" && stored.Outcome != "review" {
		return tx.Commit(ctx)
	}
	amount := int64(0)
	outcome := "refunded"
	if success {
		amount = -stored.PriceMicros
		outcome = "charged"
	}
	w.BalanceMicros += amount
	w.HeldMicros -= stored.PriceMicros
	if _, err = tx.Exec(ctx, `UPDATE wallets SET balance_micros=$2,held_micros=$3 WHERE user_id=$1`, c.UserID, w.BalanceMicros, w.HeldMicros); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE api_charges SET outcome=$2 WHERE id=$1`, c.ID, outcome); err != nil {
		return err
	}
	if !success && stored.PlanID != "" {
		for i, kind := range []string{"hour", "day", "month"} {
			if _, err = tx.Exec(ctx, `UPDATE usage_windows SET used=GREATEST(0,used-1) WHERE user_id=$1 AND subscription_id=$2 AND window_kind=$3 AND window_start=$4`, c.UserID, stored.PlanID, kind, windowStartsIn(stored.CreatedAt, zone)[i]); err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO wallet_ledger(id,user_id,kind,reference,note,amount_micros,balance_micros) VALUES($1,$2,$3,$4,'API call',$5,$6) ON CONFLICT(reference) DO NOTHING`, ids.NewUUID(), c.UserID, outcome, "call:"+c.ID, amount, w.BalanceMicros)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) SaveCallLog(ctx context.Context, v model.CallLog) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO user_call_logs(id,user_id,api_id,api_name,method,path,client_ip,status,price_micros,duration_ms,created_at,request_id,trace_id,credential_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT(id) DO NOTHING`, v.ID, v.UserID, v.APIID, v.APIName, v.Method, v.Path, v.ClientIP, v.Status, v.PriceMicros, v.DurationMS, v.CreatedAt, v.RequestID, v.TraceID, v.CredentialID)
	return err
}
func (p *Postgres) CallLogs(ctx context.Context, id string) ([]model.CallLog, error) {
	rows, err := p.pool.Query(ctx, `SELECT id,user_id,api_id,api_name,method,path,client_ip,status,price_micros,duration_ms,created_at FROM user_call_logs WHERE user_id=$1 ORDER BY created_at DESC LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []model.CallLog{}
	for rows.Next() {
		var v model.CallLog
		if err := rows.Scan(&v.ID, &v.UserID, &v.APIID, &v.APIName, &v.Method, &v.Path, &v.ClientIP, &v.Status, &v.PriceMicros, &v.DurationMS, &v.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, rows.Err()
}

// ReconcileCharges marks uncertain outcomes for audited manual resolution. Never
// assume an abandoned reservation failed: its response may already have succeeded.
func (p *Postgres) ReconcileCharges(ctx context.Context) error {
	_, err := p.pool.Exec(ctx, `UPDATE api_charges SET outcome='review' WHERE outcome='held' AND created_at<NOW()-INTERVAL '10 minutes'`)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `DELETE FROM user_call_logs WHERE id IN (SELECT id FROM user_call_logs WHERE created_at<NOW()-INTERVAL '90 days' ORDER BY created_at LIMIT 1000)`)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `DELETE FROM account_verifications WHERE id IN (SELECT id FROM account_verifications WHERE expires_at<=NOW() LIMIT 1000)`)
	return err
}

func (p *Postgres) ReviewCharges(ctx context.Context) ([]model.Charge, error) {
	rows, err := p.pool.Query(ctx, `SELECT id,user_id,api_id,price_micros,subscription_id,outcome,created_at FROM api_charges WHERE outcome='review' ORDER BY created_at LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []model.Charge{}
	for rows.Next() {
		var c model.Charge
		if err = rows.Scan(&c.ID, &c.UserID, &c.APIID, &c.PriceMicros, &c.PlanID, &c.Outcome, &c.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}
func (p *Postgres) ResolveCharge(ctx context.Context, id string, success bool) error {
	var c model.Charge
	err := p.pool.QueryRow(ctx, `SELECT id,user_id FROM api_charges WHERE id=$1 AND outcome='review'`, id).Scan(&c.ID, &c.UserID)
	if err != nil {
		return err
	}
	return p.FinishCharge(ctx, c, success)
}
func (p *Postgres) Usage(ctx context.Context, id string, now time.Time) (map[string]int64, error) {
	out := map[string]int64{"hourly": 0, "daily": 0, "monthly": 0}
	sub, err := p.Subscription(ctx, id)
	if err != nil || sub == nil {
		return out, err
	}
	starts := windowStartsIn(now, sub.TimeZone)
	for i, kind := range []string{"hour", "day", "month"} {
		var used int64
		err = p.pool.QueryRow(ctx, `SELECT used FROM usage_windows WHERE user_id=$1 AND subscription_id=$2 AND window_kind=$3 AND window_start=$4`, id, sub.ID, kind, starts[i]).Scan(&used)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		out[[]string{"hourly", "daily", "monthly"}[i]] = used
	}
	return out, nil
}

// AssignPlan grants a plan without charging the user's wallet. It is idempotent
// and explicitly expires earlier active/queued plans without deleting history.
func (p *Postgres) AssignPlan(ctx context.Context, userID, planID, ref, note string) (model.Subscription, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return model.Subscription{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = lockWallet(ctx, tx, userID); err != nil {
		return model.Subscription{}, err
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&status); err != nil {
		return model.Subscription{}, err
	}
	if status != "active" {
		return model.Subscription{}, ErrNotFound
	}
	old, e := subscriptionRow(tx.QueryRow(ctx, `SELECT id,user_id,plan_id,plan_name,hourly,daily,monthly,starts_at,expires_at,time_zone FROM subscriptions WHERE id=$1 AND user_id=$2`, ref, userID))
	if e == nil {
		if old.PlanID != planID {
			return old, ErrConflict
		}
		return old, tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return old, e
	}
	var plan model.Plan
	err = tx.QueryRow(ctx, `SELECT id,name,days,hourly,daily,monthly FROM plans WHERE id=$1 FOR SHARE`, planID).Scan(&plan.ID, &plan.Name, &plan.Days, &plan.Hourly, &plan.Daily, &plan.Monthly)
	if err != nil {
		return model.Subscription{}, err
	}
	zone, err := siteBillingZone(ctx, tx)
	if err != nil {
		return model.Subscription{}, err
	}
	loc, _ := time.LoadLocation(zone)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err = tx.Exec(ctx, `UPDATE subscriptions SET expires_at=LEAST(expires_at,$2) WHERE user_id=$1 AND expires_at>$2`, userID, now); err != nil {
		return model.Subscription{}, err
	}
	sub := model.Subscription{ID: ref, UserID: userID, PlanID: plan.ID, PlanName: plan.Name, Hourly: plan.Hourly, Daily: plan.Daily, Monthly: plan.Monthly, TimeZone: zone, StartsAt: now, ExpiresAt: now.In(loc).AddDate(0, 0, plan.Days).UTC()}
	if _, err = tx.Exec(ctx, `INSERT INTO subscriptions(id,user_id,plan_id,plan_name,hourly,daily,monthly,starts_at,expires_at,time_zone) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, sub.ID, sub.UserID, sub.PlanID, sub.PlanName, sub.Hourly, sub.Daily, sub.Monthly, sub.StartsAt, sub.ExpiresAt, sub.TimeZone); err != nil {
		return sub, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wallet_ledger(id,user_id,kind,reference,note,amount_micros,balance_micros) SELECT $1,$2,'admin_plan',$3,$4,0,balance_micros FROM wallets WHERE user_id=$2`, ids.NewUUID(), userID, "assignment:"+ref, note); err != nil {
		return sub, err
	}
	return sub, tx.Commit(ctx)
}
