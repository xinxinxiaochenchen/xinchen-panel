package routing

import "testing"

func TestRuleCursorRetainsPositionWithoutLookingUpRule(t *testing.T) {
	rule := Rule{ID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423", Priority: 42}
	priority, id, err := ParseRuleCursor(RuleCursor(rule))
	if err != nil || priority != 42 || id != rule.ID {
		t.Fatalf("cursor = %d %q %v", priority, id, err)
	}
	if _, _, err := ParseRuleCursor("42:bad-id"); err == nil {
		t.Fatal("accepted malformed cursor")
	}
}
