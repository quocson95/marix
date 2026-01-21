package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerStoreCRUD(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "marix-server-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create new store
	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Add
	server := &Server{
		ID:        "1",
		Name:      "Test Server",
		Host:      "localhost",
		Port:      22,
		Username:  "user",
		CreatedAt: time.Now().Unix(),
	}

	if err := store.Add(server); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// 2. Get
	retrieved, err := store.Get("1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if retrieved.Name != "Test Server" {
		t.Errorf("Expected name 'Test Server', got '%s'", retrieved.Name)
	}

	// 3. List
	servers := store.List()
	if len(servers) != 1 {
		t.Errorf("Expected 1 server, got %d", len(servers))
	}

	// 4. Update
	server.Name = "Updated Server"
	if err := store.Update(server); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	updated, err := store.Get("1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Updated Server" {
		t.Errorf("Expected name 'Updated Server', got '%s'", updated.Name)
	}

	// 5. Delete
	if err := store.Delete("1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if _, err := store.Get("1"); err == nil {
		t.Error("Expected Get to fail after delete")
	}

	if len(store.List()) != 0 {
		t.Error("Expected empty list after delete")
	}
}

func TestServerPersistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "marix-server-persist-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{
		ID:   "persist-1",
		Name: "Persistent Server",
	}
	if err := store.Add(server); err != nil {
		t.Fatal(err)
	}

	// Create new store instance pointing to same dir
	newStore, err := NewStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := newStore.Get("persist-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "Persistent Server" {
		t.Error("Failed to load persisted server")
	}
}

func TestCorruptedFileHandling(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "marix-corrupt-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	filePath := filepath.Join(tempDir, "servers.json")
	// Write bad JSON
	if err := os.WriteFile(filePath, []byte("{invalid-json"), 0600); err != nil {
		t.Fatal(err)
	}

	// Should recover gracefully (backup and reset)
	store, err := NewStore(tempDir)
	if err == nil {
		// It might return warning as error or nil depending on implementation,
		// but checking implementation of NewStore, it logs warning and returns nil error for corruption handled case?
		// Re-reading NewStore logic:
		// "It's a corruption - log warning but continue with empty store" -> implies err is handled inside load() but load returns fmt.Errorf which initiates warning log.
		// Wait, `load` returns error. `NewStore` catches it.
		// If `filepath.Ext(err.Error()) != ""` it logs and continues.
		// So `NewStore` returns nil error.
	} else {
		t.Fatalf("NewStore failed to handle corruption: %v", err)
	}

	if len(store.List()) != 0 {
		t.Error("Expected empty store after corruption reset")
	}

	// Verify backup file exists
	if _, err := os.Stat(filePath + ".corrupted"); os.IsNotExist(err) {
		t.Error("Backup file wasn't created")
	}
}
