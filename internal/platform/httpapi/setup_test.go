package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"controlplane/internal/identity"
)

type setupStub struct {
	complete    bool
	calls       int
	statusCalls int
	err         error
}

func (s *setupStub) Required(context.Context) (bool, error) {
	s.statusCalls++
	return !s.complete, s.err
}
func (s *setupStub) Create(_ context.Context, email, password, requestID string) (identity.PublicUser, bool, error) {
	s.calls++
	if requestID == "" {
		return identity.PublicUser{}, false, errors.New("missing request ID")
	}
	if s.complete {
		return identity.PublicUser{}, false, nil
	}
	if s.err != nil {
		return identity.PublicUser{}, false, s.err
	}
	s.complete = true
	return identity.PublicUser{ID: "initial-admin", Email: email, Roles: []string{"admin"}}, true, nil
}

func setupHandler(store *setupStub, token string) http.Handler {
	return NewHandlerWithStoresOptions(testLogger(), nil, &sessionStub{}, RouteStores{Setup: store}, HandlerOptions{SetupToken: token})
}

func TestSetupStatusNeverDisclosesToken(t *testing.T) {
	for _, complete := range []bool{false, true} {
		r := httptest.NewRecorder()
		setupHandler(&setupStub{complete: complete}, "private-token").ServeHTTP(r, httptest.NewRequest("GET", "/api/v1/setup", nil))
		want := `"required":true`
		if complete {
			want = `"required":false`
		}
		if r.Code != 200 || !strings.Contains(r.Body.String(), want) || strings.Contains(r.Body.String(), "private-token") || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status = %d %s headers=%v", r.Code, r.Body.String(), r.Header())
		}
	}
}

func TestSetupRejectsUnauthorizedAndMalformedRequestsBeforeCreation(t *testing.T) {
	for _, tc := range []struct {
		body, token string
		status      int
	}{
		{`{"token":"wrong","email":"owner@example.test","password":"long-password"}`, "private-token", 403},
		{`{"token":"private-token","email":"owner@example.test","password":"long-password"}`, "", 503},
		{`{"token":"private-token","email":"bad","password":"short"}`, "private-token", 400},
		{`{"token":"private-token","email":"` + strings.Repeat("a", 255) + `@example.test","password":"long-password"}`, "private-token", 400},
		{`{"token":"private-token","email":"owner@example.test","password":"long-password","extra":true}`, "private-token", 400},
		{`{"token":"private-token","email":"owner@example.test","password":"long-password"}{}`, "private-token", 400},
		{`{"token":"private-token","email":"owner@example.test","password":"` + strings.Repeat("密", 25) + `"}`, "private-token", 400},
	} {
		store := &setupStub{}
		r := httptest.NewRecorder()
		setupHandler(store, tc.token).ServeHTTP(r, httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(tc.body)))
		if r.Code != tc.status || store.calls != 0 {
			t.Fatalf("status=%d calls=%d body=%s", r.Code, store.calls, r.Body.String())
		}
	}
}

func TestSetupCreatesAdministratorOnceAndKeepsSecretsOutOfResponse(t *testing.T) {
	store := &setupStub{}
	h := setupHandler(store, "private-token")
	body := `{"token":"private-token","email":"owner@example.test","password":"long-password"}`
	for _, want := range []int{201, 409} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(body)))
		if r.Code != want || strings.Contains(r.Body.String(), "private-token") || strings.Contains(r.Body.String(), "long-password") {
			t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
		}
	}
	if store.calls != 1 {
		t.Fatalf("creation attempts=%d", store.calls)
	}
}

func TestSetupDisabledInPreviewAndRejectsCrossOrigin(t *testing.T) {
	r := httptest.NewRecorder()
	NewHandler(testLogger(), nil).ServeHTTP(r, httptest.NewRequest("GET", "/api/v1/setup", nil))
	if r.Code != 404 {
		t.Fatalf("preview status=%d", r.Code)
	}
	store := &setupStub{}
	r = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "http://panel.example.test/api/v1/setup", strings.NewReader(`{"token":"private-token","email":"owner@example.test","password":"long-password"}`))
	req.Header.Set("Origin", "http://evil.example.test")
	setupHandler(store, "private-token").ServeHTTP(r, req)
	if r.Code != 403 || store.calls != 0 {
		t.Fatalf("cross-origin status=%d calls=%d", r.Code, store.calls)
	}
}

func TestSetupFailureDoesNotMarkComplete(t *testing.T) {
	store := &setupStub{err: errors.New("database failed")}
	r := httptest.NewRecorder()
	setupHandler(store, "private-token").ServeHTTP(r, httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(`{"token":"private-token","email":"owner@example.test","password":"long-password"}`)))
	if r.Code != 500 || store.complete {
		t.Fatalf("status=%d complete=%t", r.Code, store.complete)
	}
}

func TestSetupRateLimitsWrongCredentialsBeforeDatabaseWork(t *testing.T) {
	store := &setupStub{}
	h := setupHandler(store, "private-token")
	for i := 0; i < 6; i++ {
		r := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(`{"token":"wrong","email":"owner@example.test","password":"long-password"}`))
		req.RemoteAddr = "192.0.2.1:" + strconv.Itoa(10000+i)
		h.ServeHTTP(r, req)
		want := 403
		if i == 5 {
			want = 429
		}
		if r.Code != want {
			t.Fatalf("attempt %d status=%d expected=%d", i, r.Code, want)
		}
	}
	if store.statusCalls != 5 || store.calls != 0 {
		t.Fatalf("status checks=%d creations=%d", store.statusCalls, store.calls)
	}
}
