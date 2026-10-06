package api

import (
	"api-manager/internal/audit"
	"api-manager/internal/store"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

func (a *Admin) pluginCache(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/apis/"), "/cache")
	api, err := a.store.GetAPI(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "接口不存在"})
		return
	}
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "缓存服务暂不可用"})
		return
	}
	cached, ok := a.store.(store.PluginCacheStore)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "缓存存储不可用"})
		return
	}
	if r.Method == http.MethodGet {
		stats, err := cached.PluginCacheStats(r.Context(), id, time.Now())
		if err != nil {
			writeJSON(w, 503, map[string]string{"error": "无法读取缓存统计"})
			return
		}
		writeJSON(w, 200, map[string]any{"config": api.PluginCache, "stats": stats})
		return
	}
	var input struct {
		Confirm bool `json:"confirm"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if r.URL.RawQuery != "" || decoder.Decode(&input) != nil || !input.Confirm || decoder.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "请确认清理此接口的缓存"})
		return
	}
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if err := a.auditor.RecordChecked(r.Context(), actor, r, "api.cache.clear.requested", "api", id, 202, nil); err != nil {
		writeJSON(w, 503, map[string]string{"error": "审计信息无法保存，缓存未清理"})
		return
	}
	if err := cached.ClearPluginCache(r.Context(), id); err != nil {
		writeJSON(w, 503, map[string]string{"error": "缓存清理失败，请稍后重试"})
		return
	}
	a.recordAudit(r, "api.cache.clear", "api", id, 200, nil)
	writeJSON(w, 200, map[string]bool{"cleared": true})
}
