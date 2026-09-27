package subscription

import (
	"controlplane/internal/entitlement"
	"testing"
)

func TestMultiHopTargetRequiresEveryGroupAndHopLimit(t *testing.T) {
	grant := entitlement.Snapshot{ResourceGroupIDs: []string{"one", "two"}, LineIDs: []string{"line"}}
	grant.Limits.MaxHops = 2
	if !lineAllowedHops("user", "line", "", []string{"one", "two"}, grant) {
		t.Fatal("allowed route rejected")
	}
	if lineAllowedHops("user", "line", "", []string{"one", "three"}, grant) {
		t.Fatal("ungranted hop allowed")
	}
	if lineAllowedHops("user", "line", "", []string{"one", "two", "one"}, grant) {
		t.Fatal("over-limit route allowed")
	}
}
