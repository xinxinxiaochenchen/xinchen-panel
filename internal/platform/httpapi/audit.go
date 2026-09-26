package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"controlplane/internal/audit"
)

type AuditStore interface {
	List(context.Context, int, string) (audit.Page, error)
}

func registerAuditRoutes(mux *http.ServeMux, sessions IdentitySessions, store AuditStore) {
	mux.HandleFunc("/api/v1/admin/audit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		if _, ok := catalogPrincipal(w, r, sessions, "audit.read", false); !ok {
			return
		}
		limit := 100
		if value := r.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 200 {
				WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid list limit")
				return
			}
			limit = parsed
		}
		cursor := r.URL.Query().Get("cursor")
		if cursor != "" {
			if _, err := audit.ParseCursor(cursor); err != nil {
				WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid list cursor")
				return
			}
		}
		page, err := store.List(r.Context(), limit, cursor)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				WriteError(w, r, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "audit store unavailable")
				return
			}
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
			return
		}
		writeCatalogJSON(w, http.StatusOK, page)
	})
}
