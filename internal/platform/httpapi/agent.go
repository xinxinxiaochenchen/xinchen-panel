package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"controlplane/internal/agentidentity"
)

type AgentEnrollment interface {
	Enroll(context.Context, string, []byte, string) (agentidentity.EnrollmentResult, error)
}

type AgentRenewal interface {
	Renew(context.Context, *x509.Certificate, []byte, string) (agentidentity.EnrollmentResult, error)
}

// NewAgentHandler is mounted only on the dedicated TLS listener. The direct
// TLS check also protects against accidentally mounting it on the HTTP preview.
func NewAgentHandler(logger *slog.Logger, service AgentEnrollment) http.Handler {
	return NewAgentHandlerWithStream(logger, service, nil)
}

func NewAgentHandlerWithStream(logger *slog.Logger, service AgentEnrollment, stream http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/relay-certificate", relayCertificateHandler(logger, service))
	if stream != nil {
		mux.Handle("/api/v1/agent/stream", stream)
	}
	mux.HandleFunc("/api/v1/agent/renew", func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		if r.Method != http.MethodPost {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) != 1 {
			WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Agent certificate required")
			return
		}
		renewer, ok := service.(AgentRenewal)
		if !ok {
			WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "Agent renewal unavailable")
			return
		}
		var input struct {
			CSRPEM  string `json:"csr_pem"`
			Version string `json:"version"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
			input.CSRPEM == "" || input.Version == "" {
			WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid Agent renewal request")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		result, err := renewer.Renew(ctx, r.TLS.PeerCertificates[0], []byte(input.CSRPEM), input.Version)
		switch {
		case errors.Is(err, agentidentity.ErrAgentUnauthorized):
			WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Agent certificate denied")
			return
		case errors.Is(err, agentidentity.ErrInvalidCSR):
			WriteError(w, r, http.StatusBadRequest, "INVALID_CSR", "invalid Agent certificate request")
			return
		case errors.Is(err, agentidentity.ErrRenewalTooEarly):
			WriteError(w, r, http.StatusConflict, "RENEWAL_TOO_EARLY", "Agent certificate renewal is too early")
			return
		case errors.Is(err, agentidentity.ErrRenewalGrantLimit):
			WriteError(w, r, http.StatusTooManyRequests, "RENEWAL_LIMIT", "Agent certificate renewal limit reached")
			return
		case err != nil:
			logger.Error("Agent renewal failed", "error", err)
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			NodeID           string    `json:"node_id"`
			CertificatePEM   string    `json:"certificate_pem"`
			CACertificatePEM string    `json:"ca_certificate_pem"`
			ExpiresAt        time.Time `json:"expires_at"`
		}{NodeID: result.NodeID, CertificatePEM: string(result.CertificatePEM),
			CACertificatePEM: string(result.CACertificatePEM), ExpiresAt: result.ExpiresAt})
	})
	ipLimiter := newLoginLimiter()
	tokenLimiter := newLoginLimiter()
	mux.HandleFunc("/api/v1/agent/enroll", func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		if r.Method != http.MethodPost {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		var input struct {
			Token   string `json:"token"`
			CSRPEM  string `json:"csr_pem"`
			Version string `json:"version"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
			input.Token == "" || input.CSRPEM == "" || input.Version == "" {
			WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid Agent enrollment request")
			return
		}
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		tokenHash := sha256.Sum256([]byte(input.Token))
		wait := ipLimiter.Allow(ip)
		if tokenWait := tokenLimiter.Allow(hex.EncodeToString(tokenHash[:])); tokenWait > wait {
			wait = tokenWait
		}
		if wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
			WriteError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "too many Agent enrollment attempts")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		result, err := service.Enroll(ctx, input.Token, []byte(input.CSRPEM), input.Version)
		if errors.Is(err, agentidentity.ErrInvalidEnrollment) {
			WriteError(w, r, http.StatusUnauthorized, "INVALID_ENROLLMENT", "invalid or expired Agent enrollment")
			return
		}
		if errors.Is(err, agentidentity.ErrInvalidCSR) {
			WriteError(w, r, http.StatusBadRequest, "INVALID_CSR", "invalid Agent certificate request")
			return
		}
		if err != nil {
			logger.Error("Agent enrollment failed", "error", err)
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(struct {
			NodeID           string    `json:"node_id"`
			CertificatePEM   string    `json:"certificate_pem"`
			CACertificatePEM string    `json:"ca_certificate_pem"`
			ExpiresAt        time.Time `json:"expires_at"`
		}{NodeID: result.NodeID, CertificatePEM: string(result.CertificatePEM),
			CACertificatePEM: string(result.CACertificatePEM), ExpiresAt: result.ExpiresAt})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
	})
	return withMiddleware(logger, mux)
}
