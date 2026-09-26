package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"time"

	"controlplane/internal/agentidentity"
	"controlplane/internal/agentproto"
	"controlplane/internal/platform/id"
	"github.com/coder/websocket"
)

func (s *AgentStreamHandler) acceptCertificateUpdate(ctx context.Context, nodeID string,
	original, active *x509.Certificate, payload []byte) (*x509.Certificate, string, error) {
	update, err := agentproto.DecodeCertificateUpdate(payload)
	if err != nil {
		return nil, "", err
	}
	block, rest := pem.Decode([]byte(update.CertificatePEM))
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, "", errors.New("invalid Agent certificate update PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, "", errors.New("invalid Agent certificate update")
	}
	identity, err := agentidentity.CertificateNodeID(certificate)
	if err != nil || identity != nodeID {
		return nil, "", agentidentity.ErrAgentUnauthorized
	}
	originalKey, originalOK := original.PublicKey.(ed25519.PublicKey)
	updatedKey, updatedOK := certificate.PublicKey.(ed25519.PublicKey)
	if !originalOK || !updatedOK || !bytes.Equal(originalKey, updatedKey) {
		return nil, "", agentidentity.ErrAgentUnauthorized
	}
	if certificate.NotAfter.Before(active.NotAfter) ||
		(certificate.NotAfter.Equal(active.NotAfter) && !bytes.Equal(certificate.Raw, active.Raw)) {
		return nil, "", errors.New("Agent certificate update is not newer")
	}
	authCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	activeID, err := s.auth.AuthenticateCertificate(authCtx, active)
	if err != nil {
		return nil, "", err
	}
	if activeID != nodeID {
		return nil, "", agentidentity.ErrAgentUnauthorized
	}
	authorizedID, err := s.auth.AuthenticateCertificate(authCtx, certificate)
	if err != nil {
		return nil, "", err
	}
	if authorizedID != nodeID {
		return nil, "", agentidentity.ErrAgentUnauthorized
	}
	sum := sha256.Sum256(certificate.Raw)
	return certificate, hex.EncodeToString(sum[:]), nil
}

func (s *AgentStreamHandler) sendCertificateUpdateAck(ctx context.Context, connection *websocket.Conn,
	nodeID, fingerprint string) error {
	payload, err := json.Marshal(agentproto.CertificateUpdateAck{Fingerprint: fingerprint})
	if err != nil {
		return err
	}
	messageID, err := id.NewV7()
	if err != nil {
		return err
	}
	frame, err := agentproto.Encode(agentproto.Envelope{ProtocolVersion: agentproto.ProtocolVersion,
		MessageID: messageID, NodeID: nodeID, Type: agentproto.TypeCertificateUpdateAck,
		SentAt: time.Now().UTC(), Payload: payload})
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return connection.Write(writeCtx, websocket.MessageText, frame)
}
