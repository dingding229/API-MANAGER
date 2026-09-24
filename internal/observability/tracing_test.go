package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestTracingMiddlewareExtractsTraceContext(t *testing.T) {
	shutdown, err := InitTracing(context.Background(), TraceConfig{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()) }()
	provider := tracesdk.NewTracerProvider()
	otel.SetTracerProvider(provider)
	defer func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(noop.NewTracerProvider())
	}()

	var captured trace.SpanContext
	handler := Middleware(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		captured = trace.SpanContextFromContext(request.Context())
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/orders/42", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if !captured.IsValid() || captured.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("expected extracted trace context, got %#v", captured)
	}
}
