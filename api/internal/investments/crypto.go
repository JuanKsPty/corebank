package investments

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// ErrEncryptionUnavailable means IBKR_TOKEN_ENCRYPTION_KEY is not configured.
// A link cannot be stored without it: storing a token this process could
// never decrypt again would be worse than refusing to store it.
var ErrEncryptionUnavailable = errors.New("investments: no encryption key configured for storing IBKR tokens")

// encryptToken seals a Flex Web Service token with AES-256-GCM.
//
// The nonce is generated fresh per call and prepended to the ciphertext
// rather than stored in its own column: GCM's nonce need only be unique per
// key, never secret, and keeping it alongside the ciphertext it belongs to
// means there is one blob to store and one to lose track of, not two that
// could end up out of sync.
func encryptToken(key []byte, plaintext string) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrEncryptionUnavailable
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("investments: building cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("investments: building GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("investments: generating nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// decryptToken reverses encryptToken.
func decryptToken(key []byte, ciphertext []byte) (string, error) {
	if len(key) == 0 {
		return "", ErrEncryptionUnavailable
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("investments: building cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("investments: building GCM: %w", err)
	}

	if len(ciphertext) < gcm.NonceSize() {
		return "", errors.New("investments: stored token is shorter than a nonce")
	}
	nonce, sealed := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]

	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("investments: decrypting stored token: %w", err)
	}
	return string(plaintext), nil
}
