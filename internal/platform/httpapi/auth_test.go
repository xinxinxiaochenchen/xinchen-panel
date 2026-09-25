package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"controlplane/internal/identity"
)

type sessionStub struct {
	logoutCalled bool
	loginCalls   int
}

func (s *sessionStub) Login(_ context.Context, email, password string) (identity.LoginResult, error) {
	s.loginCalls++
	if email != "member@example.com" || password != "correct" {
		return identity.LoginResult{}, identity.ErrInvalidCredentials
	}
	return identity.LoginResult{User: identity.PublicUser{ID: "id-1", Email: email, Permissions: []string{"nodes.read"}},
		Token: strings.Repeat("a", 64), CSRFToken: strings.Repeat("b", 64)}, nil
}

func TestLoginRateLimitStopsPasswordChecks(t *testing.T) {
	stub := &sessionStub{}
	handler := NewHandlerWithIdentity(testLogger(), nil, stub)
	for attempt := 0; attempt < 6; attempt++ {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"member@example.com","password":"wrong"}`)))
		if attempt < 5 && recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d", attempt, recorder.Code)
		}
		if attempt == 5 && (recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") == "") {
			t.Fatalf("limited attempt = %d headers=%v", recorder.Code, recorder.Header())
		}
	}
	if stub.loginCalls != 5 {
		t.Fatalf("password checks = %d", stub.loginCalls)
	}
}

func (s *sessionStub) Authenticate(_ context.Context, token string) (identity.PublicUser, error) {
	if token != strings.Repeat("a", 64) {
		return identity.PublicUser{}, identity.ErrUnauthenticated
	}
	return identity.PublicUser{ID: "id-1", Email: "member@example.com", Permissions: []string{"nodes.read"}}, nil
}

func (s *sessionStub) Logout(_ context.Context, token, csrf string) error {
	if token != strings.Repeat("a", 64) {
		return identity.ErrUnauthenticated
	}
	if csrf != strings.Repeat("b", 64) {
		return identity.ErrInvalidCSRF
	}
	s.logoutCalled = true
	return nil
}

func TestLoginSetsSecureCookiesAndMeRequiresSession(t *testing.T) {
	stub := &sessionStub{}
	handler := NewHandlerWithIdentity(testLogger(), nil, stub)
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"member@example.com","password":"correct"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d %s", login.Code, login.Body.String())
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range login.Result().Cookies() {
		switch cookie.Name {
		case "__Host-control_session":
			sessionCookie = cookie
		case "__Host-control_csrf":
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteLaxMode || sessionCookie.Path != "/" {
		t.Fatalf("invalid session cookie: %+v", sessionCookie)
	}
	if csrfCookie == nil || csrfCookie.HttpOnly || !csrfCookie.Secure || csrfCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("invalid CSRF cookie: %+v", csrfCookie)
	}
	if login.Header().Get("Cache-Control") != "no-store" || strings.Contains(login.Body.String(), sessionCookie.Value) {
		t.Fatalf("session token leaked in login response: %s", login.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, request)
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing session = %d", missing.Code)
	}
	request.AddCookie(sessionCookie)
	me := httptest.NewRecorder()
	handler.ServeHTTP(me, request)
	if me.Code != http.StatusOK {
		t.Fatalf("me = %d %s", me.Code, me.Body.String())
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &body); err != nil || body.Email != "member@example.com" {
		t.Fatalf("me body = %s, err = %v", me.Body.String(), err)
	}
}

func TestLogoutRequiresCSRFAndClearsCookies(t *testing.T) {
	stub := &sessionStub{}
	handler := NewHandlerWithIdentity(testLogger(), nil, stub)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-control_session", Value: strings.Repeat("a", 64)})
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, request)
	if denied.Code != http.StatusForbidden || stub.logoutCalled {
		t.Fatalf("logout without CSRF = %d", denied.Code)
	}
	request.Header.Set("X-CSRF-Token", strings.Repeat("b", 64))
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, request)
	if accepted.Code != http.StatusNoContent || !stub.logoutCalled {
		t.Fatalf("logout with CSRF = %d", accepted.Code)
	}
	for _, cookie := range accepted.Result().Cookies() {
		if cookie.MaxAge != -1 {
			t.Fatalf("cookie not cleared: %+v", cookie)
		}
	}
}

func TestLoginRejectsBadCredentialsWithoutLeakingInternals(t *testing.T) {
	handler := NewHandlerWithIdentity(testLogger(), nil, &sessionStub{})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"member@example.com","password":"wrong"}`)))
	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), `"code":"INVALID_CREDENTIALS"`) || strings.Contains(recorder.Body.String(), "wrong") {
		t.Fatalf("invalid login = %d %s", recorder.Code, recorder.Body.String())
	}
}
