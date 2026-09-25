package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func catalogTestDatabaseURL() string {
	if value := os.Getenv("CONTROL_TEST_DATABASE_URL"); value != "" {
		return value
	}
	if os.Getenv("CONTROL_TEST_DB_NAME") != "" && os.Getenv("POSTGRES_PASSWORD") != "" {
		return (&url.URL{Scheme: "postgres", User: url.UserPassword("controlplane", os.Getenv("POSTGRES_PASSWORD")), Host: "db:5432", Path: "/" + os.Getenv("CONTROL_TEST_DB_NAME")}).String()
	}
	return ""
}

func TestPostgresCatalogAuditsAndScopesNodes(t *testing.T) {
	databaseURL := catalogTestDatabaseURL()
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var actorID, memberID string
	for _, email := range []string{"catalog-admin@example.invalid", "catalog-member@example.invalid"} {
		var id string
		err := pool.QueryRow(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),$1,'hash','active') RETURNING id::text`, email).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		if actorID == "" {
			actorID = id
		} else {
			memberID = id
		}
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM memberships WHERE user_id=$1`, memberID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM plans WHERE name='catalog-integration-plan'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE actor_user_id=$1`, actorID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM nodes WHERE name LIKE 'Catalog Test %'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM resource_groups WHERE code IN ('TEST.CAT1','TEST.CAT2')`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1 OR id=$2`, actorID, memberID)
	}()
	repo := NewPostgresRepository(pool)
	first, err := repo.CreateGroup(ctx, GroupInput{Code: "TEST.CAT1", Name: "Test One", Region: "JP", Enabled: true}, actorID, "request-one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.CreateGroup(ctx, GroupInput{Code: "TEST.CAT2", Name: "Test Two", Region: "US", Enabled: true}, actorID, "request-two")
	if err != nil {
		t.Fatal(err)
	}
	nodeOne, err := repo.CreateNode(ctx, NodeInput{GroupID: first.ID, Name: "Catalog Test One", Region: "JP", Host: "test-one.example.invalid", Capabilities: []string{"forward"}, MultiplierMilli: 1000, Tags: []string{}, Enabled: true}, actorID, "request-three")
	if err != nil {
		t.Fatal(err)
	}
	nodeTwo, err := repo.CreateNode(ctx, NodeInput{GroupID: second.ID, Name: "Catalog Test Two", Region: "US", Host: "test-two.example.invalid", Capabilities: []string{"forward"}, MultiplierMilli: 1000, Tags: []string{}, Enabled: true}, actorID, "request-four")
	if err != nil {
		t.Fatal(err)
	}
	var planID string
	if err := pool.QueryRow(ctx, `INSERT INTO plans(id,name,quota_bytes) VALUES (gen_random_uuid(),'catalog-integration-plan',1000000) RETURNING id::text`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(map[string]any{"resource_group_ids": []string{first.ID}})
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json)
VALUES (gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, memberID, planID, snapshot); err != nil {
		t.Fatal(err)
	}
	allowed, err := repo.ListAllowedNodes(ctx, memberID, 20, "")
	if err != nil || len(allowed) != 1 || allowed[0].ID != nodeOne.ID {
		t.Fatalf("allowed nodes = %+v, %v", allowed, err)
	}
	if _, err := repo.GetAllowedNode(ctx, memberID, nodeTwo.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unauthorized node detail = %v", err)
	}
	_, err = repo.CreateNode(ctx, NodeInput{GroupID: first.ID, Name: "Catalog Test Extra", Region: "JP", Host: "test-extra.example.invalid", Capabilities: []string{"forward"}, MultiplierMilli: 1000, Tags: []string{}, Enabled: true}, actorID, "request-extra")
	if err != nil {
		t.Fatal(err)
	}
	firstPage, err := repo.ListAllowedNodes(ctx, memberID, 1, "")
	if err != nil || len(firstPage) != 1 {
		t.Fatalf("first node page = %+v, %v", firstPage, err)
	}
	secondPage, err := repo.ListAllowedNodes(ctx, memberID, 1, firstPage[0].ID)
	if err != nil || len(secondPage) != 1 || secondPage[0].ID == firstPage[0].ID {
		t.Fatalf("second node page = %+v, %v", secondPage, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_user_id=$1 AND request_id IN ('request-one','request-two','request-three','request-four')`, actorID).Scan(&count); err != nil || count != 4 {
		t.Fatalf("audit rows = %d, %v", count, err)
	}
	proxyNode, err := repo.CreateNode(ctx, NodeInput{GroupID: first.ID, Name: "Catalog Test Duplicate", Region: "JP", Host: "test-one.example.invalid", PublicIP: stringPointer("203.0.113.1"), ProxyPort: intPointer(443), Capabilities: []string{"proxy"}, BandwidthBPS: int64Pointer(1000000000), MultiplierMilli: 1000, Tags: []string{"premium"}, Enabled: true}, actorID, "request-five")
	if err != nil {
		t.Fatal(err)
	}
	readProxy, err := repo.GetAllowedNode(ctx, memberID, proxyNode.ID)
	if err != nil || readProxy.PublicIP == nil || *readProxy.PublicIP != "203.0.113.1" || readProxy.ProxyPort == nil || *readProxy.ProxyPort != 443 || readProxy.BandwidthBPS == nil || *readProxy.BandwidthBPS != 1000000000 {
		t.Fatalf("proxy node optional fields = %+v, %v", readProxy, err)
	}
	_, err = repo.CreateNode(ctx, NodeInput{GroupID: first.ID, Name: "Catalog Test Conflict", Region: "JP", Host: "TEST-ONE.example.invalid", ProxyPort: intPointer(443), Capabilities: []string{"proxy"}, MultiplierMilli: 1000, Tags: []string{}, Enabled: true}, actorID, "request-six")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate proxy endpoint = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET ends_at=$1 WHERE user_id=$2`, time.Now().Add(-time.Minute), memberID); err != nil {
		t.Fatal(err)
	}
	allowed, err = repo.ListAllowedNodes(ctx, memberID, 20, "")
	if err != nil || len(allowed) != 0 {
		t.Fatalf("expired membership nodes = %+v, %v", allowed, err)
	}
}

func stringPointer(value string) *string { return &value }
func int64Pointer(value int64) *int64    { return &value }
