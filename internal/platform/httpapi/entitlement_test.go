package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controlplane/internal/entitlement"
	"controlplane/internal/identity"
)

type entitlementSessions struct{ catalogSessions }

func (entitlementSessions) Authenticate(_ context.Context, token string) (identity.PublicUser, error) {
	switch token {
	case "admin-token":
		return identity.PublicUser{ID: "admin-id", Permissions: []string{"plans.read", "plans.write", "dashboard.read"}}, nil
	case "member-token":
		return identity.PublicUser{ID: "member-id", Permissions: []string{"dashboard.read"}}, nil
	default:
		return identity.PublicUser{}, identity.ErrUnauthenticated
	}
}

func (s entitlementSessions) VerifyCSRF(ctx context.Context, token, csrf string) (identity.PublicUser, error) {
	user, err := s.Authenticate(ctx, token)
	if err != nil {
		return identity.PublicUser{}, err
	}
	if csrf != "valid-csrf" {
		return identity.PublicUser{}, identity.ErrInvalidCSRF
	}
	return user, nil
}

type entitlementStub struct {
	createdBy string
	readFor   string
	missing   bool
}

func (s *entitlementStub) CreatePlan(_ context.Context, input entitlement.PlanInput, actor, _ string) (entitlement.Plan, error) {
	s.createdBy = actor
	return entitlement.Plan{ID: "11111111-1111-7111-8111-111111111111", Name: input.Name,
		QuotaBytes: input.QuotaBytes, Limits: input.Limits}, nil
}
func (s *entitlementStub) ListPlans(context.Context, int, string) ([]entitlement.Plan, error) {
	return []entitlement.Plan{}, nil
}
func (s *entitlementStub) ListMemberships(context.Context, int, string) ([]entitlement.Membership, error) {
	return []entitlement.Membership{{ID: "22222222-2222-7222-8222-222222222222", UserID: "member-id", Status: "active"}}, nil
}
func (s *entitlementStub) SetMembershipStatus(_ context.Context, id, status, actor, _ string) (entitlement.Membership, error) {
	s.createdBy = actor
	return entitlement.Membership{ID: id, UserID: "member-id", Status: status}, nil
}
func (s *entitlementStub) SetPlanStatus(_ context.Context, id, status, actor, _ string) (entitlement.Plan, error) {
	s.createdBy = actor
	return entitlement.Plan{ID: id, Name: "Standard", Status: status}, nil
}
func (s *entitlementStub) CreateMembership(_ context.Context, input entitlement.MembershipInput, actor, _ string) (entitlement.Membership, error) {
	s.createdBy = actor
	return entitlement.Membership{ID: "22222222-2222-7222-8222-222222222222", UserID: input.UserID,
		PlanID: input.PlanID, Status: "active"}, nil
}
func (s *entitlementStub) GetCurrentMembership(_ context.Context, userID string) (entitlement.Membership, error) {
	s.readFor = userID
	if s.missing {
		return entitlement.Membership{}, entitlement.ErrNotFound
	}
	return entitlement.Membership{ID: "33333333-3333-7333-8333-333333333333", UserID: userID,
		Status: "active", Snapshot: entitlement.Snapshot{PlanName: "Member Plan", QuotaBytes: 123}}, nil
}

func TestPlanAndMembershipWritesRequireAdminSessionAndCSRF(t *testing.T) {
	store := &entitlementStub{}
	handler := NewHandlerWithEntitlements(testLogger(), nil, entitlementSessions{}, nil, store)
	planBody := `{"name":"Standard","quota_bytes":1000,"limits":{"max_hops":1},"resource_group_ids":[],"line_ids":[]}`
	for _, tc := range []struct {
		token, csrf string
		status      int
	}{
		{"", "", 401}, {"member-token", "valid-csrf", 403},
		{"admin-token", "", 403}, {"admin-token", "valid-csrf", 201},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, catalogRequest(http.MethodPost, "/api/v1/admin/plans", tc.token, tc.csrf, planBody))
		if response.Code != tc.status {
			t.Fatalf("plan create token=%s status=%d body=%s", tc.token, response.Code, response.Body.String())
		}
	}
	if store.createdBy != "admin-id" {
		t.Fatalf("plan actor = %q", store.createdBy)
	}
	now := time.Now().UTC()
	membershipBody := `{"user_id":"11111111-1111-7111-8111-111111111111","plan_id":"22222222-2222-7222-8222-222222222222","starts_at":"` + now.Add(-time.Hour).Format(time.RFC3339) + `","ends_at":"` + now.Add(time.Hour).Format(time.RFC3339) + `","anchor_day":26,"timezone":"UTC"}`
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, catalogRequest(http.MethodPost, "/api/v1/admin/memberships", "admin-token", "valid-csrf", membershipBody))
	if response.Code != 201 || !strings.Contains(response.Body.String(), `"status":"active"`) {
		t.Fatalf("membership create = %d %s", response.Code, response.Body.String())
	}
}

func TestMemberSeesOnlyOwnFrozenEntitlements(t *testing.T) {
	store := &entitlementStub{}
	handler := NewHandlerWithEntitlements(testLogger(), nil, entitlementSessions{}, nil, store)
	for _, path := range []string{"/api/v1/my/membership", "/api/v1/my/entitlements"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, catalogRequest(http.MethodGet, path, "member-token", "", ""))
		if response.Code != 200 || store.readFor != "member-id" || !strings.Contains(response.Body.String(), "Member Plan") {
			t.Fatalf("member read %s = %d %s for=%s", path, response.Code, response.Body.String(), store.readFor)
		}
	}
	store.missing = true
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, catalogRequest(http.MethodGet, "/api/v1/my/membership", "member-token", "", ""))
	if response.Code != 404 {
		t.Fatalf("missing membership = %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, catalogRequest(http.MethodGet, "/api/v1/admin/plans", "member-token", "", ""))
	if response.Code != 403 {
		t.Fatalf("member admin list = %d %s", response.Code, response.Body.String())
	}
}

func TestMembershipDirectoryRequiresPlanRead(t *testing.T) {
	store := &entitlementStub{}
	handler := NewHandlerWithEntitlements(testLogger(), nil, entitlementSessions{}, nil, store)
	for _, tc := range []struct {
		token  string
		status int
	}{
		{"", http.StatusUnauthorized},
		{"member-token", http.StatusForbidden},
		{"admin-token", http.StatusOK},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, catalogRequest(http.MethodGet, "/api/v1/admin/memberships", tc.token, "", ""))
		if response.Code != tc.status {
			t.Fatalf("membership list token=%q status=%d body=%s", tc.token, response.Code, response.Body.String())
		}
		if tc.status == http.StatusOK && !strings.Contains(response.Body.String(), `"user_id":"member-id"`) {
			t.Fatalf("membership directory = %s", response.Body.String())
		}
	}
}

func TestMembershipCancellationRequiresWritePermissionAndCSRF(t *testing.T) {
	store := &entitlementStub{}
	handler := NewHandlerWithEntitlements(testLogger(), nil, entitlementSessions{}, nil, store)
	for _, tc := range []struct {
		token, csrf string
		status      int
	}{
		{"", "", http.StatusUnauthorized},
		{"member-token", "valid-csrf", http.StatusForbidden},
		{"admin-token", "", http.StatusForbidden},
		{"admin-token", "valid-csrf", http.StatusOK},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, catalogRequest(http.MethodPatch, "/api/v1/admin/memberships/22222222-2222-7222-8222-222222222222", tc.token, tc.csrf, `{"status":"cancelled"}`))
		if response.Code != tc.status {
			t.Fatalf("membership cancel token=%q csrf=%q status=%d body=%s", tc.token, tc.csrf, response.Code, response.Body.String())
		}
	}
	if store.createdBy != "admin-id" {
		t.Fatalf("membership cancel actor = %q", store.createdBy)
	}
}

func TestPlanStatusChangeRequiresWritePermissionAndCSRF(t *testing.T) {
	store := &entitlementStub{}
	handler := NewHandlerWithEntitlements(testLogger(), nil, entitlementSessions{}, nil, store)
	for _, tc := range []struct {
		token, csrf string
		status      int
	}{
		{"", "", http.StatusUnauthorized},
		{"member-token", "valid-csrf", http.StatusForbidden},
		{"admin-token", "", http.StatusForbidden},
		{"admin-token", "valid-csrf", http.StatusOK},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, catalogRequest(http.MethodPatch, "/api/v1/admin/plans/11111111-1111-7111-8111-111111111111", tc.token, tc.csrf, `{"status":"archived"}`))
		if response.Code != tc.status {
			t.Fatalf("plan status token=%q csrf=%q status=%d body=%s", tc.token, tc.csrf, response.Code, response.Body.String())
		}
	}
	if store.createdBy != "admin-id" {
		t.Fatalf("plan status actor = %q", store.createdBy)
	}
}
