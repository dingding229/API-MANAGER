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

func TestValidateAPIRequestOnlyAcceptsKeys(t *testing.T) {
	for _, mode := range []string{"", "api_key", "none", "jwt", "hmac", "other"} {
		r := model.CreateAPIRequest{Name: "secured", Method: "POST", Path: "/api/secured", AuthMode: mode, ResponseBody: `{}`}
		err := validateAPIRequest(r, true)
		if (err == nil) != (mode == "" || mode == "api_key") {
			t.Fatalf("mode %q: %v", mode, err)
		}
	}
}

func TestRollbackRejectsLegacyModesBeforeChangingRoutes(t *testing.T) {
	for _, mode := range []string{"none", "jwt", "hmac", ""} {
		err := validateStoredAPI(model.API{Name: "legacy", Method: "GET", Path: "/api/legacy", AuthMode: mode}, true)
		if err == nil {
			t.Fatalf("legacy mode %q accepted", mode)
		}
	}
}
