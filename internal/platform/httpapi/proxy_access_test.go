package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"controlplane/internal/identity"
	"controlplane/internal/proxyaccess"
)

const testProxyID = "33333333-3333-7333-8333-333333333333"
const testProxyLineID = "11111111-1111-7111-8111-111111111111"

type proxySessions struct{ catalogSessions }

func (proxySessions) Authenticate(_ context.Context, token string) (identity.PublicUser, error) {
	switch token {
	case "member-token":
		return identity.PublicUser{ID: "member-id", Permissions: []string{"proxy_accesses.read", "proxy_accesses.write"}}, nil
	case "read-token":
		return identity.PublicUser{ID: "reader-id", Permissions: []string{"proxy_accesses.read"}}, nil
	default:
		return identity.PublicUser{}, identity.ErrUnauthenticated
	}
}

func (s proxySessions) VerifyCSRF(ctx context.Context, token, csrf string) (identity.PublicUser, error) {
	user, err := s.Authenticate(ctx, token)
	if err != nil {
		return identity.PublicUser{}, err
	}
	if csrf != "valid-csrf" {
		return identity.PublicUser{}, identity.ErrInvalidCSRF
	}
	return user, nil
}

type proxyStub struct{ createdFor, revealedFor, rotatedFor string }

func (s *proxyStub) Create(_ context.Context, ownerID string, input proxyaccess.AccessInput, _ string) (proxyaccess.Access, string, error) {
	s.createdFor = ownerID
	return proxyaccess.Access{ID: testProxyID, UserID: ownerID, LineID: input.LineID, Name: input.Name, Enabled: input.Enabled}, "secret", nil
}
func (s *proxyStub) ListOwn(_ context.Context, _ string, _ int, _ string) ([]proxyaccess.Access, error) {
	return []proxyaccess.Access{{ID: testProxyID, Name: "Japan"}}, nil
}
func (s *proxyStub) GetOwn(_ context.Context, _, _ string) (proxyaccess.Access, error) {
	return proxyaccess.Access{ID: testProxyID, Name: "Japan"}, nil
}
func (s *proxyStub) RevealOwn(_ context.Context, ownerID, _ string) (string, error) {
	s.revealedFor = ownerID
	return "secret", nil
}
func (s *proxyStub) RotateOwn(_ context.Context, ownerID, _, _ string) (string, error) {
	s.rotatedFor = ownerID
	return "new-secret", nil
}
func (s *proxyStub) UpdateOwn(_ context.Context, _, _ string, _ proxyaccess.AccessPatch, _ string) (proxyaccess.Access, error) {
	return proxyaccess.Access{ID: testProxyID}, nil
}
func (s *proxyStub) DeleteOwn(_ context.Context, _, _, _ string) error { return nil }

func TestProxyAccessRoutesEnforceRBACCSRFAndSecretResponses(t *testing.T) {
	store := &proxyStub{}
	handler := NewHandlerWithProxyAccess(testLogger(), nil, proxySessions{}, store)
	body := `{"name":"Japan","line_id":"` + testProxyLineID + `"}`
	for _, tc := range []struct {
		token, csrf string
		want        int
	}{
		{"", "", 401}, {"read-token", "valid-csrf", 403},
		{"member-token", "", 403}, {"member-token", "valid-csrf", 201},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, catalogRequest(http.MethodPost, "/api/v1/proxy-accesses", tc.token, tc.csrf, body))
		if response.Code != tc.want {
			t.Fatalf("create %q = %d %s", tc.token, response.Code, response.Body.String())
		}
		if tc.want == 201 && (!strings.Contains(response.Body.String(), "secret") || response.Header().Get("Cache-Control") != "no-store") {
			t.Fatalf("create response = %s", response.Body.String())
		}
	}
	if store.createdFor != "member-id" {
		t.Fatalf("created for %q", store.createdFor)
	}
	for _, path := range []string{"/api/v1/proxy-accesses", "/api/v1/proxy-accesses/" + testProxyID} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, catalogRequest(http.MethodGet, path, "member-token", "", ""))
		if response.Code != 200 || strings.Contains(response.Body.String(), "secret") {
			t.Fatalf("ordinary read %s = %d %s", path, response.Code, response.Body.String())
		}
	}
	secret := httptest.NewRecorder()
	handler.ServeHTTP(secret, catalogRequest(http.MethodGet, "/api/v1/proxy-accesses/"+testProxyID+"/credential", "member-token", "", ""))
	if secret.Code != 200 || store.revealedFor != "member-id" || !strings.Contains(secret.Body.String(), "secret") {
		t.Fatalf("owner credential response = %d %s", secret.Code, secret.Body.String())
	}
	rotate := httptest.NewRecorder()
	handler.ServeHTTP(rotate, catalogRequest(http.MethodPost, "/api/v1/proxy-accesses/"+testProxyID+"/credential-rotation", "member-token", "valid-csrf", ""))
	if rotate.Code != 200 || store.rotatedFor != "member-id" || !strings.Contains(rotate.Body.String(), "new-secret") {
		t.Fatalf("rotation = %d %s", rotate.Code, rotate.Body.String())
	}
	closed := NewHandlerWithProxyAccess(testLogger(), nil, nil, store)
	response := httptest.NewRecorder()
	closed.ServeHTTP(response, catalogRequest(http.MethodGet, "/api/v1/proxy-accesses", "", "", ""))
	if response.Code != 404 {
		t.Fatalf("public HTTP proxy API = %d", response.Code)
	}
}
