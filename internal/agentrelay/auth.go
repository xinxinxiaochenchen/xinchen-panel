package agentrelay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

func openMAC(value Open, secret []byte) ([]byte, error) {
	if len(secret) != 32 {
		return nil, errors.New("invalid relay route secret length")
	}
	value.Proof = ""
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("encode relay open request")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("network-control-plane/relay/open/v1\x00"))
	_, _ = mac.Write(payload)
	return mac.Sum(nil), nil
}

func SignOpen(value Open, secret []byte) (Open, error) {
	if err := validateOpen(value); err != nil {
		return Open{}, err
	}
	if value.Proof != "" {
		return Open{}, errors.New("relay open request already contains a proof")
	}
	proof, err := openMAC(value, secret)
	if err != nil {
		return Open{}, err
	}
	value.Proof = hex.EncodeToString(proof)
	return value, nil
}

// ReplayWindow is scoped to one applied line generation. A full window fails
// closed; a caller must create a fresh window when that generation changes.
type ReplayWindow struct {
	mu       sync.Mutex
	entries  map[string]time.Time
	capacity int
}

func NewReplayWindow(capacity int) *ReplayWindow {
	if capacity < 1 {
		capacity = 1
	}
	return &ReplayWindow{entries: make(map[string]time.Time, capacity), capacity: capacity}
}

func (window *ReplayWindow) mark(key string, now time.Time) error {
	if window == nil {
		return errors.New("relay replay protection is required")
	}
	window.mu.Lock()
	defer window.mu.Unlock()
	for candidate, expires := range window.entries {
		if !expires.After(now) {
			delete(window.entries, candidate)
		}
	}
	if _, duplicate := window.entries[key]; duplicate {
		return errors.New("replayed relay open request")
	}
	if len(window.entries) >= window.capacity {
		return errors.New("relay replay window is full")
	}
	window.entries[key] = now.Add(2 * handshakeSkew)
	return nil
}

// VerifyOpen checks the line generation and proof after the caller has checked
// the mTLS source certificate against its applied route configuration.
func VerifyOpen(value Open, secret []byte, expectedLineID string, generation uint64, window *ReplayWindow, now time.Time) error {
	if err := validateOpen(value); err != nil {
		return err
	}
	if value.LineID != expectedLineID || value.Generation != generation || !proofPattern.MatchString(value.Proof) ||
		!now.Before(value.SentAt.Add(handshakeSkew)) || value.SentAt.After(now.Add(handshakeSkew)) {
		return errors.New("relay open request is unauthorized or stale")
	}
	provided, err := hex.DecodeString(value.Proof)
	if err != nil {
		return errors.New("invalid relay proof")
	}
	expected, err := openMAC(value, secret)
	if err != nil {
		return err
	}
	if !hmac.Equal(provided, expected) {
		return errors.New("invalid relay proof")
	}
	return window.mark(value.LineID+":"+value.ConnectionID+":"+fmt.Sprint(value.Generation), now)
}
