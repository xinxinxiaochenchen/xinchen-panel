package httpapi

import (
	"context"
	"net/http"
	"strings"

	"controlplane/internal/catalog"
	"controlplane/internal/forward"
)

type ForwardPolicyStore interface {
	CreateTargetPolicy(context.Context, forward.TargetPolicyInput, string, string) (forward.TargetPolicy, error)
	ListTargetPolicies(context.Context, int, string) ([]forward.TargetPolicy, error)
	GetTargetPolicy(context.Context, string) (forward.TargetPolicy, error)
	UpdateTargetPolicy(context.Context, string, forward.TargetPolicyPatch, string, string) (forward.TargetPolicy, error)
	DeleteTargetPolicy(context.Context, string, string, string) error
}

func registerForwardPolicyRoutes(mux *http.ServeMux, sessions IdentitySessions, store ForwardPolicyStore) {
	mux.HandleFunc("/api/v1/admin/forward-target-policies", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, ok := catalogPrincipal(w, r, sessions, "forward_policies.write", false); !ok {
				return
			}
			limit, after, ok := catalogPageParams(w, r, false)
			if !ok {
				return
			}
			policies, err := store.ListTargetPolicies(r.Context(), limit+1, after)
			if err != nil {
				writeForwardError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, catalogPage(policies, limit, func(policy forward.TargetPolicy) string { return policy.ID }))
		case http.MethodPost:
			actor, ok := catalogPrincipal(w, r, sessions, "forward_policies.write", true)
			if !ok {
				return
			}
			var input forward.NewTargetPolicy
			if !decodeCatalogJSON(w, r, &input) {
				return
			}
			normalized, err := forward.NormalizeTargetPolicy(input)
			if err != nil {
				writeForwardError(w, r, err)
				return
			}
			policy, err := store.CreateTargetPolicy(r.Context(), normalized, actor.ID, requestID(r))
			if err != nil {
				writeForwardError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusCreated, policy)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/admin/forward-target-policies/", func(w http.ResponseWriter, r *http.Request) {
		policyID := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/forward-target-policies/")
		if !catalog.ValidID(policyID) || strings.Contains(policyID, "/") {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			if _, ok := catalogPrincipal(w, r, sessions, "forward_policies.write", false); !ok {
				return
			}
			policy, err := store.GetTargetPolicy(r.Context(), policyID)
			if err != nil {
				writeForwardError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, policy)
		case http.MethodPatch:
			actor, ok := catalogPrincipal(w, r, sessions, "forward_policies.write", true)
			if !ok {
				return
			}
			var patch forward.TargetPolicyPatch
			if !decodeCatalogJSON(w, r, &patch) {
				return
			}
			patch, err := forward.NormalizeTargetPolicyPatch(patch)
			if err != nil {
				writeForwardError(w, r, err)
				return
			}
			policy, err := store.UpdateTargetPolicy(r.Context(), policyID, patch, actor.ID, requestID(r))
			if err != nil {
				writeForwardError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, policy)
		case http.MethodDelete:
			actor, ok := catalogPrincipal(w, r, sessions, "forward_policies.write", true)
			if !ok {
				return
			}
			if err := store.DeleteTargetPolicy(r.Context(), policyID, actor.ID, requestID(r)); err != nil {
				writeForwardError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
}
