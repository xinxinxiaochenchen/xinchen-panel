package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRepositorySessionLifecycle(t *testing.T) {
	databaseURL := testDatabaseURL()
	if databaseURL == "" {
		t.Skip("CONTROL_TEST_DATABASE_URL is required for PostgreSQL integration")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var userID string
	err = pool.QueryRow(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(), 'identity-integration@example.invalid', 'hash', 'active') RETURNING id::text`).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID); err != nil {
			t.Error(err)
		}
		pool.Close()
	})
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_code) VALUES ($1,'user')`, userID); err != nil {
		t.Fatal(err)
	}
	repo := NewPostgresRepository(pool)
	user, err := repo.FindUserByEmail(ctx, "identity-integration@example.invalid")
	if err != nil || user.ID != userID || !slices.Contains(user.Permissions, "nodes.read") || !slices.Contains(user.Permissions, "lines.write.self") || slices.Contains(user.Permissions, "lines.write") || !slices.Contains(user.Roles, "user") {
		t.Fatalf("lookup = %+v, %v", user, err)
	}
	record := Session{TokenHash: sha256.Sum256([]byte("session")), CSRFHash: sha256.Sum256([]byte("csrf")), UserID: userID, ExpiresAt: time.Now().Add(time.Hour)}
	if err := repo.InsertSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	found, principal, err := repo.FindSession(ctx, record.TokenHash)
	if err != nil || found.CSRFHash != record.CSRFHash || principal.Email != user.Email {
		t.Fatalf("session = %+v, user = %+v, err = %v", found, principal, err)
	}
	if err := repo.DeleteSession(ctx, record.TokenHash); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.FindSession(ctx, record.TokenHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted session = %v", err)
	}
	expiredHash := sha256.Sum256([]byte("expired-session"))
	if _, err := pool.Exec(ctx, `INSERT INTO browser_sessions(token_hash,csrf_hash,user_id,created_at,expires_at)
VALUES ($1,$2,$3,now()-interval '2 hours',now()-interval '1 hour')`, expiredHash[:], record.CSRFHash[:], userID); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	deleted, err := repo.DeleteExpiredSessions(ctx, time.Now(), 1)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted expired sessions = %d, %v", deleted, err)
	}
	if _, _, err := repo.FindSession(ctx, expiredHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session still exists: %v", err)
	}
	if _, _, err := repo.FindSession(ctx, record.TokenHash); err != nil {
		t.Fatalf("live session removed: %v", err)
	}
}
