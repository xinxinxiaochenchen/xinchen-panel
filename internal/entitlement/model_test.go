package entitlement

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLegacySnapshotKeepsBusinessLimitsWithoutRemovedRoutingLimit(t *testing.T) {
	var snapshot Snapshot
	err := json.Unmarshal([]byte(`{"plan_name":"Existing plan","quota_bytes":1000000,"resource_group_ids":["group"],"line_ids":["line"],"limits":{"max_subscriptions":3,"max_hops":2,"max_proxy_lines":4,"max_routing_rules":20}}`), &snapshot)
	if err != nil || snapshot.QuotaBytes != 1000000 || snapshot.Limits.MaxSubscriptions != 3 || snapshot.Limits.MaxHops != 2 || snapshot.Limits.MaxProxyLines != 4 || !reflect.DeepEqual(snapshot.LineIDs, []string{"line"}) || !reflect.DeepEqual(snapshot.ResourceGroupIDs, []string{"group"}) {
		t.Fatalf("legacy snapshot lost business grants: %+v %v", snapshot, err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || strings.Contains(string(encoded), "max_routing_rules") {
		t.Fatalf("removed limit still exposed: %s %v", encoded, err)
	}
}

const (
	testGroupID = "11111111-1111-7111-8111-111111111111"
	testLineID  = "22222222-2222-7222-8222-222222222222"
	testUserID  = "33333333-3333-7333-8333-333333333333"
	testPlanID  = "44444444-4444-7444-8444-444444444444"
)

func TestNormalizePlanFreezesSortedExplicitGrants(t *testing.T) {
	plan, err := NormalizePlan(NewPlan{
		Name: " Standard ", QuotaBytes: 1_000_000,
		ResourceGroupIDs: []string{testGroupID}, LineIDs: []string{testLineID},
		Limits: PlanLimits{MaxForwardRulesPerNode: 2, MaxSubscriptions: 3, AllowCustomLines: true, MaxCustomLines: 2},
	})
	if err != nil || plan.Name != "Standard" || plan.DefaultMultiplierMilli != 1000 || plan.Limits.MaxHops != 1 {
		t.Fatalf("normalized plan = %+v, %v", plan, err)
	}
	if !reflect.DeepEqual(plan.ResourceGroupIDs, []string{testGroupID}) || !reflect.DeepEqual(plan.LineIDs, []string{testLineID}) {
		t.Fatalf("grants = %+v %+v", plan.ResourceGroupIDs, plan.LineIDs)
	}
	multi, err := NormalizePlan(NewPlan{Name: "Multi", Limits: PlanLimits{AllowCustomLines: true, MaxCustomLines: 2, MaxHops: 3}})
	if err != nil || multi.Limits.MaxHops != 3 {
		t.Fatalf("multi-hop allowance = %+v, %v", multi, err)
	}
	ordered, err := NormalizePlan(NewPlan{Name: "Sorted", ResourceGroupIDs: []string{testLineID, testGroupID}})
	if err != nil || !reflect.DeepEqual(ordered.ResourceGroupIDs, []string{testGroupID, testLineID}) {
		t.Fatalf("sorted grants = %+v, %v", ordered.ResourceGroupIDs, err)
	}
	for _, input := range []NewPlan{
		{Name: "Bad", QuotaBytes: -1},
		{Name: "Bad", QuotaBytes: 1, ResourceGroupIDs: []string{"wrong"}},
		{Name: "Bad", QuotaBytes: 1, ResourceGroupIDs: []string{testGroupID, testGroupID}},
		{Name: "Bad", QuotaBytes: 1, LineIDs: []string{testLineID, testLineID}},
		{Name: "Bad", QuotaBytes: 1, Limits: PlanLimits{MaxForwardRulesPerNode: -1}},
		{Name: "Bad", QuotaBytes: 1, Limits: PlanLimits{MaxHops: 9}},
		{Name: "Bad", QuotaBytes: 1, DefaultMultiplierMilli: intPointer(0)},
	} {
		if _, err := NormalizePlan(input); err == nil {
			t.Fatalf("accepted invalid plan: %+v", input)
		}
	}
}

func TestNormalizePlanDefaultsAndBoundsProxyLineLimit(t *testing.T) {
	plan, err := NormalizePlan(NewPlan{Name: "Single"})
	if err != nil || plan.Limits.MaxProxyLines != 1 {
		t.Fatalf("default proxy line limit = %+v, %v", plan.Limits, err)
	}
	plan, err = NormalizePlan(NewPlan{Name: "Pool", Limits: PlanLimits{MaxProxyLines: 8}})
	if err != nil || plan.Limits.MaxProxyLines != 8 {
		t.Fatalf("custom proxy line limit = %+v, %v", plan.Limits, err)
	}
	if _, err := NormalizePlan(NewPlan{Name: "Bad", Limits: PlanLimits{MaxProxyLines: 33}}); err == nil {
		t.Fatal("oversized proxy line limit accepted")
	}
}

func TestPlanSnapshotKeepsBillingScheduleForAccountDisplay(t *testing.T) {
	plan := Plan{Name: "Quarterly", BillingMode: "monthly_anchor", PeriodMonths: 3, QuotaBytes: 10_000,
		DefaultMultiplierMilli: 1250, Limits: PlanLimits{MaxHops: 2, MaxProxyLines: 4}}
	snapshot := plan.Snapshot()
	if snapshot.BillingMode != "monthly_anchor" || snapshot.PeriodMonths != 3 || snapshot.DefaultMultiplierMilli != 1250 {
		t.Fatalf("snapshot billing fields = %+v", snapshot)
	}
	if snapshot.Limits.MaxHops != 2 || snapshot.Limits.MaxProxyLines != 4 {
		t.Fatalf("snapshot limits = %+v", snapshot.Limits)
	}
}

func TestNormalizeMembershipRequiresCurrentBoundedTerm(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	input := NewMembership{UserID: testUserID, PlanID: testPlanID,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(24 * time.Hour), AnchorDay: 26, Timezone: "Asia/Shanghai"}
	value, err := NormalizeMembership(input, now)
	if err != nil || value.UserID != testUserID || value.Timezone != "Asia/Shanghai" {
		t.Fatalf("normalized membership = %+v, %v", value, err)
	}
	for _, change := range []func(*NewMembership){
		func(v *NewMembership) { v.UserID = "bad" },
		func(v *NewMembership) { v.PlanID = "bad" },
		func(v *NewMembership) { v.StartsAt = now.Add(time.Hour) },
		func(v *NewMembership) { v.EndsAt = now.Add(-time.Minute) },
		func(v *NewMembership) { v.EndsAt = now.Add(2 * time.Second) },
		func(v *NewMembership) { v.AnchorDay = 32 },
		func(v *NewMembership) { v.Timezone = "Mars/Base" },
		func(v *NewMembership) { v.Timezone = "Local" },
	} {
		candidate := input
		change(&candidate)
		if _, err := NormalizeMembership(candidate, now); err == nil {
			t.Fatalf("accepted invalid membership: %+v", candidate)
		}
	}
}

func intPointer(value int) *int { return &value }
