package proxyaccess

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

const credentialVersion = "v1."

func GenerateCredential() (string, error) {
	var raw [32]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", fmt.Errorf("generate proxy credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// TrojanDigest is the SHA-224 hex digest specified by the Trojan protocol.
func TrojanDigest(credential string) string {
	sum := sha256.Sum224([]byte(credential))
	return hex.EncodeToString(sum[:])
}

func ParseKey(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, errors.New("proxy credential key must be 32 base64url-encoded bytes")
	}
	return decoded, nil
}

type CredentialCipher struct{ aead cipher.AEAD }

func NewCredentialCipher(key []byte) (*CredentialCipher, error) {
	if len(key) != 32 {
		return nil, errors.New("proxy credential key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &CredentialCipher{aead: aead}, nil
}

func (c *CredentialCipher) Seal(accessID, ownerID, credential string) (string, error) {
	if c == nil || c.aead == nil || accessID == "" || ownerID == "" || credential == "" {
		return "", errors.New("invalid proxy credential context")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate proxy credential nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(credential), []byte(accessID+":"+ownerID))
	return credentialVersion + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *CredentialCipher) Open(accessID, ownerID, encoded string) (string, error) {
	if c == nil || c.aead == nil || accessID == "" || ownerID == "" || !strings.HasPrefix(encoded, credentialVersion) {
		return "", errors.New("invalid proxy credential ciphertext")
	}
	payload := strings.TrimPrefix(encoded, credentialVersion)
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || len(raw) <= c.aead.NonceSize() || base64.RawURLEncoding.EncodeToString(raw) != payload {
		return "", errors.New("invalid proxy credential ciphertext")
	}
	nonce := raw[:c.aead.NonceSize()]
	plaintext, err := c.aead.Open(nil, nonce, raw[c.aead.NonceSize():], []byte(accessID+":"+ownerID))
	if err != nil {
		return "", errors.New("invalid proxy credential ciphertext")
	}
	return string(plaintext), nil
}
