package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"controlplane/internal/catalog"
)

type LineStore interface {
	CreateSharedLine(context.Context, catalog.LineInput, string, string) (catalog.Line, error)
	CreateCustomLine(context.Context, catalog.LineInput, string, string) (catalog.Line, error)
	ListAllLines(context.Context, int, string) ([]catalog.Line, error)
	ListAllowedLines(context.Context, string, int, string) ([]catalog.Line, error)
	GetLine(context.Context, string) (catalog.Line, error)
	GetAllowedLine(context.Context, string, string) (catalog.Line, error)
	UpdateSharedLine(context.Context, string, catalog.LinePatch, string, string) (catalog.Line, error)
	UpdateOwnLine(context.Context, string, string, catalog.LinePatch, string) (catalog.Line, error)
	DeleteOwnLine(context.Context, string, string, string) error
}

type lineHealthStore interface {
	GetLineHealth(context.Context, string) (catalog.LineHealth, error)
	GetAllowedLineHealth(context.Context, string, string) (catalog.LineHealth, error)
}

func registerLineRoutes(mux *http.ServeMux, sessions IdentitySessions, store LineStore) {
	mux.HandleFunc("/api/v1/admin/lines", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, ok := catalogPrincipal(w, r, sessions, "lines.write", false); !ok {
				return
			}
			limit, after, ok := catalogPageParams(w, r, false)
			if !ok {
				return
			}
			lines, err := store.ListAllLines(r.Context(), limit+1, after)
			if err != nil {
				writeLineError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, catalogPage(lines, limit, func(line catalog.Line) string { return line.ID }))
		case http.MethodPost:
			actor, ok := catalogPrincipal(w, r, sessions, "lines.write", true)
			if !ok {
				return
			}
			var input catalog.NewLine
			if !decodeCatalogJSON(w, r, &input) {
				return
			}
			normalized, err := catalog.NormalizeLine(input, true)
			if err != nil {
				writeLineError(w, r, err)
				return
			}
			line, err := store.CreateSharedLine(r.Context(), normalized, actor.ID, requestID(r))
			if err != nil {
				writeLineError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusCreated, line)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/lines", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			actor, ok := catalogPrincipal(w, r, sessions, "lines.read", false)
			if !ok {
				return
			}
			limit, after, ok := catalogPageParams(w, r, false)
			if !ok {
				return
			}
			lines, err := store.ListAllowedLines(r.Context(), actor.ID, limit+1, after)
			if err != nil {
				writeLineError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, catalogPage(lines, limit, func(line catalog.Line) string { return line.ID }))
		case http.MethodPost:
			actor, ok := catalogPrincipal(w, r, sessions, "lines.write.self", true)
			if !ok {
				return
			}
			var input catalog.NewLine
			if !decodeCatalogJSON(w, r, &input) {
				return
			}
			normalized, err := catalog.NormalizeLine(input, false)
			if err != nil {
				writeLineError(w, r, err)
				return
			}
			line, err := store.CreateCustomLine(r.Context(), normalized, actor.ID, requestID(r))
			if err != nil {
				writeLineError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusCreated, line)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/admin/lines/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/health") {
			serveLineHealth(w, r, sessions, store, true)
			return
		}
		lineID, ok := lineIDFromPath(w, r, "/api/v1/admin/lines/")
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			if _, ok := catalogPrincipal(w, r, sessions, "lines.write", false); !ok {
				return
			}
			line, err := store.GetLine(r.Context(), lineID)
			if err != nil {
				writeLineError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, line)
		case http.MethodPatch:
			actor, ok := catalogPrincipal(w, r, sessions, "lines.write", true)
			if !ok {
				return
			}
			var patch catalog.LinePatch
			if !decodeCatalogJSON(w, r, &patch) {
				return
			}
			line, err := store.UpdateSharedLine(r.Context(), lineID, patch, actor.ID, requestID(r))
			if err != nil {
				writeLineError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, line)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/lines/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/health") {
			serveLineHealth(w, r, sessions, store, false)
			return
		}
		lineID, ok := lineIDFromPath(w, r, "/api/v1/lines/")
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			actor, ok := catalogPrincipal(w, r, sessions, "lines.read", false)
			if !ok {
				return
			}
			line, err := store.GetAllowedLine(r.Context(), actor.ID, lineID)
			if err != nil {
				writeLineError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, line)
		case http.MethodPatch:
			actor, ok := catalogPrincipal(w, r, sessions, "lines.write.self", true)
			if !ok {
				return
			}
			var patch catalog.LinePatch
			if !decodeCatalogJSON(w, r, &patch) {
				return
			}
			line, err := store.UpdateOwnLine(r.Context(), actor.ID, lineID, patch, requestID(r))
			if err != nil {
				writeLineError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, line)
		case http.MethodDelete:
			actor, ok := catalogPrincipal(w, r, sessions, "lines.write.self", true)
			if !ok {
				return
			}
			if err := store.DeleteOwnLine(r.Context(), actor.ID, lineID, requestID(r)); err != nil {
				writeLineError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
}

func serveLineHealth(w http.ResponseWriter, r *http.Request, sessions IdentitySessions, store LineStore, admin bool) {
	if r.Method != http.MethodGet {
		WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	prefix := "/api/v1/lines/"
	permission := "lines.read"
	if admin {
		prefix = "/api/v1/admin/lines/"
		permission = "lines.write"
	}
	lineID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/health")
	if !catalog.ValidID(lineID) || strings.Contains(lineID, "/") {
		WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
		return
	}
	actor, ok := catalogPrincipal(w, r, sessions, permission, false)
	if !ok {
		return
	}
	health, ok := store.(lineHealthStore)
	if !ok {
		WriteError(w, r, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "line health unavailable")
		return
	}
	var value catalog.LineHealth
	var err error
	if admin {
		value, err = health.GetLineHealth(r.Context(), lineID)
	} else {
		value, err = health.GetAllowedLineHealth(r.Context(), actor.ID, lineID)
	}
	if err != nil {
		writeLineError(w, r, err)
		return
	}
	writeCatalogJSON(w, http.StatusOK, value)
}

func lineIDFromPath(w http.ResponseWriter, r *http.Request, prefix string) (string, bool) {
	lineID := strings.TrimPrefix(r.URL.Path, prefix)
	if !catalog.ValidID(lineID) || strings.Contains(lineID, "/") {
		WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
		return "", false
	}
	return lineID, true
}

func writeLineError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, catalog.ErrLimitReached) {
		WriteError(w, r, http.StatusConflict, "LIMIT_REACHED", "custom line limit reached")
		return
	}
	writeCatalogError(w, r, err)
}
