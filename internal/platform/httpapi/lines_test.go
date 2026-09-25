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

const testLineID = "22222222-2222-7222-8222-222222222222"
const testLineNodeID = "11111111-1111-7111-8111-111111111111"

type lineSessions struct{ catalogSessions }

func (lineSessions) Authenticate(_ context.Context, token string) (identity.PublicUser, error) {
	switch token {
	case "admin-token":
		return identity.PublicUser{ID: "admin-id", Permissions: []string{"lines.read", "lines.write", "lines.write.self"}}, nil
	case "member-token":
		return identity.PublicUser{ID: "member-id", Permissions: []string{"lines.read", "lines.write.self"}}, nil
	default:
		return identity.PublicUser{}, identity.ErrUnauthenticated
	}
}

func (s lineSessions) VerifyCSRF(ctx context.Context, token, csrf string) (identity.PublicUser, error) {
	user, err := s.Authenticate(ctx, token)
	if err != nil {
		return identity.PublicUser{}, err
	}
	if csrf != "valid-csrf" {
		return identity.PublicUser{}, identity.ErrInvalidCSRF
	}
	return user, nil
}

type lineStub struct {
	createdBy string
	readFor   string
	updatedBy string
	deletedBy string
	createErr error
	lines     []catalog.Line
}

func (s *lineStub) CreateSharedLine(_ context.Context, input catalog.LineInput, actorID, _ string) (catalog.Line, error) {
	if s.createErr != nil {
		return catalog.Line{}, s.createErr
	}
	s.createdBy = actorID
	return catalog.Line{ID: testLineID, Name: input.Name, Hops: []catalog.LineHop{{NodeID: input.NodeID, Role: "egress"}}}, nil
}
func (s *lineStub) CreateCustomLine(_ context.Context, input catalog.LineInput, ownerID, _ string) (catalog.Line, error) {
	if s.createErr != nil {
		return catalog.Line{}, s.createErr
	}
	s.createdBy = ownerID
	return catalog.Line{ID: testLineID, OwnerUserID: &ownerID, Name: input.Name, Hops: []catalog.LineHop{{NodeID: input.NodeID, Role: "egress"}}}, nil
}
func (s *lineStub) ListAllLines(context.Context, int, string) ([]catalog.Line, error) {
	return []catalog.Line{{ID: testLineID, Name: "Shared JP"}}, nil
}
func (s *lineStub) ListAllowedLines(_ context.Context, userID string, limit int, after string) ([]catalog.Line, error) {
	s.readFor = userID
	if s.lines != nil {
		var result []catalog.Line
		for _, line := range s.lines {
			if line.ID > after {
				result = append(result, line)
			}
		}
		if len(result) > limit {
			result = result[:limit]
		}
		return result, nil
	}
	return []catalog.Line{{ID: testLineID, Name: "Own JP"}}, nil
}
func (s *lineStub) GetLine(_ context.Context, lineID string) (catalog.Line, error) {
	if lineID != testLineID {
		return catalog.Line{}, catalog.ErrNotFound
	}
	return catalog.Line{ID: lineID, Name: "Shared JP"}, nil
}
func (s *lineStub) GetAllowedLine(_ context.Context, userID, lineID string) (catalog.Line, error) {
	s.readFor = userID
	if lineID != testLineID {
		return catalog.Line{}, catalog.ErrNotFound
	}
	return catalog.Line{ID: lineID, Name: "Own JP"}, nil
}
func (s *lineStub) UpdateSharedLine(_ context.Context, lineID string, patch catalog.LinePatch, actorID, _ string) (catalog.Line, error) {
	s.updatedBy = actorID
	return catalog.Line{ID: lineID, Name: *patch.Name}, nil
}
func (s *lineStub) UpdateOwnLine(_ context.Context, ownerID, lineID string, patch catalog.LinePatch, _ string) (catalog.Line, error) {
	s.updatedBy = ownerID
	return catalog.Line{ID: lineID, OwnerUserID: &ownerID, Name: *patch.Name}, nil
}
func (s *lineStub) DeleteOwnLine(_ context.Context, ownerID, _, _ string) error {
	s.deletedBy = ownerID
	return nil
}

func TestLineRoutesEnforceRolesCSRFAndOwnership(t *testing.T) {
	store := &lineStub{}
	handler := NewHandlerWithLines(testLogger(), nil, lineSessions{}, nil, nil, nil, store)
	body := `{"name":"Japan","node_id":"` + testLineNodeID + `"}`
	for _, tc := range []struct {
		path, token, csrf string
		status            int
	}{
		{"/api/v1/admin/lines", "", "", 401},
		{"/api/v1/admin/lines", "member-token", "valid-csrf", 403},
		{"/api/v1/admin/lines", "admin-token", "", 403},
		{"/api/v1/admin/lines", "admin-token", "valid-csrf", 201},
		{"/api/v1/lines", "member-token", "valid-csrf", 201},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, catalogRequest(http.MethodPost, tc.path, tc.token, tc.csrf, body))
		if response.Code != tc.status {
			t.Fatalf("create %s token=%s status=%d body=%s", tc.path, tc.token, response.Code, response.Body.String())
		}
	}
	if store.createdBy != "member-id" {
		t.Fatalf("owner = %q", store.createdBy)
	}
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, catalogRequest(http.MethodPost, "/api/v1/lines", "member-token", "valid-csrf", `{"name":"Japan","node_id":"`+testLineNodeID+`","multiplier_milli":2000}`))
	if invalid.Code != 422 {
		t.Fatalf("member multiplier = %d %s", invalid.Code, invalid.Body.String())
	}
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, catalogRequest(http.MethodGet, "/api/v1/lines", "member-token", "", ""))
	if list.Code != 200 || store.readFor != "member-id" || !strings.Contains(list.Body.String(), "Own JP") {
		t.Fatalf("member list = %d %s", list.Code, list.Body.String())
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, catalogRequest(http.MethodGet, "/api/v1/lines/33333333-3333-7333-8333-333333333333", "member-token", "", ""))
	if missing.Code != 404 {
		t.Fatalf("other line detail = %d", missing.Code)
	}
	patch := httptest.NewRecorder()
	handler.ServeHTTP(patch, catalogRequest(http.MethodPatch, "/api/v1/lines/"+testLineID, "member-token", "valid-csrf", `{"name":"Renamed"}`))
	if patch.Code != 200 || store.updatedBy != "member-id" {
		t.Fatalf("member update = %d for=%s", patch.Code, store.updatedBy)
	}
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, catalogRequest(http.MethodDelete, "/api/v1/lines/"+testLineID, "member-token", "valid-csrf", ""))
	if deleted.Code != 204 || store.deletedBy != "member-id" {
		t.Fatalf("member delete = %d for=%s", deleted.Code, store.deletedBy)
	}
}

func TestLineCreateMapsLimitAndClosesPlainHTTPPreview(t *testing.T) {
	store := &lineStub{createErr: catalog.ErrLimitReached}
	handler := NewHandlerWithLines(testLogger(), nil, lineSessions{}, nil, nil, nil, store)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, catalogRequest(http.MethodPost, "/api/v1/lines", "member-token", "valid-csrf", `{"name":"Japan","node_id":"`+testLineNodeID+`"}`))
	if response.Code != 409 || !strings.Contains(response.Body.String(), "LIMIT_REACHED") {
		t.Fatalf("line limit = %d %s", response.Code, response.Body.String())
	}
	closed := NewHandlerWithLines(testLogger(), nil, nil, nil, nil, nil, store)
	response = httptest.NewRecorder()
	closed.ServeHTTP(response, catalogRequest(http.MethodGet, "/api/v1/lines", "", "", ""))
	if response.Code != 404 {
		t.Fatalf("public HTTP line route = %d", response.Code)
	}
}

func TestLinePaginationAndMissingEntitlement(t *testing.T) {
	store := &lineStub{lines: []catalog.Line{
		{ID: "11111111-1111-7111-8111-111111111111", Name: "First"},
		{ID: "22222222-2222-7222-8222-222222222222", Name: "Second"},
	}}
	handler := NewHandlerWithLines(testLogger(), nil, lineSessions{}, nil, nil, nil, store)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, catalogRequest(http.MethodGet, "/api/v1/lines?limit=1", "member-token", "", ""))
	var page struct {
		Items      []catalog.Line `json:"items"`
		NextCursor *string        `json:"next_cursor"`
	}
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatalf("first page = %d %s", first.Code, first.Body.String())
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, catalogRequest(http.MethodGet, "/api/v1/lines?limit=1&cursor="+*page.NextCursor, "member-token", "", ""))
	if second.Code != 200 || !strings.Contains(second.Body.String(), "Second") || strings.Contains(second.Body.String(), "First") {
		t.Fatalf("second page = %d %s", second.Code, second.Body.String())
	}
	store.createErr = catalog.ErrNotFound
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, catalogRequest(http.MethodPost, "/api/v1/lines", "member-token", "valid-csrf", `{"name":"Japan","node_id":"`+testLineNodeID+`"}`))
	if missing.Code != 404 {
		t.Fatalf("missing entitlement = %d %s", missing.Code, missing.Body.String())
	}
	badJSON := httptest.NewRecorder()
	handler.ServeHTTP(badJSON, catalogRequest(http.MethodPost, "/api/v1/lines", "member-token", "valid-csrf", `{"name":`))
	if badJSON.Code != 400 {
		t.Fatalf("invalid JSON = %d", badJSON.Code)
	}
}
