package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"controlplane/internal/catalog"
	"controlplane/internal/forward"
)

type ForwardStore interface {
	CreateRule(context.Context, forward.RuleInput, string, string) (forward.Rule, error)
	ListOwnRules(context.Context, string, int, string) ([]forward.Rule, error)
	GetOwnRule(context.Context, string, string) (forward.Rule, error)
	UpdateOwnRule(context.Context, string, string, forward.RulePatch, string) (forward.Rule, error)
	DeleteOwnRule(context.Context, string, string, string) error
}

func registerForwardRoutes(mux *http.ServeMux, sessions IdentitySessions, store ForwardStore) {
	mux.HandleFunc("/api/v1/forward-rules", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			actor, ok := catalogPrincipal(w, r, sessions, "forward_rules.read", false)
			if !ok {
				return
			}
			limit, after, ok := catalogPageParams(w, r, false)
			if !ok {
				return
			}
			rules, err := store.ListOwnRules(r.Context(), actor.ID, limit+1, after)
			if err != nil {
				writeForwardError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, catalogPage(rules, limit, func(rule forward.Rule) string { return rule.ID }))
		case http.MethodPost:
			actor, ok := catalogPrincipal(w, r, sessions, "forward_rules.write", true)
			if !ok {
				return
			}
			var input forward.NewRule
			if !decodeCatalogJSON(w, r, &input) {
				return
			}
			normalized, err := forward.NormalizeRule(input)
			if err != nil {
				writeForwardError(w, r, err)
				return
			}
			rule, err := store.CreateRule(r.Context(), normalized, actor.ID, requestID(r))
			if err != nil {
				writeForwardError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusCreated, rule)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/forward-rules/", func(w http.ResponseWriter, r *http.Request) {
		ruleID := strings.TrimPrefix(r.URL.Path, "/api/v1/forward-rules/")
		if !catalog.ValidID(ruleID) || strings.Contains(ruleID, "/") {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			actor, ok := catalogPrincipal(w, r, sessions, "forward_rules.read", false)
			if !ok {
				return
			}
			rule, err := store.GetOwnRule(r.Context(), actor.ID, ruleID)
			if err != nil {
				writeForwardError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, rule)
		case http.MethodPatch:
			actor, ok := catalogPrincipal(w, r, sessions, "forward_rules.write", true)
			if !ok {
				return
			}
			var patch forward.RulePatch
			if !decodeCatalogJSON(w, r, &patch) {
				return
			}
			patch, err := forward.NormalizeRulePatch(patch)
			if err != nil {
				writeForwardError(w, r, err)
				return
			}
			rule, err := store.UpdateOwnRule(r.Context(), actor.ID, ruleID, patch, requestID(r))
			if err != nil {
				writeForwardError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, rule)
		case http.MethodDelete:
			actor, ok := catalogPrincipal(w, r, sessions, "forward_rules.write", true)
			if !ok {
				return
			}
			if err := store.DeleteOwnRule(r.Context(), actor.ID, ruleID, requestID(r)); err != nil {
				writeForwardError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
}

func writeForwardError(w http.ResponseWriter, r *http.Request, err error) {
	var validation forward.ValidationError
	switch {
	case errors.As(err, &validation):
		WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", validation.Error())
	case errors.Is(err, forward.ErrLimitReached):
		WriteError(w, r, http.StatusConflict, "LIMIT_REACHED", "forward rule limit reached")
	case errors.Is(err, forward.ErrPolicyDenied):
		WriteError(w, r, http.StatusForbidden, "TARGET_POLICY_DENIED", "forward destination is not approved")
	case errors.Is(err, forward.ErrConflict):
		WriteError(w, r, http.StatusConflict, "CONFLICT", "forward port or rule name is already in use")
	case errors.Is(err, forward.ErrNotFound):
		WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
	default:
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
	}
}
