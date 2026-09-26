package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"controlplane/internal/billing"
	"controlplane/internal/catalog"
)

type UsageStore interface {
	GetCurrentOverview(context.Context, string) (billing.Overview, error)
	ListDailyUsage(context.Context, string, time.Time, time.Time) ([]billing.DailyUsage, error)
	ListAdminUsage(context.Context, billing.AdminUsageQuery) ([]billing.UsageBucket, error)
}

func registerUsageRoutes(mux *http.ServeMux, sessions IdentitySessions, store UsageStore) {
	mux.HandleFunc("/api/v1/my/usage/current", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		user, ok := catalogPrincipal(w, r, sessions, "dashboard.read", false)
		if !ok {
			return
		}
		overview, err := store.GetCurrentOverview(r.Context(), user.ID)
		if errors.Is(err, billing.ErrNotFound) {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "current billing period not found")
			return
		}
		if err != nil {
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
			return
		}
		writeCatalogJSON(w, http.StatusOK, overview)
	})
	mux.HandleFunc("/api/v1/my/usage/daily", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		user, ok := catalogPrincipal(w, r, sessions, "usage.read", false)
		if !ok {
			return
		}
		from, until, ok := dailyRange(r)
		if !ok {
			WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid UTC date range")
			return
		}
		items, err := store.ListDailyUsage(r.Context(), user.ID, from, until)
		if err != nil {
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
			return
		}
		writeCatalogJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	mux.HandleFunc("/api/v1/admin/usage/daily", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		if _, ok := catalogPrincipal(w, r, sessions, "usage.admin", false); !ok {
			return
		}
		query, limit, ok := adminUsageParams(r)
		if !ok {
			WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid usage filters")
			return
		}
		items, err := store.ListAdminUsage(r.Context(), query)
		if err != nil {
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
			return
		}
		var next *string
		if len(items) > limit {
			items = items[:limit]
			value := base64.RawURLEncoding.EncodeToString([]byte(items[len(items)-1].Date + "|" + items[len(items)-1].DimensionID))
			next = &value
		}
		writeCatalogJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
	})
}

func adminUsageParams(r *http.Request) (billing.AdminUsageQuery, int, bool) {
	from, until, ok := dailyRange(r)
	if !ok {
		return billing.AdminUsageQuery{}, 0, false
	}
	values := r.URL.Query()
	query := billing.AdminUsageQuery{From: from, Until: until, GroupBy: values.Get("group_by"),
		UserID: values.Get("user_id"), NodeID: values.Get("node_id"), LineID: values.Get("line_id")}
	if query.GroupBy != "date" && query.GroupBy != "user" && query.GroupBy != "node" && query.GroupBy != "line" {
		return billing.AdminUsageQuery{}, 0, false
	}
	for _, key := range []string{"group_by", "user_id", "node_id", "line_id", "limit", "cursor"} {
		if len(values[key]) > 1 {
			return billing.AdminUsageQuery{}, 0, false
		}
	}
	for _, value := range []string{query.UserID, query.NodeID, query.LineID} {
		if value != "" && !catalog.ValidID(value) {
			return billing.AdminUsageQuery{}, 0, false
		}
	}
	limit := 100
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			return billing.AdminUsageQuery{}, 0, false
		}
		limit = parsed
	}
	if cursor := values.Get("cursor"); cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(decoded) > 60 {
			return billing.AdminUsageQuery{}, 0, false
		}
		parts := strings.Split(string(decoded), "|")
		if len(parts) != 2 {
			return billing.AdminUsageQuery{}, 0, false
		}
		cursorDate, err := time.Parse("2006-01-02", parts[0])
		if err != nil || cursorDate.Format("2006-01-02") != parts[0] || cursorDate.Before(from) || !cursorDate.Before(until) ||
			parts[1] != "" && !catalog.ValidID(parts[1]) || query.GroupBy == "date" && parts[1] != "" {
			return billing.AdminUsageQuery{}, 0, false
		}
		query.AfterDate, query.AfterID = parts[0], parts[1]
	}
	query.Limit = limit + 1
	return query, limit, true
}

func dailyRange(r *http.Request) (time.Time, time.Time, bool) {
	values := r.URL.Query()
	if len(values["from"]) != 1 || len(values["to"]) != 1 {
		return time.Time{}, time.Time{}, false
	}
	from, err := time.Parse("2006-01-02", values.Get("from"))
	if err != nil || from.Format("2006-01-02") != values.Get("from") {
		return time.Time{}, time.Time{}, false
	}
	to, err := time.Parse("2006-01-02", values.Get("to"))
	if err != nil || to.Format("2006-01-02") != values.Get("to") || to.Before(from) || to.Sub(from) >= 90*24*time.Hour {
		return time.Time{}, time.Time{}, false
	}
	return from, to.AddDate(0, 0, 1), true
}
