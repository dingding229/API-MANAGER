package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrometheusMetricsExposeGatewayCountersAndHistogram(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveHTTP("gateway", http.MethodGet, http.StatusOK, 15*time.Millisecond)
	metrics.ObserveGateway(http.MethodGet, http.StatusOK, 15*time.Millisecond)
	metrics.IncRateLimit()
	metrics.IncAuthFailure()
	metrics.IncSchemaFailure()
	response := httptest.NewRecorder()
	metrics.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, expected := range []string{
		`api_manager_http_requests_total{route="gateway",method="GET",status="200"} 1`,
		`api_manager_gateway_requests_total{method="GET",status="200"} 1`,
		`api_manager_gateway_request_duration_seconds_bucket{method="GET",le="0.025"} 1`,
		`api_manager_http_request_duration_seconds_bucket{route="gateway",method="GET",le="0.025"} 1`,
		`api_manager_gateway_rate_limit_hits_total 1`,
		`api_manager_gateway_auth_failures_total 1`,
		`api_manager_gateway_schema_validation_failures_total 1`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics output does not contain %q:\n%s", expected, body)
		}
	}
}

func TestMetricsMiddlewareCapturesStatus(t *testing.T) {
	metrics := NewMetrics()
	handler := metrics.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/admin/v1/apis", nil))
	response := httptest.NewRecorder()
	metrics.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(response.Body.String(), `api_manager_http_requests_total{route="admin",method="GET",status="418"} 1`) {
		t.Fatal("middleware did not capture status")
	}
}
