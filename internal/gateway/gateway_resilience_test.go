package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"api-manager/internal/model"
	"api-manager/internal/plugin"
	"api-manager/internal/ratelimit"
	"api-manager/internal/store"
	"api-manager/internal/upstream"
)

func testGateway(t *testing.T, api model.API) *Gateway {
	t.Helper()
	memory := store.NewMemory()
	if err := memory.CreateAPI(api); err != nil {
		t.Fatal(err)
	}
	return New(memory, plugin.NewRegistry(), ratelimit.NewMemory(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func publishedAPI(id, method, path string) model.API {
	now := time.Now().UTC()
	return model.API{ID: id, Name: id, Method: method, Path: path, AuthMode: "none", ResponseStatus: http.StatusOK, ResponseBody: `{"ok":true}`, Enabled: true, PublishedAt: &now, CreatedAt: now, UpdatedAt: now}
}

func TestGatewayValidatesPathQueryAndHeaderParameters(t *testing.T) {
	api := publishedAPI("parameter-api", http.MethodGet, "/api/orders/{id}")
	api.ParametersSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"object","required":["id"],"properties":{"id":{"const":"42"}}},
			"query":{"type":"object","required":["include"],"properties":{"include":{"const":"items"}}},
			"header":{"type":"object","required":["X-Client-Mode"],"properties":{"X-Client-Mode":{"const":"full"}}}
		}
	}`)
	gateway := testGateway(t, api)

	valid := httptest.NewRequest(http.MethodGet, "/api/orders/42?include=items", nil)
	valid.Header.Set("X-Client-Mode", "full")
	validResponse := httptest.NewRecorder()
	gateway.ServeHTTP(validResponse, valid)
	if validResponse.Code != http.StatusOK {
		t.Fatalf("valid parameters returned %d: %s", validResponse.Code, validResponse.Body.String())
	}

	invalid := httptest.NewRequest(http.MethodGet, "/api/orders/42", nil)
	invalid.Header.Set("X-Client-Mode", "full")
	invalidResponse := httptest.NewRecorder()
	gateway.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid parameters returned %d: %s", invalidResponse.Code, invalidResponse.Body.String())
	}
}

func TestGatewayRetriesUpstreamAndOpensCircuit(t *testing.T) {
	var retryCalls atomic.Int32
	retryUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if retryCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"upstream":"ok"}`))
	}))
	defer retryUpstream.Close()
	retryAPI := publishedAPI("retry-api", http.MethodGet, "/api/retry")
	retryAPI.UpstreamURL, retryAPI.UpstreamRetries = retryUpstream.URL, 1
	retryAPI.UpstreamAuthRef = "approved-local-retry"
	retryGateway := testGateway(t, retryAPI)
	retryGateway.SetUpstreamCredentials(upstream.Credentials{"approved-local-retry": {Origin: retryUpstream.URL, APIKey: "test"}})
	retryResponse := httptest.NewRecorder()
	retryGateway.ServeHTTP(retryResponse, httptest.NewRequest(http.MethodGet, "/api/retry", nil))
	if retryResponse.Code != http.StatusOK || retryCalls.Load() != 2 {
		t.Fatalf("retry response=%d calls=%d body=%s", retryResponse.Code, retryCalls.Load(), retryResponse.Body.String())
	}

	var failureCalls atomic.Int32
	failureUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		failureCalls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failureUpstream.Close()
	failureAPI := publishedAPI("circuit-api", http.MethodGet, "/api/circuit")
	failureAPI.UpstreamURL, failureAPI.CircuitThreshold, failureAPI.CircuitResetSecs = failureUpstream.URL, 2, 60
	failureAPI.UpstreamAuthRef = "approved-local-failure"
	failureGateway := testGateway(t, failureAPI)
	failureGateway.SetUpstreamCredentials(upstream.Credentials{"approved-local-failure": {Origin: failureUpstream.URL, APIKey: "test"}})
	for i := 0; i < 2; i++ {
		response := httptest.NewRecorder()
		failureGateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/circuit", nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("failure %d returned %d", i, response.Code)
		}
	}
	openResponse := httptest.NewRecorder()
	failureGateway.ServeHTTP(openResponse, httptest.NewRequest(http.MethodGet, "/api/circuit", nil))
	if openResponse.Code != http.StatusServiceUnavailable || failureCalls.Load() != 2 {
		t.Fatalf("open circuit response=%d upstream calls=%d", openResponse.Code, failureCalls.Load())
	}
}

func TestClientIdentityUsesSameBudgetForKeyHeaders(t *testing.T) {
	a := httptest.NewRequest(http.MethodGet, "/api/game-discount/v1/offers", nil)
	b := httptest.NewRequest(http.MethodGet, "/api/game-discount/v1/offers", nil)
	a.Header.Set("X-API-Key", "client-key")
	b.Header.Set("Authorization", "Bearer client-key")
	if clientIdentity(a) != clientIdentity(b) {
		t.Fatal("changing key header bypasses rate limit")
	}
	a.Header.Del("X-API-Key")
	b.Header.Del("Authorization")
	a.RemoteAddr = "127.0.0.1:1234"
	b.RemoteAddr = "127.0.0.1:9876"
	if clientIdentity(a) != clientIdentity(b) {
		t.Fatal("changing source port bypasses rate limit")
	}
}
