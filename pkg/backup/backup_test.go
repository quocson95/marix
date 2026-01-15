package backup

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	password := "test-password-123"
	testData := []byte("This is secret test data")

	// Encrypt
	backup, err := Encrypt(testData, password)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Verify backup structure
	if backup.Version != "1.0" {
		t.Errorf("Expected version 1.0, got %s", backup.Version)
	}
	if backup.Salt == "" || backup.Nonce == "" || backup.EncryptedData == "" {
		t.Error("Backup missing required fields")
	}

	// Decrypt
	decrypted, err := Decrypt(backup, password)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	// Verify data matches
	if !bytes.Equal(decrypted, testData) {
		t.Errorf("Decrypted data doesn't match. Got %s, want %s", decrypted, testData)
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	testData := []byte("Secret data")
	correctPassword := "correct-password"
	wrongPassword := "wrong-password"

	// Encrypt with correct password
	backup, err := Encrypt(testData, correctPassword)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Try to decrypt with wrong password
	_, err = Decrypt(backup, wrongPassword)
	if err == nil {
		t.Error("Expected decryption to fail with wrong password, but it succeeded")
	}
}

func TestCreateRestoreBackup(t *testing.T) {
	type TestData struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	// Test data
	original := TestData{
		Name:  "test",
		Value: 42,
	}
	password := "test-password"

	// Create backup
	backupJSON, err := CreateBackup(original, password)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	// Verify it's valid JSON
	var backupFile BackupFile
	if err := json.Unmarshal([]byte(backupJSON), &backupFile); err != nil {
		t.Fatalf("Backup is not valid JSON: %v", err)
	}

	// Restore backup
	var restored TestData
	if err := RestoreBackup(backupJSON, password, &restored); err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}

	// Verify data matches
	if restored.Name != original.Name || restored.Value != original.Value {
		t.Errorf("Restored data doesn't match. Got %+v, want %+v", restored, original)
	}
}

func TestTamperedData(t *testing.T) {
	testData := []byte("Original data")
	password := "password"

	// Create backup
	backup, err := Encrypt(testData, password)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Tamper with encrypted data (change one character)
	tamperedData := backup.EncryptedData
	if len(tamperedData) > 10 {
		runes := []rune(tamperedData)
		if runes[10] == 'A' {
			runes[10] = 'B'
		} else {
			runes[10] = 'A'
		}
		backup.EncryptedData = string(runes)
	}

	// Try to decrypt tampered data
	_, err = Decrypt(backup, password)
	if err == nil {
		t.Error("Expected decryption to fail with tampered data, but it succeeded")
	}
}
