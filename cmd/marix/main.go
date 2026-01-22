package main

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/quocson95/marix/pkg/tui"
)

func main() {
	// Set up logging to file
	homeDir, err := os.UserHomeDir()
	if err != nil {
		slog.Error("Error getting home directory", "error", err)
		os.Exit(1)
	}

	dataDir := filepath.Join(homeDir, ".marix")
	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		slog.Error("Error creating data directory", "error", err)
		os.Exit(1)
	}

	// Open log file
	logFile, err := os.OpenFile(
		filepath.Join(dataDir, "debug.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0600,
	)
	if err != nil {
		slog.Error("Error opening log file", "error", err)
		os.Exit(1)
	}
	defer logFile.Close()

	// Redirect log output to file
	log.SetOutput(logFile)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// Configure slog to write to file
	handler := slog.NewTextHandler(logFile, nil)
	slog.SetDefault(slog.New(handler))

	// Create the application model
	appModel, err := tui.NewAppModel()
	if err != nil {
		slog.Error("Error initializing application", "error", err)
		// Print to stderr as well since we are exiting
		fmt.Fprintf(os.Stderr, "Error initializing application: %v\n", err)
		os.Exit(1)
	}

	// Create the TUI program
	p := tea.NewProgram(
		appModel,
		tea.WithAltScreen(),
	)

	// Run the program
	if _, err := p.Run(); err != nil {
		slog.Error("Error running program", "error", err)
		// Print to stderr as well since we are exiting
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
