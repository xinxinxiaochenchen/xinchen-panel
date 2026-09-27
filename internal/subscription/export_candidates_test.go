package subscription

import (
	"testing"

	"controlplane/internal/entitlement"
	"controlplane/internal/subscriptionconfig"
)

func TestSelectEligibleExportTargetsKeepsOneAutomaticPoolTarget(t *testing.T) {
	grant := entitlement.Snapshot{ResourceGroupIDs: []string{"group"}, LineIDs: []string{"fallback"}}
	grant.Limits.MaxHops = 1
	rows := []candidateExportRow{
		{Target: subscriptionconfig.Target{ID: "access", Name: "Japan", LineID: "default", LineName: "Default"}, ConfiguredCount: 2, PoolApplied: true, LineOwner: "", Groups: []string{"group"}, Priority: 10, Weight: 1},
		{Target: subscriptionconfig.Target{ID: "access", Name: "Japan", LineID: "fallback", LineName: "Fallback"}, ConfiguredCount: 2, PoolApplied: true, LineOwner: "", Groups: []string{"group"}, Priority: 20, Weight: 3},
	}
	selected := selectEligibleExportTargets("user", grant, rows)
	if len(selected) != 1 || selected[0].Target.ID != "access" || selected[0].Target.LineID != "" || selected[0].Target.LineName != "自动线路" || selected[0].RankID != "access" || selected[0].Priority != 20 {
		t.Fatalf("automatic target = %+v", selected)
	}
}

func TestSelectEligibleExportTargetsLabelsLegacyAgentAsSingleLine(t *testing.T) {
	grant := entitlement.Snapshot{ResourceGroupIDs: []string{"group"}, LineIDs: []string{"default"}}
	grant.Limits.MaxHops = 1
	rows := []candidateExportRow{{Target: subscriptionconfig.Target{ID: "access", LineID: "default", LineName: "Default"},
		ConfiguredCount: 2, PoolApplied: false, Groups: []string{"group"}, Weight: 1}}
	selected := selectEligibleExportTargets("user", grant, rows)
	if len(selected) != 1 || selected[0].Target.LineID != "default" || selected[0].Target.LineName != "Default" {
		t.Fatalf("legacy Agent target mislabeled: %+v", selected)
	}
}

func TestSelectEligibleExportTargetsPreservesSingleLineRouting(t *testing.T) {
	grant := entitlement.Snapshot{ResourceGroupIDs: []string{"group"}, LineIDs: []string{"line"}}
	grant.Limits.MaxHops = 1
	rows := []candidateExportRow{{Target: subscriptionconfig.Target{ID: "access", LineID: "line", LineName: "Line"}, ConfiguredCount: 1, Groups: []string{"group"}, Weight: 1}}
	selected := selectEligibleExportTargets("user", grant, rows)
	if len(selected) != 1 || selected[0].Target.LineID != "line" {
		t.Fatalf("single-line target = %+v", selected)
	}
}
