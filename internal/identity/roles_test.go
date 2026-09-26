package identity

import (
	"errors"
	"testing"
)

func TestNormalizeRoleRejectsSystemCodesAndDuplicatePermissions(t *testing.T) {
	if _, err := NormalizeRole(NewRole{Code: "elevated", Description: "Elevated", Permissions: []string{"roles.write"}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("delegated RBAC write error = %v", err)
	}
	if _, err := NormalizeRole(NewRole{Code: "support", Description: "Support"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing permission set error = %v", err)
	}
	for _, code := range []string{"admin", "user", "bad role", "-support"} {
		if _, err := NormalizeRole(NewRole{Code: code, Description: "Support", Permissions: []string{"nodes.read"}}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("code %q error = %v", code, err)
		}
	}
	role, err := NormalizeRole(NewRole{Code: "support", Description: " Support team ", Permissions: []string{"usage.read", "nodes.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if role.Code != "support" || role.Description != "Support team" || len(role.Permissions) != 2 || role.Permissions[0] != "nodes.read" || role.Permissions[1] != "usage.read" {
		t.Fatalf("normalized role = %+v", role)
	}
}

func TestNormalizeRolePatchRequiresAChange(t *testing.T) {
	if _, err := NormalizeRolePatch(RolePatch{Permissions: &[]string{"roles.write"}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("delegated RBAC write patch error = %v", err)
	}
	if _, err := NormalizeRolePatch(RolePatch{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty patch error = %v", err)
	}
	if _, err := NormalizeRolePatch(RolePatch{Permissions: &[]string{"nodes.read", "nodes.read"}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate permission patch error = %v", err)
	}
}

func TestNormalizeRoleAssignmentRejectsSystemRolesAndDuplicates(t *testing.T) {
	if _, err := NormalizeRoleAssignment(RoleAssignment{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing role assignment error = %v", err)
	}
	if _, err := NormalizeRoleAssignment(RoleAssignment{RoleCodes: []string{"user"}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("user role assignment error = %v", err)
	}
	if _, err := NormalizeRoleAssignment(RoleAssignment{RoleCodes: []string{"support", "support"}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate role assignment error = %v", err)
	}
	assignment, err := NormalizeRoleAssignment(RoleAssignment{RoleCodes: []string{"support", "billing"}})
	if err != nil || len(assignment.RoleCodes) != 2 {
		t.Fatalf("assignment = %+v, error = %v", assignment, err)
	}
}
