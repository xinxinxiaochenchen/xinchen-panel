package subscription

import (
	"controlplane/internal/proxyaccess"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

func GenerateToken() (string, error) { return proxyaccess.GenerateCredential() }
func TokenHash(token string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != token {
		return "", errors.New("invalid subscription token")
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:]), nil
}
func sealToken(cipher *proxyaccess.CredentialCipher, id, owner, token string) (string, error) {
	return cipher.Seal("subscription:"+id, owner, token)
}
func openToken(cipher *proxyaccess.CredentialCipher, id, owner, sealed string) (string, error) {
	return cipher.Open("subscription:"+id, owner, sealed)
}
