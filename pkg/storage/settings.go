package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// Settings represents application settings
type Settings struct {
	DefaultPort        int    `json:"defaultPort"`
	DefaultUsername    string `json:"defaultUsername"`
	Theme              string `json:"theme"`
	TerminalFont       string `json:"terminalFont"`
	AutoSave           bool   `json:"autoSave"`
	MasterPasswordHash string `json:"masterPasswordHash,omitempty"` // Bcrypt hash of master password
	S3Host             string `json:"s3Host,omitempty"`             // S3 Endpoint
	S3AccessKey        string `json:"s3AccessKey,omitempty"`        // S3 Access Key
	S3SecretKey        string `json:"s3SecretKey,omitempty"`        // S3 Secret Key
	AutoBackup         bool   `json:"autoBackup"`                   // Automatically backup on server add/delete
	DisableRsync       bool   `json:"disableRsync"`                 // Disable rsync engine
}

// SettingsStore manages application settings
type SettingsStore struct {
	settings Settings
	filePath string
	mu       sync.RWMutex
}

// NewSettingsStore creates a new settings store
func NewSettingsStore(dataDir string) (*SettingsStore, error) {
	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	filePath := filepath.Join(dataDir, "settings.json")
	store := &SettingsStore{
		settings: getDefaultSettings(),
		filePath: filePath,
	}

	// Load existing settings
	if err := store.load(); err != nil {
		// If file doesn't exist, that's okay, use defaults
		if !os.IsNotExist(err) {
			return nil, err
		}
		// Save default settings
		store.save()
	}

	return store, nil
}

// getDefaultSettings returns default settings
func getDefaultSettings() Settings {
	return Settings{
		DefaultPort:        22,
		DefaultUsername:    "root",
		Theme:              "default",
		TerminalFont:       "monospace",
		AutoSave:           true,
		MasterPasswordHash: "", // Empty means no encryption by default
		AutoBackup:         false,
		DisableRsync:       false,
	}
}

// load reads settings from disk
func (s *SettingsStore) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &s.settings)
}

// save writes settings to disk
func (s *SettingsStore) save() error {
	data, err := json.MarshalIndent(s.settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	return os.WriteFile(s.filePath, data, 0600)
}

// Get returns current settings
func (s *SettingsStore) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// Update updates settings
func (s *SettingsStore) Update(settings Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.settings = settings
	return s.save()
}

// Set sets a specific setting
func (s *SettingsStore) SetDefaultPort(port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.settings.DefaultPort = port
	return s.save()
}

func (s *SettingsStore) SetDefaultUsername(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.settings.DefaultUsername = username
	return s.save()
}

func (s *SettingsStore) SetTheme(theme string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.settings.Theme = theme
	return s.save()
}

func (s *SettingsStore) SetAutoSave(autoSave bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.settings.AutoSave = autoSave
	return s.save()
}

func (s *SettingsStore) SetAutoBackup(autoBackup bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.settings.AutoBackup = autoBackup
	return s.save()
}

func (s *SettingsStore) SetDisableRsync(disable bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.settings.DisableRsync = disable
	return s.save()
}

// Reset resets settings to defaults
func (s *SettingsStore) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.settings = getDefaultSettings()
	return s.save()
}

// VerifyMasterPassword checks if the provided password matches the stored hash
func (s *SettingsStore) VerifyMasterPassword(password string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.settings.MasterPasswordHash == "" {
		return false
	}

	err := bcrypt.CompareHashAndPassword([]byte(s.settings.MasterPasswordHash), []byte(password))
	return err == nil
}

// SetMasterPassword sets the master encryption password (hashes it)
func (s *SettingsStore) SetMasterPassword(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if password == "" {
		s.settings.MasterPasswordHash = ""
		return s.save()
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	s.settings.MasterPasswordHash = string(hash)
	return s.save()
}

// GetDataDir returns the directory where settings are stored
func (s *SettingsStore) GetDataDir() string {
	return filepath.Dir(s.filePath)
}
