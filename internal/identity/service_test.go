package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type memoryRepository struct {
	user     User
	sessions map[[32]byte]Session
	onFind   func()
}

type auditedMemoryRepository struct {
	*memoryRepository
	requestIDs []string
	logoutIDs  []string
}

func (r *auditedMemoryRepository) InsertSessionWithAudit(ctx context.Context, record Session, expectedPasswordHash, requestID string) error {
	r.requestIDs = append(r.requestIDs, requestID)
	return r.memoryRepository.InsertSession(ctx, record, expectedPasswordHash)
}

func (r *memoryRepository) FindUserByEmail(_ context.Context, email string) (User, error) {
	if email != r.user.Email {
		return User{}, ErrNotFound
	}
	user := r.user
	if r.onFind != nil {
		r.onFind()
	}
	return user, nil
}

func (r *memoryRepository) InsertSession(_ context.Context, record Session, expectedPasswordHash string) error {
	if r.user.PasswordHash != expectedPasswordHash || r.user.Status != "active" {
		return ErrInvalidCredentials
	}
	if r.sessions == nil {
		r.sessions = map[[32]byte]Session{}
	}
	r.sessions[record.TokenHash] = record
	return nil
}

func (r *memoryRepository) InsertSessionWithAudit(ctx context.Context, record Session, expectedPasswordHash, _ string) error {
	return r.InsertSession(ctx, record, expectedPasswordHash)
}

func (r *memoryRepository) FindSession(_ context.Context, hash [32]byte) (Session, User, error) {
	record, ok := r.sessions[hash]
	if !ok {
		return Session{}, User{}, ErrNotFound
	}
	return record, r.user, nil
}

func (r *memoryRepository) DeleteSession(_ context.Context, hash [32]byte) error {
	delete(r.sessions, hash)
	return nil
}

func (r *memoryRepository) DeleteSessionWithAudit(ctx context.Context, hash [32]byte, _ string, _ string) error {
	return r.DeleteSession(ctx, hash)
}

func (r *auditedMemoryRepository) DeleteSessionWithAudit(ctx context.Context, hash [32]byte, _ string, requestID string) error {
	r.logoutIDs = append(r.logoutIDs, requestID)
	return r.memoryRepository.DeleteSession(ctx, hash)
}

func activeRepository(t *testing.T) *memoryRepository {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return &memoryRepository{user: User{
		ID: "11111111-1111-7111-8111-111111111111", Email: "member@example.com",
		PasswordHash: string(hash), Status: "active", Timezone: "Asia/Shanghai",
		Roles: []string{"user"}, Permissions: []string{"nodes.read"},
	}}
}

func TestLoginCreatesHashedSessionAndAuthenticates(t *testing.T) {
	repo := activeRepository(t)
	service := NewService(repo)
	result, err := service.Login(context.Background(), " MEMBER@example.com ", "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if result.User.Email != repo.user.Email || result.Token == "" || result.CSRFToken == "" || result.Token == result.CSRFToken {
		t.Fatalf("unexpected login result: %+v", result.User)
	}
	if len(repo.sessions) != 1 {
		t.Fatalf("session count = %d", len(repo.sessions))
	}
	rawToken, err := hex.DecodeString(result.Token)
	if err != nil || len(rawToken) != 32 {
		t.Fatalf("invalid opaque token: %v", err)
	}
	rawCSRF, err := hex.DecodeString(result.CSRFToken)
	if err != nil || len(rawCSRF) != 32 {
		t.Fatalf("invalid CSRF token: %v", err)
	}
	for hash, record := range repo.sessions {
		if hash != sha256.Sum256(rawToken) || hash != record.TokenHash {
			t.Fatal("session hash mismatch")
		}
		if record.CSRFHash != sha256.Sum256(rawCSRF) {
			t.Fatal("CSRF hash mismatch")
		}
		if time.Until(record.ExpiresAt) < 11*time.Hour {
			t.Fatal("session expires too early")
		}
	}
	principal, err := service.Authenticate(context.Background(), result.Token)
	if err != nil || principal.Email != repo.user.Email {
		t.Fatalf("authentication = %+v, %v", principal, err)
	}
}

func TestLoginWithRequestIDUsesAtomicAuditAwareSessionInsert(t *testing.T) {
	repo := &auditedMemoryRepository{memoryRepository: activeRepository(t)}
	service := NewService(repo)
	if _, err := service.LoginWithRequestID(context.Background(), repo.user.Email, "correct-password", "request-login-1"); err != nil {
		t.Fatal(err)
	}
	if len(repo.requestIDs) != 1 || repo.requestIDs[0] != "request-login-1" {
		t.Fatalf("audit request IDs = %#v", repo.requestIDs)
	}
}

func TestLogoutWithRequestIDUsesAtomicAuditAwareSessionDelete(t *testing.T) {
	repo := &auditedMemoryRepository{memoryRepository: activeRepository(t)}
	service := NewService(repo)
	login, err := service.Login(context.Background(), repo.user.Email, "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.LogoutWithRequestID(context.Background(), login.Token, login.CSRFToken, "request-logout-1"); err != nil {
		t.Fatal(err)
	}
	if len(repo.logoutIDs) != 1 || repo.logoutIDs[0] != "request-logout-1" || len(repo.sessions) != 0 {
		t.Fatalf("logout audit IDs=%#v sessions=%d", repo.logoutIDs, len(repo.sessions))
	}
}

func TestVerifyPasswordRequiresCurrentSessionAndPassword(t *testing.T) {
	repo := activeRepository(t)
	service := NewService(repo)
	login, err := service.Login(context.Background(), repo.user.Email, "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if user, err := service.VerifyPassword(context.Background(), login.Token, "correct-password"); err != nil || user.ID != repo.user.ID {
		t.Fatalf("valid reauthentication = %+v, %v", user, err)
	}
	if _, err := service.VerifyPassword(context.Background(), login.Token, "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("incorrect password = %v", err)
	}
	if _, err := service.VerifyPassword(context.Background(), "invalid", "correct-password"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("invalid session = %v", err)
	}
	repo.user.PasswordHash = "rotated-password-hash"
	if _, err := service.VerifyPassword(context.Background(), login.Token, "correct-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("rotated password = %v", err)
	}
}

func TestLoginRejectsInvalidCredentialsUniformly(t *testing.T) {
	for _, tc := range []struct{ email, password, status string }{
		{"missing@example.com", "correct-password", "active"},
		{"member@example.com", "wrong-password", "active"},
		{"member@example.com", "correct-password", "disabled"},
	} {
		repo := activeRepository(t)
		repo.user.Status = tc.status
		_, err := NewService(repo).Login(context.Background(), tc.email, tc.password)
		if !errors.Is(err, ErrInvalidCredentials) || len(repo.sessions) != 0 {
			t.Fatalf("%+v: err=%v sessions=%d", tc, err, len(repo.sessions))
		}
	}
}

func TestLoginRejectsPasswordRotatedAfterVerification(t *testing.T) {
	repo := activeRepository(t)
	repo.onFind = func() { repo.user.PasswordHash = "rotated-password-hash" }
	if _, err := NewService(repo).Login(context.Background(), repo.user.Email, "correct-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login after password rotation = %v", err)
	}
	if len(repo.sessions) != 0 {
		t.Fatal("stale login inserted a session")
	}
}

func TestSessionExpiryCSRFAndRevocation(t *testing.T) {
	repo := activeRepository(t)
	service := NewService(repo)
	result, err := service.Login(context.Background(), repo.user.Email, "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyCSRF(context.Background(), result.Token, "wrong"); !errors.Is(err, ErrInvalidCSRF) {
		t.Fatalf("wrong CSRF = %v", err)
	}
	if _, err := service.VerifyCSRF(context.Background(), result.Token, result.CSRFToken); err != nil {
		t.Fatalf("valid CSRF = %v", err)
	}
	if err := service.Logout(context.Background(), result.Token, result.CSRFToken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), result.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked session = %v", err)
	}
	result, err = service.Login(context.Background(), repo.user.Email, "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	for key, record := range repo.sessions {
		record.ExpiresAt = time.Now().Add(-time.Second)
		repo.sessions[key] = record
	}
	if _, err := service.Authenticate(context.Background(), result.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired session = %v", err)
	}
}
