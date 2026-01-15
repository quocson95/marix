package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/quocson95/marix/pkg/tui"
)

func main() {
	// Set up logging to file
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error getting home directory: %v\n", err)
		os.Exit(1)
	}

	dataDir := filepath.Join(homeDir, ".marix")
	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		fmt.Printf("Error creating data directory: %v\n", err)
		os.Exit(1)
	}

	// Open log file
	logFile, err := os.OpenFile(
		filepath.Join(dataDir, "debug.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0600,
	)
	if err != nil {
		fmt.Printf("Error opening log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	// Redirect log output to file
	log.SetOutput(logFile)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// Create the application model
	appModel, err := tui.NewAppModel()
	if err != nil {
		log.Printf("Error initializing application: %v", err)
		fmt.Printf("Error initializing application: %v\n", err)
		os.Exit(1)
	}

	// Create the TUI program
	p := tea.NewProgram(
		appModel,
		tea.WithAltScreen(),
	)

	// Run the program
	if _, err := p.Run(); err != nil {
		log.Printf("Error running program: %v", err)
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
