package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controlplane/internal/billing"
	"controlplane/internal/identity"
)

type usageSessions struct{ catalogSessions }

func (usageSessions) Authenticate(context.Context, string) (identity.PublicUser, error) {
	return identity.PublicUser{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Permissions: []string{"dashboard.read", "usage.read"}}, nil
}

type usageStoreStub struct {
	userID      string
	value       billing.Overview
	dailyUserID string
	dailyFrom   time.Time
	dailyUntil  time.Time
	adminQuery  billing.AdminUsageQuery
	adminValues []billing.UsageBucket
}

func (s *usageStoreStub) GetCurrentOverview(_ context.Context, userID string) (billing.Overview, error) {
	s.userID = userID
	return s.value, nil
}

func (s *usageStoreStub) ListDailyUsage(_ context.Context, userID string, from, until time.Time) ([]billing.DailyUsage, error) {
	s.dailyUserID, s.dailyFrom, s.dailyUntil = userID, from, until
	return []billing.DailyUsage{{Date: "2026-09-25", UploadedBytes: 4, DownloadedBytes: 6, ChargedBytes: 10}}, nil
}

func (s *usageStoreStub) ListAdminUsage(_ context.Context, query billing.AdminUsageQuery) ([]billing.UsageBucket, error) {
	s.adminQuery = query
	if s.adminValues != nil {
		return s.adminValues, nil
	}
	return []billing.UsageBucket{{Date: "2026-09-25", DimensionID: query.UserID, ChargedBytes: 10}}, nil
}

type adminUsageSessions struct{ usageSessions }

func (adminUsageSessions) Authenticate(context.Context, string) (identity.PublicUser, error) {
	return identity.PublicUser{ID: "ffffffff-ffff-4fff-8fff-ffffffffffff", Permissions: []string{"usage.admin"}}, nil
}

func TestMyUsageCurrentScopesToAuthenticatedUser(t *testing.T) {
	store := &usageStoreStub{value: billing.Overview{PeriodID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		QuotaBytes: 1000, ChargedBytes: 250, RemainingBytes: 750, AvailableBytes: 700,
		StartsAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}}
	handler := NewHandlerWithStores(testLogger(), nil, usageSessions{}, RouteStores{Usage: store})
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/my/usage/current", nil))
	if unauthenticated.Code != http.StatusUnauthorized || store.userID != "" {
		t.Fatalf("unauthenticated read = %d user=%q", unauthenticated.Code, store.userID)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/my/usage/current?user_id=someone-else", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || store.userID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("current usage = %d user=%q body=%s", response.Code, store.userID, response.Body.String())
	}
	var body struct {
		PeriodID       string `json:"period_id"`
		ChargedBytes   int64  `json:"charged_bytes"`
		AvailableBytes int64  `json:"available_bytes"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.PeriodID != store.value.PeriodID || body.ChargedBytes != 250 || body.AvailableBytes != 700 {
		t.Fatalf("usage body = %+v", body)
	}
}

func TestMyDailyUsageValidatesRangeAndScopesOwner(t *testing.T) {
	store := &usageStoreStub{}
	handler := NewHandlerWithStores(testLogger(), nil, usageSessions{}, RouteStores{Usage: store})
	for _, query := range []string{"", "?from=2026-09-25&to=2026-09-24", "?from=2026-01-01&to=2026-09-25", "?from=bad&to=2026-09-25"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/my/usage/daily"+query, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || store.dailyUserID != "" {
			t.Fatalf("invalid range %q = %d user=%q", query, response.Code, store.dailyUserID)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/my/usage/daily?from=2026-09-25&to=2026-09-26&user_id=other", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || store.dailyUserID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" ||
		!store.dailyFrom.Equal(time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)) ||
		!store.dailyUntil.Equal(time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily read = %d user=%q from=%s until=%s", response.Code, store.dailyUserID, store.dailyFrom, store.dailyUntil)
	}
}

func TestAdminUsageRequiresPermissionAndValidFilters(t *testing.T) {
	store := &usageStoreStub{}
	url := "/api/v1/admin/usage/daily?from=2026-09-25&to=2026-09-26&group_by=user&user_id=aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa&limit=20"
	userHandler := NewHandlerWithStores(testLogger(), nil, usageSessions{}, RouteStores{Usage: store})
	request := httptest.NewRequest(http.MethodGet, url, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session"})
	response := httptest.NewRecorder()
	userHandler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("ordinary user admin usage = %d", response.Code)
	}
	adminHandler := NewHandlerWithStores(testLogger(), nil, adminUsageSessions{}, RouteStores{Usage: store})
	request = httptest.NewRequest(http.MethodGet, url, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session"})
	response = httptest.NewRecorder()
	adminHandler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || store.adminQuery.UserID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" ||
		store.adminQuery.GroupBy != "user" || store.adminQuery.Limit != 21 {
		t.Fatalf("admin usage = %d query=%+v body=%s", response.Code, store.adminQuery, response.Body.String())
	}
	bad := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/daily?from=2026-09-25&to=2026-09-26&group_by=unknown", nil)
	bad.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session"})
	response = httptest.NewRecorder()
	adminHandler.ServeHTTP(response, bad)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid grouping = %d", response.Code)
	}
}

func TestAdminUsageCursorPaginatesByDateAndDimension(t *testing.T) {
	store := &usageStoreStub{adminValues: []billing.UsageBucket{
		{Date: "2026-09-25", DimensionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		{Date: "2026-09-26", DimensionID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"},
	}}
	handler := NewHandlerWithStores(testLogger(), nil, adminUsageSessions{}, RouteStores{Usage: store})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/daily?from=2026-09-25&to=2026-09-26&group_by=user&limit=1", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var first struct {
		Items      []billing.UsageBucket `json:"items"`
		NextCursor *string               `json:"next_cursor"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first page = %d %+v", response.Code, first)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/daily?from=2026-09-25&to=2026-09-26&group_by=user&limit=1&cursor="+*first.NextCursor, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || store.adminQuery.AfterDate != "2026-09-25" || store.adminQuery.AfterID != first.Items[0].DimensionID {
		t.Fatalf("next page = %d query=%+v", response.Code, store.adminQuery)
	}
}
