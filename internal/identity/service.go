package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrNotFound           = errors.New("identity record not found")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthenticated    = errors.New("unauthenticated")
	ErrInvalidCSRF        = errors.New("invalid CSRF token")
)

var dummyPasswordHash = func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("dummy-password-for-unknown-user"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return hash
}()

type User struct {
	ID           string
	Email        string
	PasswordHash string
	Status       string
	Timezone     string
	Roles        []string
	Permissions  []string
}

type PublicUser struct {
	ID          string   `json:"id"`
	Email       string   `json:"email"`
	Status      string   `json:"status"`
	Timezone    string   `json:"timezone"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
}

func (u User) Public() PublicUser {
	return PublicUser{ID: u.ID, Email: u.Email, Status: u.Status,
		Timezone: u.Timezone, Roles: u.Roles, Permissions: u.Permissions}
}

type Session struct {
	TokenHash [32]byte
	CSRFHash  [32]byte
	UserID    string
	ExpiresAt time.Time
}

type Repository interface {
	FindUserByEmail(context.Context, string) (User, error)
	InsertSession(context.Context, Session, string) error
	FindSession(context.Context, [32]byte) (Session, User, error)
	DeleteSession(context.Context, [32]byte) error
}

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now}
}

type LoginResult struct {
	User      PublicUser
	Token     string
	CSRFToken string
	ExpiresAt time.Time
}

func (s *Service) Login(ctx context.Context, email, password string) (LoginResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	user, err := s.repository.FindUserByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
		return LoginResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, fmt.Errorf("find user: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil || user.Status != "active" {
		return LoginResult{}, ErrInvalidCredentials
	}
	tokenBytes, err := randomToken()
	if err != nil {
		return LoginResult{}, err
	}
	csrfBytes, err := randomToken()
	if err != nil {
		return LoginResult{}, err
	}
	expiresAt := s.now().Add(12 * time.Hour)
	if err := s.repository.InsertSession(ctx, Session{
		TokenHash: sha256.Sum256(tokenBytes), CSRFHash: sha256.Sum256(csrfBytes),
		UserID: user.ID, ExpiresAt: expiresAt,
	}, user.PasswordHash); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, fmt.Errorf("create session: %w", err)
	}
	return LoginResult{User: user.Public(), Token: hex.EncodeToString(tokenBytes),
		CSRFToken: hex.EncodeToString(csrfBytes), ExpiresAt: expiresAt}, nil
}

func randomToken() ([]byte, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	return token, nil
}

func hashToken(token string) ([32]byte, error) {
	var empty [32]byte
	if len(token) != 64 {
		return empty, ErrUnauthenticated
	}
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return empty, ErrUnauthenticated
	}
	return sha256.Sum256(decoded), nil
}

func (s *Service) lookup(ctx context.Context, token string) (Session, User, error) {
	hash, err := hashToken(token)
	if err != nil {
		return Session{}, User{}, err
	}
	record, user, err := s.repository.FindSession(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return Session{}, User{}, ErrUnauthenticated
	}
	if err != nil {
		return Session{}, User{}, fmt.Errorf("find session: %w", err)
	}
	if !record.ExpiresAt.After(s.now()) || user.Status != "active" {
		return Session{}, User{}, ErrUnauthenticated
	}
	return record, user, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (PublicUser, error) {
	_, user, err := s.lookup(ctx, token)
	if err != nil {
		return PublicUser{}, err
	}
	return user.Public(), nil
}

func (s *Service) VerifyCSRF(ctx context.Context, token, csrfToken string) (PublicUser, error) {
	record, user, err := s.lookup(ctx, token)
	if err != nil {
		return PublicUser{}, err
	}
	csrfBytes, err := hex.DecodeString(csrfToken)
	if err != nil || len(csrfBytes) != 32 {
		return PublicUser{}, ErrInvalidCSRF
	}
	hash := sha256.Sum256(csrfBytes)
	if subtle.ConstantTimeCompare(hash[:], record.CSRFHash[:]) != 1 {
		return PublicUser{}, ErrInvalidCSRF
	}
	return user.Public(), nil
}

func (s *Service) Logout(ctx context.Context, token, csrfToken string) error {
	if _, err := s.VerifyCSRF(ctx, token, csrfToken); err != nil {
		return err
	}
	hash, _ := hashToken(token)
	if err := s.repository.DeleteSession(ctx, hash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
