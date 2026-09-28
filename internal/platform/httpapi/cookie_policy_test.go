package httpapi

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"controlplane/internal/identity"
)

type browserSessions struct {
	catalogSessions
	logouts int
}

func (s *browserSessions) LoginWithRequestID(ctx context.Context, email, password, _ string) (identity.LoginResult, error) {
	token := "member-token"
	if email == "admin@example.com" {
		token = "admin-token"
	} else if email != "member@example.com" {
		return identity.LoginResult{}, identity.ErrInvalidCredentials
	}
	if password != "correct" {
		return identity.LoginResult{}, identity.ErrInvalidCredentials
	}
	user, err := s.Authenticate(ctx, token)
	return identity.LoginResult{User: user, Token: token, CSRFToken: "valid-csrf", ExpiresAt: time.Now().Add(time.Hour)}, err
}

func (s *browserSessions) Authenticate(ctx context.Context, token string) (identity.PublicUser, error) {
	user, err := s.catalogSessions.Authenticate(ctx, token)
	if token == "admin-token" {
		user.Permissions = append(user.Permissions, "users.read", "users.write")
	}
	return user, err
}

func (s *browserSessions) VerifyCSRF(ctx context.Context, token, csrf string) (identity.PublicUser, error) {
	user, err := s.Authenticate(ctx, token)
	if err != nil {
		return identity.PublicUser{}, err
	}
	if csrf != "valid-csrf" {
		return identity.PublicUser{}, identity.ErrInvalidCSRF
	}
	return user, nil
}

func (s *browserSessions) LogoutWithRequestID(ctx context.Context, token, csrf, _ string) error {
	if _, err := s.VerifyCSRF(ctx, token, csrf); err != nil {
		return err
	}
	s.logouts++
	return nil
}

func newBrowserCookieTestHandler(sessions IdentitySessions, stores RouteStores, secure bool) http.Handler {
	return NewHandlerWithStoresOptions(testLogger(), nil, sessions, stores, HandlerOptions{BrowserCookieSecure: secure})
}

type browserResponse struct {
	status  int
	cookies []*http.Cookie
	body    string
}

type testBrowser struct {
	client *http.Client
	url    *url.URL
}

func newHTTPTestBrowser(t *testing.T, handler http.Handler) testBrowser {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Use a non-loopback IP for Cookie decisions, while every connection stays
	// on the local test server. Browsers and Go treat localhost as trustworthy.
	serverURL.Host = net.JoinHostPort("203.0.113.10", serverURL.Port())
	transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return testBrowser{client: &http.Client{Jar: jar, Transport: transport, Timeout: 5 * time.Second}, url: serverURL}
}

func (browser testBrowser) request(t *testing.T, method, path, csrf, body string) browserResponse {
	t.Helper()
	request, err := http.NewRequest(method, browser.url.String()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", csrf)
	response, err := browser.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return browserResponse{status: response.StatusCode, cookies: response.Cookies(), body: string(responseBody)}
}

func (browser testBrowser) login(t *testing.T, email string) browserResponse {
	t.Helper()
	response := browser.request(t, http.MethodPost, "/api/v1/auth/login", "", `{"email":"`+email+`","password":"correct"}`)
	if response.status != http.StatusOK {
		t.Fatalf("login = %d %s", response.status, response.body)
	}
	return response
}

func assertBrowserCookieFlags(t *testing.T, cookies []*http.Cookie, secure, cleared bool) {
	t.Helper()
	sessionName, csrfName := "control_session", "control_csrf"
	if secure {
		sessionName, csrfName = "__Host-control_session", "__Host-control_csrf"
	}
	expectedCount := 2
	if !cleared {
		expectedCount = 4
	}
	if len(cookies) != expectedCount {
		t.Fatalf("cookies = %+v, want current session/CSRF and stale mode cleanup on login", cookies)
	}
	names := map[string]bool{sessionName: true, csrfName: false}
	for _, cookie := range cookies {
		httpOnly, expected := names[cookie.Name]
		if !expected && !cleared {
			other := newBrowserCookiePolicy(!secure)
			if (cookie.Name == other.sessionName || cookie.Name == other.csrfName) && cookie.Secure == !secure && cookie.HttpOnly == (cookie.Name == other.sessionName) && cookie.Path == "/" && cookie.Domain == "" && cookie.SameSite == http.SameSiteLaxMode && cookie.MaxAge == -1 && cookie.Value == "" {
				continue
			}
		}
		if !expected || cookie.Secure != secure || cookie.HttpOnly != httpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteLaxMode {
			t.Fatalf("invalid cookie policy: %+v, secure=%t", cookie, secure)
		}
		if cleared && (cookie.MaxAge != -1 || cookie.Value != "") {
			t.Fatalf("cookie not cleared: %+v", cookie)
		}
		delete(names, cookie.Name)
	}
	if len(names) != 0 {
		t.Fatalf("missing current mode cookies: %+v", names)
	}
}

func TestHTTPBrowserCookieJarLoginRoundTrip(t *testing.T) {
	browser := newHTTPTestBrowser(t, newBrowserCookieTestHandler(&browserSessions{}, RouteStores{
		Catalog: &catalogStub{}, Accounts: &accountStub{},
	}, false))
	login := browser.login(t, "admin@example.com")
	for _, path := range []string{"/api/v1/me", "/api/v1/me/permissions", "/api/v1/admin/resource-groups", "/api/v1/admin/users"} {
		response := browser.request(t, http.MethodGet, path, "", "")
		if response.status != http.StatusOK {
			t.Fatalf("HTTP cookie-jar request %s = %d %s", path, response.status, response.body)
		}
	}
	assertBrowserCookieFlags(t, login.cookies, false, false)
	if strings.Contains(login.body, "admin-token") {
		t.Fatal("session token leaked in login response")
	}
}

func TestHTTPSBrowserCookieJarLoginRoundTrip(t *testing.T) {
	server := httptest.NewTLSServer(NewHandlerWithIdentity(testLogger(), nil, &browserSessions{}))
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar, client.Timeout = jar, 5*time.Second
	browser := testBrowser{client: client, url: serverURL}
	login := browser.login(t, "member@example.com")
	assertBrowserCookieFlags(t, login.cookies, true, false)
	if response := browser.request(t, http.MethodGet, "/api/v1/me", "", ""); response.status != http.StatusOK {
		t.Fatalf("HTTPS cookie-jar me = %d %s", response.status, response.body)
	}
	logout := browser.request(t, http.MethodPost, "/api/v1/auth/logout", "valid-csrf", "")
	if logout.status != http.StatusNoContent {
		t.Fatalf("HTTPS logout = %d %s", logout.status, logout.body)
	}
	assertBrowserCookieFlags(t, logout.cookies, true, true)
	if cookies := client.Jar.Cookies(serverURL); len(cookies) != 0 {
		t.Fatalf("HTTPS browser kept logged-out cookies: %+v", cookies)
	}
}

func TestHTTPSAccessRemovesStaleCookiesFromOtherConfiguredMode(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, login := range []bool{false, true} {
			name := "http-cookies"
			if secure {
				name = "secure-cookies"
			}
			if login {
				name += "/login"
			} else {
				name += "/resume"
			}
			t.Run(name, func(t *testing.T) {
				server := httptest.NewTLSServer(newBrowserCookieTestHandler(&browserSessions{}, RouteStores{}, secure))
				t.Cleanup(server.Close)
				serverURL, err := url.Parse(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				jar, err := cookiejar.New(nil)
				if err != nil {
					t.Fatal(err)
				}
				jar.SetCookies(serverURL, newBrowserCookiePolicy(!secure).cookies("previous-session", "previous-csrf"))
				client := server.Client()
				client.Jar = jar
				browser := testBrowser{client: client, url: serverURL}
				if login {
					browser.login(t, "member@example.com")
				} else {
					jar.SetCookies(serverURL, newBrowserCookiePolicy(secure).cookies("member-token", "valid-csrf"))
					if response := browser.request(t, http.MethodGet, "/api/v1/me", "", ""); response.status != http.StatusOK {
						t.Fatalf("resume = %d %s", response.status, response.body)
					}
				}
				cookies := jar.Cookies(serverURL)
				if len(cookies) != 2 {
					t.Fatalf("browser kept conflicting session cookies: %+v", cookies)
				}
				csrf := ""
				for _, cookie := range cookies {
					if cookie.Value == "previous-session" || cookie.Value == "previous-csrf" {
						t.Fatalf("stale cookie survived: %+v", cookie)
					}
					if cookie.Name == newBrowserCookiePolicy(secure).csrfName {
						csrf = cookie.Value
					}
				}
				if response := browser.request(t, http.MethodPost, "/api/v1/auth/logout", csrf, ""); response.status != http.StatusNoContent {
					t.Fatalf("logout after mode switch = %d %s", response.status, response.body)
				}
			})
		}
	}
}

func TestHTTPSPasswordChangePreservesCookieSecurity(t *testing.T) {
	store := &accountStub{}
	handler := NewHandlerWithAccounts(testLogger(), nil, &browserSessions{}, nil, nil, store)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/me/password", strings.NewReader(`{"old_password":"old-password-123","new_password":"new-password-123"}`))
	request.AddCookie(&http.Cookie{Name: "__Host-control_session", Value: "member-token"})
	request.Header.Set("X-CSRF-Token", "valid-csrf")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || store.changes != 1 {
		t.Fatalf("HTTPS password change = %d %s", response.Code, response.Body.String())
	}
	assertBrowserCookieFlags(t, response.Result().Cookies(), true, true)
}

func TestHTTPBrowserMutationsRequireCSRFAndPermission(t *testing.T) {
	store := &catalogStub{}
	browser := newHTTPTestBrowser(t, newBrowserCookieTestHandler(&browserSessions{}, RouteStores{Catalog: store}, false))
	browser.login(t, "admin@example.com")
	const body = `{"code":"RFC.JPT1","name":"Tokyo","region":"JP"}`
	for _, csrf := range []string{"", "wrong"} {
		response := browser.request(t, http.MethodPost, "/api/v1/admin/resource-groups", csrf, body)
		if response.status != http.StatusForbidden || store.createdBy != "" {
			t.Fatalf("write without valid CSRF = %d %s", response.status, response.body)
		}
	}
	accepted := browser.request(t, http.MethodPost, "/api/v1/admin/resource-groups", "valid-csrf", body)
	if accepted.status != http.StatusCreated || store.createdBy != "admin-id" {
		t.Fatalf("admin write = %d %s actor=%q", accepted.status, accepted.body, store.createdBy)
	}
	browser.login(t, "member@example.com")
	denied := browser.request(t, http.MethodPost, "/api/v1/admin/resource-groups", "valid-csrf", body)
	if denied.status != http.StatusForbidden || store.createdBy != "admin-id" {
		t.Fatalf("member write = %d %s actor=%q", denied.status, denied.body, store.createdBy)
	}
}

func TestHTTPBrowserLogoutClearsConfiguredCookies(t *testing.T) {
	sessions := &browserSessions{}
	browser := newHTTPTestBrowser(t, newBrowserCookieTestHandler(sessions, RouteStores{}, false))
	browser.login(t, "member@example.com")
	denied := browser.request(t, http.MethodPost, "/api/v1/auth/logout", "", "")
	if denied.status != http.StatusForbidden || sessions.logouts != 0 {
		t.Fatalf("logout without CSRF = %d %s", denied.status, denied.body)
	}
	accepted := browser.request(t, http.MethodPost, "/api/v1/auth/logout", "valid-csrf", "")
	if accepted.status != http.StatusNoContent || sessions.logouts != 1 {
		t.Fatalf("logout = %d %s, calls=%d", accepted.status, accepted.body, sessions.logouts)
	}
	assertBrowserCookieFlags(t, accepted.cookies, false, true)
	if cookies := browser.client.Jar.Cookies(browser.url); len(cookies) != 0 {
		t.Fatalf("browser kept logged-out cookies: %+v", cookies)
	}
	if response := browser.request(t, http.MethodGet, "/api/v1/me", "", ""); response.status != http.StatusUnauthorized {
		t.Fatalf("me after logout = %d %s", response.status, response.body)
	}
}

func TestHTTPBrowserPasswordChangeClearsConfiguredCookies(t *testing.T) {
	store := &accountStub{}
	browser := newHTTPTestBrowser(t, newBrowserCookieTestHandler(&browserSessions{}, RouteStores{Accounts: store}, false))
	browser.login(t, "member@example.com")
	const body = `{"old_password":"old-password-123","new_password":"new-password-123"}`
	denied := browser.request(t, http.MethodPost, "/api/v1/me/password", "", body)
	if denied.status != http.StatusForbidden || store.changes != 0 {
		t.Fatalf("password change without CSRF = %d %s", denied.status, denied.body)
	}
	accepted := browser.request(t, http.MethodPost, "/api/v1/me/password", "valid-csrf", body)
	if accepted.status != http.StatusNoContent || store.changedFor != "member-id" || store.changes != 1 {
		t.Fatalf("password change = %d %s, store=%+v", accepted.status, accepted.body, store)
	}
	assertBrowserCookieFlags(t, accepted.cookies, false, true)
	if cookies := browser.client.Jar.Cookies(browser.url); len(cookies) != 0 {
		t.Fatalf("browser kept revoked cookies: %+v", cookies)
	}
	if response := browser.request(t, http.MethodGet, "/api/v1/me", "", ""); response.status != http.StatusUnauthorized {
		t.Fatalf("me after password change = %d %s", response.status, response.body)
	}
}

func TestBrowserCookieSecurityIgnoresTLSAndForwardedProto(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, useTLS := range []bool{false, true} {
			for _, forwardedProto := range []string{"http", "https"} {
				request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"member@example.com","password":"correct"}`))
				if useTLS {
					request.TLS = &tls.ConnectionState{}
				}
				request.Header.Set("X-Forwarded-Proto", forwardedProto)
				response := httptest.NewRecorder()
				newBrowserCookieTestHandler(&browserSessions{}, RouteStores{}, secure).ServeHTTP(response, request)
				if response.Code != http.StatusOK {
					t.Fatalf("login secure=%t TLS=%t forwarded=%s: %d", secure, useTLS, forwardedProto, response.Code)
				}
				assertBrowserCookieFlags(t, response.Result().Cookies(), secure, false)
			}
		}
	}
}

func TestBrowserCookiePoliciesAreIndependent(t *testing.T) {
	secureHandler := NewHandlerWithIdentity(testLogger(), nil, &browserSessions{})
	httpHandler := newBrowserCookieTestHandler(&browserSessions{}, RouteStores{}, false)
	for _, secure := range []bool{true, false, true} {
		handler := httpHandler
		expectedName, otherName := "control_session", "__Host-control_session"
		if secure {
			handler = secureHandler
			expectedName, otherName = otherName, expectedName
		}
		for _, cookieName := range []string{expectedName, otherName} {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
			request.AddCookie(&http.Cookie{Name: cookieName, Value: "member-token"})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			expectedStatus := http.StatusUnauthorized
			if cookieName == expectedName {
				expectedStatus = http.StatusOK
			}
			if response.Code != expectedStatus {
				t.Fatalf("secure=%t cookie=%s status=%d, want %d", secure, cookieName, response.Code, expectedStatus)
			}
		}
	}
}

func TestHTTPAgentReauthenticationUsesConfiguredCookie(t *testing.T) {
	store := &agentTokenStub{}
	handler := newBrowserCookieTestHandler(agentAdminSessions{}, RouteStores{AgentTokens: store}, false)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/nodes/"+certificateTestNodeID+"/agent-enrollment", strings.NewReader(`{"password":"correct-password"}`))
	request.AddCookie(&http.Cookie{Name: "control_session", Value: "admin-token"})
	request.Header.Set("X-CSRF-Token", "valid-csrf")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || store.calls != 1 || store.actorID != certificateTestNodeID {
		t.Fatalf("HTTP Agent token = %d %s, store=%+v", response.Code, response.Body.String(), store)
	}
}

func TestBrowserMutationsRejectCrossOriginRequests(t *testing.T) {
	for _, route := range []struct {
		path, body string
	}{
		{"/api/v1/auth/login", `{"email":"member@example.com","password":"correct"}`},
		{"/api/v1/admin/resource-groups", `{"code":"RFC.JPT1","name":"Tokyo","region":"JP"}`},
		{"/api/v1/auth/logout", ""},
		{"/api/v1/me/password", `{"old_password":"old-password-123","new_password":"new-password-123"}`},
	} {
		t.Run(route.path, func(t *testing.T) {
			for _, provenance := range []struct{ origin, fetchSite string }{
				{"https://other.example.test", ""},
				{"", "cross-site"},
				{"null", ""},
			} {
				sessions, catalog, accounts := &browserSessions{}, &catalogStub{}, &accountStub{}
				handler := NewHandlerWithStores(testLogger(), nil, sessions, RouteStores{Catalog: catalog, Accounts: accounts})
				request := httptest.NewRequest(http.MethodPost, "http://panel.example.test"+route.path, strings.NewReader(route.body))
				request.Header.Set("Origin", provenance.origin)
				request.Header.Set("Sec-Fetch-Site", provenance.fetchSite)
				request.Header.Set("X-CSRF-Token", "valid-csrf")
				request.AddCookie(&http.Cookie{Name: "__Host-control_session", Value: "admin-token"})
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusForbidden || len(response.Result().Cookies()) != 0 || sessions.logouts != 0 || catalog.createdBy != "" || accounts.changes != 0 {
					t.Fatalf("cross-origin mutation origin=%q fetch-site=%q = %d %s", provenance.origin, provenance.fetchSite, response.Code, response.Body.String())
				}
			}
		})
	}
}

func TestBrowserMutationsAllowSameOriginAndProgrammaticClients(t *testing.T) {
	for _, origin := range []string{"", "http://panel.example.test", "https://panel.example.test"} {
		request := httptest.NewRequest(http.MethodPost, "http://panel.example.test/api/v1/auth/login", strings.NewReader(`{"email":"member@example.com","password":"correct"}`))
		request.Header.Set("Origin", origin)
		request.Header.Set("X-Forwarded-Proto", "http")
		response := httptest.NewRecorder()
		NewHandlerWithIdentity(testLogger(), nil, &browserSessions{}).ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("same-origin login origin=%q = %d %s", origin, response.Code, response.Body.String())
		}
		assertBrowserCookieFlags(t, response.Result().Cookies(), true, false)
	}
}
