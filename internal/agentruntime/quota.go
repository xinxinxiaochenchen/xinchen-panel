package agentruntime

import (
	"context"
	"io"
	"time"

	"controlplane/internal/agentmeter"
)

// TrafficMeter opens one first-ingress metered connection. Implementations
// persist usage and manage quota leases; the runtime only moves payload bytes.
type TrafficMeter interface {
	Open(context.Context, string, string, uint64) (MeteredConnection, error)
}

type MeteredConnection interface {
	Copy(io.Writer, io.Reader, agentmeter.Direction) (int64, error)
	WritePacket(io.Writer, []byte, agentmeter.Direction) (int, error)
	Close() error
}

// QuotaService is supplied by the authenticated Agent stream. The runtime
// knows resources and effective payload bytes, but never chooses a user,
// billing period or multiplier.
type QuotaService interface {
	Open(context.Context, QuotaOpenRequest) (QuotaLease, error)
	Renew(context.Context, QuotaRenewRequest) (QuotaLease, error)
	Settle(context.Context, QuotaSettleRequest) (QuotaSettlement, error)
}

type QuotaOpenRequest struct {
	RequestID      string
	ConnectionID   string
	ResourceKind   string
	ResourceID     string
	Revision       int64
	RequestedBytes int64
}

type QuotaRenewRequest struct {
	RequestID      string
	ConnectionID   string
	Revision       int64
	RequestedBytes int64
}

type QuotaSettleRequest struct {
	RequestID     string
	ConnectionID  string
	LeaseID       string
	ConsumedBytes int64
}

type QuotaLease struct {
	RequestID       string
	ConnectionID    string
	PeriodID        string
	MultiplierMilli int64
	ID              string
	GrantedBytes    int64
	ConsumedBytes   int64
	IssuedAt        time.Time
	ExpiresAt       time.Time
	PeriodEndsAt    time.Time
}

type QuotaSettlement struct {
	RequestID     string
	ConnectionID  string
	LeaseID       string
	ConsumedBytes int64
}
