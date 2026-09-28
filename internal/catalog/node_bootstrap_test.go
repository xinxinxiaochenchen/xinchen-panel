package catalog

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBootstrapNodeIdentityIncludesRelayPort(t *testing.T) {
	relayPort := 24443
	changedRelayPort := relayPort + 1
	existing := Node{Region: "US", Host: "bootstrap.example.invalid", RelayPort: &relayPort, Capabilities: []string{"forward"}, MultiplierMilli: 1000, Tags: []string{}, Enabled: true}
	input := NodeInput{Region: existing.Region, Host: existing.Host, RelayPort: &changedRelayPort, Capabilities: existing.Capabilities, MultiplierMilli: existing.MultiplierMilli, Tags: existing.Tags, Enabled: existing.Enabled}
	if bootstrapNodeMatches(existing, input) {
		t.Fatalf("bootstrap identity ignored relay port: existing=%v input=%v", existing.RelayPort, input.RelayPort)
	}
	input.RelayPort = &relayPort
	if !bootstrapNodeMatches(existing, input) {
		t.Fatal("matching relay port should preserve idempotent bootstrap")
	}
	if !reflect.DeepEqual(existing.RelayPort, input.RelayPort) {
		t.Fatal("test setup did not preserve relay port pointers")
	}
}

func TestEnsureGroupAndNodeIsAtomicAndIdempotent(t *testing.T) {
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
	var actorID string
	if err := pool.QueryRow(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),'node-bootstrap-test@example.invalid','hash','active') RETURNING id::text`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE actor_user_id=$1`, actorID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM nodes WHERE name='Bootstrap Test Node'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM resource_groups WHERE code IN ('TEST.BOOTSTRAP','TEST.BOOTSTRAP.ROLLBACK')`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actorID)
	}()
	repo := NewPostgresRepository(pool)
	groupID, err := id.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	group := GroupInput{Code: "TEST.BOOTSTRAP", Name: "Bootstrap Test", Region: "US", Enabled: true}
	relayPort := 24443
	node := NodeInput{GroupID: groupID, Name: "Bootstrap Test Node", Region: "US", Host: "bootstrap.example.invalid", RelayPort: &relayPort, Capabilities: []string{"forward"}, Tags: []string{}, MultiplierMilli: 1000, Enabled: true}
	createdGroup, createdNode, inserted, err := repo.EnsureGroupAndNode(ctx, groupID, group, node, actorID, "bootstrap-first")
	if err != nil || !inserted || createdGroup.ID != groupID || createdNode.GroupID != groupID {
		t.Fatalf("first bootstrap = %+v %+v inserted=%t err=%v", createdGroup, createdNode, inserted, err)
	}
	if createdNode.RelayPort == nil || *createdNode.RelayPort != relayPort {
		t.Fatalf("first bootstrap relay port = %v, want %d", createdNode.RelayPort, relayPort)
	}
	otherID, err := id.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	node.GroupID = otherID
	_, repeatedNode, inserted, err := repo.EnsureGroupAndNode(ctx, otherID, group, node, actorID, "bootstrap-repeat")
	if err != nil || inserted || repeatedNode.ID != createdNode.ID {
		t.Fatalf("repeat bootstrap = %+v inserted=%t err=%v", repeatedNode, inserted, err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_user_id=$1`, actorID).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("audit count = %d, err=%v", auditCount, err)
	}
	node.Host = "changed.example.invalid"
	if _, _, _, err := repo.EnsureGroupAndNode(ctx, otherID, group, node, actorID, "bootstrap-changed"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed identity = %v", err)
	}
	node.Host = "bootstrap.example.invalid"
	changedRelayPort := relayPort + 1
	node.RelayPort = &changedRelayPort
	if _, _, _, err := repo.EnsureGroupAndNode(ctx, otherID, group, node, actorID, "bootstrap-changed-relay"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed relay port = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE resource_groups SET enabled=false WHERE id=$1`, groupID); err != nil {
		t.Fatal(err)
	}
	node.Host = "bootstrap.example.invalid"
	if _, _, _, err := repo.EnsureGroupAndNode(ctx, otherID, group, node, actorID, "bootstrap-disabled"); !errors.Is(err, ErrConflict) {
		t.Fatalf("disabled group = %v", err)
	}
	rollbackID, err := id.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	rollbackGroup := GroupInput{Code: "TEST.BOOTSTRAP.ROLLBACK", Name: "Rollback Test", Region: "US", Enabled: true}
	node.GroupID = rollbackID
	invalidActor, err := id.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.EnsureGroupAndNode(ctx, rollbackID, rollbackGroup, node, invalidActor, "bootstrap-rollback"); err == nil {
		t.Fatal("invalid audit actor accepted")
	}
	var groupCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM resource_groups WHERE code=$1`, rollbackGroup.Code).Scan(&groupCount); err != nil || groupCount != 0 {
		t.Fatalf("failed transaction left group count = %d, err=%v", groupCount, err)
	}
}
