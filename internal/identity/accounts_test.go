package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNormalizeNewUserAndPasswordChange(t *testing.T) {
	value, err := NormalizeNewUser(NewUser{Email: " Member@Example.com ", Password: "long-initial-password"})
	if err != nil || value.Email != "member@example.com" || value.Timezone != "Asia/Shanghai" {
		t.Fatalf("normalized user = %+v, %v", value, err)
	}
	for _, candidate := range []NewUser{
		{Email: "bad-address", Password: "long-initial-password"},
		{Email: "member@example.com", Password: "short"},
		{Email: "member@example.com", Password: strings.Repeat("a", 73)},
		{Email: "member@example.com", Password: "long-initial-password", Timezone: "Local"},
		{Email: "member@example.com", Password: "long-initial-password", Timezone: "Mars/Base"},
	} {
		if _, err := NormalizeNewUser(candidate); err == nil {
			t.Fatalf("accepted invalid user: %+v", candidate)
		}
	}
	if err := ValidatePasswordChange("old-password-12", "old-password-12"); err == nil {
		t.Fatal("accepted unchanged password")
	}
	if err := ValidatePasswordChange("old-password-12", "short"); err == nil {
		t.Fatal("accepted short new password")
	}
	if err := ValidatePasswordChange("old-password-12", "new-password-123"); err != nil {
		t.Fatalf("valid password change = %v", err)
	}
}

func TestPostgresMemberCreationAndPasswordRotation(t *testing.T) {
	databaseURL := testDatabaseURL()
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	admin, err := BootstrapAdmin(ctx, pool, "accounts-admin@example.invalid", "long-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var memberID string
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE actor_user_id=$1 OR actor_user_id=$2`, admin.ID, memberID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1 OR id=$2`, admin.ID, memberID)
	}()
	repo := NewPostgresRepository(pool)
	input, err := NormalizeNewUser(NewUser{Email: " Accounts-Member@example.invalid ", Password: "long-initial-password", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := repo.CreateMember(ctx, input, admin.ID, "create-user-request")
	if err != nil || member.ID == "" || member.Email != "accounts-member@example.invalid" || len(member.Roles) != 1 || member.Roles[0] != "user" {
		t.Fatalf("created member = %+v, %v", member, err)
	}
	memberID = member.ID
	if _, err := repo.CreateMember(ctx, input, admin.ID, "duplicate-user-request"); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate member = %v", err)
	}
	page, err := repo.ListUsers(ctx, 10, "")
	if err != nil || len(page) != 2 {
		t.Fatalf("user page = %+v, %v", page, err)
	}
	service := NewService(repo)
	beforeRotation, err := repo.FindUserByEmail(ctx, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := service.Login(ctx, member.Email, "long-initial-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ChangePassword(ctx, member.ID, "wrong-password", "new-password-123", "wrong-change-request"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong old password = %v", err)
	}
	if _, err := service.Authenticate(ctx, initial.Token); err != nil {
		t.Fatalf("wrong password revoked session: %v", err)
	}
	if err := repo.ChangePassword(ctx, member.ID, "long-initial-password", "new-password-123", "change-password-request"); err != nil {
		t.Fatal(err)
	}
	stale := Session{TokenHash: sha256.Sum256([]byte("stale-login")), CSRFHash: sha256.Sum256([]byte("stale-csrf")), UserID: member.ID, ExpiresAt: time.Now().Add(time.Hour)}
	if err := repo.InsertSession(ctx, stale, beforeRotation.PasswordHash); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("stale login after rotation = %v", err)
	}
	if _, _, err := repo.FindSession(ctx, stale.TokenHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale session was stored: %v", err)
	}
	if _, err := service.Authenticate(ctx, initial.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old session survived rotation = %v", err)
	}
	if _, err := service.Login(ctx, member.Email, "long-initial-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password accepted = %v", err)
	}
	if _, err := service.Login(ctx, member.Email, "new-password-123"); err != nil {
		t.Fatalf("new password rejected = %v", err)
	}
	var auditText string
	if err := pool.QueryRow(ctx, `SELECT string_agg(COALESCE(after_json::text,''),' ') FROM audit_logs
WHERE request_id IN ('create-user-request','change-password-request')`).Scan(&auditText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditText, "long-initial-password") || strings.Contains(auditText, "new-password-123") || strings.Contains(auditText, "password_hash") {
		t.Fatalf("audit includes password material: %q", auditText)
	}
}

func TestPostgresMemberStatusRevokesSessionsAndQueuesConvergence(t *testing.T) {
	databaseURL := testDatabaseURL()
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	admin, err := BootstrapAdmin(ctx, pool, "status-admin@example.invalid", "long-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, admin.ID); err != nil {
			t.Errorf("cleanup status administrator: %v", err)
		}
	})
	repo := NewPostgresRepository(pool)
	member, err := repo.CreateMember(ctx, MemberInput{Email: "status-member@example.invalid", Password: "long-member-password", Timezone: "UTC"}, admin.ID, "status-create")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, item := range []struct {
			query string
			args  []any
		}{
			{`DELETE FROM outbox_events WHERE kind='user.changed' AND aggregate_id=$1`, []any{member.ID}},
			{`DELETE FROM audit_logs WHERE actor_user_id=$1 OR object_id=$2`, []any{admin.ID, member.ID}},
			{`DELETE FROM browser_sessions WHERE user_id=$1`, []any{member.ID}},
			{`DELETE FROM users WHERE id=$1`, []any{member.ID}},
		} {
			if _, err := pool.Exec(context.Background(), item.query, item.args...); err != nil {
				t.Errorf("cleanup account status fixture: %v", err)
			}
		}
	})
	service := NewService(repo)
	initial, err := service.Login(ctx, member.Email, "long-member-password")
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := repo.SetUserStatus(ctx, member.ID, "disabled", admin.ID, "status-disable")
	if err != nil || disabled.Status != "disabled" {
		t.Fatalf("disable member = %+v, %v", disabled, err)
	}
	if _, err := service.Authenticate(ctx, initial.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("disabled session remains valid: %v", err)
	}
	if _, err := service.Login(ctx, member.Email, "long-member-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled member login: %v", err)
	}
	var events, audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE kind='user.changed' AND aggregate_id=$1`, member.ID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("disable events = %d, %v", events, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE object_id=$1 AND request_id='status-disable'`, member.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("disable audits = %d, %v", audits, err)
	}
	if _, err := repo.SetUserStatus(ctx, member.ID, "disabled", admin.ID, "status-idempotent"); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE kind='user.changed' AND aggregate_id=$1`, member.ID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("idempotent disable events = %d, %v", events, err)
	}
	if _, err := repo.SetUserStatus(ctx, admin.ID, "disabled", admin.ID, "status-self"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("administrator disabled own account: %v", err)
	}
	if _, err := repo.SetUserStatus(ctx, admin.ID, "disabled", member.ID, "status-last-admin"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("last administrator disabled: %v", err)
	}
	restored, err := repo.SetUserStatus(ctx, member.ID, "active", admin.ID, "status-restore")
	if err != nil || restored.Status != "active" {
		t.Fatalf("restore member = %+v, %v", restored, err)
	}
	if _, err := service.Authenticate(ctx, initial.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old session revived after restore: %v", err)
	}
	if _, err := service.Login(ctx, member.Email, "long-member-password"); err != nil {
		t.Fatalf("restored member cannot log in: %v", err)
	}
}

func TestPostgresLoginWaitsForPasswordRotationLock(t *testing.T) {
	databaseURL := testDatabaseURL()
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := NewPostgresRepository(pool)
	admin, err := BootstrapAdmin(ctx, pool, "race-admin@example.invalid", "old-race-password")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, admin.ID) }()
	before, err := repo.FindUserByEmail(ctx, admin.Email)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, admin.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		session := Session{TokenHash: sha256.Sum256([]byte("race-session")), CSRFHash: sha256.Sum256([]byte("race-csrf")), UserID: admin.ID, ExpiresAt: time.Now().Add(time.Hour)}
		result <- repo.InsertSession(ctx, session, before.PasswordHash)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-result:
			t.Fatalf("login session bypassed rotation lock: %v", err)
		default:
		}
		var waiting int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
WHERE datname=current_database() AND wait_event_type='Lock'
AND query='SELECT password_hash,status FROM users WHERE id=$1 FOR UPDATE'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("login did not wait on the user row lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash='rotated-test-hash' WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("session after locked rotation = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("login session did not complete after rotation")
	}
}
