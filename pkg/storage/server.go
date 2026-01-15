package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Server represents a saved SSH server configuration
type Server struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Host                string   `json:"host"`
	Port                int      `json:"port"`
	Username            string   `json:"username"`
	Password            string   `json:"password,omitempty"`
	PrivateKey          string   `json:"privateKey,omitempty"`          // Deprecated: file path, kept for backward compatibility
	PrivateKeyEncrypted []byte   `json:"privateKeyEncrypted,omitempty"` // Encrypted private key content
	KeyEncryptionSalt   []byte   `json:"keyEncryptionSalt,omitempty"`   // Salt for key encryption
	Protocol            string   `json:"protocol"`                      // ssh, sftp, ftp, rdp
	Tags                []string `json:"tags,omitempty"`
	Description         string   `json:"description,omitempty"`
	CreatedAt           int64    `json:"createdAt"`
	UpdatedAt           int64    `json:"updatedAt"`
}

// Store manages server configurations
type Store struct {
	servers  map[string]*Server
	filePath string
	mu       sync.RWMutex
}

// NewStore creates a new server store
func NewStore(dataDir string) (*Store, error) {
	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	filePath := filepath.Join(dataDir, "servers.json")
	store := &Store{
		servers:  make(map[string]*Server),
		filePath: filePath,
	}

	// Load existing servers
	if err := store.load(); err != nil {
		// If file doesn't exist, that's okay - will be created on first save
		if !os.IsNotExist(err) {
			// Check if it's a corruption error (contains "backed up")
			if filepath.Ext(err.Error()) != "" {
				// It's a corruption - log warning but continue with empty store
				fmt.Fprintf(os.Stderr, "WARNING: %v\n", err)
			} else {
				// Other error - return it
				return nil, err
			}
		}
	}

	return store, nil
}

// load reads servers from disk
func (s *Store) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}

	// Handle empty file - treat as empty server list
	if len(data) == 0 {
		// Initialize with empty array and save
		return s.save()
	}

	var servers []*Server
	if err := json.Unmarshal(data, &servers); err != nil {
		// Invalid JSON - create backup and reset
		backupPath := s.filePath + ".corrupted"
		if backupErr := os.WriteFile(backupPath, data, 0600); backupErr == nil {
			// Successfully backed up corrupted file
			// Reset to empty and save
			s.servers = make(map[string]*Server)
			if saveErr := s.save(); saveErr != nil {
				return fmt.Errorf("failed to parse servers file (backup saved to %s): %w", backupPath, err)
			}
			return fmt.Errorf("corrupted servers.json detected and backed up to %s - file has been reset", backupPath)
		}
		return fmt.Errorf("failed to parse servers file: %w", err)
	}

	for _, srv := range servers {
		s.servers[srv.ID] = srv
	}

	return nil
}

// save writes servers to disk
func (s *Store) save() error {
	servers := make([]*Server, 0, len(s.servers))
	for _, srv := range s.servers {
		servers = append(servers, srv)
	}

	data, err := json.MarshalIndent(servers, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal servers: %w", err)
	}

	return os.WriteFile(s.filePath, data, 0600)
}

// Add adds a new server
func (s *Store) Add(server *Server) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.servers[server.ID] = server
	return s.save()
}

// Get retrieves a server by ID
func (s *Store) Get(id string) (*Server, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	server, exists := s.servers[id]
	if !exists {
		return nil, fmt.Errorf("server not found: %s", id)
	}

	return server, nil
}

// List returns all servers
func (s *Store) List() []*Server {
	s.mu.RLock()
	defer s.mu.RUnlock()

	servers := make([]*Server, 0, len(s.servers))
	for _, srv := range s.servers {
		servers = append(servers, srv)
	}

	return servers
}

// Update updates a server
func (s *Store) Update(server *Server) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.servers[server.ID]; !exists {
		return fmt.Errorf("server not found: %s", server.ID)
	}

	s.servers[server.ID] = server
	return s.save()
}

// Delete removes a server
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.servers[id]; !exists {
		return fmt.Errorf("server not found: %s", id)
	}

	delete(s.servers, id)
	return s.save()
}
