package observability

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
)

// Hub is the lightweight embedded observability backend. It provides bounded
// application-owned logs, traces and alerts even when the optional upstream
// six-component stack is disabled.
type Hub struct {
	mu             sync.RWMutex
	logs           []LogEntry
	traces         []TraceEntry
	alerts         map[string]Alert
	maxLogs        int
	maxTraces      int
	logJournal     *rollingJournal
	traceJournal   *rollingJournal
	lastWriteError string
}

type HubOptions struct {
	Directory    string
	MaxLogs      int
	MaxTraces    int
	MaxFileBytes int64
}

type LogEntry struct {
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id,omitempty"`
	TraceID   string         `json:"trace_id,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
}

type TraceEntry struct {
	TraceID      string            `json:"trace_id"`
	SpanID       string            `json:"span_id"`
	ParentSpanID string            `json:"parent_span_id,omitempty"`
	Name         string            `json:"name"`
	Kind         string            `json:"kind"`
	Status       string            `json:"status"`
	StartedAt    time.Time         `json:"started_at"`
	EndedAt      time.Time         `json:"ended_at"`
	DurationMS   float64           `json:"duration_ms"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

type Alert struct {
	ID             string    `json:"id"`
	Severity       string    `json:"severity"`
	Title          string    `json:"title"`
	Message        string    `json:"message"`
	Source         string    `json:"source"`
	Status         string    `json:"status"`
	Acknowledged   bool      `json:"acknowledged"`
	AcknowledgedBy string    `json:"acknowledged_by,omitempty"`
	FirstSeen      time.Time `json:"first_seen"`
	LastSeen       time.Time `json:"last_seen"`
}

type LogQuery struct {
	Level     string
	Search    string
	RequestID string
	TraceID   string
	Limit     int
}

type TraceQuery struct {
	TraceID string
	Search  string
	Status  string
	Limit   int
}

type LogPage struct {
	Items []LogEntry `json:"items"`
	Total int        `json:"total"`
}

type TracePage struct {
	Items []TraceEntry `json:"items"`
	Total int          `json:"total"`
}

type StorageStatus struct {
	Logs           int    `json:"logs"`
	Traces         int    `json:"traces"`
	MaxLogs        int    `json:"max_logs"`
	MaxTraces      int    `json:"max_traces"`
	Persistent     bool   `json:"persistent"`
	Directory      string `json:"directory,omitempty"`
	LastWriteError string `json:"last_write_error,omitempty"`
}

type Dashboard struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Metrics     MetricsSnapshot `json:"metrics"`
	Alerts      []Alert         `json:"alerts"`
	Storage     StorageStatus   `json:"storage"`
}

func NewHub(options HubOptions) (*Hub, error) {
	if options.MaxLogs <= 0 {
		options.MaxLogs = 5000
	}
	if options.MaxTraces <= 0 {
		options.MaxTraces = 2000
	}
	if options.MaxFileBytes <= 0 {
		options.MaxFileBytes = 16 << 20
	}
	hub := &Hub{maxLogs: options.MaxLogs, maxTraces: options.MaxTraces, alerts: make(map[string]Alert)}
	if strings.TrimSpace(options.Directory) == "" {
		return hub, nil
	}
	if err := os.MkdirAll(options.Directory, 0o750); err != nil {
		return nil, fmt.Errorf("create embedded observability directory: %w", err)
	}
	logJournal, err := newRollingJournal(filepath.Join(options.Directory, "logs.jsonl"), options.MaxFileBytes)
	if err != nil {
		return nil, err
	}
	traceJournal, err := newRollingJournal(filepath.Join(options.Directory, "traces.jsonl"), options.MaxFileBytes)
	if err != nil {
		_ = logJournal.Close()
		return nil, err
	}
	hub.logJournal = logJournal
	hub.traceJournal = traceJournal
	hub.loadLogs(logJournal.path)
	hub.loadTraces(traceJournal.path)
	return hub, nil
}

func (h *Hub) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var errs []error
	if h.logJournal != nil {
		errs = append(errs, h.logJournal.Close())
	}
	if h.traceJournal != nil {
		errs = append(errs, h.traceJournal.Close())
	}
	return errors.Join(errs...)
}

// Write implements io.Writer so slog JSON output can be mirrored directly into
// the embedded log index without a sidecar log collector.
func (h *Hub) Write(payload []byte) (int, error) {
	for _, line := range splitLines(payload) {
		var raw map[string]any
		if json.Unmarshal(line, &raw) != nil {
			continue
		}
		entry := logEntryFromMap(raw)
		if entry.Message == "http request" && noisyPath(stringField(entry.Fields, "path")) {
			continue
		}
		h.recordLog(entry)
	}
	return len(payload), nil
}

func (h *Hub) recordLog(entry LogEntry) {
	entry = sanitizeLogEntry(entry)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = appendBounded(h.logs, entry, h.maxLogs)
	if h.logJournal != nil {
		line, _ := json.Marshal(entry)
		if err := h.logJournal.WriteLine(line); err != nil {
			h.lastWriteError = err.Error()
		}
	}
}

func (h *Hub) RecordTrace(entry TraceEntry) {
	entry.Attributes = sanitizeStringMap(entry.Attributes)
	if noisyPath(entry.Attributes["url.path"]) {
		return
	}
	line, _ := json.Marshal(entry)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.traces = appendBounded(h.traces, entry, h.maxTraces)
	if h.traceJournal != nil {
		if err := h.traceJournal.WriteLine(line); err != nil {
			h.lastWriteError = err.Error()
		}
	}
}

func (h *Hub) QueryLogs(query LogQuery) LogPage {
	query.Limit = normalizedLimit(query.Limit, 100, 500)
	level := strings.ToUpper(strings.TrimSpace(query.Level))
	search := strings.ToLower(strings.TrimSpace(query.Search))
	h.mu.RLock()
	defer h.mu.RUnlock()
	items := make([]LogEntry, 0, min(query.Limit, len(h.logs)))
	total := 0
	for index := len(h.logs) - 1; index >= 0; index-- {
		entry := h.logs[index]
		if level != "" && strings.ToUpper(entry.Level) != level {
			continue
		}
		if query.RequestID != "" && entry.RequestID != query.RequestID {
			continue
		}
		if query.TraceID != "" && entry.TraceID != query.TraceID {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(entry.Message+" "+fieldsText(entry.Fields)), search) {
			continue
		}
		total++
		if len(items) < query.Limit {
			items = append(items, entry)
		}
	}
	return LogPage{Items: items, Total: total}
}

func (h *Hub) QueryTraces(query TraceQuery) TracePage {
	query.Limit = normalizedLimit(query.Limit, 100, 500)
	search := strings.ToLower(strings.TrimSpace(query.Search))
	status := strings.ToLower(strings.TrimSpace(query.Status))
	h.mu.RLock()
	defer h.mu.RUnlock()
	items := make([]TraceEntry, 0, min(query.Limit, len(h.traces)))
	total := 0
	for index := len(h.traces) - 1; index >= 0; index-- {
		entry := h.traces[index]
		if query.TraceID != "" && entry.TraceID != query.TraceID {
			continue
		}
		if status != "" && strings.ToLower(entry.Status) != status {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(entry.TraceID+" "+entry.SpanID+" "+entry.Name+" "+stringMapText(entry.Attributes)), search) {
			continue
		}
		total++
		if len(items) < query.Limit {
			items = append(items, entry)
		}
	}
	return TracePage{Items: items, Total: total}
}

func (h *Hub) Dashboard(metrics MetricsSnapshot) Dashboard {
	h.evaluateAlerts(metrics)
	h.mu.RLock()
	defer h.mu.RUnlock()
	alerts := make([]Alert, 0, len(h.alerts))
	for _, alert := range h.alerts {
		alerts = append(alerts, alert)
	}
	sort.Slice(alerts, func(i, j int) bool {
		if alerts[i].Status != alerts[j].Status {
			return alerts[i].Status == "firing"
		}
		return alerts[i].LastSeen.After(alerts[j].LastSeen)
	})
	storage := StorageStatus{Logs: len(h.logs), Traces: len(h.traces), MaxLogs: h.maxLogs, MaxTraces: h.maxTraces, Persistent: h.logJournal != nil && h.traceJournal != nil, LastWriteError: h.lastWriteError}
	if h.logJournal != nil {
		storage.Directory = filepath.Dir(h.logJournal.path)
	}
	return Dashboard{GeneratedAt: time.Now().UTC(), Metrics: metrics, Alerts: alerts, Storage: storage}
}

func (h *Hub) AcknowledgeAlert(id, actor string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	alert, ok := h.alerts[id]
	if !ok {
		return false
	}
	alert.Acknowledged = true
	alert.AcknowledgedBy = actor
	alert.LastSeen = time.Now().UTC()
	h.alerts[id] = alert
	return true
}

func (h *Hub) evaluateAlerts(metrics MetricsSnapshot) {
	now := time.Now().UTC()
	recent := metrics.Recent(5 * time.Minute)
	errorRate := 0.0
	if recent.Requests > 0 {
		errorRate = float64(recent.Errors) / float64(recent.Requests)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.setAlert(now, "http-error-rate", recent.Requests >= 10 && errorRate >= 0.05, "critical", "HTTP 错误率过高", fmt.Sprintf("最近 5 分钟错误率 %.1f%%（%d/%d）", errorRate*100, recent.Errors, recent.Requests), "内置指标")
	h.setAlert(now, "http-latency", recent.Requests >= 10 && recent.AverageLatencyMS >= 1000, "warning", "HTTP 平均延迟过高", fmt.Sprintf("最近 5 分钟平均延迟 %.0f ms", recent.AverageLatencyMS), "内置指标")
	h.setAlert(now, "rate-limit", recent.RateLimitHits > 0, "warning", "出现限流拒绝", fmt.Sprintf("最近 5 分钟拒绝 %d 次请求", recent.RateLimitHits), "网关限流")
	h.setAlert(now, "upstream-failure", recent.UpstreamFailures > 0, "critical", "上游调用失败", fmt.Sprintf("最近 5 分钟发生 %d 次上游失败", recent.UpstreamFailures), "上游代理")
	h.setAlert(now, "plugin-failure", recent.PluginFailures > 0, "warning", "插件执行失败", fmt.Sprintf("最近 5 分钟发生 %d 次插件失败", recent.PluginFailures), "WASM 插件")
}

func (h *Hub) setAlert(now time.Time, id string, active bool, severity, title, message, source string) {
	alert, exists := h.alerts[id]
	if active {
		if !exists || alert.Status == "resolved" {
			alert = Alert{ID: id, FirstSeen: now}
		}
		alert.Severity, alert.Title, alert.Message, alert.Source = severity, title, message, source
		alert.Status = "firing"
		alert.LastSeen = now
		h.alerts[id] = alert
		return
	}
	if exists && alert.Status == "firing" {
		alert.Status = "resolved"
		alert.LastSeen = now
		h.alerts[id] = alert
	}
}

func (h *Hub) loadLogs(path string) {
	for _, file := range journalFiles(path) {
		if err := scanJSONLines(file, func(line []byte) {
			var entry LogEntry
			if json.Unmarshal(line, &entry) == nil && !entry.Timestamp.IsZero() && entry.Message != "" {
				h.logs = appendBounded(h.logs, sanitizeLogEntry(entry), h.maxLogs)
				return
			}
			var raw map[string]any
			if json.Unmarshal(line, &raw) == nil {
				h.logs = appendBounded(h.logs, logEntryFromMap(raw), h.maxLogs)
			}
		}); err != nil {
			h.lastWriteError = "load logs: " + err.Error()
		}
	}
}

func (h *Hub) loadTraces(path string) {
	for _, file := range journalFiles(path) {
		if err := scanJSONLines(file, func(line []byte) {
			var entry TraceEntry
			if json.Unmarshal(line, &entry) == nil && entry.TraceID != "" && !entry.StartedAt.IsZero() {
				entry.Attributes = sanitizeStringMap(entry.Attributes)
				h.traces = appendBounded(h.traces, entry, h.maxTraces)
			}
		}); err != nil {
			h.lastWriteError = "load traces: " + err.Error()
		}
	}
}

func logEntryFromMap(raw map[string]any) LogEntry {
	timestamp := time.Now().UTC()
	if value := stringField(raw, "time"); value != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			timestamp = parsed
		}
	}
	fields := make(map[string]any, len(raw))
	for key, value := range raw {
		if key == "time" || key == "level" || key == "msg" {
			continue
		}
		fields[key] = sanitizeValue(key, value)
	}
	return LogEntry{ID: strconv.FormatInt(timestamp.UnixNano(), 36), Timestamp: timestamp, Level: stringField(raw, "level"), Message: stringField(raw, "msg"), RequestID: stringField(fields, "request_id"), TraceID: stringField(fields, "trace_id"), Fields: fields}
}

func noisyPath(path string) bool {
	return strings.HasPrefix(path, "/console/") || strings.HasPrefix(path, "/admin/v1/observability") || strings.HasPrefix(path, "/health/") || path == "/metrics"
}

func sensitiveField(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "password") || strings.Contains(key, "secret") || strings.Contains(key, "token") || strings.Contains(key, "api_key") || strings.Contains(key, "authorization")
}

func sanitizeLogEntry(entry LogEntry) LogEntry {
	entry.Fields = sanitizeMap(entry.Fields)
	entry.RequestID = stringField(entry.Fields, "request_id")
	entry.TraceID = stringField(entry.Fields, "trace_id")
	return entry
}

func sanitizeMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = sanitizeValue(key, value)
	}
	return result
}

func sanitizeStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		if sensitiveField(key) {
			result[key] = "[REDACTED]"
		} else {
			result[key] = value
		}
	}
	return result
}

func sanitizeValue(key string, value any) any {
	if sensitiveField(key) {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case map[string]any:
		return sanitizeMap(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = sanitizeValue("", item)
		}
		return result
	default:
		return value
	}
}

func stringField(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func fieldsText(values map[string]any) string {
	parts := make([]string, 0, len(values))
	for key, value := range values {
		parts = append(parts, key+"="+fmt.Sprint(value))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

func stringMapText(values map[string]string) string {
	parts := make([]string, 0, len(values))
	for key, value := range values {
		parts = append(parts, key+"="+value)
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

func splitLines(payload []byte) [][]byte {
	lines := make([][]byte, 0, 1)
	for _, line := range strings.Split(strings.TrimSpace(string(payload)), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, []byte(line))
		}
	}
	return lines
}

func appendBounded[T any](items []T, item T, maximum int) []T {
	if maximum <= 0 {
		return items
	}
	if len(items) >= maximum {
		copy(items, items[len(items)-maximum+1:])
		items = items[:maximum-1]
	}
	return append(items, item)
}

func normalizedLimit(value, fallback, maximum int) int {
	if value <= 0 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func journalFiles(path string) []string { return []string{path + ".2", path + ".1", path} }

func scanJSONLines(path string, visit func([]byte)) error {
	// #nosec G304 -- path is an internal journal path joined to the operator-configured observability directory.
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		visit(append([]byte(nil), scanner.Bytes()...))
	}
	return scanner.Err()
}

type rollingJournal struct {
	path     string
	file     *os.File
	size     int64
	maxBytes int64
}

func newRollingJournal(path string, maxBytes int64) (*rollingJournal, error) {
	// #nosec G304 -- path is an internal journal path joined to the operator-configured observability directory.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open embedded observability journal: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &rollingJournal{path: path, file: file, size: info.Size(), maxBytes: maxBytes}, nil
}

func (j *rollingJournal) WriteLine(line []byte) error {
	if j == nil || j.file == nil {
		return nil
	}
	if j.size+int64(len(line))+1 > j.maxBytes {
		if err := j.rotate(); err != nil {
			return err
		}
	}
	written, err := j.file.Write(append(append([]byte(nil), line...), '\n'))
	j.size += int64(written)
	return err
}

func (j *rollingJournal) rotate() error {
	if err := j.file.Close(); err != nil {
		return err
	}
	_ = os.Remove(j.path + ".2")
	if _, err := os.Stat(j.path + ".1"); err == nil {
		if err := os.Rename(j.path+".1", j.path+".2"); err != nil {
			return err
		}
	}
	if _, err := os.Stat(j.path); err == nil {
		if err := os.Rename(j.path, j.path+".1"); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(j.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	j.file, j.size = file, 0
	return nil
}

func (j *rollingJournal) Close() error {
	if j == nil || j.file == nil {
		return nil
	}
	return j.file.Close()
}

type embeddedSpanExporter struct{ hub *Hub }

func (e *embeddedSpanExporter) ExportSpans(_ context.Context, spans []tracesdk.ReadOnlySpan) error {
	for _, span := range spans {
		attributes := make(map[string]string, len(span.Attributes()))
		for _, item := range span.Attributes() {
			attributes[string(item.Key)] = attributeValue(item.Value)
		}
		status := "ok"
		if span.Status().Code == codes.Error {
			status = "error"
		}
		parent := ""
		if span.Parent().IsValid() {
			parent = span.Parent().SpanID().String()
		}
		e.hub.RecordTrace(TraceEntry{TraceID: span.SpanContext().TraceID().String(), SpanID: span.SpanContext().SpanID().String(), ParentSpanID: parent, Name: span.Name(), Kind: span.SpanKind().String(), Status: status, StartedAt: span.StartTime().UTC(), EndedAt: span.EndTime().UTC(), DurationMS: float64(span.EndTime().Sub(span.StartTime()).Microseconds()) / 1000, Attributes: attributes})
	}
	return nil
}

func (e *embeddedSpanExporter) Shutdown(context.Context) error { return nil }

func attributeValue(value attribute.Value) string {
	switch value.Type() {
	case attribute.BOOL:
		return strconv.FormatBool(value.AsBool())
	case attribute.INT64:
		return strconv.FormatInt(value.AsInt64(), 10)
	case attribute.FLOAT64:
		return strconv.FormatFloat(value.AsFloat64(), 'f', -1, 64)
	case attribute.STRING:
		return value.AsString()
	default:
		return value.Emit()
	}
}

var _ io.Writer = (*Hub)(nil)
var _ tracesdk.SpanExporter = (*embeddedSpanExporter)(nil)
