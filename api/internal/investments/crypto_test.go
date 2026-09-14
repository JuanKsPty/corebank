package investments

import (
	"bytes"
	"errors"
	"testing"
)

func testKey() []byte { return bytes.Repeat([]byte{0x42}, 32) }

func TestEncryptDecryptTokenRoundTrips(t *testing.T) {
	key := testKey()
	const token = "abc123-flex-web-service-token"

	ciphertext, err := encryptToken(key, token)
	if err != nil {
		t.Fatalf("encryptToken() error = %v", err)
	}
	if bytes.Contains(ciphertext, []byte(token)) {
		t.Error("ciphertext contains the plaintext token verbatim")
	}

	got, err := decryptToken(key, ciphertext)
	if err != nil {
		t.Fatalf("decryptToken() error = %v", err)
	}
	if got != token {
		t.Errorf("decryptToken() = %q, want %q", got, token)
	}
}

func TestEncryptTokenRefusesWithoutAKey(t *testing.T) {
	if _, err := encryptToken(nil, "token"); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Errorf("encryptToken(nil, ...) error = %v, want ErrEncryptionUnavailable", err)
	}
}

func TestDecryptTokenFailsWithTheWrongKey(t *testing.T) {
	ciphertext, err := encryptToken(testKey(), "token")
	if err != nil {
		t.Fatalf("encryptToken() error = %v", err)
	}

	wrongKey := bytes.Repeat([]byte{0x99}, 32)
	if _, err := decryptToken(wrongKey, ciphertext); err == nil {
		t.Error("decryptToken() with the wrong key did not error")
	}
}

func TestEachEncryptionUsesAFreshNonce(t *testing.T) {
	key := testKey()
	a, err := encryptToken(key, "same-token")
	if err != nil {
		t.Fatal(err)
	}
	b, err := encryptToken(key, "same-token")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Error("encrypting the same token twice produced identical ciphertext; the nonce is not varying")
	}
}
