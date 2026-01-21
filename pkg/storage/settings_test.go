package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewSettingsStore(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "marix-settings-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test creation
	store, err := NewSettingsStore(tempDir)
	if err != nil {
		t.Fatalf("NewSettingsStore failed: %v", err)
	}

	// Verify defaults
	settings := store.Get()
	if settings.AutoBackup {
		t.Error("Expected AutoBackup to be false by default")
	}
	if settings.S3Host != "" {
		t.Error("Expected S3Host to be empty by default")
	}

	// Verify persistence file exists
	if _, err := os.Stat(filepath.Join(tempDir, "settings.json")); os.IsNotExist(err) {
		t.Error("settings.json was not created")
	}
}

func TestUpdateAutoBackup(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "marix-autobackup-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	store, err := NewSettingsStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	// Enable AutoBackup
	if err := store.SetAutoBackup(true); err != nil {
		t.Fatalf("SetAutoBackup failed: %v", err)
	}

	// Verify in memory
	if !store.Get().AutoBackup {
		t.Error("AutoBackup not updated in memory")
	}

	// Reload from disk to verify persistence
	newStore, err := NewSettingsStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	if !newStore.Get().AutoBackup {
		t.Error("AutoBackup not persisted to disk")
	}
}

func TestS3SettingsPersistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "marix-s3-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	store, err := NewSettingsStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	// Update S3 settings
	settings := store.Get()
	settings.S3Host = "https://example.com"
	settings.S3AccessKey = "access-key"
	settings.S3SecretKey = "secret-key"

	if err := store.Update(settings); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify persistence by reloading
	newStore, err := NewSettingsStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	startSettings := newStore.Get()
	if startSettings.S3Host != "https://example.com" {
		t.Errorf("Expected S3Host https://example.com, got %s", startSettings.S3Host)
	}
	if startSettings.S3AccessKey != "access-key" {
		t.Errorf("Expected S3AccessKey access-key, got %s", startSettings.S3AccessKey)
	}
}

func TestMasterPassword(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "marix-password-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	store, err := NewSettingsStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	// Set password
	password := "super-secret-password"
	if err := store.SetMasterPassword(password); err != nil {
		t.Fatalf("SetMasterPassword failed: %v", err)
	}

	// Verify password
	if !store.VerifyMasterPassword(password) {
		t.Error("VerifyMasterPassword failed for correct password")
	}

	if store.VerifyMasterPassword("wrong-password") {
		t.Error("VerifyMasterPassword succeeded for wrong password")
	}

	// Reload to verify hash persistence
	newStore, err := NewSettingsStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	if !newStore.VerifyMasterPassword(password) {
		t.Error("VerifyMasterPassword failed after reload")
	}
}
