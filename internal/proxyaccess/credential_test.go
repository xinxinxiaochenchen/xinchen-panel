package proxyaccess

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateCredentialProducesUniqueOpaquePasswordsAndTrojanDigest(t *testing.T) {
	first, err := GenerateCredential()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateCredential()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) != 43 || len(second) != 43 {
		t.Fatalf("unsafe generated credentials: lengths %d and %d", len(first), len(second))
	}
	if _, err := base64.RawURLEncoding.DecodeString(first); err != nil {
		t.Fatal(err)
	}
	if got := TrojanDigest("password"); got != "d63dc919e201d7bc4c825630d2cf25fdc93d4b2f0d46706d29038d01" {
		t.Fatalf("Trojan digest = %s", got)
	}
}

func TestCredentialCipherRejectsTamperWrongKeyAndWrongOwner(t *testing.T) {
	keyText := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	key, err := ParseKey(keyText)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := NewCredentialCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	secret := "opaque-password"
	sealed, err := cipher.Seal("access-id", "owner-id", secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, secret) || !strings.HasPrefix(sealed, "v1.") {
		t.Fatalf("ciphertext leaked plaintext or version: %q", sealed)
	}
	got, err := cipher.Open("access-id", "owner-id", sealed)
	if err != nil || got != secret {
		t.Fatalf("decrypt = %q, %v", got, err)
	}
	if _, err := cipher.Open("access-id", "another-owner", sealed); err == nil {
		t.Fatal("another owner decrypted credential")
	}
	wrongKey := sha256.Sum256([]byte("another-key"))
	wrongCipher, err := NewCredentialCipher(wrongKey[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongCipher.Open("access-id", "owner-id", sealed); err == nil {
		t.Fatal("wrong key decrypted credential")
	}
	if _, err := cipher.Open("access-id", "owner-id", sealed[:len(sealed)-1]+"x"); err == nil {
		t.Fatal("tampered ciphertext decrypted")
	}
}

func TestParseKeyRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"", "short", base64.RawURLEncoding.EncodeToString(make([]byte, 31)), "not+url-safe"} {
		if _, err := ParseKey(value); err == nil {
			t.Fatalf("accepted invalid key %q", value)
		}
	}
}
