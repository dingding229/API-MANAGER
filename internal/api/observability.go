package api

import (
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
