package httpapi

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"controlplane/internal/agentidentity"
)

type AgentRelayCertificate interface {
	RelayCertificate(context.Context, *x509.Certificate, []byte, string) (agentidentity.EnrollmentResult, error)
}

func relayCertificateHandler(logger *slog.Logger, service AgentEnrollment) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		issuer, ok := service.(AgentRelayCertificate)
		if !ok {
			WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "relay certificate unavailable")
			return
		}
		var input struct {
			CSRPEM            string `json:"csr_pem"`
			ParentFingerprint string `json:"parent_fingerprint"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF || input.CSRPEM == "" {
			WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid relay certificate request")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		result, err := issuer.RelayCertificate(ctx, r.TLS.PeerCertificates[0], []byte(input.CSRPEM), input.ParentFingerprint)
		switch {
		case errors.Is(err, agentidentity.ErrAgentUnauthorized):
			WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Agent certificate denied")
			return
		case errors.Is(err, agentidentity.ErrRelayUnavailable):
			WriteError(w, r, http.StatusConflict, "RELAY_UNAVAILABLE", "node relay unavailable")
			return
		case errors.Is(err, agentidentity.ErrInvalidCSR):
			WriteError(w, r, http.StatusBadRequest, "INVALID_CSR", "invalid relay certificate request")
			return
		case errors.Is(err, agentidentity.ErrRenewalTooEarly):
			WriteError(w, r, http.StatusConflict, "RENEWAL_TOO_EARLY", "relay certificate renewal is too early")
			return
		case errors.Is(err, agentidentity.ErrRenewalGrantLimit):
			WriteError(w, r, http.StatusTooManyRequests, "RENEWAL_LIMIT", "relay certificate renewal limit reached")
			return
		case err != nil:
			logger.Error("relay certificate issue failed", "error", err)
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			NodeID           string    `json:"node_id"`
			CertificatePEM   string    `json:"certificate_pem"`
			CACertificatePEM string    `json:"ca_certificate_pem"`
			Fingerprint      string    `json:"fingerprint"`
			ExpiresAt        time.Time `json:"expires_at"`
		}{result.NodeID, string(result.CertificatePEM), string(result.CACertificatePEM), result.Fingerprint, result.ExpiresAt})
	}
}
