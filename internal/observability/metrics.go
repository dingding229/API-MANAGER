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
}

type histogram struct {
	buckets []uint64
	sum     float64
	count   uint64
}

func NewMetrics() *Metrics {
	return &Metrics{httpRequests: map[string]uint64{}, httpLatency: map[string]*histogram{}, gatewayRequests: map[string]uint64{}, gatewayErrors: map[string]uint64{}, gatewayLatency: map[string]*histogram{}, started: time.Now()}
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
}

func (m *Metrics) IncRateLimit()        { m.mu.Lock(); m.rateLimitHits++; m.mu.Unlock() }
func (m *Metrics) IncAuthFailure()      { m.mu.Lock(); m.authFailures++; m.mu.Unlock() }
func (m *Metrics) IncSchemaFailure()    { m.mu.Lock(); m.schemaFailures++; m.mu.Unlock() }
func (m *Metrics) IncUpstreamFailure()  { m.mu.Lock(); m.upstreamFailures++; m.mu.Unlock() }
func (m *Metrics) IncPluginFailure()    { m.mu.Lock(); m.pluginFailures++; m.mu.Unlock() }
func (m *Metrics) IncUpstreamRetry()    { m.mu.Lock(); m.upstreamRetries++; m.mu.Unlock() }
func (m *Metrics) IncCircuitRejection() { m.mu.Lock(); m.circuitRejections++; m.mu.Unlock() }

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
