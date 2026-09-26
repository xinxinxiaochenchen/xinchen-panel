package orchestration

import (
	"context"

	"controlplane/internal/agentproto"
	"controlplane/internal/billing"
)

type UsageStore interface {
	AgentIDForNode(context.Context, string) (string, error)
	RecordUsage(context.Context, string, billing.UsageReport) (billing.UsageEvent, error)
}

type UsageRecorder struct{ store UsageStore }

func NewUsageRecorder(store UsageStore) *UsageRecorder { return &UsageRecorder{store: store} }

// RecordBatch always uses the mTLS-authenticated node, then the billing
// repository verifies that every frozen connection belongs to its Agent.
// A failed later report leaves the earlier entries committed; replaying the
// entire batch is safe because RecordUsage is idempotent by connection/sequence.
func (r *UsageRecorder) RecordBatch(ctx context.Context, nodeID string, batch agentproto.UsageBatch) error {
	agentID, err := r.store.AgentIDForNode(ctx, nodeID)
	if err != nil {
		return err
	}
	for _, report := range batch.Reports {
		_, err := r.store.RecordUsage(ctx, agentID, billing.UsageReport{
			ConnectionID: report.ConnectionID, LeaseID: report.LeaseID, Sequence: report.Sequence,
			Counters:   billing.Counters{UploadedBytes: report.UploadedBytes, DownloadedBytes: report.DownloadedBytes},
			ObservedAt: report.ObservedAt,
		})
		if err != nil {
			return err
		}
	}
	return nil
}
