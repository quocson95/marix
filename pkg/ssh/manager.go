package ssh

import (
	"fmt"
	"sync"
)

// Manager manages multiple SSH connections
type Manager struct {
	connections map[string]*Client
	mu          sync.RWMutex
}

// NewManager creates a new SSH connection manager
func NewManager() *Manager {
	return &Manager{
		connections: make(map[string]*Client),
	}
}

// Connect creates a new SSH connection
func (m *Manager) Connect(config *SSHConfig) (string, error) {
	connectionID := config.ConnectionID()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already connected
	if client, exists := m.connections[connectionID]; exists {
		if client.IsConnected() {
			return connectionID, nil
		}
		// Clean up old connection
		delete(m.connections, connectionID)
	}

	// Create new client
	client := NewClient(config)
	if err := client.Connect(); err != nil {
		return "", fmt.Errorf("connection failed: %w", err)
	}

	m.connections[connectionID] = client
	return connectionID, nil
}

// GetClient returns the SSH client for a connection ID
func (m *Manager) GetClient(connectionID string) (*Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	client, exists := m.connections[connectionID]
	if !exists {
		return nil, fmt.Errorf("connection not found: %s", connectionID)
	}

	return client, nil
}

// Disconnect closes a connection
func (m *Manager) Disconnect(connectionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	client, exists := m.connections[connectionID]
	if !exists {
		return fmt.Errorf("connection not found: %s", connectionID)
	}

	if err := client.Close(); err != nil {
		return err
	}

	delete(m.connections, connectionID)
	return nil
}

// DisconnectAll closes all connections
func (m *Manager) DisconnectAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, client := range m.connections {
		client.Close()
		delete(m.connections, id)
	}
}

// ListConnections returns all active connection IDs
func (m *Manager) ListConnections() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.connections))
	for id := range m.connections {
		ids = append(ids, id)
	}
	return ids
}
