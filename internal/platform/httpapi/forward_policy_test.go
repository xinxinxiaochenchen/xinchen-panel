package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"controlplane/internal/forward"
)

const testForwardPolicyID = "44444444-4444-7444-8444-444444444444"

type forwardPolicyStub struct {
	actorID string
	deleted string
}

func (s *forwardPolicyStub) CreateTargetPolicy(_ context.Context, input forward.TargetPolicyInput, actorID, _ string) (forward.TargetPolicy, error) {
	s.actorID = actorID
	return forward.TargetPolicy{ID: testForwardPolicyID, Kind: input.Kind, Protocol: input.Protocol,
		PortStart: input.PortStart, PortEnd: input.PortEnd, Enabled: input.Enabled}, nil
}
func (s *forwardPolicyStub) ListTargetPolicies(context.Context, int, string) ([]forward.TargetPolicy, error) {
	return []forward.TargetPolicy{{ID: testForwardPolicyID, Kind: "public_host", Protocol: "TCP", PortStart: 443, PortEnd: 443}}, nil
}
func (s *forwardPolicyStub) GetTargetPolicy(_ context.Context, id string) (forward.TargetPolicy, error) {
	if id != testForwardPolicyID {
		return forward.TargetPolicy{}, forward.ErrNotFound
	}
	return forward.TargetPolicy{ID: id, Kind: "public_host"}, nil
}
func (s *forwardPolicyStub) UpdateTargetPolicy(_ context.Context, id string, patch forward.TargetPolicyPatch, actorID, _ string) (forward.TargetPolicy, error) {
	s.actorID = actorID
	return forward.TargetPolicy{ID: id, Kind: "public_host", Enabled: *patch.Enabled}, nil
}
func (s *forwardPolicyStub) DeleteTargetPolicy(_ context.Context, id, actorID, _ string) error {
	s.actorID = actorID
	s.deleted = id
	return nil
}

func TestForwardPolicyRoutesRequireAdministratorCSRF(t *testing.T) {
	store := &forwardPolicyStub{}
	handler := NewHandlerWithForwardPolicies(testLogger(), nil, forwardSessions{}, nil, nil, nil, nil, nil, store)
	body := `{"kind":"public_host","protocol":"TCP","port_start":443,"port_end":443}`
	for _, tc := range []struct {
		token, csrf string
		status      int
	}{
		{"", "", 401},
		{"member-token", "valid-csrf", 403},
		{"admin-token", "", 403},
		{"admin-token", "valid-csrf", 201},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, catalogRequest(http.MethodPost, "/api/v1/admin/forward-target-policies", tc.token, tc.csrf, body))
		if response.Code != tc.status {
			t.Fatalf("create policy token=%q = %d %s", tc.token, response.Code, response.Body.String())
		}
	}
	if store.actorID != "admin-id" {
		t.Fatalf("policy actor = %q", store.actorID)
	}
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, catalogRequest(http.MethodPost, "/api/v1/admin/forward-target-policies", "admin-token", "valid-csrf", `{"kind":"public_host","protocol":"BOTH","port_start":443,"port_end":443}`))
	if invalid.Code != 422 {
		t.Fatalf("invalid policy = %d", invalid.Code)
	}
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, catalogRequest(http.MethodGet, "/api/v1/admin/forward-target-policies", "admin-token", "", ""))
	if list.Code != 200 || !strings.Contains(list.Body.String(), testForwardPolicyID) {
		t.Fatalf("policy list = %d %s", list.Code, list.Body.String())
	}
	patch := httptest.NewRecorder()
	handler.ServeHTTP(patch, catalogRequest(http.MethodPatch, "/api/v1/admin/forward-target-policies/"+testForwardPolicyID, "admin-token", "valid-csrf", `{"enabled":false}`))
	if patch.Code != 200 || !strings.Contains(patch.Body.String(), `"enabled":false`) {
		t.Fatalf("policy patch = %d %s", patch.Code, patch.Body.String())
	}
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, catalogRequest(http.MethodDelete, "/api/v1/admin/forward-target-policies/"+testForwardPolicyID, "admin-token", "valid-csrf", ""))
	if deleted.Code != 204 || store.deleted != testForwardPolicyID {
		t.Fatalf("policy delete = %d", deleted.Code)
	}
	closed := NewHandlerWithForwardPolicies(testLogger(), nil, nil, nil, nil, nil, nil, nil, store)
	response := httptest.NewRecorder()
	closed.ServeHTTP(response, catalogRequest(http.MethodGet, "/api/v1/admin/forward-target-policies", "", "", ""))
	if response.Code != 404 {
		t.Fatalf("public HTTP policy route = %d", response.Code)
	}
}
