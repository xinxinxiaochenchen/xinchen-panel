package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"controlplane/internal/catalog"
	"controlplane/internal/identity"
)

type RoleStore interface {
	ListRoles(context.Context) ([]identity.Role, error)
	ListPermissions(context.Context) ([]identity.Permission, error)
	CreateRole(context.Context, identity.RoleInput, string, string) (identity.Role, error)
	UpdateRole(context.Context, string, identity.RolePatchInput, string, string) (identity.Role, error)
	DeleteRole(context.Context, string, string, string) error
	SetUserRoles(context.Context, string, identity.RoleAssignment, string, string) (identity.PublicUser, error)
}

func registerRoleRoutes(mux *http.ServeMux, sessions IdentitySessions, store RoleStore) {
	mux.HandleFunc("/api/v1/admin/roles", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, ok := catalogPrincipal(w, r, sessions, "roles.read", false); !ok {
				return
			}
			roles, err := store.ListRoles(r.Context())
			if err != nil {
				writeRoleError(w, r, err)
				return
			}
			permissions, err := store.ListPermissions(r.Context())
			if err != nil {
				writeRoleError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, map[string]any{"roles": roles, "permissions": permissions})
		case http.MethodPost:
			actor, ok := roleWriter(w, r, sessions)
			if !ok {
				return
			}
			var body identity.NewRole
			if !decodeCatalogJSON(w, r, &body) {
				return
			}
			input, err := identity.NormalizeRole(body)
			if err != nil {
				writeRoleError(w, r, err)
				return
			}
			role, err := store.CreateRole(r.Context(), input, actor.ID, requestID(r))
			if err != nil {
				writeRoleError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusCreated, role)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/admin/roles/", func(w http.ResponseWriter, r *http.Request) {
		code := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/roles/"), "/")
		if code == "" || strings.Contains(code, "/") {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		actor, ok := roleWriter(w, r, sessions)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodPatch:
			var body identity.RolePatch
			if !decodeCatalogJSON(w, r, &body) {
				return
			}
			patch, err := identity.NormalizeRolePatch(body)
			if err != nil {
				writeRoleError(w, r, err)
				return
			}
			role, err := store.UpdateRole(r.Context(), code, patch, actor.ID, requestID(r))
			if err != nil {
				writeRoleError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, role)
		case http.MethodDelete:
			if err := store.DeleteRole(r.Context(), code, actor.ID, requestID(r)); err != nil {
				writeRoleError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("PUT /api/v1/admin/users/{id}/roles", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := roleWriter(w, r, sessions)
		if !ok {
			return
		}
		userID := r.PathValue("id")
		if !catalog.ValidID(userID) {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		var body identity.RoleAssignment
		if !decodeCatalogJSON(w, r, &body) {
			return
		}
		assignment, err := identity.NormalizeRoleAssignment(body)
		if err != nil {
			writeRoleError(w, r, err)
			return
		}
		user, err := store.SetUserRoles(r.Context(), userID, assignment, actor.ID, requestID(r))
		if err != nil {
			writeRoleError(w, r, err)
			return
		}
		writeCatalogJSON(w, http.StatusOK, user)
	})
}

func roleWriter(w http.ResponseWriter, r *http.Request, sessions IdentitySessions) (identity.PublicUser, bool) {
	actor, ok := catalogPrincipal(w, r, sessions, "roles.write", true)
	if !ok {
		return identity.PublicUser{}, false
	}
	if !slices.Contains(actor.Roles, "admin") {
		WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "permission denied")
		return identity.PublicUser{}, false
	}
	return actor, true
}

func writeRoleError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrInvalidInput):
		WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "invalid role input")
	case errors.Is(err, identity.ErrUnknownPermission):
		WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "unknown permission")
	case errors.Is(err, identity.ErrRoleSystem):
		WriteError(w, r, http.StatusUnprocessableEntity, "SYSTEM_ROLE", "system role cannot be changed")
	case errors.Is(err, identity.ErrRoleInUse):
		WriteError(w, r, http.StatusConflict, "CONFLICT", "role is assigned to users")
	case errors.Is(err, identity.ErrAlreadyExists):
		WriteError(w, r, http.StatusConflict, "CONFLICT", "role already exists")
	case errors.Is(err, identity.ErrNotFound):
		WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
	default:
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
	}
}
