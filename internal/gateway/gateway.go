package gateway

import (
	"api-manager/internal/upstream/security"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"api-manager/internal/auth"
	"api-manager/internal/httpx"
	"api-manager/internal/model"
	"api-manager/internal/observability"
	"api-manager/internal/plugin"
	"api-manager/internal/ratelimit"
	"api-manager/internal/resilience"
	apiSchema "api-manager/internal/schema"
	"api-manager/internal/store"
	"api-manager/internal/upstream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Gateway struct {
	credentials    upstream.Credentials
	store          store.Store
	plugins        *plugin.Registry
	limiter        ratelimit.Limiter
	logger         *slog.Logger
	requests       atomic.Uint64
	metrics        *observability.Metrics
	breaker        *resilience.CircuitBreaker
	productionMode bool
}

func New(s store.Store, plugins *plugin.Registry, limiter ratelimit.Limiter, logger *slog.Logger) *Gateway {
	return NewWithMetrics(s, plugins, limiter, logger, nil)
}

func NewWithMetrics(s store.Store, plugins *plugin.Registry, limiter ratelimit.Limiter, logger *slog.Logger, metrics *observability.Metrics) *Gateway {
	return &Gateway{store: s, plugins: plugins, limiter: limiter, logger: logger, metrics: metrics, breaker: resilience.NewCircuitBreaker()}
}

// SetUpstreamCredentials must be called only before serving requests.
func (g *Gateway) SetUpstreamCredentials(c upstream.Credentials) {
	g.credentials = make(upstream.Credentials, len(c))
	for name, credential := range c {
		g.credentials[name] = credential
	}
}

// SetProductionMode must be called only before serving requests.
func (g *Gateway) SetProductionMode(enabled bool) { g.productionMode = enabled }

func (g *Gateway) RequestCount() uint64 { return g.requests.Load() }

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	g.requests.Add(1)
	capture := newCaptureWriter(w)
	api, ok, matchErr := g.match(r.Method, r.URL.Path)
	if matchErr != nil {
		g.logger.Error("list API routes failed", "error", matchErr)
		writeJSONError(capture, http.StatusServiceUnavailable, "api routing unavailable")
		g.logRequest(r, model.API{}, capture, started)
		return
	}
	if !ok || !api.Enabled || api.PublishedAt == nil {
		writeJSONError(capture, http.StatusNotFound, "api route not found")
		g.logRequest(r, api, capture, started)
		return
	}

	if err := auth.Authorize(api, g.store, r); err != nil {
		if g.metrics != nil {
			g.metrics.IncAuthFailure()
		}
		writeJSONError(capture, auth.StatusCode(err), err.Error())
		g.logRequest(r, api, capture, started)
		return
	}

	if strings.EqualFold(api.AuthMode, "hmac") {
		// The same nonce cannot be used twice, even with a new body/signature.
		nonce := r.Header.Get("X-Nonce")
		nonceStore, ok := g.limiter.(ratelimit.NonceStore)
		if !ok || !nonceStore.UseNonce("hmac:"+api.ID+":"+nonce, 10*time.Minute) {
			writeJSONError(capture, http.StatusUnauthorized, "replayed HMAC request")
			g.logRequest(r, api, capture, started)
			return
		}
	}
	if err := validateRequestParameters(api, r); err != nil {
		if g.metrics != nil {
			g.metrics.IncSchemaFailure()
		}
		writeJSONError(capture, http.StatusBadRequest, err.Error())
		g.logRequest(r, api, capture, started)
		return
	}
	if err := validateRequestSchema(api, r); err != nil {
		if g.metrics != nil {
			g.metrics.IncSchemaFailure()
		}
		writeJSONError(capture, http.StatusBadRequest, err.Error())
		g.logRequest(r, api, capture, started)
		return
	}

	identity := clientIdentity(r)
	if !g.allow(api.ID+":"+identity, api.RateLimitPerMinute, time.Minute) ||
		!g.allow(api.ID+":"+identity+":day", api.DailyQuota, 24*time.Hour) ||
		!g.allow(api.ID+":"+identity+":month", api.MonthlyQuota, 31*24*time.Hour) {
		if g.metrics != nil {
			g.metrics.IncRateLimit()
		}
		capture.Header().Set("Retry-After", "60")
		writeJSONError(capture, http.StatusTooManyRequests, "rate limit exceeded")
		g.logRequest(r, api, capture, started)
		return
	}

	var output http.ResponseWriter = capture
	var responseValidation *responseValidator
	if !apiSchema.IsEmpty(api.ResponseSchema) {
		responseValidation = newResponseValidator(capture, api.ResponseSchema)
		output = responseValidation
	}

	if api.Plugin != "" {
		handler, release, found := g.plugins.Acquire(api.Plugin)
		if !found {
			g.logger.Error("plugin not found", "plugin", api.Plugin, "api_id", api.ID)
			writeJSONError(output, http.StatusInternalServerError, plugin.Unknown(api.Plugin).Error())
			g.logRequest(r, api, capture, started)
			return
		}
		defer release()
		if err := handler.Handle(r.Context(), output, r, api); err != nil {
			if g.metrics != nil {
				g.metrics.IncPluginFailure()
			}
			g.logger.Error("plugin handler failed", "plugin", api.Plugin, "api_id", api.ID, "error", err)
			if !capture.wroteHeader {
				if errors.Is(err, plugin.ErrRequestTooLarge) {
					writeJSONError(output, http.StatusRequestEntityTooLarge, "plugin request too large")
				} else {
					writeJSONError(output, http.StatusBadGateway, "plugin execution failed")
				}
			}
		}
	} else if api.UpstreamURL != "" {
		g.proxy(output, r, api)
	} else {
		status := api.ResponseStatus
		if status == 0 {
			status = http.StatusOK
		}
		if output.Header().Get("Content-Type") == "" {
			output.Header().Set("Content-Type", "application/json")
		}
		output.WriteHeader(status)
		if err := plugin.StaticResponse(output, api); err != nil {
			g.logger.Error("static response failed", "api_id", api.ID, "error", err)
		}
	}
	if responseValidation != nil {
		if err := responseValidation.Commit(); err != nil {
			if g.metrics != nil {
				g.metrics.IncSchemaFailure()
			}
			g.logger.Warn("response schema validation failed", "api_id", api.ID, "error", err)
		}
	}
	g.logRequest(r, api, capture, started)
}

func validateRequestParameters(api model.API, r *http.Request) error {
	if apiSchema.IsEmpty(api.ParametersSchema) {
		return nil
	}
	parameters := map[string]any{
		"path":   pathParameters(api.Path, r.URL.Path),
		"query":  queryParameters(r.URL.Query()),
		"header": headerParameters(r.Header),
	}
	payload, err := json.Marshal(parameters)
	if err != nil {
		return fmt.Errorf("encode request parameters: %w", err)
	}
	if err := apiSchema.ValidateInstance(api.ParametersSchema, payload); err != nil {
		return fmt.Errorf("invalid request parameters: %w", err)
	}
	return nil
}

func pathParameters(pattern, actual string) map[string]string {
	result := map[string]string{}
	patterns := strings.Split(strings.Trim(pattern, "/"), "/")
	values := strings.Split(strings.Trim(actual, "/"), "/")
	if len(patterns) != len(values) {
		return result
	}
	for index, part := range patterns {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			result[strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")] = values[index]
		}
	}
	return result
}

func queryParameters(values url.Values) map[string]any {
	result := make(map[string]any, len(values))
	for key, entries := range values {
		if len(entries) == 1 {
			result[key] = entries[0]
		} else {
			result[key] = entries
		}
	}
	return result
}

func headerParameters(values http.Header) map[string]string {
	result := make(map[string]string, len(values))
	for key, entries := range values {
		if len(entries) > 0 {
			result[key] = entries[0]
		}
	}
	return result
}

const maxSchemaBodyBytes = 2 << 20

func validateRequestSchema(api model.API, r *http.Request) error {
	if apiSchema.IsEmpty(api.RequestSchema) {
		return nil
	}
	var body []byte
	var err error
	if r.Body != nil {
		body, err = io.ReadAll(io.LimitReader(r.Body, maxSchemaBodyBytes+1))
	}
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(body) > maxSchemaBodyBytes {
		return fmt.Errorf("request body exceeds %d byte schema validation limit", maxSchemaBodyBytes)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err := apiSchema.ValidateInstance(api.RequestSchema, body); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	return nil
}

type responseValidator struct {
	parent      *captureWriter
	schema      json.RawMessage
	status      int
	wroteHeader bool
	passthrough bool
	body        bytes.Buffer
	overflow    bool
}

func newResponseValidator(parent *captureWriter, document json.RawMessage) *responseValidator {
	return &responseValidator{parent: parent, schema: document}
}

func (w *responseValidator) Header() http.Header { return w.parent.Header() }
func (w *responseValidator) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status, w.wroteHeader = status, true
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		w.passthrough = true
		w.parent.WriteHeader(status)
	}
}
func (w *responseValidator) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.passthrough {
		return w.parent.Write(body)
	}
	remaining := maxSchemaBodyBytes - w.body.Len()
	if remaining < len(body) {
		w.overflow = true
		if remaining > 0 {
			_, _ = w.body.Write(body[:remaining])
		}
		return len(body), nil
	}
	return w.body.Write(body)
}
func (w *responseValidator) Flush() { /* response validation intentionally buffers the response */ }
func (w *responseValidator) Commit() error {
	if w.passthrough {
		return nil
	}
	if !w.wroteHeader {
		w.status = http.StatusOK
	}
	if w.status >= 200 && w.status < 300 {
		if w.overflow {
			w.writeValidationError("response body exceeds schema validation limit")
			return fmt.Errorf("response body exceeds %d byte schema validation limit", maxSchemaBodyBytes)
		}
		if err := apiSchema.ValidateInstance(w.schema, w.body.Bytes()); err != nil {
			w.writeValidationError("upstream response does not satisfy configured schema")
			return err
		}
	}
	w.parent.WriteHeader(w.status)
	_, _ = w.parent.Write(w.body.Bytes())
	return nil
}
func (w *responseValidator) writeValidationError(message string) {
	for _, header := range []string{"Content-Length", "Content-Encoding", "Content-Type"} {
		w.parent.Header().Del(header)
	}
	writeJSONError(w.parent, http.StatusBadGateway, message)
}
func wroteHeader(writer http.ResponseWriter) bool {
	switch value := writer.(type) {
	case *captureWriter:
		return value.wroteHeader
	case *responseValidator:
		return value.wroteHeader
	default:
		return false
	}
}

func (g *Gateway) allow(key string, limit int, window time.Duration) bool {
	return g.limiter.Allow(key, limit, window, time.Now())
}

func (g *Gateway) proxy(w http.ResponseWriter, r *http.Request, api model.API) {
	ctx, span := otel.Tracer("api-manager/gateway").Start(r.Context(), "gateway.upstream", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String("api.id", api.ID),
		attribute.String("upstream.origin", security.SafeOrigin(api.UpstreamURL)),
	))
	defer span.End()
	if api.CircuitThreshold > 0 && !g.breaker.Allow(api.ID, api.CircuitThreshold) {
		if g.metrics != nil {
			g.metrics.IncUpstreamFailure()
			g.metrics.IncCircuitRejection()
		}
		span.SetStatus(codes.Error, "upstream circuit is open")
		writeJSONError(w, http.StatusServiceUnavailable, "upstream circuit breaker is open")
		return
	}
	target, err := url.Parse(api.UpstreamURL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil {
		span.SetStatus(codes.Error, "invalid upstream URL")
		g.breaker.Failure(api.ID, api.CircuitThreshold, api.CircuitResetSecs)
		writeJSONError(w, http.StatusBadGateway, "invalid upstream URL")
		return
	}
	if g.productionMode && target.Scheme != "https" {
		span.SetStatus(codes.Error, "insecure upstream URL")
		g.breaker.Failure(api.ID, api.CircuitThreshold, api.CircuitResetSecs)
		writeJSONError(w, http.StatusBadGateway, "production upstreams require HTTPS")
		return
	}
	if api.UpstreamTimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(api.UpstreamTimeoutMS)*time.Millisecond)
		defer cancel()
	}
	proxy := &httputil.ReverseProxy{}
	safeTransport := http.DefaultTransport.(*http.Transport).Clone()
	defer safeTransport.CloseIdleConnections()
	safeTransport.Proxy = nil // Never delegate DNS/IP validation to a proxy.
	safeTransport.DialContext = security.DialContext(g.credentials, api.UpstreamAuthRef, target)
	proxy.Transport = resilience.RetryTransport{Base: safeTransport, Attempts: api.UpstreamRetries, OnRetry: func() {
		if g.metrics != nil {
			g.metrics.IncUpstreamRetry()
		}
		span.AddEvent("upstream.retry")
	}}
	key, err := g.credentials.Key(api.UpstreamAuthRef, target)
	if err != nil {
		span.SetStatus(codes.Error, "upstream authentication configuration error")
		if g.metrics != nil {
			g.metrics.IncUpstreamFailure()
		}
		writeJSONError(w, http.StatusBadGateway, "upstream authentication is not configured for this destination")
		return
	}
	var rewrittenPath string
	if api.UpstreamPath != "" {
		rewrittenPath, err = upstream.Path(api.Path, r.URL.EscapedPath(), api.UpstreamPath)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "invalid upstream path mapping")
			return
		}
	} else if api.StripPath {
		rewrittenPath = "/"
	}
	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		if rewrittenPath != "" {
			escaped := strings.TrimRight(target.EscapedPath(), "/") + rewrittenPath
			decoded, _ := url.PathUnescape(escaped)
			pr.Out.URL.Path, pr.Out.URL.RawPath = decoded, escaped
		}
		// Rewrite runs after hop-by-hop headers are removed. Gateway credentials
		// authenticate the client only and must never cross the trust boundary.
		pr.SetXForwarded()
		stripGatewayCredentials(pr.Out.Header, api)
		if api.UpstreamAuthRef != "" {
			pr.Out.Header.Set("X-API-Key", key)
		}
		pr.Out.Header.Set("X-Request-ID", httpx.RequestIDFromContext(r.Context()))
		otel.GetTextMapPropagator().Inject(pr.Out.Context(), propagation.HeaderCarrier(pr.Out.Header))
	}

	proxy.ModifyResponse = func(response *http.Response) error {
		if response.StatusCode >= 500 {
			if g.metrics != nil {
				g.metrics.IncUpstreamFailure()
			}
			g.breaker.Failure(api.ID, api.CircuitThreshold, api.CircuitResetSecs)
			span.SetStatus(codes.Error, "upstream returned error")
		} else {
			g.breaker.Success(api.ID)
		}
		span.SetAttributes(attribute.Int("http.response.status_code", response.StatusCode))
		return nil
	}
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, err error) {
		if g.metrics != nil {
			g.metrics.IncUpstreamFailure()
		}
		g.breaker.Failure(api.ID, api.CircuitThreshold, api.CircuitResetSecs)
		span.RecordError(err)
		span.SetStatus(codes.Error, "upstream request failed")
		g.logger.Error("upstream request failed", "api_id", api.ID, "upstream", security.SafeOrigin(api.UpstreamURL), "error", err)
		if wroteHeader(writer) {
			return
		}
		writeJSONError(writer, http.StatusBadGateway, "upstream request failed")
	}
	proxy.ServeHTTP(w, r.WithContext(ctx))
}

func stripGatewayCredentials(header http.Header, api model.API) {
	names := []string{"Authorization", "Cookie", "X-API-Key", "X-Admin-Token", "X-Timestamp", "X-Nonce", "X-Signature"}
	if strings.EqualFold(api.AuthMode, "hmac") {
		if name := strings.TrimSpace(api.AuthConfig["timestamp_header"]); name != "" {
			names = append(names, name)
		}
		if name := strings.TrimSpace(api.AuthConfig["signature_header"]); name != "" {
			names = append(names, name)
		}
	}
	for _, name := range names {
		header.Del(name)
	}
}

func (g *Gateway) logRequest(r *http.Request, api model.API, w *captureWriter, started time.Time) {
	attrs := []any{
		"request_id", httpx.RequestIDFromContext(r.Context()),
		"method", r.Method,
		"path", r.URL.Path,
		"status", w.status,
		"bytes", w.bytes,
		"duration_ms", time.Since(started).Milliseconds(),
	}
	if api.ID != "" {
		attrs = append(attrs, "api_id", api.ID, "plugin", api.Plugin, "upstream", security.SafeOrigin(api.UpstreamURL))
	}
	if g.metrics != nil {
		g.metrics.ObserveGateway(r.Method, w.status, time.Since(started))
	}
	g.logger.Info("api request", attrs...)
}

func (g *Gateway) match(method, path string) (model.API, bool, error) {
	var apis []model.API
	if checked, ok := g.store.(interface{ ListAPIsChecked() ([]model.API, error) }); ok {
		var err error
		apis, err = checked.ListAPIsChecked()
		if err != nil {
			return model.API{}, false, err
		}
	} else {
		apis = g.store.ListAPIs()
	}
	sort.Slice(apis, func(i, j int) bool {
		a, b := apis[i].Path, apis[j].Path
		if a == path && b != path {
			return true
		}
		if b == path && a != path {
			return false
		}
		staticA, staticB := staticSegments(a), staticSegments(b)
		if staticA != staticB {
			return staticA > staticB
		}
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return a < b
	})
	for _, api := range apis {
		if strings.EqualFold(api.Method, method) && matchPath(api.Path, path) {
			return api, true, nil
		}
	}
	return model.API{}, false, nil
}

func staticSegments(path string) int {
	count := 0
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if !strings.HasPrefix(part, "{") || !strings.HasSuffix(part, "}") {
			count++
		}
	}
	return count
}

func matchPath(pattern, actual string) bool {
	if pattern == actual {
		return true
	}
	pp := strings.Split(strings.Trim(pattern, "/"), "/")
	ap := strings.Split(strings.Trim(actual, "/"), "/")
	if len(pp) != len(ap) {
		return false
	}
	for i := range pp {
		if strings.HasPrefix(pp[i], "{") && strings.HasSuffix(pp[i], "}") {
			continue
		}
		if pp[i] != ap[i] {
			return false
		}
	}
	return true
}

func clientIdentity(r *http.Request) string {
	value := r.Header.Get("X-API-Key")
	if value == "" {
		value = auth.ExtractAPIKey(r.Header.Get("Authorization"))
	}
	if value == "" {
		value = r.RemoteAddr
		if host, _, err := net.SplitHostPort(value); err == nil {
			value = host
		}
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

type captureWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func newCaptureWriter(w http.ResponseWriter) *captureWriter { return &captureWriter{ResponseWriter: w} }

func (w *captureWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *captureWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytes += n
	return n, err
}

func (w *captureWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		if !w.wroteHeader {
			w.WriteHeader(http.StatusOK)
		}
		flusher.Flush()
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": message})
}

var _ http.ResponseWriter = (*captureWriter)(nil)

func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *captureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (w *captureWriter) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

func (w *captureWriter) ReadFrom(reader io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := readerFrom.ReadFrom(reader)
		w.bytes += int(n)
		return n, err
	}
	return io.Copy(struct{ io.Writer }{Writer: w}, reader)
}
