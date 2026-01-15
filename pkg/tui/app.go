package tui

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/quocson95/marix/pkg/ssh"
	"github.com/quocson95/marix/pkg/storage"
)

// AppState represents the current screen/state of the application
type AppState int

const (
	StateMenu AppState = iota
	StateConnect
	StateServers
	StateServerEdit
	StateSettings
	StateBackup
	StateSFTP
	StateTerminal
	StatePasswordPrompt
)

// AppModel is the root model that manages all screens
type AppModel struct {
	state               AppState
	menuModel           Model
	connectModel        *ConnectModel
	serversModel        *ServersModel
	serverEditModel     *ServerEditModel
	settingsModel       *SettingsModel
	backupModel         *BackupModel
	sftpModel           *SFTPDualModel
	termModel           *TerminalModel
	passwordPrompt      *PasswordPromptModel
	pendingServer       *storage.Server
	store               *storage.Store
	settingsStore       *storage.SettingsStore
	masterPasswordCache string // Cached valid password for session
	width               int
	height              int
}

// NewAppModel creates a new application model
func NewAppModel() (*AppModel, error) {
	// Initialize storage
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	dataDir := filepath.Join(homeDir, ".marix")
	store, err := storage.NewStore(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}

	settingsStore, err := storage.NewSettingsStore(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize settings: %w", err)
	}

	// Determine initial state
	initialState := StateMenu
	var passwordPrompt *PasswordPromptModel

	settings := settingsStore.Get()
	if settings.MasterPasswordHash != "" {
		initialState = StatePasswordPrompt
		passwordPrompt = NewPasswordPromptModel(
			"🔐 Master Password Required",
			"Please enter your master password to unlock:",
		)
	}

	return &AppModel{
		state:          initialState,
		menuModel:      InitialModel(),
		store:          store,
		settingsStore:  settingsStore,
		passwordPrompt: passwordPrompt,
	}, nil
}

func (m AppModel) Init() tea.Cmd {
	if m.state == StatePasswordPrompt && m.passwordPrompt != nil {
		return m.passwordPrompt.Init()
	}
	return nil
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		// Global quit
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}

	// Route to appropriate screen
	switch m.state {
	case StateMenu:
		return m.updateMenu(msg)
	case StateConnect:
		return m.updateConnect(msg)
	case StateServers:
		return m.updateServers(msg)
	case StateServerEdit:
		return m.updateServerEdit(msg)
	case StateSettings:
		return m.updateSettings(msg)
	case StateBackup:
		return m.updateBackup(msg)
	case StateSFTP:
		return m.updateSFTP(msg)
	case StateTerminal:
		return m.updateTerminal(msg)
	case StatePasswordPrompt:
		return m.updatePasswordPrompt(msg)
	default:
		return m, nil
	}
}

func (m AppModel) updateMenu(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	updatedModel, cmd := m.menuModel.Update(msg)
	m.menuModel = updatedModel.(Model)

	// Check if a menu option was selected
	switch m.menuModel.selected {
	case MenuConnect:
		m.state = StateConnect
		connectModel := NewConnectModel()
		m.connectModel = connectModel
		m.menuModel.selected = MenuNone // Reset
		return m, m.connectModel.Init()

	case MenuServers:
		m.state = StateServers
		serversModel := NewServersModel(m.store)
		m.serversModel = serversModel
		m.menuModel.selected = MenuNone
		return m, m.serversModel.Init()

	case MenuSFTP:
		// Show servers list in SFTP mode
		m.state = StateServers
		serversModel := NewServersModelForSFTP(m.store)
		m.serversModel = serversModel
		m.menuModel.selected = MenuNone
		return m, m.serversModel.Init()

	case MenuBackup:
		// Backup & Restore
		m.state = StateBackup
		backupModel := NewBackupModel(m.settingsStore)
		m.backupModel = backupModel
		m.menuModel.selected = MenuNone
		return m, m.backupModel.Init()

	case MenuSettings:
		m.state = StateSettings
		settingsModel := NewSettingsModel(m.store, m.settingsStore)
		m.settingsModel = settingsModel
		m.menuModel.selected = MenuNone
		return m, m.settingsModel.Init()

	case MenuQuit:
		return m, tea.Quit
	}

	return m, cmd
}

func (m AppModel) updateConnect(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "esc" {
			m.state = StateMenu
			return m, nil
		}
	case ConnectSuccessMsg:
		// Transition to terminal
		m.state = StateTerminal
		m.termModel = msg.termModel
		return m, m.termModel.Init()
	}

	var cmd tea.Cmd
	updatedModel, cmd := m.connectModel.Update(msg)
	m.connectModel = updatedModel.(*ConnectModel)
	return m, cmd
}

func (m AppModel) updateServers(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "esc" {
			m.state = StateMenu
			return m, nil
		}
	case ServerSelectedMsg:
		// No longer used - servers always launch external terminal
		return m, nil
	case ServerEditMsg:
		// Edit server
		m.state = StateServerEdit
		serverEditModel := NewServerEditModel(m.store, m.settingsStore, msg.server, msg.isNew, m.masterPasswordCache)
		m.serverEditModel = serverEditModel
		return m, m.serverEditModel.Init()
	case ServerSFTPMsg:
		// Connect to server and open SFTP
		return m, m.connectToSFTP(msg.server)
	case SFTPConnectMsg:
		// Handle SFTP connection result
		if msg.err != nil {
			// Show error in servers screen
			m.serversModel.err = msg.err
			return m, nil
		}
		// Success - transition to SFTP
		m.sftpModel = msg.sftpModel
		m.state = StateSFTP
		return m, m.sftpModel.Init()
	}

	var cmd tea.Cmd
	updatedModel, cmd := m.serversModel.Update(msg)
	m.serversModel = updatedModel.(*ServersModel)
	return m, cmd
}

func (m AppModel) updateServerEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "esc" {
			// Return to servers list
			m.state = StateServers
			// Reload servers list
			m.serversModel = NewServersModel(m.store)
			return m, m.serversModel.Init()
		}
		// Check if saved
		if m.serverEditModel.saved {
			// Auto-return to servers list after save
			m.state = StateServers
			m.serversModel = NewServersModel(m.store)
			return m, m.serversModel.Init()
		}
	}

	var cmd tea.Cmd
	updatedModel, cmd := m.serverEditModel.Update(msg)
	m.serverEditModel = updatedModel.(*ServerEditModel)
	return m, cmd
}

func (m AppModel) updateBackup(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "esc" {
			m.state = StateMenu
			return m, nil
		}
	}

	var cmd tea.Cmd
	updatedModel, cmd := m.backupModel.Update(msg)
	m.backupModel = updatedModel.(*BackupModel)
	return m, cmd
}

func (m AppModel) updateSFTP(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "esc" {
			// If we have an active terminal session, return to it
			if m.termModel != nil {
				m.state = StateTerminal
				return m, nil
			}
			// Otherwise return to menu (direct SFTP connection)
			m.state = StateMenu
			return m, nil
		}
	}

	var cmd tea.Cmd
	updatedModel, cmd := m.sftpModel.Update(msg)
	m.sftpModel = updatedModel.(*SFTPDualModel)
	return m, cmd
}

func (m *AppModel) updateTerminal(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			// Disconnect and return to menu
			if m.termModel != nil && m.termModel.client != nil {
				m.termModel.client.Close()
			}
			m.state = StateMenu
			return m, nil
		case "ctrl+f":
			// Open SFTP browser
			if m.termModel != nil && m.termModel.client != nil {
				sftpModel, err := NewSFTPDualModel(m.termModel.client)
				if err == nil {
					m.sftpModel = sftpModel
					m.state = StateSFTP
					return m, m.sftpModel.Init()
				}
			}
		}
	}

	var cmd tea.Cmd
	updatedModel, cmd := m.termModel.Update(msg)
	m.termModel = updatedModel.(*TerminalModel)
	return m, cmd
}

func (m AppModel) View() string {
	switch m.state {
	case StateMenu:
		return m.menuModel.View()
	case StateConnect:
		return m.connectModel.View()
	case StateServers:
		return m.serversModel.View()
	case StateServerEdit:
		return m.serverEditModel.View()
	case StateSettings:
		return m.settingsModel.View()
	case StateBackup:
		return m.backupModel.View()
	case StateSFTP:
		return m.sftpModel.View()
	case StateTerminal:
		return m.termModel.View()
	case StatePasswordPrompt:
		return m.passwordPrompt.View()
	default:
		return "Unknown state"
	}
}

func (m *AppModel) updatePasswordPrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case PasswordSubmittedMsg:
		if msg.Cancelled {
			// If cancelled at startup, quit
			if m.masterPasswordCache == "" && m.state == StatePasswordPrompt {
				return m, tea.Quit
			}
			// Otherwise return to servers/menu
			m.state = StateServers
			return m, nil
		}

		// Verify password
		if !m.settingsStore.VerifyMasterPassword(msg.Password) {
			m.passwordPrompt.SetError(fmt.Errorf("incorrect master password"))
			return m, nil
		}

		// Success! Cache and proceed
		m.masterPasswordCache = msg.Password

		// If we were connecting to a specific server (fallback prompt), continue connection
		if m.pendingServer != nil {
			return m, m.connectToSFTPWithPassword(m.pendingServer, msg.Password)
		}

		// Otherwise go to Menu (startup success)
		m.state = StateMenu
		return m, nil
	}

	var cmd tea.Cmd
	updatedModel, cmd := m.passwordPrompt.Update(msg)
	m.passwordPrompt = updatedModel.(*PasswordPromptModel)
	return m, cmd
}

// SFTPConnectMsg is sent when SFTP connection is established
type SFTPConnectMsg struct {
	sftpModel *SFTPDualModel
	err       error
}

// connectToSFTP connects to SSH server and opens SFTP manager
func (m *AppModel) connectToSFTP(server *storage.Server) tea.Cmd {
	// Check if server uses encrypted private key
	if len(server.PrivateKeyEncrypted) > 0 {
		// 1. Try cached password first
		if m.masterPasswordCache != "" {
			return m.connectToSFTPWithPassword(server, m.masterPasswordCache)
		}

		// If no cached password but encryption exists, we must prompt.
		// (This scenario implies startup skip or something, but strictly we prompt at start)
		// Fallback prompt:
		m.pendingServer = server
		m.passwordPrompt = NewPasswordPromptModel(
			"🔐 Private Key Password",
			fmt.Sprintf("Enter master password to decrypt private key for %s", server.Name),
		)
		m.state = StatePasswordPrompt
		return m.passwordPrompt.Init()
	}

	// No encrypted key, connect directly.
	// We might use cached master password as key password if no specific key password is set?
	// The user asked to use master key in settings for key encrypt/decrypt.
	// If the private key is NOT encrypted, maybe it's a legacy plain file.
	return m.connectToSFTPWithPassword(server, "")
}

// connectToSFTPWithPassword handles the actual connection with optional key decryption
func (m *AppModel) connectToSFTPWithPassword(server *storage.Server, keyPassword string) tea.Cmd {
	return func() tea.Msg {
		// Expand tilde in private key path
		privateKey := server.PrivateKey
		if len(privateKey) > 0 && privateKey[0] == '~' {
			home := os.Getenv("HOME")
			if home != "" {
				privateKey = home + privateKey[1:]
			}
		}

		// Create SSH config from server
		config := &ssh.SSHConfig{
			Host:        server.Host,
			Port:        server.Port,
			Username:    server.Username,
			Password:    server.Password,
			KeyPassword: keyPassword,
		}

		// Handle encrypted private key
		if len(server.PrivateKeyEncrypted) > 0 {
			// Decrypt the private key
			decrypted, err := storage.DecryptPrivateKey(
				server.PrivateKeyEncrypted,
				server.KeyEncryptionSalt,
				keyPassword,
			)
			if err != nil {
				log.Printf("Failed to decrypt private key for %s: %v\n", server.Name, err)
				return SFTPConnectMsg{err: fmt.Errorf("failed to decrypt private key: %w", err)}
			}
			// Set the decrypted key content directly
			config.KeyContent = decrypted
		} else if privateKey != "" {
			// Legacy: use file path
			config.PrivateKey = privateKey
		}

		// Validate config
		if err := config.Validate(); err != nil {
			log.Printf("SSH config validation failed for %s: %v\n", server.Name, err)
			return SFTPConnectMsg{err: err}
		}

		// Create SSH client
		sshClient := ssh.NewClient(config)
		if err := sshClient.Connect(); err != nil {
			log.Printf("SSH connection failed for %s: %v\n", server.Name, err)
			return SFTPConnectMsg{err: err}
		}

		// Create SFTP model
		sftpModel, err := NewSFTPDualModel(sshClient)
		if err != nil {
			log.Printf("SFTP initialization failed for %s: %v\n", server.Name, err)
			sshClient.Close()
			return SFTPConnectMsg{err: err}
		}

		// Set initial dimensions from app model
		sftpModel.width = m.width
		sftpModel.height = m.height

		return SFTPConnectMsg{sftpModel: sftpModel}
	}
}
