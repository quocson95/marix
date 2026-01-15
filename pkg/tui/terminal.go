package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/quocson95/marix/pkg/ssh"
)

// TerminalModel represents an active SSH terminal session
type TerminalModel struct {
	client       *ssh.Client
	connectionID string
	output       []byte
	outputChan   chan []byte
	width        int
	height       int
	err          error
	quitting     bool
}

// terminalOutputMsg contains output from SSH session
type terminalOutputMsg struct {
	data []byte
}

// terminalCloseMsg indicates SSH session closed
type terminalCloseMsg struct{}

// NewTerminalModel creates a new terminal session model
func NewTerminalModel(config *ssh.SSHConfig) (*TerminalModel, error) {
	client := ssh.NewClient(config)

	// Connect to SSH server
	if err := client.Connect(); err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}

	return &TerminalModel{
		client:       client,
		connectionID: config.ConnectionID(),
		output:       []byte{},
	}, nil
}

func (m *TerminalModel) Init() tea.Cmd {
	// Create channel for receiving SSH output
	m.outputChan = make(chan []byte, 100)

	// Set up callbacks BEFORE creating shell
	m.client.OnData(func(data []byte) {
		// Send data to the TUI via channel
		select {
		case m.outputChan <- data:
		default:
			// Channel full, skip this data
		}
	})

	m.client.OnClose(func() {
		close(m.outputChan)
	})

	// Create shell session (this will block until shell is ready)
	go func() {
		if err := m.client.CreateShell(80, 24); err != nil {
			m.err = err
			close(m.outputChan)
		}
	}()

	// Return a command that listens for output
	return m.waitForOutput()
}

// waitForOutput listens for SSH output and sends it to the TUI
func (m *TerminalModel) waitForOutput() tea.Cmd {
	return func() tea.Msg {
		data, ok := <-m.outputChan
		if !ok {
			return terminalCloseMsg{}
		}
		return terminalOutputMsg{data: data}
	}
}

func (m *TerminalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Resize SSH terminal
		if m.client != nil {
			m.client.Resize(msg.Width, msg.Height)
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+d":
			m.quitting = true
			if m.client != nil {
				m.client.Close()
			}
			return m, tea.Quit

		case "esc":
			// Disconnect and return to menu
			if m.client != nil {
				m.client.Close()
			}
			return m, tea.Quit

		default:
			// Send keystrokes to SSH session
			if m.client != nil {
				// Handle special keys
				var data []byte
				switch msg.String() {
				case "enter":
					data = []byte("\r")
				case "backspace":
					data = []byte{127}
				case "tab":
					data = []byte("\t")
				case "up":
					data = []byte{27, 91, 65}
				case "down":
					data = []byte{27, 91, 66}
				case "right":
					data = []byte{27, 91, 67}
				case "left":
					data = []byte{27, 91, 68}
				default:
					data = []byte(msg.String())
				}

				if err := m.client.Write(data); err != nil {
					m.err = err
				}
			}
			return m, nil
		}

	case terminalOutputMsg:
		// Received output from SSH
		m.output = append(m.output, msg.data...)
		// Continue listening for more output
		return m, m.waitForOutput()

	case terminalCloseMsg:
		m.quitting = true
		return m, tea.Quit
	}

	return m, nil
}

func (m *TerminalModel) View() string {
	if m.quitting {
		return "Disconnected.\n"
	}

	var b strings.Builder

	// Terminal header
	header := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7D56F4")).
		Bold(true).
		Render(fmt.Sprintf("📡 Connected to %s", m.connectionID))

	b.WriteString(header)
	b.WriteString(fmt.Sprintf(" (received %d bytes)", len(m.output)))
	b.WriteString("\n\n")

	// Terminal output
	if len(m.output) > 0 {
		// Show last N lines
		maxLines := m.height - 8 // Leave more room for header/footer
		if maxLines < 5 {
			maxLines = 20
		}

		lines := strings.Split(string(m.output), "\n")
		startIdx := 0
		if len(lines) > maxLines {
			startIdx = len(lines) - maxLines
		}

		// Safely join lines
		if startIdx < len(lines) {
			b.WriteString(strings.Join(lines[startIdx:], "\n"))
		}
	} else {
		b.WriteString("Waiting for output from server...")
	}

	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("ctrl+f: sftp browser • esc: disconnect • ctrl+c/ctrl+d: quit"))

	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", m.err)))
	}

	return b.String()
}
