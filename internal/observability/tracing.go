package observability

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type TraceConfig struct {
	Enabled     bool
	ServiceName string
	Endpoint    string
	Insecure    bool
}

func InitTracing(ctx context.Context, cfg TraceConfig, hub *Hub) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if cfg.ServiceName == "" {
		cfg.ServiceName = "api-manager"
	}
	res, err := resource.New(ctx, resource.WithAttributes(attribute.String("service.name", cfg.ServiceName), attribute.String("service.version", "v1")))
	if err != nil {
		return nil, fmt.Errorf("create OTEL resource: %w", err)
	}
	options := []tracesdk.TracerProviderOption{tracesdk.WithResource(res)}
	if hub != nil {
		options = append(options, tracesdk.WithBatcher(
			&embeddedSpanExporter{hub: hub},
			tracesdk.WithBatchTimeout(500*time.Millisecond),
			tracesdk.WithMaxQueueSize(4096),
			tracesdk.WithMaxExportBatchSize(256),
		))
	}
	if cfg.Enabled {
		if cfg.Endpoint == "" {
			return nil, fmt.Errorf("OTLP endpoint is required when tracing is enabled")
		}
		exporterOptions := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			exporterOptions = append(exporterOptions, otlptracegrpc.WithInsecure())
		}
		exporter, err := otlptracegrpc.New(ctx, exporterOptions...)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}
		options = append(options, tracesdk.WithBatcher(exporter, tracesdk.WithBatchTimeout(2*time.Second)))
	}
	if hub == nil && !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}
	provider := tracesdk.NewTracerProvider(options...)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

func Middleware(next http.Handler) http.Handler {
	tracer := otel.Tracer("api-manager/http")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parent := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := tracer.Start(parent, methodLabel(r.Method)+" "+routeName(r.URL.Path), trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(
			attribute.String("http.request.method", methodLabel(r.Method)),
			attribute.String("http.route", routeName(r.URL.Path)),
			attribute.String("url.path", r.URL.Path),
			attribute.String("server.address", r.Host),
		))
		capture := &traceResponseWriter{ResponseWriter: w}
		next.ServeHTTP(capture, r.WithContext(ctx))
		status := capture.status
		if status == 0 {
			status = http.StatusOK
		}
		span.SetAttributes(attribute.Int("http.response.status_code", status), attribute.Int("http.response.body.size", capture.bytes), attribute.String("request.id", capture.Header().Get("X-Request-ID")))
		if status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(status))
		}
		span.End()
	})
}

func routeName(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/"):
		return "/api/*"
	case strings.HasPrefix(path, "/admin/"):
		return "/admin/*"
	case strings.HasPrefix(path, "/auth/"):
		return "/auth/*"
	default:
		return "/other"
	}
}

type traceResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *traceResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *traceResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytes += n
	return n, err
}
func (w *traceResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		if w.status == 0 {
			w.WriteHeader(http.StatusOK)
		}
		flusher.Flush()
	}
}

func (w *traceResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *traceResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (w *traceResponseWriter) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

func (w *traceResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := readerFrom.ReadFrom(reader)
		w.bytes += int(n)
		return n, err
	}
	return io.Copy(struct{ io.Writer }{Writer: w}, reader)
}
