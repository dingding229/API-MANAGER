package api

import (
	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/httpx"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const databaseBackupMaxBytes = 24 << 20
const databaseBackupMagic = "API_MANAGER_DATABASE_V2\n"

var databaseBackupName = regexp.MustCompile(`^[a-f0-9-]{36}\.amdb$`)

type databaseStore interface {
	DatabaseTables(context.Context) ([]model.DatabaseTable, error)
	DatabaseRows(context.Context, string, int, int) (model.DatabaseRows, error)
	DatabaseSnapshot(context.Context) (model.DatabaseSnapshot, error)
	ValidateDatabaseSnapshot(context.Context, model.DatabaseSnapshot) error
	RestoreDatabaseSnapshot(context.Context, model.DatabaseSnapshot) error
}
type databaseBackupItem struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Bytes     int64     `json:"bytes"`
}
type databaseManagement struct {
	mu          sync.Mutex
	dir         string
	restart     func()
	maintenance *httpx.Maintenance
}

func (a *Admin) ConfigureDatabaseManagement(dir string, restart func(), maintenance *httpx.Maintenance) {
	a.database.maintenance = maintenance
	a.database.dir = dir
	a.database.restart = restart
}
func (a *Admin) databasePassword(r *http.Request, password string) bool {
	return a.credentialGuard != nil && password != "" && a.credentialGuard(r, password, "") == nil
}
func (a *Admin) databaseFile(id string) (string, error) {
	if !databaseBackupName.MatchString(id) || a.database.dir == "" {
		return "", store.ErrNotFound
	}
	return filepath.Join(a.database.dir, id), nil
}
func (a *Admin) encodeDatabaseBackup(snapshot model.DatabaseSnapshot) ([]byte, error) {
	raw, e := json.Marshal(snapshot)
	if e != nil || len(raw) > store.MaxDatabaseSnapshotBytes+(2<<20) {
		return nil, errors.New("数据库较大，请使用服务器备份脚本")
	}
	var zipped bytes.Buffer
	gz := gzip.NewWriter(&zipped)
	if _, e = gz.Write(raw); e != nil {
		return nil, e
	}
	if e = gz.Close(); e != nil {
		return nil, e
	}
	sealed, e := auth.EncryptSecret(a.credentialEncryptionKey+":database-backup:v2", zipped.String())
	if e != nil {
		return nil, e
	}
	file := []byte(databaseBackupMagic + sealed)
	if len(file) > databaseBackupMaxBytes {
		return nil, errors.New("备份文件较大，请使用服务器备份脚本")
	}
	return file, nil
}
func (a *Admin) decodeDatabaseBackup(file []byte) (model.DatabaseSnapshot, error) {
	var snapshot model.DatabaseSnapshot
	if len(file) > databaseBackupMaxBytes || !bytes.HasPrefix(file, []byte(databaseBackupMagic)) {
		return snapshot, store.ErrConflict
	}
	plain, e := auth.DecryptSecret(a.credentialEncryptionKey+":database-backup:v2", string(file[len(databaseBackupMagic):]))
	if e != nil {
		return snapshot, store.ErrConflict
	}
	gz, e := gzip.NewReader(strings.NewReader(plain))
	if e != nil {
		return snapshot, store.ErrConflict
	}
	defer gz.Close()
	raw, e := io.ReadAll(io.LimitReader(gz, store.MaxDatabaseSnapshotBytes+(2<<20)+1))
	if e != nil || len(raw) > store.MaxDatabaseSnapshotBytes+(2<<20) {
		return snapshot, store.ErrConflict
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&snapshot); e != nil {
		return snapshot, store.ErrConflict
	}
	if e = decoder.Decode(&struct{}{}); !errors.Is(e, io.EOF) {
		return snapshot, store.ErrConflict
	}
	return snapshot, nil
}
func (a *Admin) saveDatabaseBackup(snapshot model.DatabaseSnapshot) (databaseBackupItem, error) {
	item := databaseBackupItem{ID: ids.NewUUID() + ".amdb", CreatedAt: time.Now().UTC()}
	file, e := a.encodeDatabaseBackup(snapshot)
	if e != nil {
		return item, e
	}
	if e = os.MkdirAll(a.database.dir, 0700); e != nil {
		return item, e
	}
	entries, e := os.ReadDir(a.database.dir)
	if e != nil {
		return item, e
	}
	var size int64
	var count int
	for _, entry := range entries {
		if !databaseBackupName.MatchString(entry.Name()) {
			continue
		}
		info, e := entry.Info()
		if e != nil || !info.Mode().IsRegular() {
			continue
		}
		size += info.Size()
		count++
	}
	if count >= 20 || size+int64(len(file)) > 128<<20 {
		return item, errors.New("备份空间已满，请下载并删除旧备份")
	}
	root, e := os.OpenRoot(a.database.dir)
	if e != nil {
		return item, e
	}
	defer root.Close()
	f, e := root.OpenFile(item.ID, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return item, e
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = root.Remove(item.ID)
		}
	}()
	if _, e = f.Write(file); e != nil {
		return item, e
	}
	if e = f.Sync(); e != nil {
		return item, e
	}
	if e = f.Close(); e != nil {
		return item, e
	}
	ok = true
	item.Bytes = int64(len(file))
	return item, nil
}
func (a *Admin) databaseHandler(w http.ResponseWriter, r *http.Request) {
	if !a.globalAPIScope(r) || !a.hasPermission(r, "database.manage") {
		writeJSON(w, 403, map[string]string{"error": "仅超级管理员可管理数据库"})
		return
	}
	db, ok := a.store.(databaseStore)
	if !ok || a.credentialEncryptionKey == "" || a.database.dir == "" {
		writeJSON(w, 503, map[string]string{"error": "数据库管理暂不可用"})
		return
	}
	if r.URL.Path == "/admin/v1/database/reveal" {
		a.databaseReveal(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	path := strings.TrimPrefix(r.URL.Path, "/admin/v1/database")
	if path == "/tables" && r.Method == "GET" {
		tables, e := db.DatabaseTables(ctx)
		if e != nil {
			writeJSON(w, 503, nil)
		} else {
			writeJSON(w, 200, tables)
		}
		return
	}
	if strings.HasPrefix(path, "/rows/") && r.Method == "GET" {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		data, e := db.DatabaseRows(ctx, strings.TrimPrefix(path, "/rows/"), page, 20)
		if e != nil {
			writeJSON(w, 400, map[string]string{"error": "表名或页码无效"})
		} else {
			writeJSON(w, 200, data)
		}
		return
	}
	if path == "/backups" && r.Method == "GET" {
		items := []databaseBackupItem{}
		entries, e := os.ReadDir(a.database.dir)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			writeJSON(w, 503, nil)
			return
		}
		for _, entry := range entries {
			if !databaseBackupName.MatchString(entry.Name()) {
				continue
			}
			info, e := entry.Info()
			if e == nil && info.Mode().IsRegular() {
				items = append(items, databaseBackupItem{ID: entry.Name(), CreatedAt: info.ModTime(), Bytes: info.Size()})
			}
		}
		writeJSON(w, 200, items)
		return
	}
	if !a.database.mu.TryLock() {
		writeJSON(w, 409, map[string]string{"error": "数据库任务正在进行，请稍后重试"})
		return
	}
	defer a.database.mu.Unlock()
	if path == "/import" && r.Method == "POST" {
		reader, e := r.MultipartReader()
		if e != nil {
			writeJSON(w, 400, map[string]string{"error": "请上传本程序生成的备份文件"})
			return
		}
		part, e := reader.NextPart()
		if e != nil || part.FormName() != "current_password" {
			writeJSON(w, 400, nil)
			return
		}
		password, e := io.ReadAll(io.LimitReader(part, 1025))
		_ = part.Close()
		if e != nil || len(password) > 1024 || !a.databasePassword(r, string(password)) {
			writeJSON(w, 403, map[string]string{"error": "请验证当前管理员密码"})
			return
		}
		part, e = reader.NextPart()
		if e != nil || part.FormName() != "backup" {
			writeJSON(w, 400, nil)
			return
		}
		file, e := io.ReadAll(io.LimitReader(part, databaseBackupMaxBytes+1))
		_ = part.Close()
		if e != nil || len(file) > databaseBackupMaxBytes {
			writeJSON(w, 413, map[string]string{"error": "备份文件过大或上传不完整"})
			return
		}

		snapshot, e := a.decodeDatabaseBackup(file)
		if e == nil {
			e = db.ValidateDatabaseSnapshot(ctx, snapshot)
		}
		if e != nil {
			writeJSON(w, 400, map[string]string{"error": "备份无效、密钥不匹配或数据库版本不一致"})
			return
		}
		item, e := a.saveDatabaseBackup(snapshot)
		if e != nil {
			writeJSON(w, 400, map[string]string{"error": e.Error()})
			return
		}
		a.recordAudit(r, "database.backup.import", "database", item.ID, 201, nil)
		writeJSON(w, 201, map[string]any{"backup": item, "tables": len(snapshot.Tables), "created_at": snapshot.CreatedAt})
		return
	}
	var q struct {
		CurrentPassword string `json:"current_password"`
		Confirm         bool   `json:"confirm"`
		BackupID        string `json:"backup_id"`
		Confirmation    string `json:"confirmation"`
	}
	if r.Method != "POST" && r.Method != "DELETE" {
		writeJSON(w, 405, nil)
		return
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	if !a.databasePassword(r, q.CurrentPassword) {
		writeJSON(w, 403, map[string]string{"error": "请验证当前管理员密码"})
		return
	}
	if path == "/backups" && r.Method == "POST" {
		snapshot, e := db.DatabaseSnapshot(ctx)
		if e != nil {
			writeJSON(w, 400, map[string]string{"error": "备份未完成，请稍后重试或使用服务器备份脚本"})
			return
		}
		item, e := a.saveDatabaseBackup(snapshot)
		if e != nil {
			writeJSON(w, 400, map[string]string{"error": e.Error()})
			return
		}
		a.recordAudit(r, "database.backup.create", "database", item.ID, 201, nil)
		writeJSON(w, 201, item)
		return
	}
	_, e := a.databaseFile(q.BackupID)
	if e != nil {
		writeJSON(w, 404, nil)
		return
	}
	root, e := os.OpenRoot(a.database.dir)
	if e != nil {
		writeJSON(w, 404, nil)
		return
	}
	defer root.Close()
	info, e := root.Lstat(q.BackupID)
	if e != nil || !info.Mode().IsRegular() || info.Size() > databaseBackupMaxBytes {
		writeJSON(w, 404, nil)
		return
	}
	if path == "/download" && r.Method == "POST" {
		file, e := root.ReadFile(q.BackupID)
		if e != nil {
			writeJSON(w, 503, nil)
			return
		}
		a.recordAudit(r, "database.backup.download", "database", q.BackupID, 200, nil)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+q.BackupID+`"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(file)
		return
	}
	if path == "/backups" && r.Method == "DELETE" {
		if !q.Confirm {
			writeJSON(w, 400, nil)
			return
		}
		if e = root.Remove(q.BackupID); e != nil {
			writeJSON(w, 503, nil)
			return
		}
		a.recordAudit(r, "database.backup.delete", "database", q.BackupID, 200, nil)
		writeJSON(w, 200, map[string]bool{"deleted": true})
		return
	}
	if path == "/restore" && r.Method == "POST" {
		if !q.Confirm || q.Confirmation != "恢复数据库" || a.database.restart == nil || a.database.maintenance == nil {
			writeJSON(w, 400, map[string]string{"error": "请输入恢复数据库并确认覆盖"})
			return
		}
		file, e := root.ReadFile(q.BackupID)
		if e != nil {
			writeJSON(w, 503, nil)
			return
		}
		snapshot, e := a.decodeDatabaseBackup(file)
		if e == nil {
			e = db.ValidateDatabaseSnapshot(ctx, snapshot)
		}
		if e != nil {
			writeJSON(w, 400, map[string]string{"error": "备份与当前数据库不兼容"})
			return
		}
		release, e := a.database.maintenance.Begin(ctx)
		if e != nil {
			writeJSON(w, 409, map[string]string{"error": "仍有请求正在处理，请稍后重试"})
			return
		}
		restarted := false
		defer func() {
			if !restarted {
				release()
			}
		}()
		current, e := db.DatabaseSnapshot(ctx)
		if e != nil {
			writeJSON(w, 503, nil)
			return
		}
		rollback, e := a.saveDatabaseBackup(current)
		if e != nil {
			writeJSON(w, 400, map[string]string{"error": "恢复前备份未成功，操作已取消"})
			return
		}
		actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
		if e = a.auditor.RecordChecked(ctx, actor, r, "database.restore.requested", "database", q.BackupID, 202, map[string]any{"rollback_backup": rollback.ID}); e != nil {
			writeJSON(w, 503, nil)
			return
		}
		if e = db.RestoreDatabaseSnapshot(ctx, snapshot); e != nil {
			writeJSON(w, 409, map[string]string{"error": "恢复失败，原数据已保留，请核对备份与数据库状态"})
			return
		}
		// All old sessions have been discarded. A restart reloads site and plugin settings.
		restarted = true
		a.auditor.Record(ctx, audit.Actor{Type: "system"}, r, "database.restore.completed", "database", q.BackupID, 200, map[string]any{"rollback_backup": rollback.ID, "operator_uid": a.actorID(r)})
		writeJSON(w, 200, map[string]any{"restored": true, "rollback_backup": rollback.ID, "restart_required": true})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		go func() { time.Sleep(500 * time.Millisecond); a.database.restart() }()
		return
	}
	writeJSON(w, 404, nil)
}
