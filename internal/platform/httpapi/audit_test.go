package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"controlplane/internal/audit"
	"controlplane/internal/identity"
)

type auditSessions struct{ accountSessions }

func (auditSessions) Authenticate(_ context.Context, token string) (identity.PublicUser, error) {
	if token == "auditor" {
		return identity.PublicUser{ID: "auditor-id", Permissions: []string{"audit.read"}}, nil
	}
	if token == "member" {
		return identity.PublicUser{ID: "member-id", Permissions: []string{"nodes.read"}}, nil
	}
	return identity.PublicUser{}, identity.ErrUnauthenticated
}

type auditStub struct {
	called bool
	limit  int
	cursor string
}

func (s *auditStub) List(_ context.Context, limit int, cursor string) (audit.Page, error) {
	s.called, s.limit, s.cursor = true, limit, cursor
	return audit.Page{Items: []audit.Record{}}, nil
}

func TestAuditRouteRequiresPermissionAndValidPage(t *testing.T) {
	store := &auditStub{}
	handler := NewHandlerWithStores(testLogger(), nil, auditSessions{}, RouteStores{Audit: store})
	for _, tc := range []struct {
		method, path, token string
		want                int
	}{
		{"GET", "/api/v1/admin/audit", "", 401},
		{"GET", "/api/v1/admin/audit", "member", 403},
		{"POST", "/api/v1/admin/audit", "auditor", 405},
		{"GET", "/api/v1/admin/audit?limit=0", "auditor", 400},
		{"GET", "/api/v1/admin/audit?limit=5&cursor=bad", "auditor", 400},
		{"GET", "/api/v1/admin/audit?limit=5", "auditor", 200},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(""))
		if tc.token != "" {
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tc.token})
		}
		handler.ServeHTTP(response, request)
		if response.Code != tc.want {
			t.Fatalf("%s %s as %s = %d %s", tc.method, tc.path, tc.token, response.Code, response.Body.String())
		}
	}
	if !store.called || store.limit != 5 {
		t.Fatalf("store call = %+v", store)
	}
}
