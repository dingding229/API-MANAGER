package store

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"context"
	"errors"
	"testing"
	"time"
)

func TestConsolidatedRoleCatalogDeletionAndRestart(t *testing.T) {
	p := accountPG(t)
	ctx := context.Background()
	roles := p.ListRoles()
	if len(roles) != 3 {
		t.Fatal("expected three roles", roles)
	}
	for _, r := range roles {
		if !SupportedRole(r.Name) {
			t.Fatal(r.Name)
		}
	}
	if e := p.DeleteRole(ctx, "super_admin"); !errors.Is(e, ErrConflict) {
		t.Fatal("administrator deleted", e)
	}
	if e := p.DeleteRole(ctx, "api_developer"); e != nil {
		t.Fatal(e)
	}
	if e := p.EnsureRBAC(); e != nil {
		t.Fatal(e)
	}
	if _, e := p.GetRoleByName("api_developer"); !errors.Is(e, ErrNotFound) {
		t.Fatal("deleted role resurrected", e)
	}
	if e := p.CreateRole(model.Role{Name: "api_developer", DisplayName: "接口开发者", Permissions: []string{"api.read", "api.test"}}); e != nil {
		t.Fatal(e)
	}
	if e := p.SaveRoleDetails(ctx, model.Role{Name: "api_developer", DisplayName: "服务开发团队", Description: "自定义说明", Permissions: []string{"api.read"}}); e != nil {
		t.Fatal(e)
	}
	if e := p.EnsureRBAC(); e != nil {
		t.Fatal(e)
	}
	r, e := p.GetRoleByName("api_developer")
	if e != nil || r.DisplayName != "服务开发团队" || r.Description != "自定义说明" || len(r.Permissions) != 1 {
		t.Fatal("custom details overwritten", r, e)
	}
	u := accountUser(t, p)
	if e := p.AssignUserRoles(u.ID, []string{"api_developer"}); e != nil {
		t.Fatal(e)
	}
	if e := p.DeleteRole(ctx, "api_developer"); !errors.Is(e, ErrConflict) {
		t.Fatal("assigned role deleted", e)
	}
	if e := p.SaveRoleDetails(ctx, model.Role{Name: "member", DisplayName: "普通用户", Permissions: []string{"user.manage"}}); !errors.Is(e, ErrConflict) {
		t.Fatal("ordinary user privilege escalation", e)
	}
	if e := p.CreateRole(model.Role{Name: "fourth", Permissions: []string{"api.read"}}); !errors.Is(e, ErrConflict) {
		t.Fatal("fourth role allowed", e)
	}
}
func TestUserLogFilteringPaginationAndDirectoryIsolation(t *testing.T) {
	p := accountPG(t)
	ctx := context.Background()
	one := accountUser(t, p)
	two := accountUser(t, p)
	n := time.Now().UTC()
	for i := 0; i < 25; i++ {
		u := one
		if i >= 23 {
			u = two
		}
		v := model.CallLog{ID: ids.NewUUID(), UserID: u.ID, APIID: "test-api", APIName: "接口测试", Method: "GET", Path: "/api/test", Status: 200, CreatedAt: n.Add(time.Duration(i) * time.Millisecond), RequestID: "request-" + ids.NewUUID(), ClientIP: "127.0.0.1"}
		if i%2 == 1 {
			v.Status = 500
		}
		if e := p.SaveCallLog(ctx, v); e != nil {
			t.Fatal(e)
		}
	}
	q := model.CallLogQuery{UserID: one.ID, Page: 1, PageSize: 20, StatusMin: 100, StatusMax: 599, From: n.Add(-time.Minute), To: n.Add(time.Minute)}
	page, e := p.QueryCallLogs(ctx, q)
	if e != nil || page.Total != 23 || len(page.Items) != 20 {
		t.Fatal(page, e)
	}
	for _, v := range page.Items {
		if v.UserID != one.ID || v.RequestID == "" {
			t.Fatal("foreign log or missing metadata", v)
		}
	}
	q.Page = 2
	page, e = p.QueryCallLogs(ctx, q)
	if e != nil || len(page.Items) != 3 {
		t.Fatal(page, e)
	}
	q.Page = 1
	q.StatusMin = 500
	q.StatusMax = 599
	page, e = p.QueryCallLogs(ctx, q)
	if e != nil || page.Total != 11 {
		t.Fatal(page, e)
	}
	q.UserID = ""
	q.StatusMin = 100
	q.StatusMax = 599
	page, e = p.QueryCallLogs(ctx, q)
	if e != nil || page.Total != 25 {
		t.Fatal("administrator missing users", page, e)
	}
	key := model.Credential{ID: ids.NewUUID(), OwnerUserID: one.ID, Name: "owned", Prefix: "ak_safe", Hash: ids.NewUUID(), EncryptedKey: "encrypted-only", CreatedAt: n}
	if e := p.CreateOwnedCredential(ctx, key); e != nil {
		t.Fatal(e)
	}
	directory, e := p.CredentialDirectory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, v := range directory {
		if v.ID == key.ID {
			found = true
			if v.OwnerUsername != one.Username || v.EncryptedKey != "" || v.Hash != "" {
				t.Fatal("unsafe or incomplete directory", v)
			}
		}
	}
	if !found {
		t.Fatal("global directory missed user credential")
	}
	own, e := p.OwnCredentials(ctx, one.ID)
	if e != nil || !own[0].KeyAvailable {
		t.Fatal("recoverable key unavailable", own, e)
	}
}

func TestLegacyRolesAreArchivedAndNeverPromoted(t *testing.T) {
	p := accountPG(t)
	ctx := context.Background()
	roleID := ids.NewUUID()
	if _, e := p.pool.Exec(ctx, `INSERT INTO roles(id,name,description,display_name) VALUES($1,'operator','legacy','旧运维')`, roleID); e != nil {
		t.Fatal(e)
	}
	u := accountUser(t, p)
	if _, e := p.pool.Exec(ctx, `UPDATE users SET role='operator' WHERE id=$1;`, u.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := p.pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, u.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := p.pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2)`, u.ID, roleID); e != nil {
		t.Fatal(e)
	}
	sql, e := migrationFiles.ReadFile("migrations/000024_console_roles_and_logs.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.pool.Exec(ctx, string(sql)); e != nil {
		t.Fatal(e)
	}
	migrated, e := p.GetUserByID(u.ID)
	if e != nil || migrated.Role != "api_developer" {
		t.Fatal(migrated, e)
	}
	for _, code := range p.GetUserPermissions(u.ID) {
		if code == "*" || code == "user.manage" || code == "credential.reveal" {
			t.Fatal("legacy account promoted", code)
		}
	}
	var saved bool
	if e = p.pool.QueryRow(ctx, `SELECT snapshot->'users' ? $2 FROM retired_role_settings WHERE role_id=$1`, roleID, u.ID).Scan(&saved); e != nil || !saved {
		t.Fatal("legacy membership not retained", saved, e)
	}
	if _, e = p.GetRoleByName("operator"); !errors.Is(e, ErrNotFound) {
		t.Fatal("retired role still active", e)
	}
}

func TestAdministrativePlanAssignmentIsFreeIdempotentAndKeepsHistory(t *testing.T) {
	p := accountPG(t)
	ctx := context.Background()
	u := accountUser(t, p)
	plan := model.Plan{ID: ids.NewUUID(), Name: "直接绑定", Days: 30, PriceMicros: 10000000, Hourly: 5, Daily: 50, Monthly: 500, Enabled: true}
	if e := p.SavePlan(ctx, plan); e != nil {
		t.Fatal(e)
	}
	before, e := p.Wallet(ctx, u.ID)
	if e != nil {
		t.Fatal(e)
	}
	ref := "grant:" + u.ID + ":" + ids.NewUUID()
	first, e := p.AssignPlan(ctx, u.ID, plan.ID, ref, "人工确认绑定")
	if e != nil {
		t.Fatal(e)
	}
	retry, e := p.AssignPlan(ctx, u.ID, plan.ID, ref, "重复请求")
	if e != nil || first.ID != retry.ID {
		t.Fatal("assignment duplicated", retry, e)
	}
	second, e := p.AssignPlan(ctx, u.ID, plan.ID, "grant:"+u.ID+":"+ids.NewUUID(), "替换")
	if e != nil {
		t.Fatal(e)
	}
	current, e := p.Subscription(ctx, u.ID)
	if e != nil || current.ID != second.ID {
		t.Fatal(current, e)
	}
	after, e := p.Wallet(ctx, u.ID)
	if e != nil || before.BalanceMicros != after.BalanceMicros {
		t.Fatal("administrative grant billed user", before, after, e)
	}
	var count int
	if e = p.pool.QueryRow(ctx, `SELECT COUNT(*) FROM subscriptions WHERE user_id=$1`, u.ID).Scan(&count); e != nil || count != 2 {
		t.Fatal("history lost or duplicate created", count, e)
	}
	records, e := p.Ledger(ctx, u.ID)
	if e != nil || len(records) != 2 || records[0].AmountMicros != 0 {
		t.Fatal(records, e)
	}
}
func TestBillingWindowsUseSnapshottedZoneAndFractionalOffsets(t *testing.T) {
	n := time.Date(2026, 10, 7, 2, 30, 0, 0, time.UTC)
	windows := windowStartsIn(n, "Asia/Kathmandu")
	if windows[0].Minute() != 0 || windows[0].Hour() != 8 || windows[1].Hour() != 0 {
		t.Fatal(windows)
	}
	if windows[0].UTC().Hour() != 2 || windows[0].UTC().Minute() != 15 {
		t.Fatal(windows)
	}
}

func TestPlanTimeZoneSnapshotSurvivesWebsiteChanges(t *testing.T) {
	p := accountPG(t)
	ctx := context.Background()
	record, err := p.GetSiteSettings()
	if errors.Is(err, ErrNotFound) {
		record = model.SiteSettingsRecord{}
	} else if err != nil {
		t.Fatal(err)
	}
	original := record.Settings
	defer func() {
		current, e := p.GetSiteSettings()
		if e == nil {
			current.Settings = original
			if _, e = p.SaveSiteSettings(current, current.Version); e != nil {
				t.Error(e)
			}
		}
	}()
	record.Settings.Site.TimeZone = "Asia/Kathmandu"
	record, err = p.SaveSiteSettings(record, record.Version)
	if err != nil {
		t.Fatal(err)
	}
	u := accountUser(t, p)
	plan := model.Plan{ID: ids.NewUUID(), Name: "timezone", Days: 30, Hourly: 10, Enabled: true}
	if err = p.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	first, err := p.AssignPlan(ctx, u.ID, plan.ID, "grant:"+u.ID+":"+ids.NewUUID(), "zone snapshot")
	if err != nil || first.TimeZone != "Asia/Kathmandu" {
		t.Fatal(first, err)
	}
	record.Settings.Site.TimeZone = "UTC"
	if _, err = p.SaveSiteSettings(record, record.Version); err != nil {
		t.Fatal(err)
	}
	current, err := p.Subscription(ctx, u.ID)
	if err != nil || current.TimeZone != first.TimeZone {
		t.Fatal("website change altered purchased quota clock", current, err)
	}
}
