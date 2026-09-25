package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"controlplane/internal/catalog"
	"controlplane/internal/identity"
)

type catalogSessions struct{}

func (catalogSessions) Login(context.Context, string, string) (identity.LoginResult, error) {
	return identity.LoginResult{}, identity.ErrInvalidCredentials
}
func (catalogSessions) Authenticate(_ context.Context, token string) (identity.PublicUser, error) {
	switch token {
	case "admin-token":
		return identity.PublicUser{ID: "admin-id", Permissions: []string{"nodes.write", "nodes.read"}}, nil
	case "member-token":
		return identity.PublicUser{ID: "member-id", Permissions: []string{"nodes.read"}}, nil
	default:
		return identity.PublicUser{}, identity.ErrUnauthenticated
	}
}
func (s catalogSessions) VerifyCSRF(ctx context.Context, token, csrf string) (identity.PublicUser, error) {
	user, err := s.Authenticate(ctx, token)
	if err != nil {
		return identity.PublicUser{}, err
	}
	if csrf != "valid-csrf" {
		return identity.PublicUser{}, identity.ErrInvalidCSRF
	}
	return user, nil
}
func (catalogSessions) Logout(context.Context, string, string) error { return nil }

type catalogStub struct {
	createdBy string
	listedFor string
	nodes     []catalog.Node
}

func (s *catalogStub) CreateGroup(_ context.Context, input catalog.GroupInput, actor, _ string) (catalog.ResourceGroup, error) {
	s.createdBy = actor
	return catalog.ResourceGroup{ID: "11111111-1111-7111-8111-111111111111", Code: input.Code, Name: input.Name, Region: input.Region, Enabled: input.Enabled}, nil
}
func (s *catalogStub) ListGroups(context.Context, int, string) ([]catalog.ResourceGroup, error) {
	return []catalog.ResourceGroup{}, nil
}
func (s *catalogStub) CreateNode(_ context.Context, input catalog.NodeInput, actor, _ string) (catalog.Node, error) {
	s.createdBy = actor
	return catalog.Node{ID: "22222222-2222-7222-8222-222222222222", GroupID: input.GroupID, Name: input.Name, Host: input.Host, Capabilities: input.Capabilities}, nil
}
func (s *catalogStub) ListAllNodes(context.Context, int, string) ([]catalog.Node, error) {
	return []catalog.Node{}, nil
}
func (s *catalogStub) ListAllowedNodes(_ context.Context, userID string, limit int, after string) ([]catalog.Node, error) {
	s.listedFor = userID
	if s.nodes == nil {
		return []catalog.Node{{ID: "33333333-3333-7333-8333-333333333333", Name: "Allowed"}}, nil
	}
	var result []catalog.Node
	for _, node := range s.nodes {
		if node.ID > after {
			result = append(result, node)
		}
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}
func (s *catalogStub) GetAllowedNode(_ context.Context, userID, nodeID string) (catalog.Node, error) {
	s.listedFor = userID
	if nodeID == "33333333-3333-7333-8333-333333333333" {
		return catalog.Node{ID: nodeID, Name: "Allowed"}, nil
	}
	return catalog.Node{}, catalog.ErrNotFound
}

func catalogRequest(method, path, token, csrf, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	return r
}

func TestAdminGroupCreateRequiresSessionRoleAndCSRF(t *testing.T) {
	service := &catalogStub{}
	handler := NewHandlerWithCatalog(testLogger(), nil, catalogSessions{}, service)
	body := `{"code":"RFC.JPT1","name":"Tokyo","region":"JP"}`
	for _, tc := range []struct {
		token, csrf string
		status      int
	}{
		{"", "", 401}, {"member-token", "valid-csrf", 403}, {"admin-token", "", 403}, {"admin-token", "valid-csrf", 201},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, catalogRequest(http.MethodPost, "/api/v1/admin/resource-groups", tc.token, tc.csrf, body))
		if response.Code != tc.status {
			t.Fatalf("token=%s csrf=%s status=%d body=%s", tc.token, tc.csrf, response.Code, response.Body.String())
		}
	}
	if service.createdBy != "admin-id" {
		t.Fatalf("actor = %q", service.createdBy)
	}
}

func TestMemberNodeListIsScopedAndDetailHidesUnauthorizedNode(t *testing.T) {
	service := &catalogStub{}
	handler := NewHandlerWithCatalog(testLogger(), nil, catalogSessions{}, service)
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, catalogRequest(http.MethodGet, "/api/v1/nodes", "member-token", "", ""))
	if list.Code != 200 || service.listedFor != "member-id" || !strings.Contains(list.Body.String(), "Allowed") {
		t.Fatalf("list = %d %s actor=%s", list.Code, list.Body.String(), service.listedFor)
	}
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, catalogRequest(http.MethodGet, "/api/v1/nodes/44444444-4444-7444-8444-444444444444", "member-token", "", ""))
	if denied.Code != 404 {
		t.Fatalf("unauthorized detail = %d %s", denied.Code, denied.Body.String())
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, catalogRequest(http.MethodGet, "/api/v1/nodes", "", "", ""))
	if missing.Code != 401 {
		t.Fatalf("anonymous list = %d", missing.Code)
	}
}

func TestAdminNodeCreateValidatesInput(t *testing.T) {
	service := &catalogStub{}
	handler := NewHandlerWithCatalog(testLogger(), nil, catalogSessions{}, service)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, catalogRequest(http.MethodPost, "/api/v1/admin/nodes", "admin-token", "valid-csrf", `{"group_id":"11111111-1111-7111-8111-111111111111","name":"Exit","region":"JP","host":"https://bad","capabilities":["proxy"]}`))
	if response.Code != 422 || service.createdBy != "" {
		t.Fatalf("invalid node = %d %s actor=%s", response.Code, response.Body.String(), service.createdBy)
	}
}

func TestNodeListUsesOpaqueCursor(t *testing.T) {
	service := &catalogStub{nodes: []catalog.Node{
		{ID: "11111111-1111-7111-8111-111111111111", Name: "First"},
		{ID: "22222222-2222-7222-8222-222222222222", Name: "Second"},
	}}
	handler := NewHandlerWithCatalog(testLogger(), nil, catalogSessions{}, service)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, catalogRequest(http.MethodGet, "/api/v1/nodes?limit=1", "member-token", "", ""))
	var page struct {
		Items      []catalog.Node `json:"items"`
		NextCursor *string        `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil || first.Code != 200 || len(page.Items) != 1 || page.NextCursor == nil || *page.NextCursor == page.Items[0].ID {
		t.Fatalf("first page = %d %s %v", first.Code, first.Body.String(), err)
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, catalogRequest(http.MethodGet, "/api/v1/nodes?limit=1&cursor="+*page.NextCursor, "member-token", "", ""))
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil || second.Code != 200 || len(page.Items) != 1 || page.Items[0].Name != "Second" || page.NextCursor != nil {
		t.Fatalf("second page = %d %s %v", second.Code, second.Body.String(), err)
	}
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, catalogRequest(http.MethodGet, "/api/v1/nodes?cursor=bad!", "member-token", "", ""))
	if invalid.Code != 400 {
		t.Fatalf("invalid cursor = %d", invalid.Code)
	}
}
