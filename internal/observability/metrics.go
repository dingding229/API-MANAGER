package observability

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type Metrics struct {
	mu                sync.RWMutex
	httpRequests      map[string]uint64
	httpLatency       map[string]*histogram
	gatewayRequests   map[string]uint64
	gatewayErrors     map[string]uint64
	gatewayLatency    map[string]*histogram
	rateLimitHits     uint64
	authFailures      uint64
	schemaFailures    uint64
	upstreamFailures  uint64
	pluginFailures    uint64
	upstreamRetries   uint64
	circuitRejections uint64
	inflight          int64
	started           time.Time
	points            map[int64]*MetricPoint
}

type histogram struct {
	buckets []uint64
	sum     float64
	count   uint64
}

type MetricPoint struct {
	Timestamp        time.Time `json:"timestamp"`
	Requests         uint64    `json:"requests"`
	Errors           uint64    `json:"errors"`
	AverageLatencyMS float64   `json:"average_latency_ms"`
	MaxLatencyMS     float64   `json:"max_latency_ms"`
	RateLimitHits    uint64    `json:"rate_limit_hits"`
	UpstreamFailures uint64    `json:"upstream_failures"`
	PluginFailures   uint64    `json:"plugin_failures"`
	durationSumMS    float64
}

type MetricsSnapshot struct {
	UptimeSeconds     float64       `json:"uptime_seconds"`
	HTTPRequestsTotal uint64        `json:"http_requests_total"`
	GatewayRequests   uint64        `json:"gateway_requests_total"`
	GatewayErrors     uint64        `json:"gateway_errors_total"`
	GatewayErrorRate  float64       `json:"gateway_error_rate"`
	AverageLatencyMS  float64       `json:"average_latency_ms"`
	P95LatencyMS      float64       `json:"p95_latency_ms"`
	Inflight          int64         `json:"inflight"`
	RateLimitHits     uint64        `json:"rate_limit_hits"`
	AuthFailures      uint64        `json:"auth_failures"`
	SchemaFailures    uint64        `json:"schema_failures"`
	UpstreamFailures  uint64        `json:"upstream_failures"`
	PluginFailures    uint64        `json:"plugin_failures"`
	UpstreamRetries   uint64        `json:"upstream_retries"`
	CircuitRejections uint64        `json:"circuit_rejections"`
	Series            []MetricPoint `json:"series"`
}

type RecentMetrics struct {
	Requests         uint64
	Errors           uint64
	AverageLatencyMS float64
	RateLimitHits    uint64
	UpstreamFailures uint64
	PluginFailures   uint64
}

func NewMetrics() *Metrics {
	return &Metrics{httpRequests: map[string]uint64{}, httpLatency: map[string]*histogram{}, gatewayRequests: map[string]uint64{}, gatewayErrors: map[string]uint64{}, gatewayLatency: map[string]*histogram{}, started: time.Now(), points: map[int64]*MetricPoint{}}
}

func (m *Metrics) ObserveHTTP(route, method string, status int, duration time.Duration) {
	method = methodLabel(method)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpRequests[labelKey(route, method, strconv.Itoa(status))]++
	key := labelKey(route, method)
	hist := m.httpLatency[key]
	if hist == nil {
		hist = &histogram{buckets: make([]uint64, len(latencyBuckets))}
		m.httpLatency[key] = hist
	}
	seconds := duration.Seconds()
	for index, boundary := range latencyBuckets {
		if seconds <= boundary {
			hist.buckets[index]++
		}
	}
	hist.count++
	hist.sum += seconds
}

func (m *Metrics) ObserveGateway(method string, status int, duration time.Duration) {
	method = methodLabel(method)
	m.mu.Lock()
	defer m.mu.Unlock()
	statusLabel := strconv.Itoa(status)
	m.gatewayRequests[labelKey(method, statusLabel)]++
	if status >= 500 {
		m.gatewayErrors[labelKey("server", method)]++
	}
	if status >= 400 && status < 500 {
		m.gatewayErrors[labelKey("client", method)]++
	}
	hist := m.gatewayLatency[method]
	if hist == nil {
		hist = &histogram{buckets: make([]uint64, len(latencyBuckets))}
		m.gatewayLatency[method] = hist
	}
	seconds := duration.Seconds()
	for index, boundary := range latencyBuckets {
		if seconds <= boundary {
			hist.buckets[index]++
		}
	}
	hist.count++
	hist.sum += seconds
	point := m.pointLocked(time.Now())
	point.Requests++
	if status >= 400 {
		point.Errors++
	}
	durationMS := duration.Seconds() * 1000
	point.durationSumMS += durationMS
	if durationMS > point.MaxLatencyMS {
		point.MaxLatencyMS = durationMS
	}
}

func (m *Metrics) IncRateLimit() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rateLimitHits++
	m.pointLocked(time.Now()).RateLimitHits++
}
func (m *Metrics) IncAuthFailure()   { m.mu.Lock(); m.authFailures++; m.mu.Unlock() }
func (m *Metrics) IncSchemaFailure() { m.mu.Lock(); m.schemaFailures++; m.mu.Unlock() }
func (m *Metrics) IncUpstreamFailure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upstreamFailures++
	m.pointLocked(time.Now()).UpstreamFailures++
}
func (m *Metrics) IncPluginFailure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pluginFailures++
	m.pointLocked(time.Now()).PluginFailures++
}
func (m *Metrics) IncUpstreamRetry()    { m.mu.Lock(); m.upstreamRetries++; m.mu.Unlock() }
func (m *Metrics) IncCircuitRejection() { m.mu.Lock(); m.circuitRejections++; m.mu.Unlock() }

func (m *Metrics) pointLocked(now time.Time) *MetricPoint {
	minute := now.UTC().Truncate(time.Minute)
	key := minute.Unix()
	point := m.points[key]
	if point == nil {
		point = &MetricPoint{Timestamp: minute}
		m.points[key] = point
	}
	cutoff := minute.Add(-2 * time.Hour).Unix()
	for timestamp := range m.points {
		if timestamp < cutoff {
			delete(m.points, timestamp)
		}
	}
	return point
}

func (m *Metrics) Snapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snapshot := MetricsSnapshot{
		UptimeSeconds: time.Since(m.started).Seconds(), Inflight: m.inflight,
		RateLimitHits: m.rateLimitHits, AuthFailures: m.authFailures,
		SchemaFailures: m.schemaFailures, UpstreamFailures: m.upstreamFailures,
		PluginFailures: m.pluginFailures, UpstreamRetries: m.upstreamRetries,
		CircuitRejections: m.circuitRejections,
	}
	for _, count := range m.httpRequests {
		snapshot.HTTPRequestsTotal += count
	}
	for key, count := range m.gatewayRequests {
		snapshot.GatewayRequests += count
		parts := strings.Split(key, "\xff")
		if len(parts) == 2 {
			status, _ := strconv.Atoi(parts[1])
			if status >= 400 {
				snapshot.GatewayErrors += count
			}
		}
	}
	if snapshot.GatewayRequests > 0 {
		snapshot.GatewayErrorRate = float64(snapshot.GatewayErrors) / float64(snapshot.GatewayRequests)
	}
	var latencyCount uint64
	var latencySum float64
	combinedBuckets := make([]uint64, len(latencyBuckets))
	for _, histogram := range m.gatewayLatency {
		latencyCount += histogram.count
		latencySum += histogram.sum
		for index, count := range histogram.buckets {
			combinedBuckets[index] += count
		}
	}
	if latencyCount > 0 {
		snapshot.AverageLatencyMS = latencySum * 1000 / float64(latencyCount)
		target := uint64(float64(latencyCount)*0.95 + 0.999999)
		for index, count := range combinedBuckets {
			if count >= target {
				snapshot.P95LatencyMS = latencyBuckets[index] * 1000
				break
			}
		}
		if snapshot.P95LatencyMS == 0 {
			snapshot.P95LatencyMS = latencyBuckets[len(latencyBuckets)-1] * 1000
		}
	}
	keys := make([]int64, 0, len(m.points))
	cutoff := time.Now().UTC().Add(-60 * time.Minute).Unix()
	for key := range m.points {
		if key >= cutoff {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, key := range keys {
		point := *m.points[key]
		if point.Requests > 0 {
			point.AverageLatencyMS = point.durationSumMS / float64(point.Requests)
		}
		point.durationSumMS = 0
		snapshot.Series = append(snapshot.Series, point)
	}
	return snapshot
}

func (s MetricsSnapshot) Recent(window time.Duration) RecentMetrics {
	cutoff := time.Now().UTC().Add(-window)
	result := RecentMetrics{}
	var duration float64
	for _, point := range s.Series {
		if point.Timestamp.Before(cutoff) {
			continue
		}
		result.Requests += point.Requests
		result.Errors += point.Errors
		result.RateLimitHits += point.RateLimitHits
		result.UpstreamFailures += point.UpstreamFailures
		result.PluginFailures += point.PluginFailures
		duration += point.AverageLatencyMS * float64(point.Requests)
	}
	if result.Requests > 0 {
		result.AverageLatencyMS = duration / float64(result.Requests)
	}
	return result
}

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		m.mu.Lock()
		m.inflight++
		m.mu.Unlock()
		capture := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(capture, r)
		m.mu.Lock()
		m.inflight--
		m.mu.Unlock()
		status := capture.status
		if status == 0 {
			status = http.StatusOK
		}
		m.ObserveHTTP(routeLabel(r.URL.Path), r.Method, status, time.Since(started))
	})
}

func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writeCounterFamily(w, "api_manager_http_requests_total", "Total HTTP requests processed by route, method and status", m.httpRequests, []string{"route", "method", "status"})
	fmt.Fprintln(w, "# HELP api_manager_http_request_duration_seconds HTTP request latency in seconds")
	fmt.Fprintln(w, "# TYPE api_manager_http_request_duration_seconds histogram")
	httpKeys := make([]string, 0, len(m.httpLatency))
	for key := range m.httpLatency {
		httpKeys = append(httpKeys, key)
	}
	sort.Strings(httpKeys)
	for _, key := range httpKeys {
		values := strings.Split(key, "\xff")
		hist := m.httpLatency[key]
		for index, boundary := range latencyBuckets {
			fmt.Fprintf(w, "api_manager_http_request_duration_seconds_bucket{route=%q,method=%q,le=%q} %d\n", values[0], values[1], strconv.FormatFloat(boundary, 'g', -1, 64), hist.buckets[index])
		}
		fmt.Fprintf(w, "api_manager_http_request_duration_seconds_bucket{route=%q,method=%q,le=\"+Inf\"} %d\n", values[0], values[1], hist.count)
		fmt.Fprintf(w, "api_manager_http_request_duration_seconds_sum{route=%q,method=%q} %g\n", values[0], values[1], hist.sum)
		fmt.Fprintf(w, "api_manager_http_request_duration_seconds_count{route=%q,method=%q} %d\n", values[0], values[1], hist.count)
	}
	writeCounterFamily(w, "api_manager_gateway_requests_total", "Total API gateway requests by method and status", m.gatewayRequests, []string{"method", "status"})
	writeCounterFamily(w, "api_manager_gateway_errors_total", "Total gateway errors by class and method", m.gatewayErrors, []string{"class", "method"})
	fmt.Fprintln(w, "# HELP api_manager_gateway_rate_limit_hits_total Gateway requests rejected by rate limiting")
	fmt.Fprintln(w, "# TYPE api_manager_gateway_rate_limit_hits_total counter")
	fmt.Fprintf(w, "api_manager_gateway_rate_limit_hits_total %d\n", m.rateLimitHits)
	fmt.Fprintln(w, "# HELP api_manager_gateway_auth_failures_total Gateway authentication failures")
	fmt.Fprintln(w, "# TYPE api_manager_gateway_auth_failures_total counter")
	fmt.Fprintf(w, "api_manager_gateway_auth_failures_total %d\n", m.authFailures)
	fmt.Fprintln(w, "# HELP api_manager_gateway_schema_validation_failures_total Request or response schema validation failures")
	fmt.Fprintln(w, "# TYPE api_manager_gateway_schema_validation_failures_total counter")
	fmt.Fprintf(w, "api_manager_gateway_schema_validation_failures_total %d\n", m.schemaFailures)
	fmt.Fprintln(w, "# HELP api_manager_gateway_upstream_failures_total Upstream proxy failures")
	fmt.Fprintln(w, "# TYPE api_manager_gateway_upstream_failures_total counter")
	fmt.Fprintf(w, "api_manager_gateway_upstream_failures_total %d\n", m.upstreamFailures)
	fmt.Fprintln(w, "# HELP api_manager_gateway_plugin_failures_total Plugin execution failures")
	fmt.Fprintln(w, "# TYPE api_manager_gateway_plugin_failures_total counter")
	fmt.Fprintf(w, "api_manager_gateway_plugin_failures_total %d\n", m.pluginFailures)
	fmt.Fprintln(w, "# HELP api_manager_gateway_upstream_retries_total Upstream retry attempts")
	fmt.Fprintln(w, "# TYPE api_manager_gateway_upstream_retries_total counter")
	fmt.Fprintf(w, "api_manager_gateway_upstream_retries_total %d\n", m.upstreamRetries)
	fmt.Fprintln(w, "# HELP api_manager_gateway_circuit_breaker_rejections_total Requests rejected by an open upstream circuit")
	fmt.Fprintln(w, "# TYPE api_manager_gateway_circuit_breaker_rejections_total counter")
	fmt.Fprintf(w, "api_manager_gateway_circuit_breaker_rejections_total %d\n", m.circuitRejections)
	fmt.Fprintln(w, "# HELP api_manager_gateway_request_duration_seconds Gateway request latency in seconds")
	fmt.Fprintln(w, "# TYPE api_manager_gateway_request_duration_seconds histogram")
	methods := make([]string, 0, len(m.gatewayLatency))
	for method := range m.gatewayLatency {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	for _, method := range methods {
		hist := m.gatewayLatency[method]
		for index, boundary := range latencyBuckets {
			fmt.Fprintf(w, "api_manager_gateway_request_duration_seconds_bucket{method=%q,le=%q} %d\n", method, strconv.FormatFloat(boundary, 'g', -1, 64), hist.buckets[index])
		}
		fmt.Fprintf(w, "api_manager_gateway_request_duration_seconds_bucket{method=%q,le=\"+Inf\"} %d\n", method, hist.count)
		fmt.Fprintf(w, "api_manager_gateway_request_duration_seconds_sum{method=%q} %g\n", method, hist.sum)
		fmt.Fprintf(w, "api_manager_gateway_request_duration_seconds_count{method=%q} %d\n", method, hist.count)
	}
	fmt.Fprintln(w, "# HELP api_manager_http_inflight_requests Current in-flight HTTP requests")
	fmt.Fprintln(w, "# TYPE api_manager_http_inflight_requests gauge")
	fmt.Fprintf(w, "api_manager_http_inflight_requests %d\n", m.inflight)
	fmt.Fprintln(w, "# HELP api_manager_process_uptime_seconds Process uptime in seconds")
	fmt.Fprintln(w, "# TYPE api_manager_process_uptime_seconds gauge")
	fmt.Fprintf(w, "api_manager_process_uptime_seconds %.3f\n", time.Since(m.started).Seconds())
	fmt.Fprintln(w, "# HELP api_manager_up API manager process status")
	fmt.Fprintln(w, "# TYPE api_manager_up gauge")
	fmt.Fprintln(w, "api_manager_up 1")
}

func writeCounterFamily(w http.ResponseWriter, name, help string, values map[string]uint64, labels []string) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		valuesForKey := strings.Split(key, "\xff")
		parts := make([]string, 0, len(labels))
		for index, label := range labels {
			parts = append(parts, label+"="+strconv.Quote(valuesForKey[index]))
		}
		fmt.Fprintf(w, "%s{%s} %d\n", name, strings.Join(parts, ","), values[key])
	}
}
func labelKey(values ...string) string { return strings.Join(values, "\xff") }
func routeLabel(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/"):
		return "gateway"
	case strings.HasPrefix(path, "/admin/"):
		return "admin"
	case strings.HasPrefix(path, "/auth/"):
		return "auth"
	case strings.HasPrefix(path, "/console/"):
		return "console"
	default:
		return "other"
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		if w.status == 0 {
			w.WriteHeader(http.StatusOK)
		}
		flusher.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (w *statusWriter) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

func (w *statusWriter) ReadFrom(reader io.Reader) (int64, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := readerFrom.ReadFrom(reader)

		return n, err
	}
	return io.Copy(struct{ io.Writer }{Writer: w}, reader)
}

func methodLabel(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodTrace, http.MethodConnect:
		return method
	default:
		return "OTHER"
	}
}
