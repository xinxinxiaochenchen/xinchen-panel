package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controlplane/internal/agentidentity"
	"controlplane/internal/identity"
)

type agentAdminSessions struct{ catalogSessions }

func (s agentAdminSessions) VerifyPassword(ctx context.Context, token, password string) (identity.PublicUser, error) {
	user, err := s.Authenticate(ctx, token)
	if err != nil {
		return identity.PublicUser{}, err
	}
	if password != "correct-password" {
		return identity.PublicUser{}, identity.ErrInvalidCredentials
	}
	return user, nil
}

func (agentAdminSessions) Authenticate(_ context.Context, token string) (identity.PublicUser, error) {
	switch token {
	case "admin-token":
		return identity.PublicUser{ID: certificateTestNodeID, Permissions: []string{"agents.write"}}, nil
	case "member-token":
		return identity.PublicUser{ID: "member-id", Permissions: []string{"nodes.read"}}, nil
	default:
		return identity.PublicUser{}, identity.ErrUnauthenticated
	}
}

func (s agentAdminSessions) VerifyCSRF(ctx context.Context, token, csrf string) (identity.PublicUser, error) {
	user, err := s.Authenticate(ctx, token)
	if err != nil {
		return identity.PublicUser{}, err
	}
	if csrf != "valid-csrf" {
		return identity.PublicUser{}, identity.ErrInvalidCSRF
	}
	return user, nil
}

type agentTokenStub struct {
	nodeID, actorID string
	calls           int
	revoked         bool
}

func (stub *agentTokenStub) CreateToken(_ context.Context, nodeID, actorID, requestID string) (agentidentity.EnrollmentToken, error) {
	stub.calls++
	stub.nodeID, stub.actorID = nodeID, actorID
	if requestID == "" {
		return agentidentity.EnrollmentToken{}, errors.New("missing request ID")
	}
	return agentidentity.EnrollmentToken{Token: "one-time-secret", ExpiresAt: time.Now().Add(10 * time.Minute)}, nil
}
func (stub *agentTokenStub) RevokeAgent(_ context.Context, nodeID, actorID, requestID string) error {
	if requestID == "" {
		return errors.New("missing request ID")
	}
	stub.nodeID, stub.actorID, stub.revoked = nodeID, actorID, true
	return nil
}

func TestAdminCanCreateAgentTokenWithCSRFAndPermission(t *testing.T) {
	store := &agentTokenStub{}
	handler := NewHandlerWithAgentTokens(slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		agentAdminSessions{}, nil, nil, nil, nil, nil, nil, store)
	path := "/api/v1/admin/nodes/" + certificateTestNodeID + "/agent-enrollment"
	request := func(session, csrf, password string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"password":"`+password+`"}`))
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		r.Header.Set("X-CSRF-Token", csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		return response
	}
	if response := request("member-token", "valid-csrf", "correct-password"); response.Code != http.StatusForbidden || store.calls != 0 {
		t.Fatalf("member minted token: %d, calls=%d", response.Code, store.calls)
	}
	if response := request("admin-token", "wrong", "correct-password"); response.Code != http.StatusForbidden || store.calls != 0 {
		t.Fatalf("missing CSRF minted token: %d, calls=%d", response.Code, store.calls)
	}
	if response := request("admin-token", "valid-csrf", "wrong-password"); response.Code != http.StatusUnauthorized || store.calls != 0 {
		t.Fatalf("old admin session minted without password: %d, calls=%d", response.Code, store.calls)
	}
	response := request("admin-token", "valid-csrf", "correct-password")
	if response.Code != http.StatusCreated || store.calls != 1 || store.nodeID != certificateTestNodeID || store.actorID != certificateTestNodeID {
		t.Fatalf("admin token = %d, calls=%d, node=%s, actor=%s", response.Code, store.calls, store.nodeID, store.actorID)
	}
	var body struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Token != "one-time-secret" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("token response = %+v, %v", body, err)
	}
}

func TestAdminAgentTokenRouteAbsentWithoutIdentity(t *testing.T) {
	handler := NewHandlerWithAgentTokens(slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		nil, nil, nil, nil, nil, nil, nil, &agentTokenStub{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/nodes/"+certificateTestNodeID+"/agent-enrollment", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("public HTTP token route = %d", response.Code)
	}
}

func TestAdminCanRevokeAgentWithCSRFAndPermission(t *testing.T) {
	store := &agentTokenStub{}
	handler := NewHandlerWithAgentTokens(slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		agentAdminSessions{}, nil, nil, nil, nil, nil, nil, store)
	path := "/api/v1/admin/nodes/" + certificateTestNodeID + "/agent"
	request := func(session, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{"status":"revoked"}`))
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		r.Header.Set("X-CSRF-Token", csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		return response
	}
	if response := request("member-token", "valid-csrf"); response.Code != http.StatusForbidden || store.revoked {
		t.Fatalf("member revoked Agent: %d, revoked=%v", response.Code, store.revoked)
	}
	if response := request("admin-token", ""); response.Code != http.StatusForbidden || store.revoked {
		t.Fatalf("missing CSRF revoked Agent: %d, revoked=%v", response.Code, store.revoked)
	}
	if response := request("admin-token", "valid-csrf"); response.Code != http.StatusNoContent || !store.revoked || store.nodeID != certificateTestNodeID || store.actorID != certificateTestNodeID {
		t.Fatalf("admin revoke = %d, revoked=%v, node=%s, actor=%s", response.Code, store.revoked, store.nodeID, store.actorID)
	}
}
