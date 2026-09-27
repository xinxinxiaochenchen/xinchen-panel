package orchestration

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

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
	if s == nil || s.pool == nil || len(s.key) != 32 || !relayLineIDPattern.MatchString(lineID) ||
		generation == 0 || generation > math.MaxInt64 || edge < 0 {
		return nil, ErrRelaySecretKey
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO line_relay_generations(line_id,generation) VALUES($1,$2) ON CONFLICT DO NOTHING`, lineID, generation); err != nil {
		return nil, fmt.Errorf("store relay generation: %w", err)
	}
	candidate := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, candidate); err != nil {
		return nil, fmt.Errorf("generate relay secret: %w", err)
	}
	ciphertext, err := sealRelaySecret(s.key, lineID, generation, edge, candidate)
	if err != nil {
		return nil, fmt.Errorf("encrypt relay secret: %w", err)
	}
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
	plain, err := openRelaySecret(s.key, lineID, generation, edge, stored)
	if err != nil {
		return nil, errors.New("invalid stored relay secret")
	}
	return plain, nil
}

// The additional authenticated data makes a ciphertext valid only for the
// exact line generation and edge row where it was created. This prevents a
// database mix-up from silently wiring one relay edge with another edge's key.
func relaySecretAAD(lineID string, generation uint64, edge int) []byte {
	value := make([]byte, 0, len(lineID)+33)
	value = append(value, []byte("relay-secret/v1/")...)
	value = append(value, []byte(lineID)...)
	value = append(value, '/')
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], generation)
	value = append(value, encoded[:]...)
	value = append(value, '/')
	var edgeBytes [8]byte
	binary.BigEndian.PutUint64(edgeBytes[:], uint64(edge))
	value = append(value, edgeBytes[:]...)
	return value
}

func sealRelaySecret(key []byte, lineID string, generation uint64, edge int, secret []byte) ([]byte, error) {
	if len(key) != 32 || !relayLineIDPattern.MatchString(lineID) || generation == 0 || generation > math.MaxInt64 || edge < 0 || len(secret) != 32 {
		return nil, ErrRelaySecretKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrRelaySecretKey
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrRelaySecretKey
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, secret, relaySecretAAD(lineID, generation, edge)), nil
}

func openRelaySecret(key []byte, lineID string, generation uint64, edge int, ciphertext []byte) ([]byte, error) {
	if len(key) != 32 || !relayLineIDPattern.MatchString(lineID) || generation == 0 || generation > math.MaxInt64 || edge < 0 {
		return nil, ErrRelaySecretKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrRelaySecretKey
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("invalid relay secret")
	}
	if len(ciphertext) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("invalid relay secret")
	}
	plain, err := gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], relaySecretAAD(lineID, generation, edge))
	if err != nil || len(plain) != 32 {
		return nil, errors.New("invalid relay secret")
	}
	return plain, nil
}
