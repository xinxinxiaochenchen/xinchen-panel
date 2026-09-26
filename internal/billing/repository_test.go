package billing

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresBillingLedger(t *testing.T) {
	dsn := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	add := func(q string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, q, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	owner := add(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),'billing-owner@example.invalid','hash','active') RETURNING id::text`)
	group := add(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),'TEST.BILL','Billing','JP') RETURNING id::text`)
	node := add(`INSERT INTO nodes(id,group_id,name,region,host,capabilities) VALUES(gen_random_uuid(),$1,'Billing','JP','billing.example.invalid',ARRAY['forward']) RETURNING id::text`, group)
	agent := add(`INSERT INTO agents(id,node_id,status) VALUES(gen_random_uuid(),$1,'online') RETURNING id::text`, node)
	plan := add(`INSERT INTO plans(id,name,quota_bytes) VALUES(gen_random_uuid(),'billing-plan',2000) RETURNING id::text`)
	exec(`INSERT INTO plan_limits(plan_id) VALUES($1)`, plan)
	frozen, _ := json.Marshal(map[string]any{"plan_name": "Frozen Plan", "quota_bytes": 1000, "default_multiplier_milli": 500, "resource_group_ids": []string{group}, "line_ids": []string{}, "limits": map[string]int{"max_hops": 1}})
	membership := add(`INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json) VALUES(gen_random_uuid(),$1,$2,'2025-01-31 00:00:00+00','2025-06-01 00:00:00+00','active',31,'UTC',$3) RETURNING id::text`, owner, plan, frozen)
	repo := NewPostgresRepository(pool)
	at := instant(t, "2025-02-01T00:00:00Z")
	var wg sync.WaitGroup
	results := make(chan Period, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); p, e := repo.EnsurePeriod(ctx, membership, at); results <- p; failures <- e }()
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	var period Period
	for p := range results {
		if period.ID != "" && p.ID != period.ID {
			t.Fatal("concurrent creation duplicated period")
		}
		period = p
	}
	if period.QuotaBytes != 1000 || !period.EndsAt.Equal(instant(t, "2025-02-28T00:00:00Z")) {
		t.Fatalf("first frozen period: %+v", period)
	}
	next, err := repo.EnsurePeriod(ctx, membership, instant(t, "2025-03-01T00:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if next.QuotaBytes != 2000 || next.ID == period.ID || !next.EndsAt.Equal(instant(t, "2025-03-31T00:00:00Z")) {
		t.Fatalf("renewed period: %+v", next)
	}
	exec(`UPDATE plans SET quota_bytes=4000 WHERE id=$1`, plan)
	again, err := repo.EnsurePeriod(ctx, membership, instant(t, "2025-03-02T00:00:00Z"))
	if err != nil || again.QuotaBytes != 2000 {
		t.Fatalf("period changed: %+v %v", again, err)
	}
	leaseID := add(`INSERT INTO quota_leases(id,billing_period_id,agent_id,request_id,requested_bytes,granted_bytes,issued_at,expires_at)
    VALUES(gen_random_uuid(),$1,$2,gen_random_uuid(),1000,1000,$3,$4) RETURNING id::text`, period.ID, agent, at, at.Add(30*time.Second))
	exec(`UPDATE billing_periods SET reserved_bytes=1000 WHERE id=$1`, period.ID)
	connectionID := "00000000-0000-4000-8000-000000000001"
	input := Connection{ID: connectionID, PeriodID: period.ID, LeaseID: leaseID, AgentID: agent, IngressNodeID: node, MultiplierMilli: 500, StartedAt: at.Add(123 * time.Nanosecond)}
	registrationErrors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); registrationErrors <- repo.RegisterConnection(ctx, input) }()
	}
	wg.Wait()
	close(registrationErrors)
	for e := range registrationErrors {
		if e != nil {
			t.Fatalf("concurrent connection registration: %v", e)
		}
	}
	if err := repo.RegisterConnection(ctx, input); err != nil {
		t.Fatalf("duplicate connection: %v", err)
	}
	changed := input
	changed.MultiplierMilli = 2000
	if err := repo.RegisterConnection(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed frozen multiplier: %v", err)
	}
	exec(`UPDATE nodes SET multiplier_milli=2000 WHERE id=$1`, node)
	report := UsageReport{ConnectionID: connectionID, LeaseID: leaseID, Sequence: 1, Counters: Counters{UploadedBytes: 1}, ObservedAt: at.Add(time.Second + 345*time.Nanosecond)}
	event, err := repo.RecordUsage(ctx, agent, report)
	if err != nil || event.ChargedBytes != 0 {
		t.Fatalf("first report: %+v %v", event, err)
	}
	report.Sequence = 2
	report.UploadedBytes = 3
	report.DownloadedBytes = 2
	report.ObservedAt = at.Add(2*time.Second + 345*time.Nanosecond)
	event, err = repo.RecordUsage(ctx, agent, report)
	if err != nil || event.ChargedBytes != 2 || event.UploadedBytes != 2 || event.DownloadedBytes != 2 {
		t.Fatalf("second report: %+v %v", event, err)
	}
	duplicate, err := repo.RecordUsage(ctx, agent, report)
	if err != nil || duplicate.ID != event.ID {
		t.Fatalf("duplicate: %+v %v", duplicate, err)
	}
	report.UploadedBytes++
	if _, err := repo.RecordUsage(ctx, agent, report); !errors.Is(err, ErrConflict) {
		t.Fatalf("same sequence changed payload: %v", err)
	}
	report.Sequence = 4
	if _, err := repo.RecordUsage(ctx, agent, report); !errors.Is(err, ErrSequence) {
		t.Fatalf("missing sequence: %v", err)
	}
	report.Sequence = 3
	report.UploadedBytes = 2
	if _, err := repo.RecordUsage(ctx, agent, report); !errors.Is(err, ErrInvalidMeter) {
		t.Fatalf("decreasing counter: %v", err)
	}
	report.UploadedBytes = 4
	report.ObservedAt = at.Add(3 * time.Second)
	if _, err := repo.RecordUsage(ctx, "00000000-0000-4000-8000-000000000002", report); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other agent: %v", err)
	}
	if _, err := repo.RecordUsage(ctx, agent, report); err != nil {
		t.Fatal(err)
	}
	concurrent := UsageReport{ConnectionID: connectionID, LeaseID: leaseID, Sequence: 4, Counters: Counters{UploadedBytes: 5, DownloadedBytes: 2}, ObservedAt: at.Add(4 * time.Second)}
	var reportWG sync.WaitGroup
	resultIDs := make(chan string, 8)
	for i := 0; i < 8; i++ {
		reportWG.Add(1)
		go func() {
			defer reportWG.Done()
			result, err := repo.RecordUsage(ctx, agent, concurrent)
			if err != nil {
				resultIDs <- "error: " + err.Error()
				return
			}
			resultIDs <- result.ID
		}()
	}
	reportWG.Wait()
	close(resultIDs)
	var oneID string
	for got := range resultIDs {
		if oneID != "" && got != oneID {
			t.Fatalf("concurrent duplicate events: %q / %q", oneID, got)
		}
		oneID = got
	}
	concurrent.ObservedAt = concurrent.ObservedAt.Add(time.Second)
	if _, err := repo.RecordUsage(ctx, agent, concurrent); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed duplicate timestamp: %v", err)
	}
	concurrent.Sequence = 5
	concurrent.ObservedAt = at.Add(2 * time.Second)
	if _, err := repo.RecordUsage(ctx, agent, concurrent); !errors.Is(err, ErrInvalidMeter) {
		t.Fatalf("backwards observed time: %v", err)
	}
	concurrent.ObservedAt = period.EndsAt.Add(time.Second)
	if _, err := repo.RecordUsage(ctx, agent, concurrent); !errors.Is(err, ErrInvalidMeter) {
		t.Fatalf("outside frozen period: %v", err)
	}
	concurrent.ObservedAt = at.Add(5 * time.Second)
	concurrent.UploadedBytes = math.MaxInt64
	if _, err := repo.RecordUsage(ctx, agent, concurrent); !errors.Is(err, ErrInvalidMeter) {
		t.Fatalf("overflow report: %v", err)
	}
	// An aggregate overflow occurs after event/session updates; all of those
	// writes must roll back with the failed period update.
	exec(`UPDATE billing_periods SET charged_bytes=$2 WHERE id=$1`, period.ID, int64(math.MaxInt64))
	concurrent.UploadedBytes = 8
	if _, err := repo.RecordUsage(ctx, agent, concurrent); !errors.Is(err, ErrInvalidMeter) {
		t.Fatalf("aggregate overflow: %v", err)
	}
	exec(`UPDATE billing_periods SET charged_bytes=3 WHERE id=$1`, period.ID)
	var persistedSequence int64
	if err := pool.QueryRow(ctx, `SELECT last_sequence FROM usage_sessions WHERE id=$1`, connectionID).Scan(&persistedSequence); err != nil || persistedSequence != 4 {
		t.Fatalf("failed transaction advanced session: %d %v", persistedSequence, err)
	}
	var uploaded, downloaded, charged int64
	if err := pool.QueryRow(ctx, `SELECT uploaded_bytes,downloaded_bytes,charged_bytes FROM billing_periods WHERE id=$1`, period.ID).Scan(&uploaded, &downloaded, &charged); err != nil {
		t.Fatal(err)
	}
	if uploaded != 5 || downloaded != 2 || charged != 3 {
		t.Fatalf("ledger totals: %d %d %d", uploaded, downloaded, charged)
	}
	if err := pool.QueryRow(ctx, `SELECT charged_bytes FROM billing_periods WHERE id=$1`, next.ID).Scan(&charged); err != nil || charged != 0 {
		t.Fatalf("late report charged new period: %d %v", charged, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM usage_events WHERE connection_id=$1`, connectionID).Scan(&count); err != nil || count != 4 {
		t.Fatalf("events: %d %v", count, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE usage_events SET charged_bytes=999 WHERE connection_id=$1`, connectionID); err == nil {
		t.Fatal("ledger should reject updates")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM usage_events WHERE connection_id=$1`, connectionID); err == nil {
		t.Fatal("ledger should reject deletes")
	}
}
