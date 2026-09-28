package entitlement

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"controlplane/internal/catalog"
	"github.com/jackc/pgx/v5/pgxpool"
)

func entitlementTestDatabaseURL() string {
	if value := os.Getenv("CONTROL_TEST_DATABASE_URL"); value != "" {
		return value
	}
	if os.Getenv("CONTROL_TEST_DB_NAME") != "" && os.Getenv("POSTGRES_PASSWORD") != "" {
		return (&url.URL{Scheme: "postgres", User: url.UserPassword("controlplane", os.Getenv("POSTGRES_PASSWORD")), Host: "db:5432", Path: "/" + os.Getenv("CONTROL_TEST_DB_NAME")}).String()
	}
	return ""
}

func TestPostgresPlanMembershipFreezesGrantsAndAudits(t *testing.T) {
	databaseURL := entitlementTestDatabaseURL()
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var actorID, memberID, otherID, firstGroup, secondGroup, privateLineID, firstNodeID, secondNodeID, sharedLineID, otherLineID string
	for i, email := range []string{"entitlement-admin@example.invalid", "entitlement-member@example.invalid", "entitlement-other@example.invalid"} {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),$1,'hash','active') RETURNING id::text`, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		switch i {
		case 0:
			actorID = id
		case 1:
			memberID = id
		case 2:
			otherID = id
		}
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM outbox_events WHERE kind='membership.changed' AND payload->>'user_id'=$1`, memberID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM memberships WHERE user_id=$1`, memberID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE actor_user_id=$1`, actorID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM plans WHERE name IN ('entitlement-integration-plan','private-line-grant','mismatched-line-grant')`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM lines WHERE id=$1 OR id=$2 OR id=$3`, privateLineID, sharedLineID, otherLineID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM nodes WHERE name IN ('Entitlement Test One','Entitlement Test Two')`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM resource_groups WHERE code IN ('TEST.ENT1','TEST.ENT2')`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1 OR id=$2 OR id=$3`, actorID, memberID, otherID)
	}()
	for i, code := range []string{"TEST.ENT1", "TEST.ENT2"} {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO resource_groups(id,code,name,region) VALUES (gen_random_uuid(),$1,$1,'JP') RETURNING id::text`, code).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstGroup = id
		} else {
			secondGroup = id
		}
		var nodeID string
		if err := pool.QueryRow(ctx, `INSERT INTO nodes(id,group_id,name,region,host,proxy_port,capabilities)
VALUES (gen_random_uuid(),$1,$2,'JP',$3,443,ARRAY['proxy']::text[]) RETURNING id::text`, id,
			[]string{"Entitlement Test One", "Entitlement Test Two"}[i],
			[]string{"entitlement-one.example.invalid", "entitlement-two.example.invalid"}[i]).Scan(&nodeID); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstNodeID = nodeID
		} else {
			secondNodeID = nodeID
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO lines(id,owner_user_id,name,created_by)
VALUES (gen_random_uuid(),$1,'Entitlement Test Private',$2) RETURNING id::text`, memberID, actorID).Scan(&privateLineID); err != nil {
		t.Fatal(err)
	}
	for i, spec := range []struct{ name, nodeID string }{{"Entitlement Test Shared", firstNodeID}, {"Entitlement Test Other", secondNodeID}} {
		var lineID string
		if err := pool.QueryRow(ctx, `INSERT INTO lines(id,name,created_by)
VALUES (gen_random_uuid(),$1,$2) RETURNING id::text`, spec.name, actorID).Scan(&lineID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO line_hops(line_id,position,node_id,role) VALUES ($1,0,$2,'egress')`, lineID, spec.nodeID); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			sharedLineID = lineID
		} else {
			otherLineID = lineID
		}
	}
	repo := NewPostgresRepository(pool)
	if _, err := repo.CreatePlan(ctx, PlanInput{Name: "private-line-grant", QuotaBytes: 1, DefaultMultiplierMilli: 1000,
		Limits: PlanLimits{MaxHops: 1}, ResourceGroupIDs: []string{firstGroup}, LineIDs: []string{privateLineID}},
		actorID, "private-line-grant"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private line grant = %v", err)
	}
	if _, err := repo.CreatePlan(ctx, PlanInput{Name: "mismatched-line-grant", QuotaBytes: 1, DefaultMultiplierMilli: 1000,
		Limits: PlanLimits{MaxHops: 1}, ResourceGroupIDs: []string{firstGroup}, LineIDs: []string{otherLineID}},
		actorID, "mismatched-line-grant"); err == nil {
		t.Fatal("accepted shared line outside granted group")
	}
	plan, err := repo.CreatePlan(ctx, PlanInput{Name: "entitlement-integration-plan", QuotaBytes: 1_000_000,
		DefaultMultiplierMilli: 2000, Limits: PlanLimits{MaxForwardRulesPerNode: 2, MaxHops: 1},
		ResourceGroupIDs: []string{firstGroup}, LineIDs: []string{sharedLineID}}, actorID, "plan-request")
	if err != nil || plan.ID == "" || len(plan.ResourceGroupIDs) != 1 || len(plan.LineIDs) != 1 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	page, err := repo.ListPlans(ctx, 10, "")
	if err != nil || len(page) != 1 || page[0].ID != plan.ID {
		t.Fatalf("plan page = %+v, %v", page, err)
	}
	if _, err := repo.CreatePlan(ctx, PlanInput{Name: plan.Name, QuotaBytes: 1, DefaultMultiplierMilli: 1000,
		Limits: PlanLimits{MaxHops: 1}}, actorID, "duplicate-plan"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate plan = %v", err)
	}
	if _, err := repo.CreatePlan(ctx, PlanInput{Name: "missing-grant", QuotaBytes: 1, DefaultMultiplierMilli: 1000,
		Limits: PlanLimits{MaxHops: 1}, ResourceGroupIDs: []string{"55555555-5555-7555-8555-555555555555"}}, actorID, "missing-grant"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing grant = %v", err)
	}
	now := time.Now()
	if _, err := repo.CreateMembership(ctx, MembershipInput{UserID: memberID, PlanID: plan.ID,
		StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour), AnchorDay: 26, Timezone: "UTC"},
		actorID, "future-membership"); err == nil {
		t.Fatal("accepted future start as active membership")
	}
	if _, err := repo.CreateMembership(ctx, MembershipInput{UserID: memberID, PlanID: plan.ID,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(-time.Second), AnchorDay: 26, Timezone: "UTC"},
		actorID, "expired-membership"); err == nil {
		t.Fatal("accepted already expired membership")
	}
	input := MembershipInput{UserID: memberID, PlanID: plan.ID, StartsAt: now.Add(-time.Hour),
		EndsAt: now.Add(24 * time.Hour), AnchorDay: 26, Timezone: "Asia/Shanghai"}
	membership, err := repo.CreateMembership(ctx, input, actorID, "membership-request")
	if err != nil || membership.Status != "active" || membership.Snapshot.QuotaBytes != 1_000_000 || membership.Snapshot.DefaultMultiplierMilli != 2000 {
		t.Fatalf("membership = %+v, %v", membership, err)
	}
	membershipPage, err := repo.ListMemberships(ctx, 10, "")
	if err != nil || len(membershipPage) == 0 {
		t.Fatalf("membership directory = %+v, %v", membershipPage, err)
	}
	foundMembership := false
	for _, item := range membershipPage {
		if item.ID == membership.ID {
			foundMembership = item.UserID == memberID && item.Snapshot.PlanName == plan.Name
		}
	}
	if !foundMembership {
		t.Fatalf("membership missing from directory: %+v", membershipPage)
	}
	for _, item := range membershipPage {
		if item.ID == membership.ID {
			pageAfter, err := repo.ListMemberships(ctx, 10, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, later := range pageAfter {
				if later.ID == item.ID {
					t.Fatalf("cursor returned same membership: %+v", pageAfter)
				}
			}
			break
		}
	}
	if _, err := repo.CreateMembership(ctx, input, actorID, "duplicate-membership"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate membership = %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM plan_resource_group_grants WHERE plan_id=$1`, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plan_resource_group_grants(plan_id,resource_group_id) VALUES ($1,$2)`, plan.ID, secondGroup); err != nil {
		t.Fatal(err)
	}
	current, err := repo.GetCurrentMembership(ctx, memberID)
	if err != nil || current.ID != membership.ID || len(current.Snapshot.ResourceGroupIDs) != 1 || current.Snapshot.ResourceGroupIDs[0] != firstGroup || len(current.Snapshot.LineIDs) != 1 || current.Snapshot.LineIDs[0] != sharedLineID {
		t.Fatalf("frozen membership = %+v, %v", current, err)
	}
	allowedNodes, err := catalog.NewPostgresRepository(pool).ListAllowedNodes(ctx, memberID, 10, "")
	if err != nil || len(allowedNodes) != 1 || allowedNodes[0].GroupID != firstGroup {
		t.Fatalf("nodes from frozen membership = %+v, %v", allowedNodes, err)
	}
	if _, err := repo.GetCurrentMembership(ctx, otherID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user membership = %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_user_id=$1 AND request_id IN ('plan-request','membership-request')`, actorID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("audit count = %d, %v", count, err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT snapshot_json FROM memberships WHERE id=$1`, membership.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var persisted Snapshot
	if err := json.Unmarshal(raw, &persisted); err != nil || len(persisted.ResourceGroupIDs) != 1 || persisted.ResourceGroupIDs[0] != firstGroup {
		t.Fatalf("persisted snapshot = %+v, %v", persisted, err)
	}
	cancelled, err := repo.SetMembershipStatus(ctx, membership.ID, "cancelled", actorID, "cancel-membership")
	if err != nil || cancelled.Status != "cancelled" || cancelled.Snapshot.PlanName != plan.Name {
		t.Fatalf("cancelled membership = %+v, %v", cancelled, err)
	}
	if _, err := repo.GetCurrentMembership(ctx, memberID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancelled membership remains current: %v", err)
	}
	var cancellationEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE kind='membership.changed' AND aggregate_id=$1`, membership.ID).Scan(&cancellationEvents); err != nil || cancellationEvents != 1 {
		t.Fatalf("cancellation events = %d, %v", cancellationEvents, err)
	}
	if _, err := repo.SetMembershipStatus(ctx, membership.ID, "cancelled", actorID, "repeat-cancel"); err != nil {
		t.Fatalf("idempotent cancellation: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE kind='membership.changed' AND aggregate_id=$1`, membership.ID).Scan(&cancellationEvents); err != nil || cancellationEvents != 1 {
		t.Fatalf("repeat cancellation events = %d, %v", cancellationEvents, err)
	}
	archived, err := repo.SetPlanStatus(ctx, plan.ID, "archived", actorID, "archive-plan")
	if err != nil || archived.Status != "archived" {
		t.Fatalf("archived plan = %+v, %v", archived, err)
	}
	if _, err := repo.CreateMembership(ctx, input, actorID, "archived-plan-membership"); !errors.Is(err, ErrConflict) {
		t.Fatalf("archived plan accepted new membership: %v", err)
	}
	active, err := repo.SetPlanStatus(ctx, plan.ID, "active", actorID, "restore-plan")
	if err != nil || active.Status != "active" {
		t.Fatalf("restored plan = %+v, %v", active, err)
	}
}
