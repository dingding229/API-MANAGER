package store

import "testing"

func TestEnsureRBACPreservesCustomizedRolePermissions(t *testing.T) {
	m := NewMemory()
	if err := m.UpdateRolePermissions("viewer", []string{"api.read"}); err != nil {
		t.Fatal(err)
	}
	if err := m.EnsureRBAC(); err != nil {
		t.Fatal(err)
	}
	role, err := m.GetRoleByName("viewer")
	if err != nil {
		t.Fatal(err)
	}
	if len(role.Permissions) != 1 || role.Permissions[0] != "api.read" {
		t.Fatalf("customized permissions were reset: %#v", role.Permissions)
	}
}
