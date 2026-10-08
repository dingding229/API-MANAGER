package store

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresPluginUpdateIsAtomicAndPreservesBusinessState(t *testing.T) {
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
	now := time.Now()
	plugin := model.Plugin{ID: "plugin_" + ids.NewUUID(), Name: "update" + ids.NewUUID()[:8], Version: "1.0.0", Runtime: "wasm", Manifest: []byte(`{}`), Checksum: "old", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if e = p.CreatePlugin(plugin); e != nil {
		t.Fatal(e)
	}
	a := model.API{ID: "api_" + ids.NewUUID(), Name: "fixture", Method: "GET", Path: "/api/update/" + ids.NewUUID(), AuthMode: "api_key", Plugin: plugin.Name, Enabled: true, PriceMicros: 1200, CreatedAt: now, UpdatedAt: now}
	if e = p.CreateAPI(a); e != nil {
		t.Fatal(e)
	}
	next := plugin
	next.Version = "1.1.0"
	next.Checksum = "new"
	a.Description = "plugin-owned docs"
	a.ParametersSchema = []byte(`{"type":"object"}`)
	if e = p.UpdateManagedPlugin(ctx, next, "wrong", []model.API{a}); e == nil {
		t.Fatal("checksum CAS skipped")
	}
	if e = p.UpdateManagedPlugin(ctx, next, "old", []model.API{a}); e != nil {
		t.Fatal(e)
	}
	after, _ := p.GetPlugin(plugin.ID)
	stored, _ := p.GetAPI(a.ID)
	if after.Version != "1.1.0" || !after.Enabled || stored.PriceMicros != 1200 || stored.Description != "plugin-owned docs" {
		t.Fatal("update changed identity/business state")
	}
	bad := next
	bad.Version = "1.2.0"
	bad.Checksum = "bad"
	if e = p.UpdateManagedPlugin(ctx, bad, "new", []model.API{{ID: "missing", Plugin: plugin.Name}}); e == nil {
		t.Fatal("partial contract update accepted")
	}
	after, _ = p.GetPlugin(plugin.ID)
	if after.Checksum != "new" {
		t.Fatal("transaction did not roll back")
	}
}
