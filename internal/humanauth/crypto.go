package humanauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// encryptRefreshToken encrypts plaintext with AES-256-GCM under key (must be
// exactly 32 bytes), returning a base64-encoded nonce||ciphertext blob. This
// is what protects storage.Session.RefreshToken at rest — a database
// read/leak alone doesn't yield a token usable against Keycloak, unlike a
// plain string column would.
func encryptRefreshToken(key []byte, plaintext string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decryptRefreshToken reverses encryptRefreshToken. It fails (rather than
// returning garbage) if key is wrong or encoded was tampered with — AES-GCM
// authenticates the ciphertext, it doesn't just decrypt it.
func decryptRefreshToken(key []byte, encoded string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt (wrong key or tampered data): %w", err)
	}
	return string(plaintext), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be exactly 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return gcm, nil
}

// EncryptRefreshTokenForTesting exposes encryptRefreshToken for other
// packages' tests that need to construct a Session row with a correctly
// encrypted RefreshToken, matching what OIDCProvider actually writes.
func EncryptRefreshTokenForTesting(key []byte, plaintext string) (string, error) {
	return encryptRefreshToken(key, plaintext)
}

// DecryptRefreshTokenForTesting exposes decryptRefreshToken for other
// packages' tests.
func DecryptRefreshTokenForTesting(key []byte, encoded string) (string, error) {
	return decryptRefreshToken(key, encoded)
}
