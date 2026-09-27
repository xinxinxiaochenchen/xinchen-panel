package subscription

import (
	"testing"

	"controlplane/internal/subscriptionconfig"
)

func TestOrderExportTargetsUsesPriorityAndStableWeights(t *testing.T) {
	values := []exportTarget{
		{Target: subscriptionconfig.Target{ID: "hk-a", LineID: "hk"}, Priority: 10, Weight: 1},
		{Target: subscriptionconfig.Target{ID: "jp", LineID: "jp"}, Priority: 10, Weight: 5},
		{Target: subscriptionconfig.Target{ID: "hk-b", LineID: "hk"}, Priority: 10, Weight: 1},
		{Target: subscriptionconfig.Target{ID: "us", LineID: "us"}, Priority: 20, Weight: 100},
	}
	first, err := orderExportTargets("subscription-123", values)
	if err != nil {
		t.Fatal(err)
	}
	again, err := orderExportTargets("subscription-123", []exportTarget{values[3], values[1], values[0], values[2]})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(again) {
		t.Fatalf("ordered lengths differ: %d and %d", len(first), len(again))
	}
	for i := range first {
		if first[i].ID != again[i].ID {
			t.Fatalf("unstable order: first=%+v again=%+v", first, again)
		}
	}
	if first[0].LineID != "hk" || first[1].LineID != "hk" || first[0].ID != "hk-a" || first[1].ID != "hk-b" || first[2].LineID != "jp" || first[3].LineID != "us" {
		t.Fatalf("unexpected weighted order: %+v", first)
	}
}

func TestOrderExportTargetsRejectsInvalidLineWeight(t *testing.T) {
	_, err := orderExportTargets("subscription-123", []exportTarget{{Target: subscriptionconfig.Target{ID: "bad", LineID: "line"}, Priority: 1, Weight: 0}})
	if err == nil {
		t.Fatal("invalid line weight accepted")
	}
}
