package orchestration

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type convergenceFixture struct {
	pool    *pgxpool.Pool
	groupID string
	nodes   []string
	events  []string
}

func newConvergenceFixture(t *testing.T, nodeCount int) *convergenceFixture {
	t.Helper()
	databaseURL := orchestrationTestDatabaseURL()
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%X", random)
	fixture := &convergenceFixture{pool: pool}
	t.Cleanup(func() {
		for _, eventID := range fixture.events {
			if _, err := pool.Exec(ctx, `DELETE FROM outbox_events WHERE id=$1`, eventID); err != nil {
				t.Errorf("cleanup convergence event: %v", err)
			}
		}
		for _, nodeID := range fixture.nodes {
			if _, err := pool.Exec(ctx, `DELETE FROM config_revisions WHERE node_id=$1`, nodeID); err != nil {
				t.Errorf("cleanup convergence revisions: %v", err)
			}
			if _, err := pool.Exec(ctx, `DELETE FROM agents WHERE node_id=$1`, nodeID); err != nil {
				t.Errorf("cleanup convergence agent: %v", err)
			}
			if _, err := pool.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, nodeID); err != nil {
				t.Errorf("cleanup convergence node: %v", err)
			}
		}
		if fixture.groupID != "" {
			if _, err := pool.Exec(ctx, `DELETE FROM resource_groups WHERE id=$1`, fixture.groupID); err != nil {
				t.Errorf("cleanup convergence group: %v", err)
			}
		}
		pool.Close()
	})
	if err := pool.QueryRow(ctx, `INSERT INTO resource_groups(id,code,name,region)
VALUES (gen_random_uuid(),$1,'Convergence','US') RETURNING id::text`, "TEST.CONVERGENCE."+suffix).Scan(&fixture.groupID); err != nil {
		t.Fatal(err)
	}
	for index := range nodeCount {
		var nodeID string
		if err := pool.QueryRow(ctx, `INSERT INTO nodes(id,group_id,name,region,host,capabilities)
VALUES (gen_random_uuid(),$1,$2,'US',$3,ARRAY['forward']) RETURNING id::text`,
			fixture.groupID, fmt.Sprintf("Convergence %d", index), fmt.Sprintf("convergence-%d-%s.example.org", index, suffix)).Scan(&nodeID); err != nil {
			t.Fatal(err)
		}
		fixture.nodes = append(fixture.nodes, nodeID)
		if _, err := pool.Exec(ctx, `INSERT INTO agents(id,node_id) VALUES (gen_random_uuid(),$1)`, nodeID); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (fixture *convergenceFixture) addEvent(t *testing.T, kind, payload string) string {
	t.Helper()
	ctx := context.Background()
	var eventID string
	if err := fixture.pool.QueryRow(ctx, `INSERT INTO outbox_events(id,kind,aggregate_id,payload,idempotency_key)
VALUES (gen_random_uuid(),$1,gen_random_uuid(),$2,gen_random_uuid()::text) RETURNING id::text`, kind, payload).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	fixture.events = append(fixture.events, eventID)
	return eventID
}

func TestConvergenceWorkerConsumesRuleAndPolicyEvents(t *testing.T) {
	fixture := newConvergenceFixture(t, 2)
	ctx := context.Background()
	revisions := NewRevisionRepository(fixture.pool)
	worker := NewConvergenceWorker(fixture.pool, revisions)
	ruleEvent := fixture.addEvent(t, "forward_rule.changed", fmt.Sprintf(`{"node_id":%q}`, fixture.nodes[0]))
	processed, err := worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("rule event = %t, %v", processed, err)
	}
	var processedAt *time.Time
	if err := fixture.pool.QueryRow(ctx, `SELECT processed_at FROM outbox_events WHERE id=$1`, ruleEvent).Scan(&processedAt); err != nil || processedAt == nil {
		t.Fatalf("rule event not marked processed: %v, %v", processedAt, err)
	}
	first, err := revisions.Desired(ctx, fixture.nodes[0])
	if err != nil || first.Revision != 1 || len(first.Snapshot.Rules) != 0 {
		t.Fatalf("node revision after rule event = %+v, %v", first, err)
	}
	if _, err := revisions.Desired(ctx, fixture.nodes[1]); !errors.Is(err, ErrRevisionNotFound) {
		t.Fatalf("unaffected node was reconciled: %v", err)
	}
	unrelated := fixture.addEvent(t, "billing.changed", `{}`)
	policyEvent := fixture.addEvent(t, "forward_policy.changed", `{}`)
	processed, err = worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("policy event = %t, %v", processed, err)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT processed_at FROM outbox_events WHERE id=$1`, policyEvent).Scan(&processedAt); err != nil || processedAt == nil {
		t.Fatalf("policy event not marked processed: %v, %v", processedAt, err)
	}
	if _, err := revisions.Desired(ctx, fixture.nodes[1]); err != nil {
		t.Fatalf("policy did not reconcile second Agent: %v", err)
	}
	processed, err = worker.ProcessOne(ctx)
	if err != nil || processed {
		t.Fatalf("processed unrelated or replayed event: %t, %v", processed, err)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT processed_at FROM outbox_events WHERE id=$1`, unrelated).Scan(&processedAt); err != nil || processedAt != nil {
		t.Fatalf("unrelated event consumed: %v, %v", processedAt, err)
	}
}

func TestConvergenceWorkerRetriesBadEventAndSkipsLockedRow(t *testing.T) {
	fixture := newConvergenceFixture(t, 1)
	ctx := context.Background()
	worker := NewConvergenceWorker(fixture.pool, NewRevisionRepository(fixture.pool))
	badEvent := fixture.addEvent(t, "forward_rule.changed", `{"node_id":"not-a-uuid"}`)
	processed, err := worker.ProcessOne(ctx)
	if err == nil || processed {
		t.Fatalf("invalid event = %t, %v", processed, err)
	}
	var attempts int
	var availableAt time.Time
	var processedAt *time.Time
	if err := fixture.pool.QueryRow(ctx, `SELECT retry_count,available_at,processed_at FROM outbox_events WHERE id=$1`, badEvent).Scan(&attempts, &availableAt, &processedAt); err != nil || attempts != 1 || processedAt != nil || !availableAt.After(time.Now()) {
		t.Fatalf("invalid event retry = %d, %s, %v, %v", attempts, availableAt, processedAt, err)
	}
	goodEvent := fixture.addEvent(t, "forward_rule.changed", fmt.Sprintf(`{"node_id":%q}`, fixture.nodes[0]))
	tx, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM outbox_events WHERE id=$1 FOR UPDATE`, goodEvent); err != nil {
		t.Fatal(err)
	}
	processed, err = worker.ProcessOne(ctx)
	if err != nil || processed {
		t.Fatalf("locked event was processed: %t, %v", processed, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	processed, err = worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("unlocked event not processed: %t, %v", processed, err)
	}
}

func TestConvergenceSweepHandlesNodesWithoutEvents(t *testing.T) {
	fixture := newConvergenceFixture(t, 2)
	ctx := context.Background()
	worker := NewConvergenceWorker(fixture.pool, NewRevisionRepository(fixture.pool))
	count, err := worker.Sweep(ctx)
	if err != nil || count != 2 {
		t.Fatalf("first sweep = %d, %v", count, err)
	}
	count, err = worker.Sweep(ctx)
	if err != nil || count != 0 {
		t.Fatalf("idempotent sweep = %d, %v", count, err)
	}
	for _, nodeID := range fixture.nodes {
		var desired int64
		if err := fixture.pool.QueryRow(ctx, `SELECT desired_revision FROM agents WHERE node_id=$1`, nodeID).Scan(&desired); err != nil || desired != 1 {
			t.Fatalf("swept node revision = %d, %v", desired, err)
		}
	}
}

type failingReconciler struct{}

func (failingReconciler) Reconcile(context.Context, string) (DesiredRevision, bool, error) {
	return DesiredRevision{}, false, errors.New("transient database failure")
}

func TestConvergenceWorkerRollbackKeepsEventAfterReconcileFailure(t *testing.T) {
	fixture := newConvergenceFixture(t, 1)
	ctx := context.Background()
	worker := NewConvergenceWorker(fixture.pool, failingReconciler{})
	eventID := fixture.addEvent(t, "forward_rule.changed", fmt.Sprintf(`{"node_id":%q}`, fixture.nodes[0]))
	processed, err := worker.ProcessOne(ctx)
	if err == nil || processed {
		t.Fatalf("missing Agent event = %t, %v", processed, err)
	}
	var retries int
	if err := fixture.pool.QueryRow(ctx, `SELECT retry_count FROM outbox_events WHERE id=$1`, eventID).Scan(&retries); err != nil || retries != 1 {
		t.Fatalf("event retry count = %d, %v", retries, err)
	}
}

func TestConvergenceWorkerOnlyClaimsRelevantKinds(t *testing.T) {
	fixture := newConvergenceFixture(t, 0)
	ctx := context.Background()
	worker := NewConvergenceWorker(fixture.pool, NewRevisionRepository(fixture.pool))
	eventID := fixture.addEvent(t, "identity.changed", `{}`)
	processed, err := worker.ProcessOne(ctx)
	if err != nil || processed {
		t.Fatalf("unrelated event = %t, %v", processed, err)
	}
	var retained bool
	if err := fixture.pool.QueryRow(ctx, `SELECT processed_at IS NULL FROM outbox_events WHERE id=$1`, eventID).Scan(&retained); err != nil || !retained {
		t.Fatalf("unrelated event retained = %t, %v", retained, err)
	}
}

func TestConvergenceWorkerConsumesBillingPeriodEvent(t *testing.T) {
	fixture := newConvergenceFixture(t, 1)
	ctx := context.Background()
	repo := NewRevisionRepository(fixture.pool)
	worker := NewConvergenceWorker(fixture.pool, repo)
	event := fixture.addEvent(t, "billing.period_renewed", `{"user_id":"00000000-0000-4000-8000-000000000001"}`)
	processed, err := worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("billing event: %t %v", processed, err)
	}
	if _, err := repo.Desired(ctx, fixture.nodes[0]); err != nil {
		t.Fatalf("billing renewal did not reconcile Agent: %v", err)
	}
	var done bool
	if err := fixture.pool.QueryRow(ctx, `SELECT processed_at IS NOT NULL FROM outbox_events WHERE id=$1`, event).Scan(&done); err != nil || !done {
		t.Fatalf("billing event not acknowledged: %t %v", done, err)
	}
}
