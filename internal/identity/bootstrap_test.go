package identity

import (
	"context"
	"errors"
	"net/url"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func TestBootstrapAdminPersistsRoleAndRejectsDuplicate(t *testing.T) {
	databaseURL := testDatabaseURL()
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	adminID := ""
	t.Cleanup(func() {
		if adminID != "" {
			if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, adminID); err != nil {
				t.Error(err)
			}
		}
		pool.Close()
	})
	admin, err := BootstrapAdmin(ctx, pool, "Bootstrap-Integration@example.invalid", "long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	adminID = admin.ID
	var hash, role string
	err = pool.QueryRow(ctx, `SELECT u.password_hash, ur.role_code FROM users u JOIN user_roles ur ON ur.user_id=u.id WHERE u.id=$1`, admin.ID).Scan(&hash, &role)
	if err != nil || role != "admin" || admin.Email != "bootstrap-integration@example.invalid" {
		t.Fatalf("admin = %+v role = %q error = %v", admin, role, err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("long-test-password")) != nil {
		t.Fatal("password hash does not verify")
	}
	loaded, err := NewPostgresRepository(pool).FindUserByEmail(ctx, admin.Email)
	if err != nil || !slices.Contains(loaded.Permissions, "lines.write") {
		t.Fatalf("admin permissions = %+v, err = %v", loaded.Permissions, err)
	}
	if _, err := BootstrapAdmin(ctx, pool, "BOOTSTRAP-INTEGRATION@example.invalid", "another-long-password"); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate admin = %v", err)
	}
	if _, err := BootstrapAdmin(ctx, pool, "bad@example.invalid", "short"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("short password = %v", err)
	}
}

func testDatabaseURL() string {
	if value := os.Getenv("CONTROL_TEST_DATABASE_URL"); value != "" {
		return value
	}
	if os.Getenv("CONTROL_TEST_DB_NAME") != "" && os.Getenv("POSTGRES_PASSWORD") != "" {
		return (&url.URL{Scheme: "postgres", User: url.UserPassword("controlplane", os.Getenv("POSTGRES_PASSWORD")), Host: "db:5432", Path: "/" + os.Getenv("CONTROL_TEST_DB_NAME")}).String()
	}
	return ""
}
