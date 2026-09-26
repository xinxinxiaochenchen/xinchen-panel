package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"

	"controlplane/internal/catalog"
	"controlplane/internal/identity"
)

type CatalogStore interface {
	CreateGroup(context.Context, catalog.GroupInput, string, string) (catalog.ResourceGroup, error)
	ListGroups(context.Context, int, string) ([]catalog.ResourceGroup, error)
	CreateNode(context.Context, catalog.NodeInput, string, string) (catalog.Node, error)
	ListAllNodes(context.Context, int, string) ([]catalog.Node, error)
	GetNodeMetrics(context.Context, string) (catalog.NodeMetrics, error)
	ListAllowedNodes(context.Context, string, int, string) ([]catalog.Node, error)
	GetAllowedNode(context.Context, string, string) (catalog.Node, error)
}

func registerCatalogRoutes(mux *http.ServeMux, sessions IdentitySessions, store CatalogStore) {
	mux.HandleFunc("/api/v1/admin/resource-groups", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			user, ok := catalogPrincipal(w, r, sessions, "", false)
			if !ok {
				return
			}
			if !slices.Contains(user.Permissions, "nodes.write") && !slices.Contains(user.Permissions, "forward_policies.write") {
				WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "permission denied")
				return
			}
			limit, after, ok := catalogPageParams(w, r, true)
			if !ok {
				return
			}
			groups, err := store.ListGroups(r.Context(), limit+1, after)
			if err != nil {
				writeCatalogError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, catalogPage(groups, limit, func(group catalog.ResourceGroup) string { return group.Code }))
		case http.MethodPost:
			user, ok := catalogPrincipal(w, r, sessions, "nodes.write", true)
			if !ok {
				return
			}
			var input catalog.NewGroup
			if !decodeCatalogJSON(w, r, &input) {
				return
			}
			normalized, err := catalog.NormalizeGroup(input)
			if err != nil {
				writeCatalogError(w, r, err)
				return
			}
			group, err := store.CreateGroup(r.Context(), normalized, user.ID, requestID(r))
			if err != nil {
				writeCatalogError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusCreated, group)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/admin/nodes", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			user, ok := catalogPrincipal(w, r, sessions, "", false)
			if !ok {
				return
			}
			if !slices.Contains(user.Permissions, "nodes.write") && !slices.Contains(user.Permissions, "agents.write") {
				WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "permission denied")
				return
			}
			limit, after, ok := catalogPageParams(w, r, false)
			if !ok {
				return
			}
			nodes, err := store.ListAllNodes(r.Context(), limit+1, after)
			if err != nil {
				writeCatalogError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, catalogPage(nodes, limit, func(node catalog.Node) string { return node.ID }))
		case http.MethodPost:
			user, ok := catalogPrincipal(w, r, sessions, "nodes.write", true)
			if !ok {
				return
			}
			var input catalog.NewNode
			if !decodeCatalogJSON(w, r, &input) {
				return
			}
			normalized, err := catalog.NormalizeNode(input)
			if err != nil {
				writeCatalogError(w, r, err)
				return
			}
			node, err := store.CreateNode(r.Context(), normalized, user.ID, requestID(r))
			if err != nil {
				writeCatalogError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusCreated, node)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("GET /api/v1/admin/nodes/{id}/metrics", func(w http.ResponseWriter, r *http.Request) {
		user, ok := catalogPrincipal(w, r, sessions, "", false)
		if !ok {
			return
		}
		if !slices.Contains(user.Permissions, "nodes.write") && !slices.Contains(user.Permissions, "agents.write") {
			WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "permission denied")
			return
		}
		id := r.PathValue("id")
		if !catalog.ValidID(id) {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		metrics, err := store.GetNodeMetrics(r.Context(), id)
		if err != nil {
			writeCatalogError(w, r, err)
			return
		}
		writeCatalogJSON(w, http.StatusOK, metrics)
	})
	mux.HandleFunc("/api/v1/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		user, ok := catalogPrincipal(w, r, sessions, "nodes.read", false)
		if !ok {
			return
		}
		limit, after, ok := catalogPageParams(w, r, false)
		if !ok {
			return
		}
		nodes, err := store.ListAllowedNodes(r.Context(), user.ID, limit+1, after)
		if err != nil {
			writeCatalogError(w, r, err)
			return
		}
		writeCatalogJSON(w, http.StatusOK, catalogPage(nodes, limit, func(node catalog.Node) string { return node.ID }))
	})
	mux.HandleFunc("/api/v1/nodes/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		user, ok := catalogPrincipal(w, r, sessions, "nodes.read", false)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if id == "" {
			id = r.URL.Path[len("/api/v1/nodes/"):]
		}
		if !catalog.ValidID(id) {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		node, err := store.GetAllowedNode(r.Context(), user.ID, id)
		if err != nil {
			writeCatalogError(w, r, err)
			return
		}
		writeCatalogJSON(w, http.StatusOK, node)
	})
}

func catalogPrincipal(w http.ResponseWriter, r *http.Request, sessions IdentitySessions, permission string, csrf bool) (identity.PublicUser, bool) {
	w.Header().Set("Cache-Control", "no-store")
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return identity.PublicUser{}, false
	}
	var user identity.PublicUser
	if csrf {
		user, err = sessions.VerifyCSRF(r.Context(), cookie.Value, r.Header.Get("X-CSRF-Token"))
	} else {
		user, err = sessions.Authenticate(r.Context(), cookie.Value)
	}
	if err != nil {
		writeIdentityError(w, r, err)
		return identity.PublicUser{}, false
	}
	if permission != "" && !slices.Contains(user.Permissions, permission) {
		WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "permission denied")
		return identity.PublicUser{}, false
	}
	return user, true
}

func decodeCatalogJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON request")
		return false
	}
	return true
}

func catalogPageParams(w http.ResponseWriter, r *http.Request, group bool) (int, string, bool) {
	value := r.URL.Query().Get("limit")
	limit := 100
	if value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 200 {
			WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid list limit")
			return 0, "", false
		}
		limit = parsed
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor == "" {
		return limit, "", true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(decoded) > 64 {
		WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid list cursor")
		return 0, "", false
	}
	after := string(decoded)
	if (group && !catalog.ValidGroupCode(after)) || (!group && !catalog.ValidID(after)) {
		WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid list cursor")
		return 0, "", false
	}
	return limit, after, true
}

func catalogPage[T any](items []T, limit int, key func(T) string) map[string]any {
	var next *string
	if len(items) > limit {
		items = items[:limit]
		cursor := base64.RawURLEncoding.EncodeToString([]byte(key(items[len(items)-1])))
		next = &cursor
	}
	if items == nil {
		items = make([]T, 0)
	}
	return map[string]any{"items": items, "next_cursor": next}
}

func requestID(r *http.Request) string {
	value, _ := r.Context().Value(requestIDKey{}).(string)
	return value
}

func writeCatalogJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeCatalogError(w http.ResponseWriter, r *http.Request, err error) {
	var validation catalog.ValidationError
	switch {
	case errors.As(err, &validation):
		WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", validation.Error())
	case errors.Is(err, catalog.ErrConflict):
		WriteError(w, r, http.StatusConflict, "CONFLICT", "resource already exists")
	case errors.Is(err, catalog.ErrNotFound):
		WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
	default:
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
	}
}
