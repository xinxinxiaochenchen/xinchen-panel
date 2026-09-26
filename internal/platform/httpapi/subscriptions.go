package httpapi

import (
	"context"
	"controlplane/internal/catalog"
	"controlplane/internal/subscription"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type SubscriptionStore interface {
	ResolveToken(context.Context, string) (subscription.Subscription, error)
	Create(context.Context, string, subscription.Input, string) (subscription.Subscription, string, error)
	ListOwn(context.Context, string, int, string) ([]subscription.Subscription, error)
	GetOwn(context.Context, string, string) (subscription.Subscription, error)
	RevealOwn(context.Context, string, string) (string, error)
	RotateOwn(context.Context, string, string, string) (string, error)
	UpdateOwn(context.Context, string, string, subscription.Patch, string) (subscription.Subscription, error)
	DeleteOwn(context.Context, string, string, string) error
	Export(context.Context, string, string) ([]byte, string, error)
	ExportOwn(context.Context, string, string, string) ([]byte, string, error)
}

func registerSubscriptionRoutes(mux *http.ServeMux, sessions IdentitySessions, store SubscriptionStore) {
	mux.HandleFunc("/api/v1/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			actor, ok := catalogPrincipal(w, r, sessions, "subscriptions.read", false)
			if !ok {
				return
			}
			limit, after, ok := catalogPageParams(w, r, false)
			if !ok {
				return
			}
			items, err := store.ListOwn(r.Context(), actor.ID, limit+1, after)
			if err != nil {
				writeSubscriptionError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, catalogPage(items, limit, func(s subscription.Subscription) string { return s.ID }))
		case http.MethodPost:
			actor, ok := catalogPrincipal(w, r, sessions, "subscriptions.write", true)
			if !ok {
				return
			}
			var body subscription.NewSubscription
			if !decodeCatalogJSON(w, r, &body) {
				return
			}
			input, err := subscription.Normalize(body)
			if err != nil {
				writeSubscriptionError(w, r, err)
				return
			}
			sub, token, err := store.Create(r.Context(), actor.ID, input, requestID(r))
			if err != nil {
				writeSubscriptionError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusCreated, struct {
				Subscription subscription.Subscription `json:"subscription"`
				Token        string                    `json:"token"`
				Path         string                    `json:"path"`
			}{sub, token, "/sub/" + token + "/mihomo"})
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/subscriptions/", func(w http.ResponseWriter, r *http.Request) {
		remainder := strings.TrimPrefix(r.URL.Path, "/api/v1/subscriptions/")
		id, suffix, hasSuffix := strings.Cut(remainder, "/")
		if !catalog.ValidID(id) || (hasSuffix && suffix != "token-rotation" && suffix != "url" && suffix != "preview") {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		if hasSuffix {
			switch suffix {
			case "token-rotation":
				if r.Method != http.MethodPost {
					WriteError(w, r, 405, "METHOD_NOT_ALLOWED", "method not allowed")
					return
				}
				actor, ok := catalogPrincipal(w, r, sessions, "subscriptions.write", true)
				if !ok {
					return
				}
				token, err := store.RotateOwn(r.Context(), actor.ID, id, requestID(r))
				if err != nil {
					writeSubscriptionError(w, r, err)
					return
				}
				writeCatalogJSON(w, 200, map[string]string{"token": token, "path": "/sub/" + token + "/mihomo"})
				return
			case "url", "preview":
				if r.Method != http.MethodGet {
					WriteError(w, r, 405, "METHOD_NOT_ALLOWED", "method not allowed")
					return
				}
				actor, ok := catalogPrincipal(w, r, sessions, "subscriptions.read", false)
				if !ok {
					return
				}
				format := r.URL.Query().Get("format")
				if format != "mihomo" && format != "sing-box" {
					WriteError(w, r, 400, "INVALID_FORMAT", "unsupported format")
					return
				}
				if suffix == "preview" {
					body, contentType, err := store.ExportOwn(r.Context(), actor.ID, id, format)
					if err != nil {
						writeSubscriptionError(w, r, err)
						return
					}
					w.Header().Set("Content-Type", contentType)
					w.WriteHeader(200)
					_, _ = w.Write(body)
					return
				}
				token, err := store.RevealOwn(r.Context(), actor.ID, id)
				if err != nil {
					writeSubscriptionError(w, r, err)
					return
				}
				writeCatalogJSON(w, 200, map[string]string{"path": "/sub/" + token + "/" + format})
				return
			}
		}
		switch r.Method {
		case http.MethodGet:
			actor, ok := catalogPrincipal(w, r, sessions, "subscriptions.read", false)
			if !ok {
				return
			}
			sub, err := store.GetOwn(r.Context(), actor.ID, id)
			if err != nil {
				writeSubscriptionError(w, r, err)
				return
			}
			writeCatalogJSON(w, 200, sub)
		case http.MethodPatch:
			actor, ok := catalogPrincipal(w, r, sessions, "subscriptions.write", true)
			if !ok {
				return
			}
			var patch subscription.Patch
			if !decodeCatalogJSON(w, r, &patch) {
				return
			}
			sub, err := store.UpdateOwn(r.Context(), actor.ID, id, patch, requestID(r))
			if err != nil {
				writeSubscriptionError(w, r, err)
				return
			}
			writeCatalogJSON(w, 200, sub)
		case http.MethodDelete:
			actor, ok := catalogPrincipal(w, r, sessions, "subscriptions.write", true)
			if !ok {
				return
			}
			if err := store.DeleteOwn(r.Context(), actor.ID, id, requestID(r)); err != nil {
				writeSubscriptionError(w, r, err)
				return
			}
			w.WriteHeader(204)
		default:
			WriteError(w, r, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	limiter := newLoginLimiter()
	lookupSlots := make(chan struct{}, 8)
	mux.HandleFunc("/sub/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method != http.MethodGet {
			WriteError(w, r, 405, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/sub/")
		token, format, ok := strings.Cut(path, "/")
		if !ok || strings.Contains(format, "/") || format != "mihomo" && format != "sing-box" {
			WriteError(w, r, 404, "NOT_FOUND", "resource not found")
			return
		}
		tokenHash, err := subscription.TokenHash(token)
		if err != nil {
			WriteError(w, r, 404, "NOT_FOUND", "resource not found")
			return
		}
		select {
		case lookupSlots <- struct{}{}:
			defer func() { <-lookupSlots }()
		default:
			w.Header().Set("Retry-After", "1")
			WriteError(w, r, 429, "RATE_LIMITED", "subscription service busy")
			return
		}
		if _, err := store.ResolveToken(r.Context(), token); err != nil {
			writeSubscriptionError(w, r, err)
			return
		}
		if wait := limiter.Allow(tokenHash); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
			WriteError(w, r, 429, "RATE_LIMITED", "too many subscription requests")
			return
		}
		serveSubscriptionExport(w, r, store, token, format)
	})
}
func serveSubscriptionExport(w http.ResponseWriter, r *http.Request, store SubscriptionStore, token, format string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	body, contentType, err := store.Export(r.Context(), token, format)
	if err != nil {
		writeSubscriptionError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
func writeSubscriptionError(w http.ResponseWriter, r *http.Request, err error) {
	var validation subscription.ValidationError
	switch {
	case errors.As(err, &validation):
		WriteError(w, r, 422, "VALIDATION_ERROR", validation.Error())
	case errors.Is(err, subscription.ErrNotFound):
		WriteError(w, r, 404, "NOT_FOUND", "resource not found")
	case errors.Is(err, subscription.ErrUnavailable):
		WriteError(w, r, 503, "SUBSCRIPTION_UNAVAILABLE", "no available proxy accesses")
	case errors.Is(err, subscription.ErrConflict):
		WriteError(w, r, 409, "CONFLICT", "subscription conflict")
	case errors.Is(err, subscription.ErrLimit):
		WriteError(w, r, 409, "PLAN_LIMIT_EXCEEDED", "subscription limit exceeded")
	default:
		WriteError(w, r, 500, "INTERNAL", "internal server error")
	}
}
