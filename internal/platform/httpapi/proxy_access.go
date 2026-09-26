package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"controlplane/internal/catalog"
	"controlplane/internal/proxyaccess"
)

type ProxyAccessStore interface {
	Create(context.Context, string, proxyaccess.AccessInput, string) (proxyaccess.Access, string, error)
	ListOwn(context.Context, string, int, string) ([]proxyaccess.Access, error)
	GetOwn(context.Context, string, string) (proxyaccess.Access, error)
	RevealOwn(context.Context, string, string) (string, error)
	RotateOwn(context.Context, string, string, string) (string, error)
	UpdateOwn(context.Context, string, string, proxyaccess.AccessPatch, string) (proxyaccess.Access, error)
	DeleteOwn(context.Context, string, string, string) error
}

func registerProxyAccessRoutes(mux *http.ServeMux, sessions IdentitySessions, store ProxyAccessStore) {
	mux.HandleFunc("/api/v1/proxy-accesses", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			actor, ok := catalogPrincipal(w, r, sessions, "proxy_accesses.read", false)
			if !ok {
				return
			}
			limit, after, ok := catalogPageParams(w, r, false)
			if !ok {
				return
			}
			items, err := store.ListOwn(r.Context(), actor.ID, limit+1, after)
			if err != nil {
				writeProxyAccessError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, catalogPage(items, limit, func(value proxyaccess.Access) string { return value.ID }))
		case http.MethodPost:
			actor, ok := catalogPrincipal(w, r, sessions, "proxy_accesses.write", true)
			if !ok {
				return
			}
			var input proxyaccess.NewAccess
			if !decodeCatalogJSON(w, r, &input) {
				return
			}
			normalized, err := proxyaccess.NormalizeAccess(input)
			if err != nil {
				writeProxyAccessError(w, r, err)
				return
			}
			access, credential, err := store.Create(r.Context(), actor.ID, normalized, requestID(r))
			if err != nil {
				writeProxyAccessError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusCreated, struct {
				Access     proxyaccess.Access `json:"access"`
				Credential string             `json:"credential"`
			}{Access: access, Credential: credential})
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/proxy-accesses/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/proxy-accesses/")
		accessID, suffix, _ := strings.Cut(path, "/")
		if !catalog.ValidID(accessID) || (suffix != "" && suffix != "credential" && suffix != "credential-rotation") {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		if suffix == "credential" {
			if r.Method != http.MethodGet {
				WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
				return
			}
			actor, ok := catalogPrincipal(w, r, sessions, "proxy_accesses.read", false)
			if !ok {
				return
			}
			credential, err := store.RevealOwn(r.Context(), actor.ID, accessID)
			if err != nil {
				writeProxyAccessError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, map[string]string{"credential": credential})
			return
		}
		if suffix == "credential-rotation" {
			if r.Method != http.MethodPost {
				WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
				return
			}
			actor, ok := catalogPrincipal(w, r, sessions, "proxy_accesses.write", true)
			if !ok {
				return
			}
			credential, err := store.RotateOwn(r.Context(), actor.ID, accessID, requestID(r))
			if err != nil {
				writeProxyAccessError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, map[string]string{"credential": credential})
			return
		}
		switch r.Method {
		case http.MethodGet:
			actor, ok := catalogPrincipal(w, r, sessions, "proxy_accesses.read", false)
			if !ok {
				return
			}
			access, err := store.GetOwn(r.Context(), actor.ID, accessID)
			if err != nil {
				writeProxyAccessError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, access)
		case http.MethodPatch:
			actor, ok := catalogPrincipal(w, r, sessions, "proxy_accesses.write", true)
			if !ok {
				return
			}
			var patch proxyaccess.AccessPatch
			if !decodeCatalogJSON(w, r, &patch) {
				return
			}
			access, err := store.UpdateOwn(r.Context(), actor.ID, accessID, patch, requestID(r))
			if err != nil {
				writeProxyAccessError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, access)
		case http.MethodDelete:
			actor, ok := catalogPrincipal(w, r, sessions, "proxy_accesses.write", true)
			if !ok {
				return
			}
			if err := store.DeleteOwn(r.Context(), actor.ID, accessID, requestID(r)); err != nil {
				writeProxyAccessError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
}

func writeProxyAccessError(w http.ResponseWriter, r *http.Request, err error) {
	var validation proxyaccess.ValidationError
	switch {
	case errors.As(err, &validation):
		WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", validation.Error())
	case errors.Is(err, proxyaccess.ErrConflict):
		WriteError(w, r, http.StatusConflict, "CONFLICT", "proxy access conflict")
	case errors.Is(err, proxyaccess.ErrUnauthorized), errors.Is(err, proxyaccess.ErrNotFound):
		WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
	default:
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
	}
}
