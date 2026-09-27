package orchestration

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRelaySecretKey = errors.New("invalid relay secret encryption key")

type PostgresRelaySecretStore struct {
	pool *pgxpool.Pool
	key  []byte
}

func NewPostgresRelaySecretStore(pool *pgxpool.Pool, key []byte) (*PostgresRelaySecretStore, error) {
	if pool == nil || len(key) != 32 {
		return nil, ErrRelaySecretKey
	}
	return &PostgresRelaySecretStore{pool: pool, key: append([]byte(nil), key...)}, nil
}
func (s *PostgresRelaySecretStore) GetOrCreate(ctx context.Context, lineID string, generation uint64, edge int) ([]byte, error) {
	if s == nil || s.pool == nil || len(s.key) != 32 || lineID == "" || generation == 0 || edge < 0 {
		return nil, ErrRelaySecretKey
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, ErrRelaySecretKey
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrRelaySecretKey
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO line_relay_generations(line_id,generation) VALUES($1,$2) ON CONFLICT DO NOTHING`, lineID, generation); err != nil {
		return nil, fmt.Errorf("store relay generation: %w", err)
	}
	candidate := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, candidate); err != nil {
		return nil, fmt.Errorf("generate relay secret: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate relay nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, candidate, nil)
	if _, err := s.pool.Exec(ctx, `INSERT INTO line_relay_secrets(line_id,generation,edge_position,secret_ciphertext) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, lineID, generation, edge, ciphertext); err != nil {
		return nil, fmt.Errorf("store relay secret: %w", err)
	}
	var stored []byte
	if err := s.pool.QueryRow(ctx, `SELECT secret_ciphertext FROM line_relay_secrets WHERE line_id=$1 AND generation=$2 AND edge_position=$3`, lineID, generation, edge).Scan(&stored); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("relay secret disappeared")
		}
		return nil, fmt.Errorf("read relay secret: %w", err)
	}
	if len(stored) < gcm.NonceSize() {
		return nil, errors.New("invalid stored relay secret")
	}
	plain, err := gcm.Open(nil, stored[:gcm.NonceSize()], stored[gcm.NonceSize():], nil)
	if err != nil || len(plain) != 32 {
		return nil, errors.New("invalid stored relay secret")
	}
	return plain, nil
}
