package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/quocson95/marix/pkg/ssh"
	"github.com/quocson95/marix/pkg/storage"
)

// ConnectSuccessMsg is sent when SSH connection succeeds
type ConnectSuccessMsg struct {
	termModel *TerminalModel
}

// ConnectModel handles SSH connection
type ConnectModel struct {
	inputs  []textinput.Model
	focused int
	err     error
	width   int
	height  int
	server  *storage.Server // Pre-filled server if connecting from server list
}

const (
	inputHost = iota
	inputPort
	inputUsername
	inputPassword
	inputPrivateKey
)

// NewConnectModel creates a new connection model
func NewConnectModel() *ConnectModel {
	return newConnectModel(nil)
}

// NewConnectModelWithServer creates a connection model pre-filled with server data
func NewConnectModelWithServer(server *storage.Server) *ConnectModel {
	return newConnectModel(server)
}

func newConnectModel(server *storage.Server) *ConnectModel {
	inputs := make([]textinput.Model, 5)

	inputs[inputHost] = textinput.New()
	inputs[inputHost].Placeholder = "192.168.1.1"
	inputs[inputHost].Focus()
	inputs[inputHost].CharLimit = 253
	inputs[inputHost].Width = 50
	inputs[inputHost].Prompt = "Host: "

	inputs[inputPort] = textinput.New()
	inputs[inputPort].Placeholder = "22"
	inputs[inputPort].CharLimit = 5
	inputs[inputPort].Width = 50
	inputs[inputPort].Prompt = "Port: "

	inputs[inputUsername] = textinput.New()
	inputs[inputUsername].Placeholder = "root"
	inputs[inputUsername].CharLimit = 32
	inputs[inputUsername].Width = 50
	inputs[inputUsername].Prompt = "Username: "

	inputs[inputPassword] = textinput.New()
	inputs[inputPassword].Placeholder = "(optional if using key)"
	inputs[inputPassword].CharLimit = 128
	inputs[inputPassword].Width = 50
	inputs[inputPassword].Prompt = "Password: "
	inputs[inputPassword].EchoMode = textinput.EchoPassword
	inputs[inputPassword].EchoCharacter = '•'

	inputs[inputPrivateKey] = textinput.New()
	inputs[inputPrivateKey].Placeholder = "~/.ssh/id_rsa (optional)"
	inputs[inputPrivateKey].CharLimit = 256
	inputs[inputPrivateKey].Width = 50
	inputs[inputPrivateKey].Prompt = "Private Key: "

	m := &ConnectModel{
		inputs:  inputs,
		focused: 0,
		server:  server,
	}

	// Pre-fill if server provided
	if server != nil {
		m.inputs[inputHost].SetValue(server.Host)
		m.inputs[inputPort].SetValue(fmt.Sprintf("%d", server.Port))
		m.inputs[inputUsername].SetValue(server.Username)
		m.inputs[inputPassword].SetValue(server.Password)
		if server.PrivateKey != "" {
			m.inputs[inputPrivateKey].SetValue(server.PrivateKey)
		}
	}

	return m
}

func (m *ConnectModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *ConnectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "shift+tab", "up", "down":
			// Navigate between inputs
			if msg.String() == "up" || msg.String() == "shift+tab" {
				m.focused--
			} else {
				m.focused++
			}

			if m.focused > len(m.inputs)-1 {
				m.focused = 0
			} else if m.focused < 0 {
				m.focused = len(m.inputs) - 1
			}

			// Update focus
			for i := range m.inputs {
				if i == m.focused {
					m.inputs[i].Focus()
				} else {
					m.inputs[i].Blur()
				}
			}

			return m, nil

		case "enter":
			// Trigger SSH connection
			return m, m.connect()
		}
	}

	// Handle character input
	cmd := m.updateInputs(msg)
	return m, cmd
}

func (m *ConnectModel) connect() tea.Cmd {
	return func() tea.Msg {
		// Parse port
		portStr := m.inputs[inputPort].Value()
		if portStr == "" {
			portStr = "22"
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			m.err = fmt.Errorf("invalid port: %s", portStr)
			return nil
		}

		// Get private key path (expand ~ to home directory)
		keyPath := m.inputs[inputPrivateKey].Value()
		if keyPath != "" && strings.HasPrefix(keyPath, "~") {
			home := os.Getenv("HOME")
			if home == "" {
				home = "/home/" + m.inputs[inputUsername].Value()
			}
			keyPath = strings.Replace(keyPath, "~", home, 1)
		}

		config := &ssh.SSHConfig{
			Host:       m.inputs[inputHost].Value(),
			Port:       port,
			Username:   m.inputs[inputUsername].Value(),
			Password:   m.inputs[inputPassword].Value(),
			PrivateKey: keyPath,
		}

		if err := config.Validate(); err != nil {
			m.err = err
			return nil
		}

		// Create terminal model
		termModel, err := NewTerminalModel(config)
		if err != nil {
			m.err = fmt.Errorf("connection failed: %w", err)
			return nil
		}

		return ConnectSuccessMsg{termModel: termModel}
	}
}

func (m *ConnectModel) updateInputs(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, len(m.inputs))

	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}

	return tea.Batch(cmds...)
}

func (m *ConnectModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("🌐 Connect to SSH Server"))
	b.WriteString("\n\n")

	for i := range m.inputs {
		b.WriteString(m.inputs[i].View())
		if i < len(m.inputs)-1 {
			b.WriteString("\n")
		}
	}

	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("tab: next • enter: connect • esc: back"))

	if m.err != nil {
		b.WriteString("\n\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", m.err)))
	}

	return boxStyle.Render(b.String())
}
