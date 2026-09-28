package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"controlplane/internal/identity"
)

const (
	sessionCookieName = "__Host-control_session"
	csrfCookieName    = "__Host-control_csrf"
)

type IdentitySessions interface {
	LoginWithRequestID(context.Context, string, string, string) (identity.LoginResult, error)
	Authenticate(context.Context, string) (identity.PublicUser, error)
	VerifyCSRF(context.Context, string, string) (identity.PublicUser, error)
	Logout(context.Context, string, string) error
	LogoutWithRequestID(context.Context, string, string, string) error
}

func registerIdentityRoutes(mux *http.ServeMux, sessions IdentitySessions) {
	limiter := newLoginLimiter()
	mux.HandleFunc("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		var input struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF || input.Email == "" || input.Password == "" {
			WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid login request")
			return
		}
		if wait := limiter.Allow(input.Email); wait > 0 {
			seconds := int((wait + time.Second - 1) / time.Second)
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			WriteError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "too many login attempts")
			return
		}
		result, err := sessions.LoginWithRequestID(r.Context(), input.Email, input.Password, requestID(r))
		if errors.Is(err, identity.ErrInvalidCredentials) {
			WriteError(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid credentials")
			return
		}
		if err != nil {
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
			return
		}
		setIdentityCookie(w, sessionCookieName, result.Token, true, result.ExpiresAt)
		setIdentityCookie(w, csrfCookieName, result.CSRFToken, false, result.ExpiresAt)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			User      identity.PublicUser `json:"user"`
			CSRFToken string              `json:"csrf_token"`
			ExpiresAt time.Time           `json:"expires_at"`
		}{User: result.User, CSRFToken: result.CSRFToken, ExpiresAt: result.ExpiresAt})
	})
	mux.HandleFunc("/api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		if err := sessions.LogoutWithRequestID(r.Context(), cookie.Value, r.Header.Get("X-CSRF-Token"), requestID(r)); err != nil {
			writeIdentityError(w, r, err)
			return
		}
		clearIdentityCookie(w, sessionCookieName, true)
		clearIdentityCookie(w, csrfCookieName, false)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		user, ok := authenticatedUser(w, r, sessions)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(user)
	})
	mux.HandleFunc("/api/v1/me/permissions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		user, ok := authenticatedUser(w, r, sessions)
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Roles       []string `json:"roles"`
			Permissions []string `json:"permissions"`
		}{Roles: user.Roles, Permissions: user.Permissions})
	})
}

func authenticatedUser(w http.ResponseWriter, r *http.Request, sessions IdentitySessions) (identity.PublicUser, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return identity.PublicUser{}, false
	}
	user, err := sessions.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		writeIdentityError(w, r, err)
		return identity.PublicUser{}, false
	}
	return user, true
}

func writeIdentityError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
	case errors.Is(err, identity.ErrInvalidCSRF):
		WriteError(w, r, http.StatusForbidden, "INVALID_CSRF", "invalid CSRF token")
	default:
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
	}
}

func setIdentityCookie(w http.ResponseWriter, name, value string, httpOnly bool, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true,
		HttpOnly: httpOnly, SameSite: http.SameSiteLaxMode, Expires: expires})
}

func clearIdentityCookie(w http.ResponseWriter, name string, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/", Secure: true,
		HttpOnly: httpOnly, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}
