package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/quocson95/marix/pkg/storage"
)

// ServerSelectedMsg is sent when a server is selected
type ServerSelectedMsg struct {
	server *storage.Server
}

// ServerEditMsg is sent when editing a server
type ServerEditMsg struct {
	server *storage.Server
	isNew  bool
}

// ServerSFTPMsg is sent when connecting to server via SFTP
type ServerSFTPMsg struct {
	server *storage.Server
}

// ServersModel manages the server list
type ServersModel struct {
	store          *storage.Store
	settingsStore  *storage.SettingsStore
	masterPassword string
	servers        []*storage.Server
	cursor         int
	err            error
	statusMsg      string
	width          int
	height         int
	sftpMode       bool // If true, selecting server opens SFTP instead of terminal
}

// NewServersModel creates a new servers model
func NewServersModel(store *storage.Store, settingsStore *storage.SettingsStore, masterPassword string) *ServersModel {
	return &ServersModel{
		store:          store,
		settingsStore:  settingsStore,
		masterPassword: masterPassword,
		servers:        store.List(),
		cursor:         0,
		sftpMode:       false,
	}
}

// NewServersModelForSFTP creates servers model for SFTP selection
func NewServersModelForSFTP(store *storage.Store, settingsStore *storage.SettingsStore, masterPassword string) *ServersModel {
	return &ServersModel{
		store:          store,
		settingsStore:  settingsStore,
		masterPassword: masterPassword,
		servers:        store.List(),
		cursor:         0,
		sftpMode:       true,
	}
}

func (m *ServersModel) Init() tea.Cmd {
	return nil
}

func (m *ServersModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}

		case "down", "j":
			if m.cursor < len(m.servers)-1 {
				m.cursor++
			}

		case "enter", " ":
			if len(m.servers) > 0 && m.cursor < len(m.servers) {
				server := m.servers[m.cursor]

				if m.sftpMode {
					// Open SFTP for this server
					return m, func() tea.Msg {
						return ServerSFTPMsg{server: server}
					}
				} else {
					// Prepare private key (decrypt if needed)
					privateKey := server.PrivateKey
					if len(server.PrivateKeyEncrypted) > 0 {
						if m.masterPassword != "" {
							decrypted, err := storage.DecryptPrivateKey(server.PrivateKeyEncrypted, server.KeyEncryptionSalt, m.masterPassword)
							if err != nil {
								m.err = fmt.Errorf("failed to decrypt key: %w", err)
								return m, nil
							}
							privateKey = string(decrypted)
						} else {
							m.err = fmt.Errorf("master password required to decrypt key")
							return m, nil
						}
					}

					// Always launch in external terminal for SSH
					err := LaunchExternalTerminal(server.Host, server.Port, server.Username, server.Password, privateKey)
					if err != nil {
						m.err = fmt.Errorf("failed to launch terminal: %w", err)
					}
				}
			}

		case "e":
			// Edit selected server
			if len(m.servers) > 0 && m.cursor < len(m.servers) {
				return m, func() tea.Msg {
					return ServerEditMsg{server: m.servers[m.cursor], isNew: false}
				}
			}

		case "a":
			// Add new server
			newServer := &storage.Server{
				Name:     "New Server",
				Host:     "192.168.1.1",
				Port:     22,
				Username: "root",
				Password: "",
				Protocol: "ssh",
			}
			return m, func() tea.Msg {
				return ServerEditMsg{server: newServer, isNew: true}
			}

		case "d":
			// Delete selected server
			if len(m.servers) > 0 && m.cursor < len(m.servers) {
				m.store.Delete(m.servers[m.cursor].ID)
				m.servers = m.store.List()
				if m.cursor >= len(m.servers) && m.cursor > 0 {
					m.cursor--
				}
				// Trigger auto-backup
				return m, RunAutoBackup(m.settingsStore, m.masterPassword, "deleted")
			}
		}

	case AutoBackupMsg:
		if msg.Err != nil {
			m.statusMsg = fmt.Sprintf("Auto-backup failed: %v", msg.Err)
		} else {
			m.statusMsg = fmt.Sprintf("Server %s & Auto-backup successful!", msg.Action)
			// Clear status after 3 seconds
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return statusClearMsg{}
			})
		}
		return m, nil

	case statusClearMsg:
		m.statusMsg = ""
		return m, nil
	}

	return m, nil
}

type statusClearMsg struct{}

func (m *ServersModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("📚 Saved Servers"))
	b.WriteString("\n\n")

	if len(m.servers) == 0 {
		b.WriteString(helpStyle.Render("No saved servers. Press 'a' to add one."))
	} else {
		for i, server := range m.servers {
			cursor := "  "
			style := itemStyle

			if m.cursor == i {
				cursor = "→ "
				style = selectedItemStyle
			}

			serverInfo := fmt.Sprintf("%s (%s@%s:%d)",
				server.Name,
				server.Username,
				server.Host,
				server.Port,
			)

			b.WriteString(cursor + style.Render(serverInfo))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	if m.sftpMode {
		b.WriteString(helpStyle.Render("↑/k up • ↓/j down • enter: open sftp • e: edit • a: add • d: delete • esc: back"))
	} else {
		b.WriteString(helpStyle.Render("↑/k up • ↓/j down • enter: open terminal • e: edit • a: add • d: delete • esc: back"))
	}

	if m.err != nil {
		b.WriteString("\n\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", m.err)))
	}

	if m.statusMsg != "" {
		b.WriteString("\n\n")
		b.WriteString(successStyle.Render(m.statusMsg))
	}

	return boxStyle.Render(b.String())
}
