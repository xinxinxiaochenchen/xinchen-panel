package httpapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"controlplane/internal/identity"
	"controlplane/internal/routing"
)

const testRouteProfileID = "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423"
const testRouteRuleID = "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e424"

type routingSessions struct{ catalogSessions }

func (routingSessions) Authenticate(_ context.Context, token string) (identity.PublicUser, error) {
	if token == "writer" {
		return identity.PublicUser{ID: "owner", Permissions: []string{"routing.read", "routing.write"}}, nil
	}
	if token == "reader" {
		return identity.PublicUser{ID: "reader", Permissions: []string{"routing.read"}}, nil
	}
	return identity.PublicUser{}, identity.ErrUnauthenticated
}
func (s routingSessions) VerifyCSRF(ctx context.Context, token, csrf string) (identity.PublicUser, error) {
	if csrf != "valid-csrf" {
		return identity.PublicUser{}, identity.ErrInvalidCSRF
	}
	return s.Authenticate(ctx, token)
}

type routingStub struct {
	RoutingStore
	owner  string
	ruleID string
}

func (s *routingStub) ListRules(_ context.Context, owner, profileID string, limit int, after string) ([]routing.Rule, error) {
	s.owner = owner
	if after != "10:"+testRouteRuleID {
		return nil, routing.ValidationError{Field: "cursor", Reason: "wrong position"}
	}
	return []routing.Rule{{ID: testRouteRuleID, ProfileID: profileID, Priority: 20}}, nil
}

func TestRoutingRuleCursorCarriesPriority(t *testing.T) {
	store := &routingStub{}
	h := NewHandlerWithStores(testLogger(), nil, routingSessions{}, RouteStores{Routing: store})
	cursor := base64.RawURLEncoding.EncodeToString([]byte("10:" + testRouteRuleID))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, catalogRequest(http.MethodGet, "/api/v1/routing-profiles/"+testRouteProfileID+"/rules?cursor="+cursor, "writer", "", ""))
	if w.Code != 200 || store.owner != "owner" {
		t.Fatalf("cursor list=%d owner=%q body=%s", w.Code, store.owner, w.Body.String())
	}
}

func (s *routingStub) CreateProfile(_ context.Context, owner string, input routing.ProfileInput, _ string) (routing.Profile, error) {
	s.owner = owner
	return routing.Profile{ID: testRouteProfileID, Name: input.Name}, nil
}
func (s *routingStub) UpdateRule(_ context.Context, owner, profileID, ruleID string, patch routing.RulePatch, _ string) (routing.Rule, error) {
	s.owner = owner
	s.ruleID = ruleID
	return routing.Rule{ID: ruleID, ProfileID: profileID, Priority: *patch.Priority}, nil
}

func TestRoutingWriteRequiresPermissionAndCSRF(t *testing.T) {
	store := &routingStub{}
	h := NewHandlerWithStores(testLogger(), nil, routingSessions{}, RouteStores{Routing: store})
	for _, tc := range []struct {
		token, csrf string
		status      int
	}{{"", "", 401}, {"reader", "valid-csrf", 403}, {"writer", "", 403}, {"writer", "valid-csrf", 201}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, catalogRequest("POST", "/api/v1/routing-profiles", tc.token, tc.csrf, `{"name":"Main","fallback_kind":"direct"}`))
		if w.Code != tc.status {
			t.Fatalf("profile create=%d %s", w.Code, w.Body.String())
		}
	}
	if store.owner != "owner" {
		t.Fatalf("wrong owner %q", store.owner)
	}
}
func TestRoutingRuleUpdateScopesOwnerAndParsesIDs(t *testing.T) {
	store := &routingStub{}
	h := NewHandlerWithStores(testLogger(), nil, routingSessions{}, RouteStores{Routing: store})
	path := "/api/v1/routing-profiles/" + testRouteProfileID + "/rules/" + testRouteRuleID
	w := httptest.NewRecorder()
	h.ServeHTTP(w, catalogRequest(http.MethodPatch, path, "writer", "valid-csrf", `{"priority":20}`))
	if w.Code != 200 || store.owner != "owner" || store.ruleID != testRouteRuleID {
		t.Fatalf("rule update=%d owner=%q rule=%q body=%s", w.Code, store.owner, store.ruleID, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, catalogRequest(http.MethodPatch, "/api/v1/routing-profiles/bad/rules/"+testRouteRuleID, "writer", "valid-csrf", `{"priority":20}`))
	if w.Code != 404 {
		t.Fatalf("bad path=%d", w.Code)
	}
}
