package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/quocson95/marix/pkg/storage"
)

// ServerEditModel manages editing a server
type ServerEditModel struct {
	store               *storage.Store
	settingsStore       *storage.SettingsStore
	server              *storage.Server
	inputs              []textinput.Model
	focused             int
	isNew               bool
	err                 error
	saved               bool
	width               int
	height              int
	passwordPrompt      *PasswordPromptModel
	awaitingKeyPassword bool
	privateKeyPath      string
	masterPassword      string // Cached master password for encryption
}

const (
	editName = iota
	editHost
	editPort
	editUsername
	editPassword
	editPrivateKey
)

// NewServerEditModel creates a new server edit model
func NewServerEditModel(store *storage.Store, settingsStore *storage.SettingsStore, server *storage.Server, isNew bool, masterPassword string) *ServerEditModel {
	inputs := make([]textinput.Model, 6)

	inputs[editName] = textinput.New()
	inputs[editName].Placeholder = "My Server"
	inputs[editName].CharLimit = 64
	inputs[editName].Width = 50
	inputs[editName].Prompt = "Name: "
	inputs[editName].Focus()

	inputs[editHost] = textinput.New()
	inputs[editHost].Placeholder = "192.168.1.1"
	inputs[editHost].CharLimit = 253
	inputs[editHost].Width = 50
	inputs[editHost].Prompt = "Host: "

	inputs[editPort] = textinput.New()
	inputs[editPort].Placeholder = "22"
	inputs[editPort].CharLimit = 5
	inputs[editPort].Width = 50
	inputs[editPort].Prompt = "Port: "

	inputs[editUsername] = textinput.New()
	inputs[editUsername].Placeholder = "root"
	inputs[editUsername].CharLimit = 32
	inputs[editUsername].Width = 50
	inputs[editUsername].Prompt = "Username: "

	inputs[editPassword] = textinput.New()
	inputs[editPassword].Placeholder = "(optional)"
	inputs[editPassword].CharLimit = 128
	inputs[editPassword].Width = 50
	inputs[editPassword].Prompt = "Password: "
	inputs[editPassword].EchoMode = textinput.EchoPassword
	inputs[editPassword].EchoCharacter = '•'

	inputs[editPrivateKey] = textinput.New()
	inputs[editPrivateKey].Placeholder = "~/.ssh/id_rsa (optional)"
	inputs[editPrivateKey].CharLimit = 256
	inputs[editPrivateKey].Width = 50
	inputs[editPrivateKey].Prompt = "Private Key Path: "

	m := &ServerEditModel{
		store:          store,
		settingsStore:  settingsStore,
		server:         server,
		inputs:         inputs,
		focused:        0,
		isNew:          isNew,
		masterPassword: masterPassword,
	}

	// Pre-fill values if editing existing server
	if !isNew && server != nil {
		m.inputs[editName].SetValue(server.Name)
		m.inputs[editHost].SetValue(server.Host)
		m.inputs[editPort].SetValue(fmt.Sprintf("%d", server.Port))
		m.inputs[editUsername].SetValue(server.Username)
		m.inputs[editPassword].SetValue(server.Password)
		m.inputs[editPrivateKey].SetValue(server.PrivateKey)
	}

	return m
}

func (m *ServerEditModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *ServerEditModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

		case "ctrl+s", "enter":
			// Save server
			return m, m.save()
		}
	}

	// Handle character input
	cmd := m.updateInputs(msg)
	return m, cmd
}

func (m *ServerEditModel) updateInputs(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, len(m.inputs))

	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}

	return tea.Batch(cmds...)
}

func (m *ServerEditModel) save() tea.Cmd {
	return func() tea.Msg {
		// Get values
		name := m.inputs[editName].Value()
		if name == "" {
			name = "Server"
		}

		host := m.inputs[editHost].Value()
		if host == "" {
			m.err = fmt.Errorf("host is required")
			return nil
		}

		portStr := m.inputs[editPort].Value()
		if portStr == "" {
			portStr = "22"
		}
		port := 22
		fmt.Sscanf(portStr, "%d", &port)

		username := m.inputs[editUsername].Value()
		if username == "" {
			username = "root"
		}

		password := m.inputs[editPassword].Value()
		privateKeyPath := m.inputs[editPrivateKey].Value()
		settings := m.settingsStore.Get()

		// Use Master Password if hash is set
		keyPassword := ""
		if settings.MasterPasswordHash != "" {
			// Ensure we have the password cached
			if m.masterPassword == "" {
				m.err = fmt.Errorf("master password required for encryption but not cached")
				return nil
			}
			// Verify it just in case (optional, but good sanity check)
			if !m.settingsStore.VerifyMasterPassword(m.masterPassword) {
				m.err = fmt.Errorf("cached master password invalid")
				return nil
			}
			keyPassword = m.masterPassword
		}

		// Handle private key encryption if provided
		var privateKeyEncrypted []byte
		var keyEncryptionSalt []byte
		var privateKeyContent string

		if privateKeyPath != "" {
			// Expand tilde in path
			if len(privateKeyPath) > 0 && privateKeyPath[0] == '~' {
				home, err := os.UserHomeDir()
				if err == nil {
					privateKeyPath = filepath.Join(home, privateKeyPath[1:])
				}
			}

			// Read private key file
			keyContent, err := os.ReadFile(privateKeyPath)
			if err != nil {
				m.err = fmt.Errorf("failed to read private key: %w", err)
				return nil
			}

			if keyPassword != "" {
				// Encrypt the private key content
				encrypted, salt, err := storage.EncryptPrivateKey(keyContent, keyPassword)
				if err != nil {
					m.err = fmt.Errorf("failed to encrypt private key: %w", err)
					return nil
				}
				privateKeyEncrypted = encrypted
				keyEncryptionSalt = salt
			} else {
				// Save as plaintext content
				privateKeyContent = string(keyContent)
			}
		}

		// Update or create server
		if m.isNew {
			server := &storage.Server{
				ID:                  fmt.Sprintf("server-%d", time.Now().Unix()),
				Name:                name,
				Host:                host,
				Port:                port,
				Username:            username,
				Password:            password,
				PrivateKey:          privateKeyContent, // Content if plaintext, empty if encrypted
				PrivateKeyEncrypted: privateKeyEncrypted,
				KeyEncryptionSalt:   keyEncryptionSalt,
				Protocol:            "ssh",
				CreatedAt:           time.Now().Unix(),
				UpdatedAt:           time.Now().Unix(),
			}
			m.store.Add(server)
		} else {
			m.server.Name = name
			m.server.Host = host
			m.server.Port = port
			m.server.Username = username
			m.server.Password = password

			if len(privateKeyEncrypted) > 0 {
				m.server.PrivateKeyEncrypted = privateKeyEncrypted
				m.server.KeyEncryptionSalt = keyEncryptionSalt
				m.server.PrivateKey = ""
			} else if privateKeyContent != "" {
				m.server.PrivateKey = privateKeyContent
				m.server.PrivateKeyEncrypted = nil
				m.server.KeyEncryptionSalt = nil
			}

			m.server.UpdatedAt = time.Now().Unix()
			m.store.Update(m.server)
		}

		m.saved = true
		m.err = nil
		return nil
	}
}

func (m *ServerEditModel) View() string {
	var b strings.Builder

	title := "✏️  Edit Server"
	if m.isNew {
		title = "➕ Add New Server"
	}
	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n\n")

	for i := range m.inputs {
		b.WriteString(m.inputs[i].View())
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("tab: next • ctrl+s/enter: save • esc: cancel"))

	if m.saved {
		b.WriteString("\n\n")
		b.WriteString(successStyle.Render("✓ Server saved!"))
	}

	if m.err != nil {
		b.WriteString("\n\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", m.err)))
	}

	return boxStyle.Render(b.String())
}
