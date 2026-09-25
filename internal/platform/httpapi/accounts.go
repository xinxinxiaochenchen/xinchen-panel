package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"controlplane/internal/identity"
)

type AccountStore interface {
	CreateMember(context.Context, identity.MemberInput, string, string) (identity.PublicUser, error)
	ListUsers(context.Context, int, string) ([]identity.PublicUser, error)
	ChangePassword(context.Context, string, string, string, string) error
}

func registerAccountRoutes(mux *http.ServeMux, sessions IdentitySessions, store AccountStore) {
	passwordLimiter := newLoginLimiter()
	mux.HandleFunc("/api/v1/admin/users", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, ok := catalogPrincipal(w, r, sessions, "users.read", false); !ok {
				return
			}
			limit, after, ok := catalogPageParams(w, r, false)
			if !ok {
				return
			}
			users, err := store.ListUsers(r.Context(), limit+1, after)
			if err != nil {
				writeAccountError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusOK, catalogPage(users, limit, func(user identity.PublicUser) string { return user.ID }))
		case http.MethodPost:
			actor, ok := catalogPrincipal(w, r, sessions, "users.write", true)
			if !ok {
				return
			}
			var input identity.NewUser
			if !decodeCatalogJSON(w, r, &input) {
				return
			}
			normalized, err := identity.NormalizeNewUser(input)
			if err != nil {
				writeAccountError(w, r, err)
				return
			}
			user, err := store.CreateMember(r.Context(), normalized, actor.ID, requestID(r))
			if err != nil {
				writeAccountError(w, r, err)
				return
			}
			writeCatalogJSON(w, http.StatusCreated, user)
		default:
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/me/password", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		user, ok := catalogPrincipal(w, r, sessions, "", true)
		if !ok {
			return
		}
		if wait := passwordLimiter.Allow(user.ID); wait > 0 {
			seconds := int((wait + time.Second - 1) / time.Second)
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			WriteError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "too many password change attempts")
			return
		}
		var input struct {
			OldPassword string `json:"old_password"`
			NewPassword string `json:"new_password"`
		}
		if !decodeCatalogJSON(w, r, &input) {
			return
		}
		if err := identity.ValidatePasswordChange(input.OldPassword, input.NewPassword); err != nil {
			writeAccountError(w, r, err)
			return
		}
		if err := store.ChangePassword(r.Context(), user.ID, input.OldPassword, input.NewPassword, requestID(r)); err != nil {
			writeAccountError(w, r, err)
			return
		}
		clearIdentityCookie(w, sessionCookieName, true)
		clearIdentityCookie(w, csrfCookieName, false)
		w.WriteHeader(http.StatusNoContent)
	})
}

func writeAccountError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrInvalidInput):
		WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "invalid account input")
	case errors.Is(err, identity.ErrAlreadyExists):
		WriteError(w, r, http.StatusConflict, "CONFLICT", "user already exists")
	case errors.Is(err, identity.ErrInvalidCredentials):
		WriteError(w, r, http.StatusForbidden, "INVALID_CREDENTIALS", "invalid current password")
	case errors.Is(err, identity.ErrUnauthenticated):
		WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
	case errors.Is(err, identity.ErrNotFound):
		WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
	default:
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
	}
}
