package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"controlplane/internal/identity"
)

type roleSessions struct{ accountSessions }

func (roleSessions) Authenticate(_ context.Context, token string) (identity.PublicUser, error) {
	if token == "admin-token" {
		return identity.PublicUser{ID: "admin-id", Roles: []string{"admin"}, Permissions: []string{"roles.read", "roles.write"}}, nil
	}
	if token == "delegate-token" {
		return identity.PublicUser{ID: "delegate-id", Roles: []string{"user", "support"}, Permissions: []string{"roles.read", "roles.write"}}, nil
	}
	if token == "reader-token" {
		return identity.PublicUser{ID: "reader-id", Roles: []string{"admin"}, Permissions: []string{"roles.read"}}, nil
	}
	return identity.PublicUser{}, identity.ErrUnauthenticated
}

func (s roleSessions) VerifyCSRF(ctx context.Context, token, csrf string) (identity.PublicUser, error) {
	user, err := s.Authenticate(ctx, token)
	if err != nil {
		return identity.PublicUser{}, err
	}
	if csrf != "valid-csrf" {
		return identity.PublicUser{}, identity.ErrInvalidCSRF
	}
	return user, nil
}

type roleStub struct{ created, updated, assigned bool }

func (s *roleStub) ListRoles(context.Context) ([]identity.Role, error) { return []identity.Role{}, nil }
func (s *roleStub) ListPermissions(context.Context) ([]identity.Permission, error) {
	return []identity.Permission{}, nil
}
func (s *roleStub) CreateRole(_ context.Context, input identity.RoleInput, _, _ string) (identity.Role, error) {
	s.created = true
	return identity.Role{Code: input.Code}, nil
}
func (s *roleStub) UpdateRole(_ context.Context, code string, _ identity.RolePatchInput, _, _ string) (identity.Role, error) {
	s.updated = true
	if code == "admin" {
		return identity.Role{}, identity.ErrRoleSystem
	}
	return identity.Role{Code: code}, nil
}
func (s *roleStub) DeleteRole(context.Context, string, string, string) error { return nil }
func (s *roleStub) SetUserRoles(_ context.Context, userID string, _ identity.RoleAssignment, _, _ string) (identity.PublicUser, error) {
	s.assigned = true
	return identity.PublicUser{ID: userID}, nil
}

func TestCustomRoleRoutesRequirePermissionAndCSRF(t *testing.T) {
	store := &roleStub{}
	handler := NewHandlerWithStores(testLogger(), nil, roleSessions{}, RouteStores{Roles: store})
	for _, tc := range []struct {
		method, path, token, csrf, body string
		want                            int
	}{
		{http.MethodGet, "/api/v1/admin/roles", "", "", "", 401},
		{http.MethodGet, "/api/v1/admin/roles", "reader-token", "", "", 200},
		{http.MethodPost, "/api/v1/admin/roles", "reader-token", "valid-csrf", `{"code":"support","description":"Support","permissions":[]}`, 403},
		{http.MethodPost, "/api/v1/admin/roles", "delegate-token", "valid-csrf", `{"code":"support","description":"Support","permissions":[]}`, 403},
		{http.MethodPatch, "/api/v1/admin/roles/support", "delegate-token", "valid-csrf", `{"permissions":["plans.write"]}`, 403},
		{http.MethodPut, "/api/v1/admin/users/11111111-1111-7111-8111-111111111111/roles", "delegate-token", "valid-csrf", `{"role_codes":["support"]}`, 403},
		{http.MethodPost, "/api/v1/admin/roles", "admin-token", "", `{"code":"support","description":"Support","permissions":[]}`, 403},
		{http.MethodPost, "/api/v1/admin/roles", "admin-token", "valid-csrf", `{"code":"support","description":"Support","permissions":[]}`, 201},
		{http.MethodPatch, "/api/v1/admin/roles/admin", "admin-token", "valid-csrf", `{"description":"changed"}`, 422},
		{http.MethodPut, "/api/v1/admin/users/11111111-1111-7111-8111-111111111111/roles", "admin-token", "valid-csrf", `{"role_codes":["support"]}`, 200},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, catalogRequest(tc.method, tc.path, tc.token, tc.csrf, tc.body))
		if response.Code != tc.want {
			t.Fatalf("%s %s = %d, want %d: %s", tc.method, tc.path, response.Code, tc.want, response.Body.String())
		}
	}
	if !store.created || !store.assigned {
		t.Fatalf("mutations = %+v", store)
	}
}
