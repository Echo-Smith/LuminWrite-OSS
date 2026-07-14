package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// Encrypt encrypts plaintext using AES-256-GCM with the given key.
// The key must be 32 bytes (256 bits). Returns base64-encoded ciphertext.
func Encrypt(plaintext string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("key must be 32 bytes (256 bits), got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Seal appends the ciphertext after the nonce
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a base64-encoded AES-256-GCM ciphertext.
func Decrypt(ciphertextB64 string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("key must be 32 bytes (256 bits), got %d", len(key))
	}

	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return string(plaintext), nil
}

// DeriveKey derives a 32-byte key from a passphrase string.
// If the passphrase is already 32 bytes, it's used directly.
// Otherwise, it's padded or truncated to 32 bytes.
// For production, use a proper KDF like scrypt or argon2.
func DeriveKey(passphrase string) []byte {
	if len(passphrase) == 32 {
		return []byte(passphrase)
	}
	// Pad or truncate to 32 bytes
	key := make([]byte, 32)
	copy(key, passphrase)
	return key
}

// IsEncrypted checks if a string looks like an encrypted value (base64-encoded with nonce prefix).
// This is a heuristic — it checks if the string is valid base64 and has a reasonable length.
func IsEncrypted(s string) bool {
	if len(s) < 24 { // nonce (12) + at least 1 byte ciphertext + tag (16) = 29, base64 ~ 40 chars
		return false
	}
	_, err := base64.StdEncoding.DecodeString(s)
	return err == nil
}
