package agentclient

import (
	"context"

	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
)

type runtimeQuotaService struct{ exchange *QuotaExchange }

func NewRuntimeQuotaService(exchange *QuotaExchange) agentruntime.QuotaService {
	return runtimeQuotaService{exchange: exchange}
}

func (s runtimeQuotaService) Open(ctx context.Context, req agentruntime.QuotaOpenRequest) (agentruntime.QuotaLease, error) {
	grant, err := s.exchange.Open(ctx, agentproto.ConnectionOpen{
		RequestID: req.RequestID, ConnectionID: req.ConnectionID, ResourceKind: req.ResourceKind,
		ResourceID: req.ResourceID, Revision: req.Revision, RequestedBytes: req.RequestedBytes,
	})
	if err != nil {
		return agentruntime.QuotaLease{}, err
	}
	return runtimeLease(grant), nil
}

func (s runtimeQuotaService) Renew(ctx context.Context, req agentruntime.QuotaRenewRequest) (agentruntime.QuotaLease, error) {
	grant, err := s.exchange.Renew(ctx, agentproto.QuotaRequest{
		RequestID: req.RequestID, ConnectionID: req.ConnectionID, Revision: req.Revision, RequestedBytes: req.RequestedBytes,
	})
	if err != nil {
		return agentruntime.QuotaLease{}, err
	}
	return runtimeLease(grant), nil
}

func (s runtimeQuotaService) Settle(ctx context.Context, req agentruntime.QuotaSettleRequest) (agentruntime.QuotaSettlement, error) {
	settled, err := s.exchange.Settle(ctx, agentproto.QuotaSettle{
		RequestID: req.RequestID, ConnectionID: req.ConnectionID, LeaseID: req.LeaseID, ConsumedBytes: req.ConsumedBytes,
	})
	if err != nil {
		return agentruntime.QuotaSettlement{}, err
	}
	return agentruntime.QuotaSettlement{
		RequestID: settled.RequestID, ConnectionID: settled.ConnectionID,
		LeaseID: settled.LeaseID, ConsumedBytes: settled.ConsumedBytes,
	}, nil
}

func runtimeLease(grant agentproto.QuotaGrant) agentruntime.QuotaLease {
	return agentruntime.QuotaLease{
		RequestID: grant.RequestID, ConnectionID: grant.ConnectionID, PeriodID: grant.PeriodID,
		MultiplierMilli: grant.MultiplierMilli, ID: grant.LeaseID, GrantedBytes: grant.GrantedBytes,
		ConsumedBytes: grant.ConsumedBytes, IssuedAt: grant.IssuedAt, ExpiresAt: grant.ExpiresAt,
		PeriodEndsAt: grant.PeriodEndsAt,
	}
}
