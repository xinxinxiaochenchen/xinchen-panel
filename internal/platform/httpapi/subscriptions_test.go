package httpapi

import (
	"bytes"
	"context"
	"controlplane/internal/identity"
	"controlplane/internal/subscription"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

type subscriptionSessions struct{ catalogSessions }

func (subscriptionSessions) Authenticate(_ context.Context, token string) (identity.PublicUser, error) {
	if token == "member-token" {
		return identity.PublicUser{ID: "member-id", Permissions: []string{"subscriptions.read", "subscriptions.write"}}, nil
	}
	if token == "read-token" {
		return identity.PublicUser{ID: "reader-id", Permissions: []string{"subscriptions.read"}}, nil
	}
	return identity.PublicUser{}, identity.ErrUnauthenticated
}
func (s subscriptionSessions) VerifyCSRF(ctx context.Context, token, csrf string) (identity.PublicUser, error) {
	if csrf != "valid-csrf" {
		return identity.PublicUser{}, identity.ErrInvalidCSRF
	}
	return s.Authenticate(ctx, token)
}

type subscriptionStub struct {
	allowAll        bool
	owner, exported string
	exportErr       error
}

func (s *subscriptionStub) Create(_ context.Context, owner string, in subscription.Input, _ string) (subscription.Subscription, string, error) {
	s.owner = owner
	return subscription.Subscription{ID: testProxyID, Name: in.Name}, strings.Repeat("A", 43), nil
}
func (s *subscriptionStub) ListOwn(context.Context, string, int, string) ([]subscription.Subscription, error) {
	return []subscription.Subscription{{ID: testProxyID}}, nil
}
func (s *subscriptionStub) GetOwn(context.Context, string, string) (subscription.Subscription, error) {
	return subscription.Subscription{ID: testProxyID}, nil
}
func (s *subscriptionStub) RevealOwn(_ context.Context, owner, _ string) (string, error) {
	s.owner = owner
	return strings.Repeat("A", 43), nil
}
func (s *subscriptionStub) RotateOwn(_ context.Context, owner, _, _ string) (string, error) {
	s.owner = owner
	return strings.Repeat("B", 43), nil
}
func (s *subscriptionStub) UpdateOwn(context.Context, string, string, subscription.Patch, string) (subscription.Subscription, error) {
	return subscription.Subscription{ID: testProxyID}, nil
}
func (s *subscriptionStub) DeleteOwn(context.Context, string, string, string) error { return nil }
func (s *subscriptionStub) ResolveToken(_ context.Context, token string) (subscription.Subscription, error) {
	if s.allowAll || token == strings.Repeat("A", 43) {
		return subscription.Subscription{ID: testProxyID}, nil
	}
	return subscription.Subscription{}, subscription.ErrNotFound
}
func (s *subscriptionStub) ExportOwn(_ context.Context, owner, id, format string) ([]byte, string, error) {
	s.owner = owner
	s.exported = "owner-preview"
	return []byte("test profile"), "application/yaml", nil
}
func (s *subscriptionStub) Export(_ context.Context, token, format string) ([]byte, string, error) {
	s.exported = token
	return []byte("test profile"), "application/yaml", s.exportErr
}
func TestSubscriptionManagementAuthAndCSRF(t *testing.T) {
	store := &subscriptionStub{}
	h := NewHandlerWithStores(testLogger(), nil, subscriptionSessions{}, RouteStores{Subscriptions: store})
	for _, tc := range []struct {
		token, csrf string
		status      int
	}{{"", "", 401}, {"read-token", "valid-csrf", 403}, {"member-token", "", 403}, {"member-token", "valid-csrf", 201}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, catalogRequest("POST", "/api/v1/subscriptions", tc.token, tc.csrf, `{"name":"Laptop","proxy_access_ids":["`+testProxyID+`"]}`))
		if w.Code != tc.status {
			t.Fatalf("create=%d %s", w.Code, w.Body.String())
		}
	}
	if store.owner != "member-id" {
		t.Fatal("wrong authenticated owner")
	}
	w := httptest.NewRecorder()
	r := catalogRequest("GET", "/api/v1/subscriptions/"+testProxyID+"/url?format=mihomo", "member-token", "", "")
	r.Host = "attacker.invalid"
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "/sub/") || strings.Contains(w.Body.String(), "attacker.invalid") {
		t.Fatalf("URL response=%d %s", w.Code, w.Body.String())
	}
}
func TestPublicSubscriptionNoCookieRedactsLogsAndHonorsDisabledPreview(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	store := &subscriptionStub{}
	h := NewHandlerWithStores(logger, nil, subscriptionSessions{}, RouteStores{Subscriptions: store})
	token := strings.Repeat("A", 43)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/sub/"+token+"/mihomo", nil))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" || store.exported != token {
		t.Fatalf("public export=%d %s", w.Code, w.Body.String())
	}
	if strings.Contains(logs.String(), token) {
		t.Fatal("access log exposed bearer token")
	}
	store.exportErr = subscription.ErrUnavailable
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/sub/"+token+"/sing-box", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	h = NewHandlerWithStores(logger, nil, nil, RouteStores{Subscriptions: store})
	h = NewWebHandler(h, fstest.MapFS{"index.html": {Data: []byte("preview")}})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sub/"+token+"/mihomo", nil))
	if w.Code != 404 {
		t.Fatalf("preview exposes subscriptions: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(logs.String(), token) {
		t.Fatal("disabled route log exposed bearer token")
	}
}

func TestSubscriptionLogsRedactMalformedPaths(t *testing.T) {
	token := strings.Repeat("A", 43)
	for _, path := range []string{"/sub/" + token + "/bad", "//sub/" + token + "/mihomo", "/x/../sub/" + token + "/mihomo"} {
		var logs bytes.Buffer
		h := NewHandlerWithStores(slog.New(slog.NewJSONHandler(&logs, nil)), nil, nil, RouteStores{})
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
		if strings.Contains(logs.String(), token) {
			t.Fatalf("token logged for malformed subscription path %q", path)
		}
	}
}

func TestSubscriptionMalformedPathsDoNotRedirectOrCacheTokens(t *testing.T) {
	token := strings.Repeat("A", 43)
	h := NewHandlerWithStores(testLogger(), nil, nil, RouteStores{})
	h = NewWebHandler(h, fstest.MapFS{"index.html": {Data: []byte("preview")}})
	for _, path := range []string{"/sub/" + token + "//mihomo", "/x/../sub/" + token + "/mihomo"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 || w.Header().Get("Location") != "" || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), token) {
			t.Fatalf("secret redirect/cache boundary=%d %v %s", w.Code, w.Header(), w.Body.String())
		}
	}
}
func TestSubscriptionLimiterSeparatesTokensBehindProxy(t *testing.T) {
	h := NewHandlerWithStores(testLogger(), nil, subscriptionSessions{}, RouteStores{Subscriptions: &subscriptionStub{allowAll: true}})
	for i := 0; i < 6; i++ {
		token, err := subscription.GenerateToken()
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/sub/"+token+"/mihomo", nil)
		r.RemoteAddr = "172.18.0.1:43000"
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("shared proxy rejected token %d: %d", i, w.Code)
		}
	}
}

func TestUnknownTokenFloodDoesNotExhaustValidTokenLimiter(t *testing.T) {
	h := NewHandlerWithStores(testLogger(), nil, subscriptionSessions{}, RouteStores{Subscriptions: &subscriptionStub{}})
	for i := 0; i < 60; i++ {
		token, err := subscription.GenerateToken()
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/sub/"+token+"/mihomo", nil))
		if w.Code != 404 {
			t.Fatalf("unknown token request=%d", w.Code)
		}
	}
	token := strings.Repeat("A", 43)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/sub/"+token+"/mihomo", nil))
	if w.Code != 200 {
		t.Fatalf("known token blocked by unknown flood: %d", w.Code)
	}
}

func TestSubscriptionPreviewDoesNotDependOnRotatingToken(t *testing.T) {
	store := &subscriptionStub{exportErr: subscription.ErrNotFound}
	h := NewHandlerWithStores(testLogger(), nil, subscriptionSessions{}, RouteStores{Subscriptions: store})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, catalogRequest("GET", "/api/v1/subscriptions/"+testProxyID+"/preview?format=mihomo", "member-token", "", ""))
	if w.Code != 200 || store.owner != "member-id" || store.exported != "owner-preview" {
		t.Fatalf("owner preview=%d %s owner=%s source=%s", w.Code, w.Body.String(), store.owner, store.exported)
	}
}
