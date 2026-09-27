package subscription

import (
	"controlplane/internal/entitlement"
	"controlplane/internal/subscriptionconfig"
)

// candidateExportRow is one currently executable line for a proxy access.
// Rows are ordered by subscription position, candidate priority and position.
type candidateExportRow struct {
	Target          subscriptionconfig.Target
	ConfiguredCount int
	PoolApplied     bool
	LineOwner       string
	Groups          []string
	Priority        int
	Weight          int
	Sealed          string
	RankID          string
}

func selectEligibleExportTargets(owner string, grant entitlement.Snapshot, rows []candidateExportRow) []candidateExportRow {
	selected := make([]candidateExportRow, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, exists := seen[row.Target.ID]; exists || !lineAllowedHops(owner, row.Target.LineID, row.LineOwner, row.Groups, grant) {
			continue
		}
		seen[row.Target.ID] = struct{}{}
		if row.ConfiguredCount > 1 && row.PoolApplied {
			// The client selects the credential and ingress. The Agent selects the
			// actual line for each connection, so a pool is not a selectable line.
			row.Target.LineID = ""
			row.Target.LineName = "自动线路"
			row.RankID = row.Target.ID
		}
		selected = append(selected, row)
	}
	return selected
}
