package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"controlplane/internal/identity"
)

type accountSessions struct{ catalogSessions }

func (accountSessions) Authenticate(_ context.Context, token string) (identity.PublicUser, error) {
	switch token {
	case "admin-token":
		return identity.PublicUser{ID: "admin-id", Permissions: []string{"users.read", "users.write", "dashboard.read"}}, nil
	case "member-token":
		return identity.PublicUser{ID: "member-id", Permissions: []string{}}, nil
	default:
		return identity.PublicUser{}, identity.ErrUnauthenticated
	}
}

func (s accountSessions) VerifyCSRF(ctx context.Context, token, csrf string) (identity.PublicUser, error) {
	user, err := s.Authenticate(ctx, token)
	if err != nil {
		return identity.PublicUser{}, err
	}
	if csrf != "valid-csrf" {
		return identity.PublicUser{}, identity.ErrInvalidCSRF
	}
	return user, nil
}

type accountStub struct {
	createdBy  string
	changedFor string
	changeErr  error
	changes    int
}

func (s *accountStub) CreateMember(_ context.Context, input identity.MemberInput, actorID, _ string) (identity.PublicUser, error) {
	s.createdBy = actorID
	return identity.PublicUser{ID: "11111111-1111-7111-8111-111111111111", Email: input.Email,
		Status: "active", Timezone: input.Timezone, Roles: []string{"user"}, Permissions: []string{"nodes.read"}}, nil
}
func (s *accountStub) ListUsers(context.Context, int, string) ([]identity.PublicUser, error) {
	return []identity.PublicUser{}, nil
}
func (s *accountStub) ChangePassword(_ context.Context, userID, _, _, _ string) error {
	s.changedFor = userID
	s.changes++
	return s.changeErr
}

func TestAdminUserCreationRequiresSessionPermissionAndCSRF(t *testing.T) {
	store := &accountStub{}
	handler := NewHandlerWithAccounts(testLogger(), nil, accountSessions{}, nil, nil, store)
	body := `{"email":"member@example.com","password":"initial-password-123","timezone":"UTC"}`
	for _, tc := range []struct {
		token, csrf string
		status      int
	}{
		{"", "", 401}, {"member-token", "valid-csrf", 403},
		{"admin-token", "", 403}, {"admin-token", "valid-csrf", 201},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, catalogRequest(http.MethodPost, "/api/v1/admin/users", tc.token, tc.csrf, body))
		if response.Code != tc.status {
			t.Fatalf("create token=%s status=%d body=%s", tc.token, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "initial-password-123") || strings.Contains(response.Body.String(), "password_hash") {
			t.Fatalf("response leaked password material: %s", response.Body.String())
		}
	}
	if store.createdBy != "admin-id" {
		t.Fatalf("actor = %q", store.createdBy)
	}
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, catalogRequest(http.MethodGet, "/api/v1/admin/users", "member-token", "", ""))
	if list.Code != 403 {
		t.Fatalf("member list = %d", list.Code)
	}
}

func TestOwnPasswordChangeRequiresCSRFAndClearsRevokedCookies(t *testing.T) {
	store := &accountStub{}
	handler := NewHandlerWithAccounts(testLogger(), nil, accountSessions{}, nil, nil, store)
	body := `{"old_password":"old-password-123","new_password":"new-password-123"}`
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, catalogRequest(http.MethodPost, "/api/v1/me/password", "member-token", "", body))
	if denied.Code != 403 || store.changedFor != "" {
		t.Fatalf("missing CSRF = %d", denied.Code)
	}
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, catalogRequest(http.MethodPost, "/api/v1/me/password", "member-token", "valid-csrf", body))
	if accepted.Code != 204 || store.changedFor != "member-id" {
		t.Fatalf("password change = %d %s for=%s", accepted.Code, accepted.Body.String(), store.changedFor)
	}
	if len(accepted.Result().Cookies()) != 2 {
		t.Fatalf("cleared cookies = %+v", accepted.Result().Cookies())
	}
	for _, cookie := range accepted.Result().Cookies() {
		if cookie.MaxAge != -1 {
			t.Fatalf("cookie not cleared: %+v", cookie)
		}
	}
}

func TestOwnPasswordChangeLimitsOldPasswordGuesses(t *testing.T) {
	store := &accountStub{changeErr: identity.ErrInvalidCredentials}
	handler := NewHandlerWithAccounts(testLogger(), nil, accountSessions{}, nil, nil, store)
	body := `{"old_password":"wrong-password-123","new_password":"new-password-123"}`
	for attempt := 1; attempt <= 6; attempt++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, catalogRequest(http.MethodPost, "/api/v1/me/password", "member-token", "valid-csrf", body))
		if attempt <= 5 && response.Code != http.StatusForbidden {
			t.Fatalf("attempt %d = %d, want 403", attempt, response.Code)
		}
		if attempt == 6 && (response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "") {
			t.Fatalf("sixth attempt = %d, retry-after=%q", response.Code, response.Header().Get("Retry-After"))
		}
	}
	if store.changes != 5 {
		t.Fatalf("bcrypt attempts = %d, want 5", store.changes)
	}
}
