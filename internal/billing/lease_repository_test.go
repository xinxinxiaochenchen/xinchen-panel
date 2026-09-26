package billing

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresQuotaLeases(t *testing.T) {
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
	newID := func() string {
		t.Helper()
		v, e := id.NewV7()
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	add := func(q string, args ...any) string {
		t.Helper()
		var v string
		if e := pool.QueryRow(ctx, q, args...).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	owner := add(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),'lease-owner@example.invalid','hash','active') RETURNING id::text`)
	group := add(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),'TEST.LEASE','Leases','JP') RETURNING id::text`)
	nodes := []string{}
	agents := []string{}
	for _, name := range []string{"lease-a", "lease-b"} {
		n := add(`INSERT INTO nodes(id,group_id,name,region,host,capabilities) VALUES(gen_random_uuid(),$1,$2,'JP',$2,ARRAY['forward']) RETURNING id::text`, group, name)
		nodes = append(nodes, n)
		agents = append(agents, add(`INSERT INTO agents(id,node_id,status,last_seen_at) VALUES(gen_random_uuid(),$1,'online',clock_timestamp()) RETURNING id::text`, n))
	}
	plan := add(`INSERT INTO plans(id,name,quota_bytes) VALUES(gen_random_uuid(),'lease-plan',1000) RETURNING id::text`)
	exec(`INSERT INTO plan_limits(plan_id) VALUES($1)`, plan)
	snapshot, _ := json.Marshal(map[string]any{"plan_name": "Lease Plan", "quota_bytes": 1000, "default_multiplier_milli": 1000, "resource_group_ids": []string{group}, "line_ids": []string{}})
	member := add(`INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json) VALUES(gen_random_uuid(),$1,$2,clock_timestamp()-interval '1 hour',clock_timestamp()+interval '1 day','active',1,'UTC',$3) RETURNING id::text`, owner, plan, snapshot)
	repo := NewPostgresRepository(pool)
	period, err := repo.EnsurePeriod(ctx, member, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GrantLease(ctx, LeaseRequest{AgentID: agents[0], PeriodID: period.ID, RequestID: newID(), RequestedBytes: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unactivated period grant: %v", err)
	}
	if activated, err := repo.activateCurrentPeriod(ctx, member); err != nil || !activated {
		t.Fatalf("activate current period: %t %v", activated, err)
	}
	exec(`UPDATE agents SET last_seen_at=clock_timestamp()-interval '2 minutes' WHERE id=$1`, agents[0])
	if _, err := repo.GrantLease(ctx, LeaseRequest{AgentID: agents[0], PeriodID: period.ID, RequestID: newID(), RequestedBytes: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale Agent grant: %v", err)
	}
	exec(`UPDATE agents SET last_seen_at=clock_timestamp() WHERE id=$1`, agents[0])
	exec(`UPDATE nodes SET enabled=false WHERE id=$1`, nodes[0])
	if _, err := repo.GrantLease(ctx, LeaseRequest{AgentID: agents[0], PeriodID: period.ID, RequestID: newID(), RequestedBytes: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled node grant: %v", err)
	}
	exec(`UPDATE nodes SET enabled=true WHERE id=$1`, nodes[0])
	exec(`UPDATE billing_periods SET quota_bytes=0 WHERE id=$1`, period.ID)
	if _, err := repo.GrantLease(ctx, LeaseRequest{AgentID: agents[0], PeriodID: period.ID, RequestID: newID(), RequestedBytes: 1}); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("zero quota grant: %v", err)
	}
	exec(`UPDATE billing_periods SET quota_bytes=1000 WHERE id=$1`, period.ID)
	var wg sync.WaitGroup
	results := make(chan Lease, 4)
	failure := make(chan error, 4)
	for i := 0; i < 4; i++ {
		req := LeaseRequest{AgentID: agents[i%2], PeriodID: period.ID, RequestID: newID(), RequestedBytes: 400}
		wg.Add(1)
		go func() { defer wg.Done(); lease, e := repo.GrantLease(ctx, req); results <- lease; failure <- e }()
	}
	wg.Wait()
	close(results)
	close(failure)
	exhausted := 0
	for e := range failure {
		if errors.Is(e, ErrQuotaExhausted) {
			exhausted++
		} else if e != nil {
			t.Fatal(e)
		}
	}
	leases := []Lease{}
	var granted int64
	for l := range results {
		if l.ID != "" {
			leases = append(leases, l)
			granted += l.GrantedBytes
		}
	}
	if granted != 1000 || exhausted != 1 || len(leases) != 3 {
		t.Fatalf("over/under allocation: %d bytes, %d exhausted, %d leases", granted, exhausted, len(leases))
	}
	chosen := leases[0]
	retry := LeaseRequest{AgentID: chosen.AgentID, PeriodID: chosen.PeriodID, RequestID: chosen.RequestID, RequestedBytes: chosen.RequestedBytes}
	same, err := repo.GrantLease(ctx, retry)
	if err != nil || same.ID != chosen.ID || !same.ExpiresAt.Equal(chosen.ExpiresAt) {
		t.Fatalf("grant retry: %+v %v", same, err)
	}
	retry.RequestedBytes++
	if _, err := repo.GrantLease(ctx, retry); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed request: %v", err)
	}
	retry.RequestedBytes--
	repeatIDs := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, e := repo.GrantLease(ctx, retry)
			if e != nil {
				repeatIDs <- "error: " + e.Error()
			} else {
				repeatIDs <- got.ID
			}
		}()
	}
	wg.Wait()
	close(repeatIDs)
	for got := range repeatIDs {
		if got != chosen.ID {
			t.Fatalf("concurrent retry: %s", got)
		}
	}
	// Expiration does not recycle reservations before reports have been reconciled.
	for _, l := range leases {
		exec(`UPDATE quota_leases SET expires_at=issued_at+interval '1 microsecond' WHERE id=$1`, l.ID)
	}
	if _, err := repo.GrantLease(ctx, LeaseRequest{AgentID: agents[0], PeriodID: period.ID, RequestID: newID(), RequestedBytes: 1}); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("expired lease recycled: %v", err)
	}
	for _, l := range leases {
		if _, err := repo.SettleLease(ctx, l.AgentID, l.ID, 0); err != nil {
			t.Fatal(err)
		}
	}
	freshReq := LeaseRequest{AgentID: agents[0], PeriodID: period.ID, RequestID: newID(), RequestedBytes: 1000}
	l, err := repo.GrantLease(ctx, freshReq)
	if err != nil || l.GrantedBytes != 1000 {
		t.Fatalf("released quota: %+v %v", l, err)
	}
	connection := Connection{ID: newID(), PeriodID: period.ID, LeaseID: l.ID, AgentID: agents[0], IngressNodeID: nodes[0], MultiplierMilli: 1000, StartedAt: l.IssuedAt}
	if err := repo.RegisterConnection(ctx, connection); err != nil {
		t.Fatal(err)
	}
	report := UsageReport{ConnectionID: connection.ID, LeaseID: l.ID, Sequence: 1, Counters: Counters{UploadedBytes: 100, DownloadedBytes: 50}, ObservedAt: l.IssuedAt.Add(time.Microsecond)}
	if _, err := repo.RecordUsage(ctx, agents[0], report); err != nil {
		t.Fatal(err)
	}
	wrongLease := report
	wrongLease.LeaseID = chosen.ID
	if _, err := repo.RecordUsage(ctx, agents[0], wrongLease); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate report changed lease: %v", err)
	}
	if _, err := repo.SettleLease(ctx, agents[1], l.ID, 150); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other agent settlement: %v", err)
	}
	if _, err := repo.SettleLease(ctx, agents[0], l.ID, 149); !errors.Is(err, ErrConflict) {
		t.Fatalf("unreconciled settlement: %v", err)
	}
	settled, err := repo.SettleLease(ctx, agents[0], l.ID, 150)
	if err != nil || settled.State != "settled" {
		t.Fatalf("settlement: %+v %v", settled, err)
	}
	if _, err := repo.SettleLease(ctx, agents[0], l.ID, 150); err != nil {
		t.Fatalf("settlement retry: %v", err)
	}
	if _, err := repo.RecordUsage(ctx, agents[0], report); err != nil {
		t.Fatalf("duplicate after settlement: %v", err)
	}
	report.Sequence++
	report.UploadedBytes++
	if _, err := repo.RecordUsage(ctx, agents[0], report); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("new report after settlement: %v", err)
	}
	next, err := repo.GrantLease(ctx, LeaseRequest{AgentID: agents[1], PeriodID: period.ID, RequestID: newID(), RequestedBytes: 1000})
	if err != nil || next.GrantedBytes != 850 {
		t.Fatalf("remaining quota: %+v %v", next, err)
	}
	var charged, reserved int64
	if err := pool.QueryRow(ctx, `SELECT charged_bytes,reserved_bytes FROM billing_periods WHERE id=$1`, period.ID).Scan(&charged, &reserved); err != nil || charged != 150 || reserved != 850 {
		t.Fatalf("accounting: %d/%d %v", charged, reserved, err)
	}
	// A bounded data-plane boundary overrun is still real traffic. Keep it
	// in the ledger, and stop allocating any additional quota.
	overConnection := Connection{ID: newID(), PeriodID: period.ID, LeaseID: next.ID, AgentID: agents[1], IngressNodeID: nodes[1], MultiplierMilli: 1000, StartedAt: next.IssuedAt}
	if err := repo.RegisterConnection(ctx, overConnection); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RecordUsage(ctx, agents[1], UsageReport{ConnectionID: overConnection.ID, LeaseID: next.ID, Sequence: 1, Counters: Counters{UploadedBytes: 900}, ObservedAt: next.IssuedAt.Add(time.Microsecond)}); err != nil {
		t.Fatalf("lost boundary overrun: %v", err)
	}
	if _, err := repo.SettleLease(ctx, agents[1], next.ID, 900); err != nil {
		t.Fatalf("overrun settlement: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT charged_bytes,reserved_bytes FROM billing_periods WHERE id=$1`, period.ID).Scan(&charged, &reserved); err != nil || charged != 1050 || reserved != 0 {
		t.Fatalf("overrun accounting: %d/%d %v", charged, reserved, err)
	}
	if _, err := repo.GrantLease(ctx, LeaseRequest{AgentID: agents[1], PeriodID: period.ID, RequestID: newID(), RequestedBytes: 1}); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("grant after overrun: %v", err)
	}
	exec(`UPDATE users SET status='disabled' WHERE id=$1`, owner)
	if _, err := repo.GrantLease(ctx, LeaseRequest{AgentID: agents[1], PeriodID: period.ID, RequestID: newID(), RequestedBytes: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled user grant: %v", err)
	}
}
