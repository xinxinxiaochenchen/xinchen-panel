package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"controlplane/internal/agentidentity"
	"controlplane/internal/catalog"
	"controlplane/internal/identity"
)

type AgentTokenStore interface {
	CreateToken(context.Context, string, string, string) (agentidentity.EnrollmentToken, error)
	RevokeAgent(context.Context, string, string, string) error
}

type agentReauthenticator interface {
	VerifyPassword(context.Context, string, string) (identity.PublicUser, error)
}

func registerAgentTokenRoutes(mux *http.ServeMux, sessions IdentitySessions, store AgentTokenStore) {
	limiter := newLoginLimiter()
	mux.HandleFunc("POST /api/v1/admin/nodes/{id}/agent-enrollment", func(w http.ResponseWriter, r *http.Request) {
		user, ok := catalogPrincipal(w, r, sessions, "agents.write", true)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		var input struct {
			Password string `json:"password"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF || input.Password == "" {
			WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "password required")
			return
		}
		if wait := limiter.Allow(user.Email); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
			WriteError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts")
			return
		}
		verifier, ok := sessions.(agentReauthenticator)
		if !ok {
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
			return
		}
		cookie, _ := r.Cookie(sessionCookieName)
		verified, err := verifier.VerifyPassword(r.Context(), cookie.Value, input.Password)
		if errors.Is(err, identity.ErrInvalidCredentials) || errors.Is(err, identity.ErrUnauthenticated) {
			WriteError(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid credentials")
			return
		}
		if err != nil || verified.ID != user.ID {
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
			return
		}
		result, err := store.CreateToken(r.Context(), r.PathValue("id"), user.ID, requestID(r))
		if errors.Is(err, agentidentity.ErrNodeNotFound) {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "node not found")
			return
		}
		if err != nil {
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(struct {
			Token     string    `json:"token"`
			ExpiresAt time.Time `json:"expires_at"`
		}{Token: result.Token, ExpiresAt: result.ExpiresAt})
	})
	mux.HandleFunc("PATCH /api/v1/admin/nodes/{id}/agent", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := catalogPrincipal(w, r, sessions, "agents.write", true)
		if !ok {
			return
		}
		nodeID := r.PathValue("id")
		if !catalog.ValidID(nodeID) {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "node not found")
			return
		}
		var input struct {
			Status string `json:"status"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
			WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid Agent status request")
			return
		}
		if input.Status != "revoked" {
			WriteError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "only Agent revocation is supported")
			return
		}
		if err := store.RevokeAgent(r.Context(), nodeID, actor.ID, requestID(r)); err != nil {
			if errors.Is(err, agentidentity.ErrNodeNotFound) {
				WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Agent not found")
				return
			}
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
}
