package orchestration

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"math"
	"testing"
)

func TestRelaySecretCipherBindsLineGenerationAndEdge(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	secret := bytes.Repeat([]byte{0x17}, 32)
	line := "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423"
	ciphertext, err := sealRelaySecret(key, line, 7, 1, secret)
	if err != nil {
		t.Fatal(err)
	}
	got, err := openRelaySecret(key, line, 7, 1, ciphertext)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("round trip: %v", err)
	}
	for _, candidate := range []struct {
		line       string
		generation uint64
		edge       int
	}{
		{"018f7d37-c20e-7a6a-8bb8-b0c3a4d3e424", 7, 1},
		{line, 8, 1},
		{line, 7, 2},
	} {
		if _, err := openRelaySecret(key, candidate.line, candidate.generation, candidate.edge, ciphertext); err == nil {
			t.Fatalf("accepted ciphertext in another row: %+v", candidate)
		}
	}
	if _, err := openRelaySecret(bytes.Repeat([]byte{0x43}, 32), line, 7, 1, ciphertext); err == nil {
		t.Fatal("accepted wrong encryption key")
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := openRelaySecret(key, line, 7, 1, ciphertext); err == nil {
		t.Fatal("accepted modified ciphertext")
	}
}

func TestRelaySecretCipherRejectsInvalidIdentityAndLegacyUnboundCiphertext(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	secret := bytes.Repeat([]byte{0x17}, 32)
	line := "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423"
	for _, generation := range []uint64{0, math.MaxInt64 + 1} {
		if _, err := sealRelaySecret(key, line, generation, 0, secret); !errors.Is(err, ErrRelaySecretKey) {
			t.Fatalf("generation %d: %v", generation, err)
		}
	}
	if _, err := sealRelaySecret(key, line, 1, -1, secret); !errors.Is(err, ErrRelaySecretKey) {
		t.Fatalf("negative edge: %v", err)
	}
	if _, err := sealRelaySecret(key, line, 1, 0, make([]byte, 31)); !errors.Is(err, ErrRelaySecretKey) {
		t.Fatalf("short secret: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	legacy := gcm.Seal(nonce, nonce, secret, nil)
	if _, err := openRelaySecret(key, line, 1, 0, legacy); err == nil {
		t.Fatal("accepted unbound legacy ciphertext")
	}
}
