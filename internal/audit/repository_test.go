package audit

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func auditTestDatabaseURL() string {
	if value := os.Getenv("CONTROL_TEST_DATABASE_URL"); value != "" {
		return value
	}
	if os.Getenv("CONTROL_TEST_DB_NAME") != "" && os.Getenv("POSTGRES_PASSWORD") != "" {
		return (&url.URL{Scheme: "postgres", User: url.UserPassword("controlplane", os.Getenv("POSTGRES_PASSWORD")), Host: "db:5432", Path: "/" + os.Getenv("CONTROL_TEST_DB_NAME")}).String()
	}
	return ""
}

func TestListAuditUsesStableDescendingPagesWithoutExposingSnapshots(t *testing.T) {
	databaseURL := auditTestDatabaseURL()
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var actor string
	email := "audit-list-" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "") + "@example.invalid"
	if err := pool.QueryRow(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),$1,'hash','active') RETURNING id::text`, email).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE actor_user_id=$1`, actor)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor)
	})
	at := time.Date(2026, 9, 27, 8, 0, 0, 123000000, time.UTC)
	ids := []string{
		"11111111-1111-7111-8111-111111111111",
		"22222222-2222-7222-8222-222222222222",
		"33333333-3333-7333-8333-333333333333",
	}
	for index, id := range ids {
		created := at
		if index == 0 {
			created = at.Add(-time.Second)
		}
		_, err := pool.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id,created_at)
VALUES ($1,$2,'update','node',$2,$3,$4,$5)`, id, actor, json.RawMessage(`{"password":"must-not-leak"}`), "audit-request-"+id, created)
		if err != nil {
			t.Fatal(err)
		}
	}
	repo := NewPostgresRepository(pool)
	first, err := repo.List(ctx, 2, "")
	if err != nil || len(first.Items) != 2 || first.Items[0].ID != ids[2] || first.Items[1].ID != ids[1] || first.NextCursor == nil {
		t.Fatalf("first page = %+v %v", first, err)
	}
	raw, _ := json.Marshal(first)
	if strings.Contains(string(raw), "must-not-leak") || strings.Contains(string(raw), `"after"`) {
		t.Fatalf("audit snapshot leaked: %s", raw)
	}
	second, err := repo.List(ctx, 2, *first.NextCursor)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != ids[0] || second.NextCursor != nil {
		t.Fatalf("second page = %+v %v", second, err)
	}
}
