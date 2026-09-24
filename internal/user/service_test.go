package user

import (
	"testing"

	"api-manager/internal/store"
)

func TestRBACPermissionsAndRoleAssignment(t *testing.T) {
	memory := store.NewMemory()
	service := NewService(memory, "test-secret", 0)

	viewer, err := service.Create("viewer@example.com", "password123", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	if !service.Can(viewer.ID, "api.read") {
		t.Fatal("viewer should be allowed to read APIs")
	}
	if service.Can(viewer.ID, "api.write") {
		t.Fatal("viewer should not be allowed to modify APIs")
	}
	if err := service.AssignRoles(viewer.ID, []string{"api_developer"}); err != nil {
		t.Fatal(err)
	}
	if !service.Can(viewer.ID, "api.write") {
		t.Fatal("api_developer should be allowed to modify APIs")
	}
	if service.Can(viewer.ID, "api.publish") {
		t.Fatal("api_developer should not be allowed to publish APIs")
	}
}
