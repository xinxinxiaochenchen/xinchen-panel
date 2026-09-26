package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"controlplane/internal/agentproto"
	"controlplane/internal/billing"
	"controlplane/internal/platform/id"
	"github.com/coder/websocket"
)

func (s *AgentStreamHandler) handleQuotaMessage(ctx context.Context, connection *websocket.Conn, nodeID string, envelope agentproto.Envelope) error {
	if s.quota == nil {
		return errors.New("Agent quota admission is unavailable")
	}
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch envelope.Type {
	case agentproto.TypeConnectionOpen:
		value, err := agentproto.DecodeConnectionOpen(envelope.Payload)
		if err != nil {
			return err
		}
		grant, err := s.quota.OpenConnection(queryCtx, nodeID, billing.OpenRequest{ConnectionID: value.ConnectionID, RequestID: value.RequestID,
			ResourceKind: value.ResourceKind, ResourceID: value.ResourceID, Revision: value.Revision, RequestedBytes: value.RequestedBytes})
		return s.respondQuotaGrant(ctx, connection, nodeID, value.RequestID, value.ConnectionID, grant, err)
	case agentproto.TypeQuotaRequest:
		value, err := agentproto.DecodeQuotaRequest(envelope.Payload)
		if err != nil {
			return err
		}
		grant, err := s.quota.RenewConnectionLease(queryCtx, nodeID, billing.RenewRequest{ConnectionID: value.ConnectionID, RequestID: value.RequestID,
			Revision: value.Revision, RequestedBytes: value.RequestedBytes})
		return s.respondQuotaGrant(ctx, connection, nodeID, value.RequestID, value.ConnectionID, grant, err)
	case agentproto.TypeQuotaSettle:
		value, err := agentproto.DecodeQuotaSettle(envelope.Payload)
		if err != nil {
			return err
		}
		lease, err := s.quota.SettleConnectionLease(queryCtx, nodeID, value.ConnectionID, value.LeaseID, value.ConsumedBytes)
		if err != nil {
			return s.sendQuotaDenied(ctx, connection, nodeID, value.RequestID, value.ConnectionID, err)
		}
		return s.sendAgentResponse(ctx, connection, nodeID, agentproto.TypeQuotaSettled,
			agentproto.QuotaSettled{RequestID: value.RequestID, ConnectionID: value.ConnectionID, LeaseID: lease.ID, ConsumedBytes: lease.ConsumedBytes})
	default:
		return errors.New("unsupported Agent quota message")
	}
}

func (s *AgentStreamHandler) respondQuotaGrant(ctx context.Context, connection *websocket.Conn, nodeID, requestID, connectionID string,
	grant billing.AdmissionGrant, err error) error {
	if err != nil {
		return s.sendQuotaDenied(ctx, connection, nodeID, requestID, connectionID, err)
	}
	return s.sendAgentResponse(ctx, connection, nodeID, agentproto.TypeQuotaGrant, agentproto.QuotaGrant{
		RequestID: requestID, ConnectionID: connectionID, PeriodID: grant.Lease.PeriodID,
		MultiplierMilli: grant.MultiplierMilli, LeaseID: grant.Lease.ID,
		GrantedBytes: grant.Lease.GrantedBytes, ConsumedBytes: grant.Lease.ConsumedBytes, LeaseState: grant.Lease.State,
		IssuedAt: grant.Lease.IssuedAt, ExpiresAt: grant.Lease.ExpiresAt, PeriodEndsAt: grant.PeriodEndsAt,
	})
}

func quotaDenialCode(err error) string {
	switch {
	case errors.Is(err, billing.ErrQuotaExhausted):
		return "QUOTA_EXHAUSTED"
	case errors.Is(err, billing.ErrNotFound), errors.Is(err, billing.ErrLeaseClosed):
		return "NOT_AUTHORIZED"
	case errors.Is(err, billing.ErrConflict), errors.Is(err, billing.ErrInvalidMeter):
		return "CONFLICT"
	default:
		return "RETRY_LATER"
	}
}

func (s *AgentStreamHandler) sendQuotaDenied(ctx context.Context, connection *websocket.Conn, nodeID, requestID, connectionID string, cause error) error {
	code := quotaDenialCode(cause)
	if code == "RETRY_LATER" {
		s.logger.Warn("Agent quota request failed", "node_id", nodeID, "error", cause)
	}
	return s.sendAgentResponse(ctx, connection, nodeID, agentproto.TypeQuotaDenied,
		agentproto.QuotaDenied{RequestID: requestID, ConnectionID: connectionID, ErrorCode: code})
}

func (s *AgentStreamHandler) sendAgentResponse(ctx context.Context, connection *websocket.Conn, nodeID string, kind agentproto.MessageType, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	messageID, err := id.NewV7()
	if err != nil {
		return err
	}
	frame, err := agentproto.Encode(agentproto.Envelope{ProtocolVersion: agentproto.ProtocolVersion,
		MessageID: messageID, NodeID: nodeID, Type: kind, SentAt: time.Now().UTC(), Payload: payload})
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return connection.Write(writeCtx, websocket.MessageText, frame)
}
