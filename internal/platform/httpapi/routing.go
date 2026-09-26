package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"controlplane/internal/catalog"
	"controlplane/internal/routing"
)

type RoutingStore interface {
	CreateProfile(context.Context, string, routing.ProfileInput, string) (routing.Profile, error)
	ListOwnProfiles(context.Context, string, int, string) ([]routing.Profile, error)
	GetOwnProfile(context.Context, string, string) (routing.Profile, error)
	UpdateProfile(context.Context, string, string, routing.ProfilePatch, string) (routing.Profile, error)
	DeleteProfile(context.Context, string, string, string) error
	ListRules(context.Context, string, string, int, string) ([]routing.Rule, error)
	CreateRule(context.Context, string, string, routing.RuleInput, string) (routing.Rule, error)
	UpdateRule(context.Context, string, string, string, routing.RulePatch, string) (routing.Rule, error)
	DeleteRule(context.Context, string, string, string, string) error
	ValidateOwnProfile(context.Context, string, string) error
}

func registerRoutingRoutes(mux *http.ServeMux, sessions IdentitySessions, store RoutingStore) {
	mux.HandleFunc("/api/v1/routing-profiles", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := catalogPrincipal(w, r, sessions, map[string]string{http.MethodGet: "routing.read", http.MethodPost: "routing.write"}[r.Method], r.Method == http.MethodPost)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			limit, after, ok := catalogPageParams(w, r, false)
			if !ok {
				return
			}
			items, err := store.ListOwnProfiles(r.Context(), actor.ID, limit+1, after)
			if err != nil {
				writeRoutingError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, catalogPage(items, limit, func(v routing.Profile) string { return v.ID }))
		case http.MethodPost:
			var body routing.NewProfile
			if !decodeCatalogJSON(w, r, &body) {
				return
			}
			input, err := routing.NormalizeProfile(body)
			if err != nil {
				writeRoutingError(w, r, err)
				return
			}
			v, err := store.CreateProfile(r.Context(), actor.ID, input, requestID(r))
			if err != nil {
				writeRoutingError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusCreated, v)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/routing-profiles/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/v1/routing-profiles/")
		parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
		if len(parts) < 1 || !catalog.ValidID(parts[0]) || len(parts) > 3 || (len(parts) == 2 && parts[1] != "rules" && parts[1] != "validate") || (len(parts) == 3 && (parts[1] != "rules" || !catalog.ValidID(parts[2]))) {
			WriteError(w, r, 404, "NOT_FOUND", "resource not found")
			return
		}
		id := parts[0]
		if len(parts) == 2 && parts[1] == "validate" {
			if r.Method != http.MethodPost {
				WriteError(w, r, 405, "METHOD_NOT_ALLOWED", "method not allowed")
				return
			}
			actor, ok := catalogPrincipal(w, r, sessions, "routing.write", true)
			if !ok {
				return
			}
			if err := store.ValidateOwnProfile(r.Context(), actor.ID, id); err != nil {
				writeRoutingError(w, r, err)
				return
			}
			writeCatalogJSON(w, 200, map[string]any{"valid": true})
			return
		}
		if len(parts) == 2 {
			actor, ok := catalogPrincipal(w, r, sessions, map[string]string{http.MethodGet: "routing.read", http.MethodPost: "routing.write"}[r.Method], r.Method == http.MethodPost)
			if !ok {
				return
			}
			switch r.Method {
			case http.MethodGet:
				limit, after, ok := routingPageParams(w, r)
				if !ok {
					return
				}
				items, err := store.ListRules(r.Context(), actor.ID, id, limit+1, after)
				if err != nil {
					writeRoutingError(w, r, err)
					return
				}
				writeCatalogJSON(w, 200, catalogPage(items, limit, routing.RuleCursor))
			case http.MethodPost:
				var body routing.NewRule
				if !decodeCatalogJSON(w, r, &body) {
					return
				}
				input, err := routing.NormalizeRule(body)
				if err != nil {
					writeRoutingError(w, r, err)
					return
				}
				v, err := store.CreateRule(r.Context(), actor.ID, id, input, requestID(r))
				if err != nil {
					writeRoutingError(w, r, err)
					return
				}
				writeCatalogJSON(w, 201, v)
			default:
				WriteError(w, r, 405, "METHOD_NOT_ALLOWED", "method not allowed")
			}
			return
		}
		if len(parts) == 3 {
			actor, ok := catalogPrincipal(w, r, sessions, "routing.write", true)
			if !ok {
				return
			}
			ruleID := parts[2]
			switch r.Method {
			case http.MethodPatch:
				var body routing.RulePatch
				if !decodeCatalogJSON(w, r, &body) {
					return
				}
				v, err := store.UpdateRule(r.Context(), actor.ID, id, ruleID, body, requestID(r))
				if err != nil {
					writeRoutingError(w, r, err)
					return
				}
				writeCatalogJSON(w, 200, v)
			case http.MethodDelete:
				if err := store.DeleteRule(r.Context(), actor.ID, id, ruleID, requestID(r)); err != nil {
					writeRoutingError(w, r, err)
					return
				}
				w.WriteHeader(204)
			default:
				WriteError(w, r, 405, "METHOD_NOT_ALLOWED", "method not allowed")
			}
			return
		}
		actor, ok := catalogPrincipal(w, r, sessions, map[string]string{http.MethodGet: "routing.read", http.MethodPatch: "routing.write", http.MethodDelete: "routing.write"}[r.Method], r.Method == http.MethodPatch || r.Method == http.MethodDelete)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			v, err := store.GetOwnProfile(r.Context(), actor.ID, id)
			if err != nil {
				writeRoutingError(w, r, err)
				return
			}
			writeCatalogJSON(w, 200, v)
		case http.MethodPatch:
			var body routing.ProfilePatch
			if !decodeCatalogJSON(w, r, &body) {
				return
			}
			v, err := store.UpdateProfile(r.Context(), actor.ID, id, body, requestID(r))
			if err != nil {
				writeRoutingError(w, r, err)
				return
			}
			writeCatalogJSON(w, 200, v)
		case http.MethodDelete:
			if err := store.DeleteProfile(r.Context(), actor.ID, id, requestID(r)); err != nil {
				writeRoutingError(w, r, err)
				return
			}
			w.WriteHeader(204)
		default:
			WriteError(w, r, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
}

func routingPageParams(w http.ResponseWriter, r *http.Request) (int, string, bool) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			WriteError(w, r, 400, "INVALID_REQUEST", "invalid list limit")
			return 0, "", false
		}
		limit = n
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor == "" {
		return limit, "", true
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) > 64 {
		WriteError(w, r, 400, "INVALID_REQUEST", "invalid list cursor")
		return 0, "", false
	}
	if _, _, err := routing.ParseRuleCursor(string(raw)); err != nil {
		WriteError(w, r, 400, "INVALID_REQUEST", "invalid list cursor")
		return 0, "", false
	}
	return limit, string(raw), true
}

func writeRoutingError(w http.ResponseWriter, r *http.Request, err error) {
	var validation routing.ValidationError
	switch {
	case errors.As(err, &validation):
		WriteError(w, r, 422, "VALIDATION_ERROR", validation.Error())
	case errors.Is(err, routing.ErrNotFound):
		WriteError(w, r, 404, "NOT_FOUND", "resource not found")
	case errors.Is(err, routing.ErrLimit):
		WriteError(w, r, 409, "PLAN_LIMIT_EXCEEDED", "routing rule limit exceeded")
	case errors.Is(err, routing.ErrConflict):
		WriteError(w, r, 409, "CONFLICT", "resource already exists")
	default:
		WriteError(w, r, 500, "INTERNAL", "internal server error")
	}
}
