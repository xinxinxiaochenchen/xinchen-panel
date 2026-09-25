package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"controlplane/internal/forward"
	"controlplane/internal/identity"
)

const testForwardID = "33333333-3333-7333-8333-333333333333"
const testForwardNodeID = "11111111-1111-7111-8111-111111111111"

type forwardSessions struct{ catalogSessions }

func (forwardSessions) Authenticate(_ context.Context, token string) (identity.PublicUser, error) {
	switch token {
	case "admin-token":
		return identity.PublicUser{ID: "admin-id", Permissions: []string{"forward_policies.write"}}, nil
	case "member-token":
		return identity.PublicUser{ID: "member-id", Permissions: []string{"forward_rules.read", "forward_rules.write"}}, nil
	case "no-write-token":
		return identity.PublicUser{ID: "read-only-id", Permissions: []string{"forward_rules.read"}}, nil
	default:
		return identity.PublicUser{}, identity.ErrUnauthenticated
	}
}

func (s forwardSessions) VerifyCSRF(ctx context.Context, token, csrf string) (identity.PublicUser, error) {
	user, err := s.Authenticate(ctx, token)
	if err != nil {
		return identity.PublicUser{}, err
	}
	if csrf != "valid-csrf" {
		return identity.PublicUser{}, identity.ErrInvalidCSRF
	}
	return user, nil
}

type forwardStub struct {
	ownerID   string
	writeID   string
	deleteID  string
	createErr error
	rules     []forward.Rule
}

func (s *forwardStub) CreateRule(_ context.Context, input forward.RuleInput, ownerID, _ string) (forward.Rule, error) {
	if s.createErr != nil {
		return forward.Rule{}, s.createErr
	}
	s.ownerID = ownerID
	return forward.Rule{ID: testForwardID, UserID: ownerID, Name: input.Name,
		IngressNodeID: input.IngressNodeID, IngressPort: input.IngressPort,
		TargetHost: input.TargetHost, TargetPort: input.TargetPort,
		Protocol: input.Protocol, Enabled: input.Enabled, ApplyStatus: "pending"}, nil
}

func (s *forwardStub) ListOwnRules(_ context.Context, ownerID string, limit int, after string) ([]forward.Rule, error) {
	s.ownerID = ownerID
	var rules []forward.Rule
	for _, rule := range s.rules {
		if rule.ID > after {
			rules = append(rules, rule)
		}
	}
	if len(rules) > limit {
		rules = rules[:limit]
	}
	return rules, nil
}

func (s *forwardStub) GetOwnRule(_ context.Context, ownerID, ruleID string) (forward.Rule, error) {
	s.ownerID = ownerID
	if ruleID != testForwardID || ownerID != "member-id" {
		return forward.Rule{}, forward.ErrNotFound
	}
	return forward.Rule{ID: ruleID, UserID: ownerID, Name: "Forward"}, nil
}

func (s *forwardStub) UpdateOwnRule(_ context.Context, ownerID, ruleID string, patch forward.RulePatch, _ string) (forward.Rule, error) {
	s.writeID = ownerID
	if ruleID != testForwardID || ownerID != "member-id" {
		return forward.Rule{}, forward.ErrNotFound
	}
	return forward.Rule{ID: ruleID, UserID: ownerID, Name: *patch.Name}, nil
}

func (s *forwardStub) DeleteOwnRule(_ context.Context, ownerID, ruleID, _ string) error {
	s.deleteID = ownerID
	if ruleID != testForwardID || ownerID != "member-id" {
		return forward.ErrNotFound
	}
	return nil
}

func TestForwardRoutesEnforceSessionPermissionCSRFAndOwnership(t *testing.T) {
	store := &forwardStub{rules: []forward.Rule{{ID: testForwardID, Name: "Forward"}}}
	handler := NewHandlerWithForward(testLogger(), nil, forwardSessions{}, nil, nil, nil, nil, store)
	body := `{"name":"Forward","ingress_node_id":"` + testForwardNodeID + `","ingress_port":24000,"target_host":"example.org","target_port":443,"protocol":"TCP"}`
	for _, tc := range []struct {
		token, csrf string
		status      int
	}{
		{"", "", 401},
		{"no-write-token", "valid-csrf", 403},
		{"member-token", "", 403},
		{"member-token", "valid-csrf", 201},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, catalogRequest(http.MethodPost, "/api/v1/forward-rules", tc.token, tc.csrf, body))
		if response.Code != tc.status {
			t.Fatalf("create token=%q status=%d body=%s", tc.token, response.Code, response.Body.String())
		}
	}
	if store.ownerID != "member-id" {
		t.Fatalf("created for %q", store.ownerID)
	}
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, catalogRequest(http.MethodGet, "/api/v1/forward-rules", "member-token", "", ""))
	if list.Code != 200 || !strings.Contains(list.Body.String(), "Forward") {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, catalogRequest(http.MethodGet, "/api/v1/forward-rules/22222222-2222-7222-8222-222222222222", "member-token", "", ""))
	if missing.Code != 404 {
		t.Fatalf("other rule detail = %d", missing.Code)
	}
	patch := httptest.NewRecorder()
	handler.ServeHTTP(patch, catalogRequest(http.MethodPatch, "/api/v1/forward-rules/"+testForwardID, "member-token", "valid-csrf", `{"name":"Renamed"}`))
	if patch.Code != 200 || store.writeID != "member-id" {
		t.Fatalf("update = %d for=%s", patch.Code, store.writeID)
	}
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, catalogRequest(http.MethodDelete, "/api/v1/forward-rules/"+testForwardID, "member-token", "valid-csrf", ""))
	if deleted.Code != 204 || store.deleteID != "member-id" {
		t.Fatalf("delete = %d for=%s", deleted.Code, store.deleteID)
	}
	closed := NewHandlerWithForward(testLogger(), nil, nil, nil, nil, nil, nil, store)
	response := httptest.NewRecorder()
	closed.ServeHTTP(response, catalogRequest(http.MethodGet, "/api/v1/forward-rules", "", "", ""))
	if response.Code != 404 {
		t.Fatalf("public HTTP forward route = %d", response.Code)
	}
}

func TestForwardRoutesMapValidationLimitAndPagination(t *testing.T) {
	store := &forwardStub{rules: []forward.Rule{
		{ID: "11111111-1111-7111-8111-111111111111", Name: "First"},
		{ID: "22222222-2222-7222-8222-222222222222", Name: "Second"},
	}}
	handler := NewHandlerWithForward(testLogger(), nil, forwardSessions{}, nil, nil, nil, nil, store)
	body := `{"name":"Forward","ingress_node_id":"` + testForwardNodeID + `","ingress_port":24000,"target_host":"example.org","target_port":443,"protocol":"TCP"}`
	store.createErr = forward.ErrLimitReached
	limit := httptest.NewRecorder()
	handler.ServeHTTP(limit, catalogRequest(http.MethodPost, "/api/v1/forward-rules", "member-token", "valid-csrf", body))
	if limit.Code != 409 || !strings.Contains(limit.Body.String(), "LIMIT_REACHED") {
		t.Fatalf("limit = %d %s", limit.Code, limit.Body.String())
	}
	store.createErr = forward.ErrConflict
	conflict := httptest.NewRecorder()
	handler.ServeHTTP(conflict, catalogRequest(http.MethodPost, "/api/v1/forward-rules", "member-token", "valid-csrf", body))
	if conflict.Code != 409 {
		t.Fatalf("port conflict = %d", conflict.Code)
	}
	badJSON := httptest.NewRecorder()
	handler.ServeHTTP(badJSON, catalogRequest(http.MethodPost, "/api/v1/forward-rules", "member-token", "valid-csrf", `{"name":`))
	if badJSON.Code != 400 {
		t.Fatalf("bad JSON = %d", badJSON.Code)
	}
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, catalogRequest(http.MethodPost, "/api/v1/forward-rules", "member-token", "valid-csrf", strings.Replace(body, "example.org", "127.0.0.1", 1)))
	if invalid.Code != 422 {
		t.Fatalf("private destination = %d", invalid.Code)
	}
	unexpectedLine := httptest.NewRecorder()
	handler.ServeHTTP(unexpectedLine, catalogRequest(http.MethodPost, "/api/v1/forward-rules", "member-token", "valid-csrf", strings.Replace(body, `"protocol":"TCP"`, `"protocol":"TCP","line_id":null`, 1)))
	if unexpectedLine.Code != 400 {
		t.Fatalf("unsupported line_id = %d %s", unexpectedLine.Code, unexpectedLine.Body.String())
	}
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, catalogRequest(http.MethodGet, "/api/v1/forward-rules?limit=1", "member-token", "", ""))
	var page struct {
		Items      []forward.Rule `json:"items"`
		NextCursor *string        `json:"next_cursor"`
	}
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatalf("first page = %d %s", first.Code, first.Body.String())
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, catalogRequest(http.MethodGet, "/api/v1/forward-rules?limit=1&cursor="+*page.NextCursor, "member-token", "", ""))
	if second.Code != 200 || !strings.Contains(second.Body.String(), "Second") || strings.Contains(second.Body.String(), "First") {
		t.Fatalf("second page = %d %s", second.Code, second.Body.String())
	}
}
