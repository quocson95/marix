package storage

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptPrivateKey(t *testing.T) {
	// Test data
	privateKey := []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA1234567890abcdefghijklmnopqrstuvwxyz
-----END RSA PRIVATE KEY-----`)
	password := "my-secure-password"

	// Encrypt
	encrypted, salt, err := EncryptPrivateKey(privateKey, password)
	if err != nil {
		t.Fatalf("EncryptPrivateKey failed: %v", err)
	}

	if len(encrypted) == 0 {
		t.Fatal("Encrypted data is empty")
	}

	if len(salt) != saltSize {
		t.Fatalf("Salt size incorrect: expected %d, got %d", saltSize, len(salt))
	}

	// Verify encrypted data is different from original
	if bytes.Equal(encrypted, privateKey) {
		t.Fatal("Encrypted data is the same as plaintext")
	}

	// Decrypt
	decrypted, err := DecryptPrivateKey(encrypted, salt, password)
	if err != nil {
		t.Fatalf("DecryptPrivateKey failed: %v", err)
	}

	// Verify decrypted data matches original
	if !bytes.Equal(decrypted, privateKey) {
		t.Fatal("Decrypted data does not match original")
	}
}

func TestDecryptWithWrongPassword(t *testing.T) {
	privateKey := []byte("secret-key-content")
	correctPassword := "correct-password"
	wrongPassword := "wrong-password"

	// Encrypt with correct password
	encrypted, salt, err := EncryptPrivateKey(privateKey, correctPassword)
	if err != nil {
		t.Fatalf("EncryptPrivateKey failed: %v", err)
	}

	// Try to decrypt with wrong password
	_, err = DecryptPrivateKey(encrypted, salt, wrongPassword)
	if err == nil {
		t.Fatal("DecryptPrivateKey should fail with wrong password")
	}
}

func TestEncryptionDeterminism(t *testing.T) {
	privateKey := []byte("test-key-content")
	password := "test-password"

	// Encrypt twice
	encrypted1, salt1, err := EncryptPrivateKey(privateKey, password)
	if err != nil {
		t.Fatalf("First encryption failed: %v", err)
	}

	encrypted2, salt2, err := EncryptPrivateKey(privateKey, password)
	if err != nil {
		t.Fatalf("Second encryption failed: %v", err)
	}

	// Salts should be different (random)
	if bytes.Equal(salt1, salt2) {
		t.Fatal("Salts should be different for each encryption")
	}

	// Encrypted data should be different (due to different salts and nonces)
	if bytes.Equal(encrypted1, encrypted2) {
		t.Fatal("Encrypted data should be different for each encryption")
	}

	// Both should decrypt correctly with their respective salts
	decrypted1, err := DecryptPrivateKey(encrypted1, salt1, password)
	if err != nil || !bytes.Equal(decrypted1, privateKey) {
		t.Fatal("First decryption failed")
	}

	decrypted2, err := DecryptPrivateKey(encrypted2, salt2, password)
	if err != nil || !bytes.Equal(decrypted2, privateKey) {
		t.Fatal("Second decryption failed")
	}
}

func TestEncryptEmptyContent(t *testing.T) {
	_, _, err := EncryptPrivateKey([]byte{}, "password")
	if err == nil {
		t.Fatal("EncryptPrivateKey should fail with empty content")
	}
}

func TestEncryptEmptyPassword(t *testing.T) {
	_, _, err := EncryptPrivateKey([]byte("content"), "")
	if err == nil {
		t.Fatal("EncryptPrivateKey should fail with empty password")
	}
}

func TestDecryptInvalidSalt(t *testing.T) {
	encrypted := []byte("some-encrypted-data-with-proper-length-here")
	invalidSalt := []byte("short")

	_, err := DecryptPrivateKey(encrypted, invalidSalt, "password")
	if err == nil {
		t.Fatal("DecryptPrivateKey should fail with invalid salt size")
	}
}
