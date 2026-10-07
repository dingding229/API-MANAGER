package store

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPostgresDatabaseSnapshotRedactionRoundTripAndAtomicFailure(t *testing.T) {
	p := accountPG(t)
	ctx := context.Background()
	role, e := p.GetRoleByName("super_admin")
	if e != nil {
		t.Fatal(e)
	}
	admin := model.User{ID: ids.NewUUID(), Username: "dbadmin" + ids.NewUUID()[:8], Email: "db" + ids.NewUUID()[:8] + "@example.test", PasswordHash: "protected-password-hash", Status: "active", Role: role.Name, Roles: []string{role.Name}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if e = p.CreateUser(admin); e != nil {
		t.Fatal(e)
	}
	if e = p.CreateAPI(model.API{ID: "null-api-" + ids.NewUUID(), Name: "JSON null", Path: "/api/null/" + ids.NewUUID(), Method: "GET", AuthMode: "none", ResponseStatus: 200, CreatedAt: time.Now(), UpdatedAt: time.Now()}); e != nil {
		t.Fatal(e)
	}
	rows, e := p.DatabaseRows(ctx, "users", 1, 20)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range rows.Items {
		if v["password_hash"] != "[已隐藏]" {
			t.Fatal("password hash leaked")
		}
	}
	if _, e = p.DatabaseRows(ctx, "users;DROP TABLE users", 1, 20); e == nil {
		t.Fatal("table injection accepted")
	}
	original, e := p.DatabaseSnapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.ValidateDatabaseSnapshot(ctx, original); e != nil {
		t.Fatal(e)
	}
	name := admin.Username
	_, _, e = p.UpdateUserProfile(admin.ID, model.UserProfileUpdate{ExpectedAuthRevision: admin.AuthRevision, ExpectedUsername: name, ExpectedEmail: admin.Email, ExpectedPasswordHash: admin.PasswordHash, Username: name, Email: admin.Email, PasswordHash: "new-hash"})
	if e != nil {
		t.Fatal(e)
	}
	if e = p.RestoreDatabaseSnapshot(ctx, original); e != nil {
		t.Fatal(e)
	}
	actual, e := p.GetUserByID(admin.ID)
	if e != nil || actual.PasswordHash != "protected-password-hash" || actual.AuthRevision != admin.AuthRevision+1 {
		t.Fatal("restore failed", actual, e)
	}
	// A bad row must roll the entire truncate and all prior inserts back.
	raw, _ := json.Marshal(original)
	var invalid model.DatabaseSnapshot
	_ = json.Unmarshal(raw, &invalid)
	for i, table := range invalid.Tables {
		if table.Name == "users" {
			for n, row := range table.Rows {
				var fields map[string]any
				_ = json.Unmarshal(row, &fields)
				if fields["id"] == admin.ID {
					fields["id"] = "invalid-uuid"
					invalid.Tables[i].Rows[n], _ = json.Marshal(fields)
				}
			}
		}
	}
	if e = p.RestoreDatabaseSnapshot(ctx, invalid); e == nil {
		t.Fatal("invalid row was restored")
	}
	after, e := p.GetUserByID(admin.ID)
	if e != nil || after.PasswordHash != actual.PasswordHash || after.AuthRevision != actual.AuthRevision {
		t.Fatal("failed restore destroyed current data", e)
	}
	invalid = original
	invalid.Schema = "other-schema"
	if e = p.RestoreDatabaseSnapshot(ctx, invalid); e == nil {
		t.Fatal("schema mismatch accepted")
	}
}

func TestDatabaseRedactionHandlesNestedSecretsAndPreservesPublicData(t *testing.T) {
	value := map[string]any{"username": "safe", "password_hash": "secret", "settings": map[string]any{"smtp_password": "pass", "Authorization": "bearer", "public_title": "<img src=x onerror=alert(1)>"}, "nested": []any{map[string]any{"encrypted_code": "x", "value": "visible"}}}
	redactDatabaseValue(value)
	data, _ := json.Marshal(value)
	if value["password_hash"] != "[已隐藏]" || value["username"] != "safe" {
		t.Fatal(string(data))
	}
	nested := value["settings"].(map[string]any)
	if nested["smtp_password"] != "[已隐藏]" || nested["Authorization"] != "[已隐藏]" || nested["public_title"] != "<img src=x onerror=alert(1)>" {
		t.Fatal(string(data))
	}
}
