package humanauth

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testKey(t)

	for _, plaintext := range []string{"", "a", "a refresh token from keycloak", strings.Repeat("x", 4096)} {
		encoded, err := encryptRefreshToken(key, plaintext)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		// The ciphertext must not contain the plaintext — that's the entire
		// point of encrypting the column. Only checked for plaintexts long
		// enough for a substring match to mean something: base64 output
		// contains any given single character most of the time by chance, so
		// asserting it for "a" tests randomness, not encryption.
		if len(plaintext) >= 8 && strings.Contains(encoded, plaintext) {
			t.Errorf("ciphertext contains the plaintext %q", plaintext)
		}

		decrypted, err := decryptRefreshToken(key, encoded)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if decrypted != plaintext {
			t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
		}
	}
}

// GCM uses a fresh nonce per call, so the same plaintext must not produce the
// same ciphertext — otherwise equal tokens are identifiable in the database.
func TestEncryptIsNonDeterministic(t *testing.T) {
	key := testKey(t)

	first, err := encryptRefreshToken(key, "same token")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	second, err := encryptRefreshToken(key, "same token")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if first == second {
		t.Error("encrypting the same plaintext twice produced identical ciphertext")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	encoded, err := encryptRefreshToken(testKey(t), "a refresh token")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if _, err := decryptRefreshToken(testKey(t), encoded); err == nil {
		t.Error("decrypting with the wrong key succeeded")
	}
}

// AES-GCM authenticates the ciphertext, so tampering must fail loudly rather
// than yielding a plausible-looking token.
func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	key := testKey(t)
	encoded, err := encryptRefreshToken(key, "a refresh token")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	raw[len(raw)-1] ^= 0xff
	tampered := base64.StdEncoding.EncodeToString(raw)

	if _, err := decryptRefreshToken(key, tampered); err == nil {
		t.Error("decrypting tampered ciphertext succeeded")
	}
}

func TestDecryptRejectsMalformedInput(t *testing.T) {
	key := testKey(t)

	tests := map[string]string{
		"not base64":      "!!!not base64!!!",
		"empty":           "",
		"too short":       base64.StdEncoding.EncodeToString([]byte("short")),
		"valid b64, junk": base64.StdEncoding.EncodeToString(make([]byte, 64)),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decryptRefreshToken(key, input); err == nil {
				t.Error("decrypt succeeded, want error")
			}
		})
	}
}

// A wrong-length key is a configuration error and must be rejected, not padded
// or truncated into something that silently "works".
func TestWrongKeyLengthIsRejected(t *testing.T) {
	for _, size := range []int{0, 16, 31, 33, 64} {
		key := make([]byte, size)
		if _, err := encryptRefreshToken(key, "x"); err == nil {
			t.Errorf("key size %d: encrypt succeeded, want error", size)
		}
		if _, err := decryptRefreshToken(key, "x"); err == nil {
			t.Errorf("key size %d: decrypt succeeded, want error", size)
		}
	}
}
