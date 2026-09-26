package proxyaccess

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func proxyTestDatabaseURL() string {
	if value := os.Getenv("CONTROL_TEST_DATABASE_URL"); value != "" {
		return value
	}
	if os.Getenv("CONTROL_TEST_DB_NAME") != "" && os.Getenv("POSTGRES_PASSWORD") != "" {
		return (&url.URL{Scheme: "postgres", User: url.UserPassword("controlplane", os.Getenv("POSTGRES_PASSWORD")), Host: "db:5432", Path: "/" + os.Getenv("CONTROL_TEST_DB_NAME")}).String()
	}
	return ""
}

func TestPostgresProxyAccessCreationAndOwnership(t *testing.T) {
	databaseURL := proxyTestDatabaseURL()
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	insertID := func(query string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	owner := insertID(`INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),'proxy-owner@example.invalid','hash','active') RETURNING id::text`)
	other := insertID(`INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),'proxy-other@example.invalid','hash','active') RETURNING id::text`)
	group := insertID(`INSERT INTO resource_groups(id,code,name,region) VALUES (gen_random_uuid(),'TEST.PROXY','Proxy','JP') RETURNING id::text`)
	node := insertID(`INSERT INTO nodes(id,group_id,name,region,host,proxy_port,capabilities) VALUES (gen_random_uuid(),$1,'Proxy','JP','proxy.example.invalid',443,ARRAY['proxy']) RETURNING id::text`, group)
	line := insertID(`INSERT INTO lines(id,name,created_by) VALUES (gen_random_uuid(),'Shared Proxy',$1) RETURNING id::text`, owner)
	if _, err := pool.Exec(ctx, `INSERT INTO line_hops(line_id,position,node_id,role) VALUES ($1,0,$2,'egress')`, line, node); err != nil {
		t.Fatal(err)
	}
	plan := insertID(`INSERT INTO plans(id,name,quota_bytes) VALUES (gen_random_uuid(),'proxy-test-plan',1000000) RETURNING id::text`)
	snapshot, err := json.Marshal(map[string]any{"resource_group_ids": []string{group}, "line_ids": []string{line}, "limits": map[string]any{"allow_custom_lines": false}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json)
VALUES (gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, owner, plan, snapshot); err != nil {
		t.Fatal(err)
	}
	key, err := ParseKey(base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := NewCredentialCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewPostgresRepository(pool, cipher)
	input := AccessInput{Name: "JP", LineID: line, Enabled: true}
	if _, _, err := repo.Create(ctx, other, input, "other-user"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsubscribed creation = %v", err)
	}
	access, credential, err := repo.Create(ctx, owner, input, "create-proxy")
	if err != nil || access.UserID != owner || access.LineID != line || access.ApplyStatus != "pending" || len(credential) != 43 {
		t.Fatalf("created access = %+v, credential length=%d, err=%v", access, len(credential), err)
	}
	encoded, err := json.Marshal(access)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), credential) || strings.Contains(string(encoded), TrojanDigest(credential)) {
		t.Fatal("safe access DTO leaked credential")
	}
	var digest, sealed string
	if err := pool.QueryRow(ctx, `SELECT credential_hash,credential_ciphertext FROM proxy_accesses WHERE id=$1`, access.ID).Scan(&digest, &sealed); err != nil {
		t.Fatal(err)
	}
	if digest != TrojanDigest(credential) || strings.Contains(sealed, credential) {
		t.Fatal("stored credential is not hashed and encrypted")
	}
	if _, _, err := repo.Create(ctx, owner, input, "same-name"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate access name = %v", err)
	}
	if _, err := repo.GetOwn(ctx, other, access.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner read = %v", err)
	}
	if got, err := repo.GetOwn(ctx, owner, access.ID); err != nil || got.ID != access.ID {
		t.Fatalf("own read = %+v, %v", got, err)
	}
	if got, err := repo.ListOwn(ctx, owner, 10, ""); err != nil || len(got) != 1 || got[0].ID != access.ID {
		t.Fatalf("own list = %+v, %v", got, err)
	}
	if _, err := repo.RevealOwn(ctx, other, access.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner credential read = %v", err)
	}
	if original, err := repo.RevealOwn(ctx, owner, access.ID); err != nil || original != credential {
		t.Fatalf("own credential read = %q, %v", original, err)
	}
	rotated, err := repo.RotateOwn(ctx, owner, access.ID, "rotate-proxy")
	if err != nil || rotated == credential || len(rotated) != 43 {
		t.Fatalf("rotation = length %d, %v", len(rotated), err)
	}
	if err := pool.QueryRow(ctx, `SELECT credential_hash FROM proxy_accesses WHERE id=$1`, access.ID).Scan(&digest); err != nil || digest != TrojanDigest(rotated) {
		t.Fatalf("rotated hash = %s, %v", digest, err)
	}
	if got, err := repo.RevealOwn(ctx, owner, access.ID); err != nil || got != rotated {
		t.Fatalf("rotated credential read = %q, %v", got, err)
	}
	disabled := false
	updated, err := repo.UpdateOwn(ctx, owner, access.ID, AccessPatch{Enabled: &disabled}, "disable-proxy")
	if err != nil || updated.Enabled || updated.ApplyStatus != "pending" {
		t.Fatalf("disabled access = %+v, %v", updated, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='disabled' WHERE id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := repo.UpdateOwn(ctx, owner, access.ID, AccessPatch{Enabled: &enabled}, "disabled-reenable"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled user re-enabled access = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='active' WHERE id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE lines SET enabled=false WHERE id=$1`, line); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Create(ctx, owner, AccessInput{Name: "Disabled Line", LineID: line, Enabled: true}, "disabled-line"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled line creation = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE lines SET enabled=true WHERE id=$1`, line); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET enabled=false WHERE id=$1`, node); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Create(ctx, owner, AccessInput{Name: "Disabled Node", LineID: line, Enabled: true}, "disabled-node"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled node creation = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET enabled=true WHERE id=$1`, node); err != nil {
		t.Fatal(err)
	}
	var concurrent sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		concurrent.Add(1)
		go func() {
			defer concurrent.Done()
			_, _, err := repo.Create(ctx, owner, AccessInput{Name: "Concurrent", LineID: line, Enabled: true}, "concurrent")
			results <- err
		}()
	}
	concurrent.Wait()
	close(results)
	var successful, conflicting int
	for err := range results {
		switch {
		case err == nil:
			successful++
		case errors.Is(err, ErrConflict):
			conflicting++
		default:
			t.Fatalf("concurrent creation = %v", err)
		}
	}
	if successful != 1 || conflicting != 1 {
		t.Fatalf("concurrent duplicate outcome success=%d conflict=%d", successful, conflicting)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET ends_at=clock_timestamp()-interval '1 second' WHERE user_id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Create(ctx, owner, AccessInput{Name: "Expired", LineID: line, Enabled: true}, "expired"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired membership creation = %v", err)
	}
	if _, err := repo.UpdateOwn(ctx, owner, access.ID, AccessPatch{Enabled: &enabled}, "expired-reenable"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired membership re-enabled access = %v", err)
	}
	if _, err := repo.RotateOwn(ctx, owner, access.ID, "expired-rotation"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired membership rotated credential = %v", err)
	}
	var eventCount int
	var recorded string
	if err := pool.QueryRow(ctx, `SELECT count(*),coalesce(string_agg(payload::text,' '),'') FROM outbox_events
WHERE kind='proxy_access.changed' AND aggregate_id=$1`, access.ID).Scan(&eventCount, &recorded); err != nil || eventCount < 3 {
		t.Fatalf("proxy access outbox events = %d, %v", eventCount, err)
	}
	if strings.Contains(recorded, credential) || strings.Contains(recorded, rotated) {
		t.Fatal("outbox leaked a proxy credential")
	}
	var auditJSON string
	if err := pool.QueryRow(ctx, `SELECT coalesce(string_agg(after_json::text,' '),'') FROM audit_logs
WHERE object_type='proxy_access' AND object_id=$1`, access.ID).Scan(&auditJSON); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditJSON, credential) || strings.Contains(auditJSON, rotated) {
		t.Fatal("audit log leaked a proxy credential")
	}
	// A competing owner mutation must never observe DeleteOwn holding the
	// access row while it waits for the owner row. That inverted order can
	// deadlock with credential rotation or an account-status change.
	ownerLock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ownerLock.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, owner); err != nil {
		ownerLock.Rollback(ctx)
		t.Fatal(err)
	}
	deleteCtx, cancelDelete := context.WithTimeout(ctx, 5*time.Second)
	defer cancelDelete()
	deleteResult := make(chan error, 1)
	go func() { deleteResult <- repo.DeleteOwn(deleteCtx, owner, access.ID, "delete-proxy") }()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
WHERE datname=current_database() AND wait_event_type='Lock' AND pid<>pg_backend_pid())`).Scan(&waiting); err != nil {
			ownerLock.Rollback(ctx)
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if deleteCtx.Err() != nil {
			ownerLock.Rollback(ctx)
			t.Fatal("DeleteOwn did not reach the owner lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	accessLock, err := pool.Begin(ctx)
	if err != nil {
		ownerLock.Rollback(ctx)
		t.Fatal(err)
	}
	var unlockedAccessID string
	lockErr := accessLock.QueryRow(ctx, `SELECT id::text FROM proxy_accesses WHERE id=$1 FOR UPDATE NOWAIT`, access.ID).Scan(&unlockedAccessID)
	accessLock.Rollback(ctx)
	ownerLock.Rollback(ctx)
	if lockErr != nil || unlockedAccessID != access.ID {
		t.Fatalf("DeleteOwn locked access before owner: id=%q, err=%v", unlockedAccessID, lockErr)
	}
	if err := <-deleteResult; err != nil {
		t.Fatalf("expired owner delete = %v", err)
	}
	if _, err := repo.GetOwn(ctx, owner, access.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted access still readable = %v", err)
	}
}
