package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCustomRoleLifecycleAndLiveSessionPermissions(t *testing.T) {
	url := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("CONTROL_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var adminID, memberID string
	if err := pool.QueryRow(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),'rbac-admin@example.invalid','hash','active') RETURNING id::text`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_user_id=$1`, adminID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, adminID)
	}()
	if err := pool.QueryRow(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),'rbac-member@example.invalid','hash','active') RETURNING id::text`).Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, memberID) }()
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_code) VALUES($1,'admin'),($2,'user')`, adminID, memberID); err != nil {
		t.Fatal(err)
	}
	repo := NewPostgresRepository(pool)
	input, err := NormalizeRole(NewRole{Code: "support", Description: "Support", Permissions: []string{"usage.admin"}})
	if err != nil {
		t.Fatal(err)
	}
	role, err := repo.CreateRole(ctx, input, adminID, "create-role")
	if err != nil || role.Code != "support" || role.MemberCount != 0 || !slices.Contains(role.Permissions, "usage.admin") {
		t.Fatalf("create role = %+v %v", role, err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM user_roles WHERE role_code='support'`)
		_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE code='support'`)
	}()
	if _, err := repo.CreateRole(ctx, input, adminID, "duplicate-role"); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate role = %v", err)
	}
	unknown, _ := NormalizeRole(NewRole{Code: "unknown", Description: "Unknown", Permissions: []string{"permission.that.does.not.exist"}})
	if _, err := repo.CreateRole(ctx, unknown, adminID, "unknown-permission"); !errors.Is(err, ErrUnknownPermission) {
		t.Fatalf("unknown permission = %v", err)
	}
	if _, err := repo.UpdateRole(ctx, "admin", RolePatchInput{Description: ptr("Changed")}, adminID, "system-role"); !errors.Is(err, ErrRoleSystem) {
		t.Fatalf("system role update = %v", err)
	}
	if _, err := repo.SetUserRoles(ctx, adminID, RoleAssignment{RoleCodes: []string{"support"}}, adminID, "admin-roles"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("admin assignment = %v", err)
	}
	if _, err := repo.SetUserRoles(ctx, strings.ToUpper(memberID), RoleAssignment{RoleCodes: []string{"support"}}, memberID, "self-assignment-uppercase"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("case-varied self assignment = %v", err)
	}
	session := Session{TokenHash: sha256.Sum256([]byte("rbac-session")), CSRFHash: sha256.Sum256([]byte("rbac-csrf")), UserID: memberID, ExpiresAt: time.Now().Add(time.Hour)}
	if err := repo.InsertSession(ctx, session, "hash"); err != nil {
		t.Fatal(err)
	}
	initial, _, err := repo.FindSession(ctx, session.TokenHash)
	if err != nil || initial.UserID != memberID {
		t.Fatalf("initial session = %+v %v", initial, err)
	}
	member, err := repo.SetUserRoles(ctx, memberID, RoleAssignment{RoleCodes: []string{"support"}}, adminID, "assign-role")
	if err != nil || !slices.Contains(member.Permissions, "usage.admin") || !slices.Contains(member.Roles, "user") {
		t.Fatalf("assigned member = %+v %v", member, err)
	}
	_, principal, err := repo.FindSession(ctx, session.TokenHash)
	if err != nil || !slices.Contains(principal.Permissions, "usage.admin") {
		t.Fatalf("session after assign = %+v %v", principal, err)
	}
	if err := repo.DeleteRole(ctx, "support", adminID, "delete-in-use"); !errors.Is(err, ErrRoleInUse) {
		t.Fatalf("delete assigned role = %v", err)
	}
	empty := []string{}
	role, err = repo.UpdateRole(ctx, "support", RolePatchInput{Permissions: &empty}, adminID, "clear-permissions")
	if err != nil || len(role.Permissions) != 0 {
		t.Fatalf("updated role = %+v %v", role, err)
	}
	_, principal, err = repo.FindSession(ctx, session.TokenHash)
	if err != nil || slices.Contains(principal.Permissions, "usage.admin") {
		t.Fatalf("session after permission removal = %+v %v", principal, err)
	}
	member, err = repo.SetUserRoles(ctx, memberID, RoleAssignment{RoleCodes: []string{}}, adminID, "clear-roles")
	if err != nil || len(member.Roles) != 1 || member.Roles[0] != "user" {
		t.Fatalf("cleared member = %+v %v", member, err)
	}
	if err := repo.DeleteRole(ctx, "support", adminID, "delete-role"); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](value T) *T { return &value }
