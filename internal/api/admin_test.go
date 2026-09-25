package api

import (
	"strings"
	"testing"

	"api-manager/internal/model"
)

func TestValidateAPIRequestRequiresHTTPSInProduction(t *testing.T) {
	request := model.CreateAPIRequest{Name: "catalog", Method: "GET", Path: "/api/catalog", UpstreamURL: "http://catalog.example.com"}
	if err := validateAPIRequest(request, false); err != nil {
		t.Fatalf("development HTTP upstream rejected: %v", err)
	}
	if err := validateAPIRequest(request, true); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("production HTTP upstream was not rejected: %v", err)
	}
	request.UpstreamURL = "https://catalog.example.com"
	if err := validateAPIRequest(request, true); err != nil {
		t.Fatalf("production HTTPS upstream rejected: %v", err)
	}
}

func TestValidateAPIRequestRestrictsAuthSecretEnvironment(t *testing.T) {
	base := model.CreateAPIRequest{
		Name:         "secured",
		Method:       "POST",
		Path:         "/api/secured",
		ResponseBody: `{}`,
	}
	for _, tc := range []struct {
		name       string
		authMode   string
		authConfig map[string]string
		wantError  bool
	}{
		{name: "JWT platform secret rejected", authMode: "jwt", authConfig: map[string]string{"issuer": "issuer", "audience": "audience", "secret_env": "ADMIN_TOKEN"}, wantError: true},
		{name: "JWT API auth secret accepted", authMode: "jwt", authConfig: map[string]string{"issuer": "issuer", "audience": "audience", "secret_env": "API_AUTH_CUSTOM_JWT"}},
		{name: "JWT default accepted", authMode: "jwt", authConfig: map[string]string{"issuer": "issuer", "audience": "audience"}},
		{name: "HMAC platform secret rejected", authMode: "hmac", authConfig: map[string]string{"secret_env": "REDIS_PASSWORD"}, wantError: true},
		{name: "HMAC API auth secret accepted", authMode: "hmac", authConfig: map[string]string{"secret_env": "API_AUTH_ORDERS_HMAC"}},
		{name: "HMAC invalid header rejected", authMode: "hmac", authConfig: map[string]string{"secret_env": "API_AUTH_ORDERS_HMAC", "signature_header": "Bad Header"}, wantError: true},
		{name: "HMAC duplicate headers rejected", authMode: "hmac", authConfig: map[string]string{"secret_env": "API_AUTH_ORDERS_HMAC", "timestamp_header": "X-Nonce"}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := base
			request.AuthMode = tc.authMode
			request.AuthConfig = tc.authConfig
			err := validateAPIRequest(request, true)
			if tc.wantError && err == nil {
				t.Fatal("expected validation error")
			}
			if !tc.wantError && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}
