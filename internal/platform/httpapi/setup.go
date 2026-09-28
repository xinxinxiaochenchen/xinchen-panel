package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"controlplane/internal/identity"
)

type SetupStore interface {
	Required(context.Context) (bool, error)
	Create(context.Context, string, string, string) (identity.PublicUser, bool, error)
}

func registerSetupRoutes(mux *http.ServeMux, store SetupStore, token string) {
	limiter := newLoginLimiter()
	mux.HandleFunc("/api/v1/setup", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		if r.Method == http.MethodPost {
			peer, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				peer = "unknown"
			}
			if wait := limiter.Allow(peer); wait > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
				WriteError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "too many initialization attempts")
				return
			}
		}
		required, err := store.Required(r.Context())
		if err != nil {
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "initialization state unavailable")
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				Required bool `json:"required"`
				Enabled  bool `json:"enabled"`
			}{required, required && token != ""})
			return
		}
		if !required {
			WriteError(w, r, http.StatusConflict, "SETUP_COMPLETE", "administrator already initialized")
			return
		}
		if token == "" {
			WriteError(w, r, http.StatusServiceUnavailable, "SETUP_UNAVAILABLE", "initialization credential unavailable")
			return
		}
		var input struct {
			Token    string `json:"token"`
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
			WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid initialization request")
			return
		}
		if subtle.ConstantTimeCompare([]byte(input.Token), []byte(token)) != 1 {
			WriteError(w, r, http.StatusForbidden, "INVALID_SETUP_TOKEN", "invalid initialization credential")
			return
		}
		if identity.ValidateAdminCredentials(input.Email, input.Password) != nil {
			WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "valid email and a 12 to 72 byte password required")
			return
		}
		_, created, err := store.Create(r.Context(), input.Email, input.Password, requestID(r))
		switch {
		case errors.Is(err, identity.ErrInvalidInput):
			WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid administrator input")
		case errors.Is(err, identity.ErrAlreadyExists):
			WriteError(w, r, http.StatusConflict, "ALREADY_EXISTS", "email already in use")
		case err != nil:
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "administrator initialization failed")
		case !created:
			WriteError(w, r, http.StatusConflict, "SETUP_COMPLETE", "administrator already initialized")
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]bool{"initialized": true})
		}
	})
}
