package api

import (
	"api-manager/internal/model"
	"bytes"
	"encoding/json"
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
