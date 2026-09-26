package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"controlplane/internal/georules"
	"controlplane/internal/identity"
)

type geoSessions struct{ catalogSessions }

func (geoSessions) Authenticate(_ context.Context, token string) (identity.PublicUser, error) {
	switch token {
	case "writer":
		return identity.PublicUser{ID: testRouteProfileID, Permissions: []string{"routing_rulesets.read", "routing_rulesets.write"}}, nil
	case "reader":
		return identity.PublicUser{ID: testRouteProfileID, Permissions: []string{"routing_rulesets.read"}}, nil
	default:
		return identity.PublicUser{}, identity.ErrUnauthenticated
	}
}

func (s geoSessions) VerifyCSRF(ctx context.Context, token, csrf string) (identity.PublicUser, error) {
	if csrf != "valid-csrf" {
		return identity.PublicUser{}, identity.ErrInvalidCSRF
	}
	return s.Authenticate(ctx, token)
}

type geoStub struct {
	GeoRuleSetStore
	created bool
	input   georules.Input
	actor   string
}

func (s *geoStub) Create(_ context.Context, in georules.Input, actor, _ string) (georules.RuleSet, error) {
	s.created, s.input, s.actor = true, in, actor
	return georules.RuleSet{ID: testRouteRuleID, Kind: in.Kind, Code: in.Code}, nil
}

func TestGeoRuleSetUploadRequiresAdminPermissionAndCSRF(t *testing.T) {
	store := &geoStub{}
	h := NewHandlerWithStores(testLogger(), nil, geoSessions{}, RouteStores{GeoRuleSets: store})
	body := `{"kind":"geosite","code":"ai","name":"AI","version":"v1","source":"operator","entries":["suffix:example.com"]}`
	for _, tc := range []struct {
		token, csrf string
		status      int
	}{{"", "", 401}, {"reader", "valid-csrf", 403}, {"writer", "", 403}, {"writer", "valid-csrf", 201}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, catalogRequest(http.MethodPost, "/api/v1/admin/routing-rule-sets", tc.token, tc.csrf, body))
		if w.Code != tc.status {
			t.Fatalf("token %q: status %d body %s", tc.token, w.Code, w.Body.String())
		}
	}
	if !store.created || store.input.Code != "ai" || len(store.input.Entries) != 1 || store.actor != testRouteProfileID {
		t.Fatalf("unexpected upload: %+v", store)
	}
}

func TestGeoRuleSetUploadRejectsOversizeAndUnsafeEntries(t *testing.T) {
	store := &geoStub{}
	h := NewHandlerWithStores(testLogger(), nil, geoSessions{}, RouteStores{GeoRuleSets: store})
	unsafe := `{"kind":"geosite","code":"ai","name":"AI","version":"v1","source":"operator","entries":["evil.com,REJECT"]}`
	w := httptest.NewRecorder()
	h.ServeHTTP(w, catalogRequest(http.MethodPost, "/api/v1/admin/routing-rule-sets", "writer", "valid-csrf", unsafe))
	if w.Code != 422 || store.created {
		t.Fatalf("unsafe upload status=%d created=%v", w.Code, store.created)
	}
	oversize := strings.Repeat(" ", 8<<20) + unsafe
	w = httptest.NewRecorder()
	h.ServeHTTP(w, catalogRequest(http.MethodPost, "/api/v1/admin/routing-rule-sets", "writer", "valid-csrf", oversize))
	if w.Code != 400 {
		t.Fatalf("oversize upload status=%d", w.Code)
	}
}
