package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"controlplane/internal/catalog"
	"controlplane/internal/georules"
)

type GeoRuleSetStore interface {
	List(context.Context, int, string) ([]georules.RuleSet, error)
	Create(context.Context, georules.Input, string, string) (georules.RuleSet, error)
	SetEnabled(context.Context, string, bool, string, string) (georules.RuleSet, error)
}

func registerGeoRuleSetRoutes(mux *http.ServeMux, sessions IdentitySessions, store GeoRuleSetStore) {
	mux.HandleFunc("/api/v1/admin/routing-rule-sets", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, ok := catalogPrincipal(w, r, sessions, "routing_rulesets.read", false)
			if !ok {
				return
			}
			limit, after, ok := catalogPageParams(w, r, false)
			if !ok {
				return
			}
			items, err := store.List(r.Context(), limit+1, after)
			if err != nil {
				writeGeoRuleSetError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, catalogPage(items, limit, func(item georules.RuleSet) string { return item.ID }))
		case http.MethodPost:
			actor, ok := catalogPrincipal(w, r, sessions, "routing_rulesets.write", true)
			if !ok {
				return
			}
			var body georules.NewRuleSet
			if !decodeGeoRuleSetJSON(w, r, &body) {
				return
			}
			input, err := georules.Normalize(body)
			if err != nil {
				writeGeoRuleSetError(w, r, err)
				return
			}
			item, err := store.Create(r.Context(), input, actor.ID, requestID(r))
			if err != nil {
				writeGeoRuleSetError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusCreated, item)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/admin/routing-rule-sets/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/routing-rule-sets/"), "/")
		if !catalog.ValidID(id) {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		actor, ok := catalogPrincipal(w, r, sessions, "routing_rulesets.write", true)
		if !ok {
			return
		}
		if r.Method != http.MethodPatch {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if !decodeCatalogJSON(w, r, &body) {
			return
		}
		if body.Enabled == nil {
			WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "enabled is required")
			return
		}
		item, err := store.SetEnabled(r.Context(), id, *body.Enabled, actor.ID, requestID(r))
		if err != nil {
			writeGeoRuleSetError(w, r, err)
			return
		}
		writeCatalogJSON(w, http.StatusOK, item)
	})
}

func decodeGeoRuleSetJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON request")
		return false
	}
	return true
}

func writeGeoRuleSetError(w http.ResponseWriter, r *http.Request, err error) {
	var validation georules.ValidationError
	switch {
	case errors.As(err, &validation):
		WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", validation.Error())
	case errors.Is(err, georules.ErrConflict):
		WriteError(w, r, http.StatusConflict, "CONFLICT", "resource already exists")
	case errors.Is(err, georules.ErrNotFound):
		WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
	default:
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
	}
}
