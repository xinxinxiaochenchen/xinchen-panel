package subscription

import (
	"fmt"

	"controlplane/internal/orchestration"
	"controlplane/internal/subscriptionconfig"
)

type exportTarget struct {
	Target   subscriptionconfig.Target
	Priority int
	Weight   int
	RankID   string
}

// orderExportTargets applies the server's line priority/weight policy to the
// deterministic order emitted in a subscription. Client formats do not expose
// the control-plane selector, so this order is the portable representation of
// the policy; targets on the same line retain their configured order.
func orderExportTargets(connectionID string, values []exportTarget) ([]subscriptionconfig.Target, error) {
	if connectionID == "" {
		return nil, fmt.Errorf("subscription ID is required")
	}
	byLine := make(map[string][]exportTarget, len(values))
	candidates := make([]orchestration.LineCandidate, 0, len(values))
	for _, value := range values {
		key := value.Target.LineID
		if value.RankID != "" {
			key = "AUTO:" + value.RankID
		}
		if key == "" {
			return nil, fmt.Errorf("subscription target has no ranking line")
		}
		byLine[key] = append(byLine[key], value)
	}
	for line, items := range byLine {
		if len(items) == 0 {
			continue
		}
		candidates = append(candidates, orchestration.LineCandidate{ID: line, Priority: items[0].Priority, Weight: items[0].Weight, Healthy: true})
	}
	ranked, err := orchestration.RankHealthyLines(connectionID, candidates)
	if err != nil {
		return nil, fmt.Errorf("rank subscription lines: %w", err)
	}
	ordered := make([]subscriptionconfig.Target, 0, len(values))
	for _, candidate := range ranked {
		for _, value := range byLine[candidate.ID] {
			ordered = append(ordered, value.Target)
		}
	}
	return ordered, nil
}
