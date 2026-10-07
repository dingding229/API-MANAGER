package api

import (
	"api-manager/internal/auth"
	"api-manager/internal/httpx"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/store"
	"api-manager/internal/user"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresDatabaseAPIBackupImportRestoreAndPasswordGate(t *testing.T) {
	dsn := os.Getenv("TEST_ACCOUNT_DSN")
	if dsn == "" {
		t.Skip("isolated database required")
	}
	if !strings.Contains(dsn, "/api_account_review_") {
		t.Fatal("isolated database required")
	}
	p, e := store.NewPostgres(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	users := user.NewService(p)
	u, e := users.Create("dbroot"+ids.NewUUID()[:8], "Password888", "super_admin")
	if e != nil {
		t.Fatal(e)
	}
	_, token, e := users.Authenticate(u.Username, "Password888")
	if e != nil {
		t.Fatal(e)
	}
	a := NewAdminWithUserManagement(p, plugin.NewRegistry(), users, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.SetCredentialEncryptionKey("0123456789abcdefghijklmnopqrstuvwxyz")
	a.SetCredentialGuard(func(r *http.Request, password, code string) error {
		if password != "Password888" {
			return errors.New("wrong password")
		}
		return nil
	})
	restart := make(chan struct{}, 1)
	a.ConfigureDatabaseManagement(t.TempDir(), func() { restart <- struct{}{} }, &httpx.Maintenance{})
	call := func(method, path string, data any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(data)
		r := httptest.NewRequest(method, "https://example.test/admin/v1/database"+path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	plainKey := "ak_database_" + ids.NewUUID()
	secret, e := auth.EncryptSecret(a.credentialEncryptionKey, plainKey)
	if e != nil {
		t.Fatal(e)
	}
	keyID := ids.NewUUID()
	if e = p.CreateCredential(model.Credential{ID: keyID, Name: "private key", Prefix: "ak_", Hash: auth.HashAPIKey(plainKey), EncryptedKey: secret, CreatedAt: time.Now()}); e != nil {
		t.Fatal(e)
	}
	for _, password := range []string{"wrong", "Password888"} {
		result := call("POST", "/reveal", map[string]any{"table": "api_credentials", "primary": map[string]string{"id": keyID}, "current_password": password})
		if password == "wrong" && result.Code != 403 {
			t.Fatal("reveal password bypass")
		}
		if password == "Password888" && (result.Code != 200 || !strings.Contains(result.Body.String(), plainKey)) {
			t.Fatal("encrypted field not revealed", result.Code, result.Body.String())
		}
	}
	result := call("POST", "/reveal", map[string]any{"table": "users", "primary": map[string]string{"id": u.ID}, "current_password": "Password888"})
	if result.Code != 400 || strings.Contains(result.Body.String(), "$2") {
		t.Fatal("login password material exposed", result.Code)
	}
	if result = call("POST", "/reveal", map[string]any{"table": "api_credentials", "primary": map[string]string{"id": "';DROP TABLE users;--"}, "current_password": "Password888"}); result.Code != 404 {
		t.Fatal("SQL primary injection", result.Code)
	}
	if w := call("POST", "/backups", map[string]any{"current_password": "wrong"}); w.Code != 403 {
		t.Fatal("password bypass", w.Code)
	}
	w := call("POST", "/backups", map[string]any{"current_password": "Password888"})
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var item databaseBackupItem
	if e = json.Unmarshal(w.Body.Bytes(), &item); e != nil {
		t.Fatal(e)
	}
	w = call("POST", "/download", map[string]any{"current_password": "Password888", "backup_id": item.ID})
	if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte("password_hash")) {
		t.Fatal("backup not encrypted", w.Code)
	}
	file := append([]byte(nil), w.Body.Bytes()...)
	importFile := func(file []byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("current_password", "Password888")
		part, _ := writer.CreateFormFile("backup", "backup.amdb")
		_, _ = part.Write(file)
		_ = writer.Close()
		r := httptest.NewRequest("POST", "https://example.test/admin/v1/database/import", &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	if w = importFile([]byte("DROP TABLE users;")); w.Code != 400 {
		t.Fatal("SQL import accepted", w.Code)
	}
	w = importFile(file)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var imported struct {
		Backup databaseBackupItem `json:"backup"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &imported)
	if w = call("POST", "/restore", map[string]any{"current_password": "Password888", "backup_id": imported.Backup.ID, "confirm": true, "confirmation": "wrong"}); w.Code != 400 {
		t.Fatal("restore lacks exact confirmation")
	}
	w = call("POST", "/restore", map[string]any{"current_password": "Password888", "backup_id": imported.Backup.ID, "confirm": true, "confirmation": "恢复数据库"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, e = p.GetSession(auth.HashAPIKey(token)); e == nil {
		t.Fatal("restored session still valid")
	}
	actual, e := p.GetUserByID(u.ID)
	if e != nil || actual.ID != u.ID {
		t.Fatal("user lost", e)
	}
	select {
	case <-restart:
	case <-time.After(time.Second * 2):
		t.Fatal("settings were not scheduled for reload")
	}
}
