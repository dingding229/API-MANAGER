package api

import (
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseBackupEncryptionTamperKeyMismatchAndPathSafety(t *testing.T) {
	a := &Admin{credentialEncryptionKey: "test-key-012345678901234567890123456789"}
	a.database.dir = t.TempDir()
	snapshot := model.DatabaseSnapshot{Format: 2, Schema: "test-schema", Tables: []model.DatabaseSnapshotTable{{Name: "users", Rows: []json.RawMessage{json.RawMessage(`{"password_hash":"private"}`)}}}}
	file, e := a.encodeDatabaseBackup(snapshot)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(file, []byte("private")) {
		t.Fatal("unencrypted backup")
	}
	if _, e = a.decodeDatabaseBackup(file); e != nil {
		t.Fatal(e)
	}
	altered := append([]byte(nil), file...)
	altered[len(altered)-3] ^= 1
	if _, e = a.decodeDatabaseBackup(altered); e == nil {
		t.Fatal("tampered backup accepted")
	}
	b := &Admin{credentialEncryptionKey: "different-key"}
	if _, e = b.decodeDatabaseBackup(file); e == nil {
		t.Fatal("foreign encryption key accepted")
	}
	for _, name := range []string{"../backup.amdb", "/tmp/backup.amdb", "users;drop", "foo.amdb"} {
		if _, e = a.databaseFile(name); e == nil {
			t.Fatal("unsafe filename", name)
		}
	}
	item, e := a.saveDatabaseBackup(snapshot)
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(filepath.Join(a.database.dir, item.ID))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("backup file permissions", e)
	}
}

func TestDatabaseRevealHasExplicitDecryptScopesAndNeverReturnsLoginPasswords(t *testing.T) {
	keys := databaseRecoverableFields("api_credentials", map[string]any{}, "master")
	if keys["encrypted_key"] != "master" {
		t.Fatal(keys)
	}
	keys = databaseRecoverableFields("plugin_settings", map[string]any{"plugin_id": "owner-plugin"}, "master")
	if keys["encrypted_settings"] != "master:plugin-settings:owner-plugin" {
		t.Fatal(keys)
	}
	for _, table := range []string{"users", "user_sessions", "password_resets", "account_security", "account_verifications"} {
		if len(databaseRecoverableFields(table, map[string]any{}, "master")) != 0 {
			t.Fatal("sensitive irreversible/verification field exposed", table)
		}
	}
}

func TestDatabaseLocatorsHidePrimaryHashesAndAreBoundToSessionAndTable(t *testing.T) {
	a := &Admin{credentialEncryptionKey: "test-locator-key-0123456789abcdef"}
	primary := map[string]json.RawMessage{"hash": json.RawMessage(`"private-session-hash"`)}
	data := model.DatabaseRows{Table: "user_sessions", Items: []map[string]any{{"hash": "[已隐藏]"}}, Primary: []map[string]json.RawMessage{primary}}
	r := httptest.NewRequest("GET", "https://example.test/admin/v1/database/rows/user_sessions", nil)
	r.Header.Set("Authorization", "Bearer current-session")
	if e := a.attachDatabaseLocators(r, &data); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(data)
	if bytes.Contains(raw, []byte("private-session-hash")) {
		t.Fatal("primary secret leaked before reauthentication")
	}
	locator := data.Items[0]["_record_locator"].(string)
	recovered, e := a.databaseLocatorPrimary(r, "user_sessions", locator)
	if e != nil || string(recovered["hash"]) != string(primary["hash"]) {
		t.Fatal("hidden primary could not be addressed", e)
	}
	for _, table := range []string{"users", "user_sessions"} {
		other := r.Clone(context.Background())
		other.Header.Set("Authorization", "Bearer other-session")
		if _, e = a.databaseLocatorPrimary(other, table, locator); e == nil {
			t.Fatal("locator crossed session or table")
		}
	}
	if _, e = a.databaseLocatorPrimary(r, "users", locator); e == nil {
		t.Fatal("locator crossed table")
	}
}

type hiddenRecordTestStore struct {
	*store.Memory
	databaseStore
	reads int
}

func (s *hiddenRecordTestStore) DatabaseRecord(ctx context.Context, table string, primary map[string]json.RawMessage) (map[string]any, error) {
	s.reads++
	return map[string]any{"id": "record-id", "password_hash": "stored-one-way-password-hash", "token_hash": "stored-one-way-token-hash"}, nil
}
func TestDatabaseHiddenValuesRequireSuperAdminAndReauthentication(t *testing.T) {
	st := &hiddenRecordTestStore{Memory: store.NewMemory()}
	us := user.NewService(st)
	admin, e := us.Create("dbrootfixture", "Password888", "super_admin")
	if e != nil {
		t.Fatal(e)
	}
	member, e := us.Create("dbmemberfixture", "Password888", "member")
	if e != nil {
		t.Fatal(e)
	}
	_, rootToken, _ := us.Authenticate(admin.Username, "Password888")
	_, memberToken, _ := us.Authenticate(member.Username, "Password888")
	a := NewAdminWithUserManagement(st, plugin.NewRegistry(), us, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.SetCredentialEncryptionKey("fixture-encryption-key-0123456789")
	a.ConfigureDatabaseManagement(t.TempDir(), nil, nil)
	a.SetCredentialGuard(func(r *http.Request, password, captcha string) error {
		token, _ := auth.SessionToken(r)
		actor, e := us.ValidateSession(token)
		if e != nil {
			return e
		}
		if auth.PasskeyConfirmed(r.Context(), actor.ID) {
			return nil
		}
		_, e = us.VerifyPassword(actor.Username, password)
		return e
	})
	call := func(token, password string, passkey bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"table": "users", "primary": map[string]string{"id": "record-id"}, "current_password": password})
		r := httptest.NewRequest("POST", "https://example.test/admin/v1/database/reveal", bytes.NewReader(raw))
		r.Header.Set("Origin", "https://example.test")
		r.Header.Set("X-API-Request", "1")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if passkey {
			r = r.WithContext(auth.WithPasskeyConfirmation(r.Context(), admin.ID))
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	for _, c := range []struct{ token, password string }{{"", "Password888"}, {memberToken, "Password888"}, {rootToken, ""}, {rootToken, "incorrect-password"}} {
		before := st.reads
		w := call(c.token, c.password, false)
		if w.Code < 400 || st.reads != before {
			t.Fatal("unverified database record was read", w.Code)
		}
	}
	for _, passkey := range []bool{false, true} {
		password := "Password888"
		if passkey {
			password = ""
		}
		w := call(rootToken, password, passkey)
		if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("stored-one-way-password-hash")) {
			t.Fatal("confirmed hidden fields unavailable", w.Code, w.Body.String())
		}
		if bytes.Contains(w.Body.Bytes(), []byte("Password888")) {
			t.Fatal("original password exposed")
		}
	}
}
