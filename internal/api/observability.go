package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"api-manager/internal/audit"
	"api-manager/internal/observability"
)

func (a *Admin) observabilitySummary(w http.ResponseWriter) {
	if a.observabilityHub == nil || a.metrics == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "embedded observability unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, a.observabilityHub.Dashboard(a.metrics.Snapshot()))
}

func (a *Admin) observabilityLogs(w http.ResponseWriter, r *http.Request) {
	if a.observabilityHub == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "embedded observability unavailable"})
		return
	}
	query := r.URL.Query()
	writeJSON(w, http.StatusOK, a.observabilityHub.QueryLogs(observability.LogQuery{
		Level: query.Get("level"), Search: query.Get("search"), RequestID: query.Get("request_id"), TraceID: query.Get("trace_id"), Limit: queryInt(query, "limit"),
	}))
}

func (a *Admin) clearObservabilityLogs(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "日志清理不支持筛选条件，请确认清理全部应用日志", "code": "invalid_log_cleanup_scope"})
		return
	}
	var input struct {
		Confirm bool `json:"confirm"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || !input.Confirm {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请确认清理全部应用日志", "code": "log_cleanup_confirmation_required"})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "清理请求格式不正确", "code": "invalid_log_cleanup_request"})
		return
	}
	if a.observabilityHub == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "应用日志服务暂不可用", "code": "observability_unavailable"})
		return
	}
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if err := a.auditor.RecordChecked(r.Context(), actor, r, "observability.logs.clear.requested", "application_logs", "all", http.StatusAccepted, nil); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "无法保存审计信息，日志未清理，请稍后重试", "code": "log_cleanup_audit_unavailable"})
		return
	}
	deleted, err := a.observabilityHub.ClearLogs()
	if err != nil {
		a.recordAudit(r, "observability.logs.clear", "application_logs", "all", http.StatusInternalServerError, map[string]any{"success": false})
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法完成日志清理，请检查存储状态后重试", "code": "log_cleanup_failed"})
		return
	}
	a.recordAudit(r, "observability.logs.clear", "application_logs", "all", http.StatusOK, map[string]any{"indexed_entries": deleted, "success": true})
	writeJSON(w, http.StatusOK, map[string]any{"cleared": true, "indexed_entries": deleted})
}

func (a *Admin) observabilityTraces(w http.ResponseWriter, r *http.Request) {
	if a.observabilityHub == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "embedded observability unavailable"})
		return
	}
	query := r.URL.Query()
	writeJSON(w, http.StatusOK, a.observabilityHub.QueryTraces(observability.TraceQuery{
		TraceID: query.Get("trace_id"), Search: query.Get("search"), Status: query.Get("status"), Limit: queryInt(query, "limit"),
	}))
}

func (a *Admin) acknowledgeObservabilityAlert(w http.ResponseWriter, r *http.Request) {
	if a.observabilityHub == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "embedded observability unavailable"})
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/observability/alerts/"), "/ack")
	id, err := url.PathUnescape(id)
	if err != nil || strings.TrimSpace(id) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid alert id"})
		return
	}
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	acknowledgedBy := actor.Email
	if acknowledgedBy == "" {
		acknowledgedBy = actor.Type
	}
	if !a.observabilityHub.AcknowledgeAlert(id, acknowledgedBy) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "alert not found"})
		return
	}
	a.recordAudit(r, "observability.alert.acknowledge", "alert", id, http.StatusOK, nil)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "acknowledged": true})
}

func queryInt(values url.Values, key string) int {
	value, _ := strconv.Atoi(values.Get(key))
	return value
}
