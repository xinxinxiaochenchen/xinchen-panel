package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"controlplane/internal/catalog"
	"controlplane/internal/entitlement"
)

type EntitlementStore interface {
	CreatePlan(context.Context, entitlement.PlanInput, string, string) (entitlement.Plan, error)
	ListPlans(context.Context, int, string) ([]entitlement.Plan, error)
	SetPlanStatus(context.Context, string, string, string, string) (entitlement.Plan, error)
	CreateMembership(context.Context, entitlement.MembershipInput, string, string) (entitlement.Membership, error)
	ListMemberships(context.Context, int, string) ([]entitlement.Membership, error)
	SetMembershipStatus(context.Context, string, string, string, string) (entitlement.Membership, error)
	GetCurrentMembership(context.Context, string) (entitlement.Membership, error)
}

func registerEntitlementRoutes(mux *http.ServeMux, sessions IdentitySessions, store EntitlementStore) {
	mux.HandleFunc("/api/v1/admin/plans", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, ok := catalogPrincipal(w, r, sessions, "plans.read", false); !ok {
				return
			}
			limit, after, ok := catalogPageParams(w, r, false)
			if !ok {
				return
			}
			plans, err := store.ListPlans(r.Context(), limit+1, after)
			if err != nil {
				writeEntitlementError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, catalogPage(plans, limit, func(plan entitlement.Plan) string { return plan.ID }))
		case http.MethodPost:
			actor, ok := catalogPrincipal(w, r, sessions, "plans.write", true)
			if !ok {
				return
			}
			var input entitlement.NewPlan
			if !decodeEntitlementJSON(w, r, &input) {
				return
			}
			normalized, err := entitlement.NormalizePlan(input)
			if err != nil {
				writeEntitlementError(w, r, err)
				return
			}
			plan, err := store.CreatePlan(r.Context(), normalized, actor.ID, requestID(r))
			if err != nil {
				writeEntitlementError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusCreated, plan)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/admin/memberships", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, ok := catalogPrincipal(w, r, sessions, "plans.read", false); !ok {
				return
			}
			limit, after, ok := catalogPageParams(w, r, false)
			if !ok {
				return
			}
			memberships, err := store.ListMemberships(r.Context(), limit+1, after)
			if err != nil {
				writeEntitlementError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, catalogPage(memberships, limit, func(membership entitlement.Membership) string { return membership.ID }))
		case http.MethodPost:
			actor, ok := catalogPrincipal(w, r, sessions, "plans.write", true)
			if !ok {
				return
			}
			var input entitlement.NewMembership
			if !decodeEntitlementJSON(w, r, &input) {
				return
			}
			normalized, err := entitlement.NormalizeMembership(input, time.Now())
			if err != nil {
				writeEntitlementError(w, r, err)
				return
			}
			membership, err := store.CreateMembership(r.Context(), normalized, actor.ID, requestID(r))
			if err != nil {
				writeEntitlementError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusCreated, membership)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("PATCH /api/v1/admin/plans/{id}", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := catalogPrincipal(w, r, sessions, "plans.write", true)
		if !ok {
			return
		}
		planID := r.PathValue("id")
		if !catalog.ValidID(planID) {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		var input struct {
			Status string `json:"status"`
		}
		if !decodeEntitlementJSON(w, r, &input) {
			return
		}
		if input.Status != "active" && input.Status != "archived" {
			WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "invalid plan status")
			return
		}
		plan, err := store.SetPlanStatus(r.Context(), planID, input.Status, actor.ID, requestID(r))
		if err != nil {
			writeEntitlementError(w, r, err)
			return
		}
		writeCatalogJSON(w, http.StatusOK, plan)
	})
	mux.HandleFunc("PATCH /api/v1/admin/memberships/{id}", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := catalogPrincipal(w, r, sessions, "plans.write", true)
		if !ok {
			return
		}
		membershipID := r.PathValue("id")
		if !catalog.ValidID(membershipID) {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		var input struct {
			Status string `json:"status"`
		}
		if !decodeEntitlementJSON(w, r, &input) {
			return
		}
		if input.Status != "cancelled" {
			WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "only cancellation is supported")
			return
		}
		membership, err := store.SetMembershipStatus(r.Context(), membershipID, input.Status, actor.ID, requestID(r))
		if err != nil {
			writeEntitlementError(w, r, err)
			return
		}
		writeCatalogJSON(w, http.StatusOK, membership)
	})
	mux.HandleFunc("/api/v1/my/membership", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		user, ok := catalogPrincipal(w, r, sessions, "dashboard.read", false)
		if !ok {
			return
		}
		membership, err := store.GetCurrentMembership(r.Context(), user.ID)
		if err != nil {
			writeEntitlementError(w, r, err)
			return
		}
		writeCatalogJSON(w, http.StatusOK, membership)
	})
	mux.HandleFunc("/api/v1/my/entitlements", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		user, ok := catalogPrincipal(w, r, sessions, "dashboard.read", false)
		if !ok {
			return
		}
		membership, err := store.GetCurrentMembership(r.Context(), user.ID)
		if err != nil {
			writeEntitlementError(w, r, err)
			return
		}
		writeCatalogJSON(w, http.StatusOK, membership.Snapshot)
	})
}

func decodeEntitlementJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON request")
		return false
	}
	return true
}

func writeEntitlementError(w http.ResponseWriter, r *http.Request, err error) {
	var validation entitlement.ValidationError
	switch {
	case errors.As(err, &validation):
		WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", validation.Error())
	case errors.Is(err, entitlement.ErrConflict):
		WriteError(w, r, http.StatusConflict, "CONFLICT", "resource conflict")
	case errors.Is(err, entitlement.ErrNotFound):
		WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
	default:
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
	}
}
