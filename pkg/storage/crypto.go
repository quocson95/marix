package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/pbkdf2"
)

const (
	// PBKDF2 parameters
	pbkdf2Iterations = 100000
	pbkdf2KeyLen     = 32 // AES-256
	saltSize         = 32

	// AES-GCM nonce size
	nonceSize = 12
)

// EncryptPrivateKey encrypts a private key using AES-256-GCM with a password-derived key
// Returns the encrypted data and the salt used for key derivation
func EncryptPrivateKey(keyContent []byte, password string) (encrypted []byte, salt []byte, err error) {
	if len(keyContent) == 0 {
		return nil, nil, fmt.Errorf("key content cannot be empty")
	}
	if password == "" {
		return nil, nil, fmt.Errorf("password cannot be empty")
	}

	// Generate random salt
	salt = make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, nil, fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive encryption key from password using PBKDF2
	key := pbkdf2.Key([]byte(password), salt, pbkdf2Iterations, pbkdf2KeyLen, sha256.New)

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt the data
	// Format: nonce + ciphertext (GCM appends auth tag automatically)
	ciphertext := gcm.Seal(nonce, nonce, keyContent, nil)

	return ciphertext, salt, nil
}

// DecryptPrivateKey decrypts a private key using AES-256-GCM with a password-derived key
// The encrypted data should contain the nonce prepended to the ciphertext
func DecryptPrivateKey(encrypted []byte, salt []byte, password string) ([]byte, error) {
	if len(encrypted) == 0 {
		return nil, fmt.Errorf("encrypted data cannot be empty")
	}
	if len(salt) != saltSize {
		return nil, fmt.Errorf("invalid salt size: expected %d, got %d", saltSize, len(salt))
	}
	if password == "" {
		return nil, fmt.Errorf("password cannot be empty")
	}

	// Derive decryption key from password using PBKDF2
	key := pbkdf2.Key([]byte(password), salt, pbkdf2Iterations, pbkdf2KeyLen, sha256.New)

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Check minimum size (nonce + some data)
	if len(encrypted) < nonceSize {
		return nil, fmt.Errorf("encrypted data too short")
	}

	// Extract nonce and ciphertext
	nonce := encrypted[:nonceSize]
	ciphertext := encrypted[nonceSize:]

	// Decrypt the data
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (wrong password?): %w", err)
	}

	return plaintext, nil
}
