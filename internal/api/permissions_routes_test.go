package api

import (
	"net/http/httptest"
	"testing"
)

func TestRoleReadAndWritePermissionsAreSeparated(t *testing.T) {
	for _, tc := range []struct{ method, path, permission string }{
		{"GET", "/admin/v1/roles", "user.read"}, {"GET", "/admin/v1/permissions", "user.read"},
		{"POST", "/admin/v1/roles", "user.manage"}, {"PUT", "/admin/v1/roles/operator/permissions", "user.manage"},
		{"PUT", "/admin/v1/users/id/roles", "user.manage"}, {"PUT", "/admin/v1/users/id/status", "user.manage"},
	} {
		if got := requiredPermission(httptest.NewRequest(tc.method, tc.path, nil)); got != tc.permission {
			t.Fatalf("%s %s: got %s", tc.method, tc.path, got)
		}
	}
}
