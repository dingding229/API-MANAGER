package store

import (
	"api-manager/internal/model"
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in; runs only against an explicitly provided isolated PostgreSQL service.
func TestPostgresProductionInvariants(t *testing.T) {
	file := os.Getenv("TEST_POSTGRES_PASSWORD_FILE")
	if file == "" {
		t.Skip("isolated PostgreSQL not configured")
	}
	password, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	dsn := "postgres://api_manager:" + url.QueryEscape(strings.TrimSpace(string(password))) + "@postgres:5432/api_manager?sslmode=disable"
	ctx := context.Background()
	connection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(ctx)
	schema := fmt.Sprintf("audit_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = connection.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
	p, err := NewPostgres(ctx, dsn+"&search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	now := time.Now().UTC()
	a := model.API{ID: "a", Name: "a", Method: "GET", Methods: []string{"GET", "POST"}, Path: "/api/items/{id}", AuthMode: "none", ResponseStatus: 200, CreatedAt: now, UpdatedAt: now}
	b := a
	b.ID = "b"
	b.Method = "POST"
	b.Methods = []string{"POST", "PUT"}
	b.Path = "/api/items/{name}"
	var wg sync.WaitGroup
	result := make(chan error, 2)
	for _, api := range []model.API{a, b} {
		wg.Add(1)
		go func(api model.API) { defer wg.Done(); result <- p.CreateAPI(api) }(api)
	}
	wg.Wait()
	close(result)
	wins := 0
	for err := range result {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("route conflicts were not atomic: %d", wins)
	}
	apis, err := p.ListAPIsChecked()
	if err != nil || len(apis) != 1 {
		t.Fatal("failed route transaction leaked")
	}
	live := apis[0]
	// Simulate a snapshot write failure and verify the route update rolls back.
	if _, err = p.pool.Exec(ctx, `CREATE FUNCTION fail_release() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test snapshot failure'; END $$; CREATE TRIGGER fail_release BEFORE INSERT ON api_releases FOR EACH ROW EXECUTE FUNCTION fail_release()`); err != nil {
		t.Fatal(err)
	}
	live.Enabled = true
	live.PublishedAt = &now
	if _, err = p.UpdateAndRelease(live); err == nil {
		t.Fatal("snapshot failure accepted")
	}
	stored, _ := p.GetAPI(live.ID)
	if stored.Enabled {
		t.Fatal("failed release left route published")
	}
	if _, err = p.pool.Exec(ctx, `DROP TRIGGER fail_release ON api_releases`); err != nil {
		t.Fatal(err)
	}
	if _, err = p.UpdateAndRelease(live); err != nil {
		t.Fatal(err)
	}
	if len(p.ListReleases(live.ID)) != 1 {
		t.Fatal("release not committed")
	}
	hash := strings.Repeat("a", 64)
	if err = p.ConfigureBootstrap(hash); err != nil {
		t.Fatal(err)
	}
	registrations := make(chan error, 2)
	for _, id := range []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			registrations <- p.RegisterInitialAdmin(hash, model.User{ID: id, Username: "admin" + id, Email: id + "@example.test", PasswordHash: "test-only-hash", CreatedAt: now})
		}(id)
	}
	wg.Wait()
	close(registrations)
	wins = 0
	for err := range registrations {
		if err == nil {
			wins++
		}
	}
	if wins != 1 || p.CountUsers() != 1 {
		t.Fatal("bootstrap registration was not single-use")
	}
	first := p.ListUsers()[0]
	second := model.User{ID: "00000000-0000-4000-8000-000000000003", Username: "second", PasswordHash: "test-only-hash", Role: "super_admin", Roles: []string{"super_admin"}, Status: "active", CreatedAt: now, UpdatedAt: now}
	if err = p.CreateUser(second); err != nil {
		t.Fatal(err)
	}
	demotions := make(chan error, 2)
	for _, id := range []string{first.ID, second.ID} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); demotions <- p.AssignUserRoles(id, []string{"viewer"}) }(id)
	}
	wg.Wait()
	close(demotions)
	wins = 0
	for err := range demotions {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatal("concurrent demotion removed final administrator")
	}
	failures := make(chan bool, 30)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			users, roles := p.ListUsers(), p.ListRoles()
			failures <- len(users) != 2 || len(roles) != len(DefaultRoles())
		}()
	}
	wg.Wait()
	close(failures)
	for failed := range failures {
		if failed {
			t.Fatal("concurrent user/role listing exhausted the connection pool")
		}
	}

	record := model.SiteSettingsRecord{Settings: model.SiteSettings{Site: model.PublicSiteInfo{Name: "stored-site"}}, EncryptedSMTPPassword: "encrypted-fixture", UpdatedAt: time.Now().UTC()}
	saved, err := p.SaveSiteSettings(record, 0)
	if err != nil || saved.Version != 1 {
		t.Fatal("initial site settings not persisted")
	}
	if _, err = p.SaveSiteSettings(record, 0); !errors.Is(err, ErrConflict) {
		t.Fatal("site settings optimistic guard ignored")
	}
	loaded, err := p.GetSiteSettings()
	if err != nil || loaded.EncryptedSMTPPassword != "encrypted-fixture" || loaded.Settings.Site.Name != "stored-site" {
		t.Fatal("site settings persistence changed")
	}

}
