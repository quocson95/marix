package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/argon2"
)

// BackupFile represents the encrypted backup file format
type BackupFile struct {
	Version       string `json:"version"`
	Timestamp     string `json:"timestamp"`
	Salt          string `json:"salt"`           // base64-encoded
	Nonce         string `json:"nonce"`          // base64-encoded
	EncryptedData string `json:"encrypted_data"` // base64-encoded
}

// Argon2id parameters (secure, memory-hard)
const (
	argon2Time    = 3         // 3 iterations
	argon2Memory  = 64 * 1024 // 64 MB
	argon2Threads = 4         // 4 parallel threads
	argon2KeyLen  = 32        // 32 bytes = 256 bits
	saltLen       = 32        // 32 bytes
	nonceLen      = 12        // 12 bytes for GCM
)

// DeriveKey derives a 256-bit key from password using Argon2id
func DeriveKey(password string, salt []byte) []byte {
	return argon2.IDKey(
		[]byte(password),
		salt,
		argon2Time,
		argon2Memory,
		argon2Threads,
		argon2KeyLen,
	)
}

// Encrypt encrypts data with AES-256-GCM using password
func Encrypt(data []byte, password string) (*BackupFile, error) {
	// Generate random salt
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive key using Argon2id
	key := DeriveKey(password, salt)

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

	// Generate random nonce
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt data
	ciphertext := gcm.Seal(nil, nonce, data, nil)

	// Create backup file
	backup := &BackupFile{
		Version:       "1.0",
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		Salt:          base64.StdEncoding.EncodeToString(salt),
		Nonce:         base64.StdEncoding.EncodeToString(nonce),
		EncryptedData: base64.StdEncoding.EncodeToString(ciphertext),
	}

	return backup, nil
}

// Decrypt decrypts backup file using password
func Decrypt(backup *BackupFile, password string) ([]byte, error) {
	// Decode salt
	salt, err := base64.StdEncoding.DecodeString(backup.Salt)
	if err != nil {
		return nil, fmt.Errorf("invalid salt: %w", err)
	}

	// Derive key using same parameters
	key := DeriveKey(password, salt)

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

	// Decode nonce
	nonce, err := base64.StdEncoding.DecodeString(backup.Nonce)
	if err != nil {
		return nil, fmt.Errorf("invalid nonce: %w", err)
	}

	// Decode ciphertext
	ciphertext, err := base64.StdEncoding.DecodeString(backup.EncryptedData)
	if err != nil {
		return nil, fmt.Errorf("invalid encrypted data: %w", err)
	}

	// Decrypt and verify
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("decryption failed: wrong password or corrupted data")
	}

	return plaintext, nil
}

// CreateBackup creates an encrypted backup from arbitrary data
func CreateBackup(data interface{}, password string) (string, error) {
	// Marshal data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to marshal data: %w", err)
	}

	// Encrypt data
	backup, err := Encrypt(jsonData, password)
	if err != nil {
		return "", fmt.Errorf("encryption failed: %w", err)
	}

	// Marshal backup to JSON
	backupJSON, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to create backup JSON: %w", err)
	}

	return string(backupJSON), nil
}

// RestoreBackup restores data from encrypted backup
func RestoreBackup(backupJSON string, password string, target interface{}) error {
	// Parse backup JSON
	var backup BackupFile
	if err := json.Unmarshal([]byte(backupJSON), &backup); err != nil {
		return fmt.Errorf("invalid backup format: %w", err)
	}

	// Decrypt data
	plaintext, err := Decrypt(&backup, password)
	if err != nil {
		return err
	}

	// Unmarshal to target
	if err := json.Unmarshal(plaintext, target); err != nil {
		return fmt.Errorf("failed to parse backup data: %w", err)
	}

	return nil
}
