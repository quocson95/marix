package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/quocson95/marix/pkg/storage"
)

// SettingsModel manages application settings
type SettingsModel struct {
	serverStore   *storage.Store // Added to support migration
	settingsStore *storage.SettingsStore
	settings      storage.Settings
	inputs        []textinput.Model
	focused       int
	cursor        int
	err           error
	width         int
	height        int
	saved         bool
}

const (
	settingPort           = 0
	settingUsername       = 1
	settingTheme          = 2
	settingMasterPassword = 3
	settingOldPassword    = 4
)

// BackupMsg indicates the result of a backup operation
type BackupMsg struct {
	err error
}

// RestoreMsg indicates the result of a restore operation
type RestoreMsg struct {
	err error
}

var themes = []string{"default", "dark", "light", "monokai", "solarized"}

// NewSettingsModel creates a new settings model
func NewSettingsModel(serverStore *storage.Store, settingsStore *storage.SettingsStore) *SettingsModel {
	settings := settingsStore.Get()

	// 5 inputs: Port, User, Theme, MasterPwd, OldPwd
	inputs := make([]textinput.Model, 5)

	inputs[0] = textinput.New()
	inputs[0].Placeholder = "22"
	inputs[0].CharLimit = 5
	inputs[0].Width = 40
	inputs[0].Prompt = "Default SSH Port: "
	inputs[0].SetValue(fmt.Sprintf("%d", settings.DefaultPort))

	inputs[1] = textinput.New()
	inputs[1].Placeholder = "root"
	inputs[1].CharLimit = 32
	inputs[1].Width = 40
	inputs[1].Prompt = "Default Username: "
	inputs[1].SetValue(settings.DefaultUsername)

	inputs[2] = textinput.New()
	inputs[2].Placeholder = "default"
	inputs[2].CharLimit = 32
	inputs[2].Width = 40
	inputs[2].Prompt = "Theme: "
	inputs[2].SetValue(settings.Theme)

	inputs[3] = textinput.New()
	if settings.MasterPasswordHash != "" {
		inputs[3].Placeholder = "(Master Password Set - Leave empty to keep)"
	} else {
		inputs[3].Placeholder = "(leave empty for no encryption)"
	}
	inputs[3].CharLimit = 128
	inputs[3].Width = 40
	inputs[3].Prompt = "Master Password: "
	inputs[3].EchoMode = textinput.EchoPassword
	inputs[3].EchoCharacter = '•'
	inputs[3].SetValue("") // Never show the hash or password

	inputs[4] = textinput.New()
	if settings.MasterPasswordHash != "" {
		inputs[4].Placeholder = "(required to change password)"
	} else {
		inputs[4].Placeholder = "(not needed for initial setup)"
	}
	inputs[4].CharLimit = 128
	inputs[4].Width = 40
	inputs[4].Prompt = "Old Password: "
	inputs[4].EchoMode = textinput.EchoPassword
	inputs[4].EchoCharacter = '•'
	inputs[4].SetValue("")

	return &SettingsModel{
		serverStore:   serverStore,
		settingsStore: settingsStore,
		settings:      settings,
		inputs:        inputs,
		focused:       -1, // Start with no input focused
		cursor:        0,
	}
}

func (m *SettingsModel) Init() tea.Cmd {
	return nil
}

func (m *SettingsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Handle Tab navigation globally
		if msg.String() == "tab" || msg.String() == "shift+tab" {
			// Blur current input if focused
			if m.focused >= 0 {
				m.inputs[m.focused].Blur()
				m.focused = -1
			}

			// Determine direction
			direction := 1
			if msg.String() == "shift+tab" {
				direction = -1
			}

			// Move cursor
			m.cursor += direction

			// Calculate max cursor index
			// Inputs (5) + AutoSave (1) + Save (1) + Reset (1) = 8 items (0-7)
			maxIndex := len(m.inputs) + 2

			// Wrap around
			if m.cursor > maxIndex {
				m.cursor = 0
			} else if m.cursor < 0 {
				m.cursor = maxIndex
			}

			// If exact cursor lands on an input field, auto-focus it for editing
			if m.cursor < len(m.inputs) {
				m.focused = m.cursor
				m.inputs[m.focused].Focus()
			}

			return m, nil
		}

		// If an input is focused, handle input
		if m.focused >= 0 && m.focused < len(m.inputs) {
			switch msg.String() {
			case "enter", "esc":
				// Unfocus input
				m.inputs[m.focused].Blur()
				m.focused = -1
				m.saved = false
				return m, nil
			default:
				// Update the focused input
				var cmd tea.Cmd
				m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)
				return m, cmd
			}
		}

		// Otherwise, handle navigation
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}

		case "down", "j":
			// Inputs (5)
			// + Auto-Save Toggle (1)
			// + Save Button (1)
			// + Reset Button (1)
			// Total items = 5 + 3 = 8 items (0 to 7)
			maxCursor := len(m.inputs) + 2
			if m.cursor < maxCursor {
				m.cursor++
			}

		case "enter", " ":
			// Handle selection based on cursor position
			if m.cursor < len(m.inputs) {
				// Focus on input
				m.focused = m.cursor
				m.inputs[m.focused].Focus()
			} else if m.cursor == len(m.inputs) {
				// Toggle auto-save
				m.settings.AutoSave = !m.settings.AutoSave
			} else if m.cursor == len(m.inputs)+1 {
				// Save settings
				return m, m.saveSettings()
			} else if m.cursor == len(m.inputs)+2 {
				// Reset to defaults
				return m, m.resetSettings()
			}

		case "s":
			// Quick save
			return m, m.saveSettings()

		case "r":
			// Quick reset
			return m, m.resetSettings()
		}

		// Handle Backup/Restore messages removed - now in dedicated screen
	}

	return m, nil
}

func (m *SettingsModel) saveSettings() tea.Cmd {
	return func() tea.Msg {
		// Parse port
		portStr := m.inputs[settingPort].Value()
		if portStr != "" {
			port, err := strconv.Atoi(portStr)
			if err == nil && port > 0 && port <= 65535 {
				m.settings.DefaultPort = port
			}
		}

		// Get username
		if username := m.inputs[settingUsername].Value(); username != "" {
			m.settings.DefaultUsername = username
		}

		// Get theme
		if theme := m.inputs[settingTheme].Value(); theme != "" {
			m.settings.Theme = theme
		}

		// Handle Master Password
		newPassword := m.inputs[settingMasterPassword].Value()
		if newPassword != "" {
			// Check if password hash already exists (changing password)
			if m.settings.MasterPasswordHash != "" {
				// Require old password verification
				oldPassword := m.inputs[settingOldPassword].Value()
				if oldPassword == "" {
					m.err = fmt.Errorf("old password required to change master password")
					return nil
				}

				// Verify old password
				if !m.settingsStore.VerifyMasterPassword(oldPassword) {
					m.err = fmt.Errorf("old password is incorrect")
					return nil
				}

				// Re-encrypt all private keys with new password
				if err := m.reencryptKeysWithNewPassword(oldPassword, newPassword); err != nil {
					m.err = fmt.Errorf("failed to re-encrypt keys: %v", err)
					return nil
				}
			}

			// User wants to set/change password
			if err := m.settingsStore.SetMasterPassword(newPassword); err != nil {
				m.err = err
				return nil
			}
			// Update the local settings struct to reflect the new hash
			m.settings = m.settingsStore.Get()

			// Trigger Migration for plaintext keys (first-time setup)
			if err := m.migrateKeysToMasterPassword(newPassword); err != nil {
				m.err = fmt.Errorf("settings saved but key migration failed: %v", err)
				return nil
			}
		}

		// Save to store (for non-password fields)
		if err := m.settingsStore.Update(m.settings); err != nil {
			m.err = err
			return nil
		}

		m.saved = true
		m.err = nil

		// Clear password inputs after save and update placeholders
		m.inputs[settingMasterPassword].SetValue("")
		m.inputs[settingOldPassword].SetValue("")
		if m.settings.MasterPasswordHash != "" {
			m.inputs[settingMasterPassword].Placeholder = "(Master Password Set - Leave empty to keep)"
			m.inputs[settingOldPassword].Placeholder = "(required to change password)"
		} else {
			m.inputs[settingOldPassword].Placeholder = "(not needed for initial setup)"
		}

		return nil
	}
}

func (m *SettingsModel) reencryptKeysWithNewPassword(oldPassword, newPassword string) error {
	servers := m.serverStore.List()
	updatedCount := 0

	for _, s := range servers {
		// Only re-encrypt if key is already encrypted
		if len(s.PrivateKeyEncrypted) == 0 {
			continue
		}

		// Decrypt with old password
		decrypted, err := storage.DecryptPrivateKey(
			s.PrivateKeyEncrypted,
			s.KeyEncryptionSalt,
			oldPassword,
		)
		if err != nil {
			return fmt.Errorf("failed to decrypt key for %s: %w", s.Name, err)
		}

		// Re-encrypt with new password
		encrypted, salt, err := storage.EncryptPrivateKey(decrypted, newPassword)
		if err != nil {
			return fmt.Errorf("failed to re-encrypt key for %s: %w", s.Name, err)
		}

		// Update server
		s.PrivateKeyEncrypted = encrypted
		s.KeyEncryptionSalt = salt
		s.UpdatedAt = time.Now().Unix()

		if err := m.serverStore.Update(s); err != nil {
			return fmt.Errorf("failed to update server %s: %w", s.Name, err)
		}
		updatedCount++
	}

	return nil
}

func (m *SettingsModel) migrateKeysToMasterPassword(password string) error {
	servers := m.serverStore.List()
	updatedCount := 0

	for _, s := range servers {
		// Only migrate if we have a Private Key path/content AND it's NOT already encrypted
		if s.PrivateKey != "" && len(s.PrivateKeyEncrypted) == 0 {
			var keyContent []byte
			var err error

			// Check if it's a file
			info, err := os.Stat(s.PrivateKey)
			if err == nil && !info.IsDir() {
				// It's a file path
				keyContent, err = os.ReadFile(s.PrivateKey)
				if err != nil {
					continue // Skip if can't read file
				}
			} else {
				// It's likely content
				keyContent = []byte(s.PrivateKey)
				// Basic check if it looks like a key (contains BEGIN)
				if !strings.Contains(string(keyContent), "BEGIN") {
					continue // Unsure what this is, skip
				}
			}

			// Encrypt
			encrypted, salt, err := storage.EncryptPrivateKey(keyContent, password)
			if err != nil {
				continue // Skip if encryption fails
			}

			// Update Server
			s.PrivateKeyEncrypted = encrypted
			s.KeyEncryptionSalt = salt
			s.PrivateKey = "" // Clear plaintext
			s.UpdatedAt = time.Now().Unix()

			if err := m.serverStore.Update(s); err != nil {
				continue
			}
			updatedCount++
		}
	}
	return nil
}

func (m *SettingsModel) resetSettings() tea.Cmd {
	return func() tea.Msg {
		if err := m.settingsStore.Reset(); err != nil {
			m.err = err
			return nil
		}

		// Reload settings
		m.settings = m.settingsStore.Get()
		m.inputs[settingPort].SetValue(fmt.Sprintf("%d", m.settings.DefaultPort))
		m.inputs[settingUsername].SetValue(m.settings.DefaultUsername)
		m.inputs[settingTheme].SetValue(m.settings.Theme)

		// Reset password input
		m.inputs[settingMasterPassword].SetValue("")
		m.inputs[settingOldPassword].SetValue("")
		if m.settings.MasterPasswordHash != "" {
			m.inputs[settingMasterPassword].Placeholder = "(Master Password Set - Leave empty to keep)"
			m.inputs[settingOldPassword].Placeholder = "(required to change password)"
		} else {
			m.inputs[settingMasterPassword].Placeholder = "(leave empty for no encryption)"
			m.inputs[settingOldPassword].Placeholder = "(not needed for initial setup)"
		}

		m.saved = true
		m.err = nil
		return nil
	}
}

func (m *SettingsModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("⚙️  Settings"))
	b.WriteString("\n\n")

	// Settings inputs (0-3: Port, Username, Theme, Master Password)
	for i := 0; i <= 3; i++ {
		input := m.inputs[i]
		cursor := "  "
		if m.cursor == i && m.focused < 0 {
			cursor = "→ "
		}
		b.WriteString(cursor)
		b.WriteString(input.View())
		b.WriteString("\n")
	}

	// Old Password  input (4) - only show if Master Password is set
	if m.settings.MasterPasswordHash != "" {
		input := m.inputs[settingOldPassword]
		cursor := "  "
		if m.cursor == settingOldPassword && m.focused < 0 {
			cursor = "→ "
		}
		b.WriteString(cursor)
		b.WriteString(input.View())
		b.WriteString("\n")
	}

	// Auto-save toggle
	cursor := "  "
	if m.cursor == len(m.inputs) {
		cursor = "→ "
	}
	autoSaveStatus := "☐"
	if m.settings.AutoSave {
		autoSaveStatus = "☑"
	}
	autoSaveStyle := itemStyle
	if m.cursor == len(m.inputs) {
		autoSaveStyle = selectedItemStyle
	}
	b.WriteString(cursor + autoSaveStyle.Render(fmt.Sprintf("%s Auto-save servers", autoSaveStatus)))
	b.WriteString("\n\n")

	// Main Actions (Save | Reset)
	cursorSave := " "
	styleSave := itemStyle
	if m.cursor == len(m.inputs)+1 { // len(m.inputs)+1 is 6
		cursorSave = "→"
		styleSave = selectedItemStyle
	}

	cursorReset := " "
	styleReset := itemStyle
	if m.cursor == len(m.inputs)+2 {
		cursorReset = "→"
		styleReset = selectedItemStyle
	}

	b.WriteString(fmt.Sprintf("%s%s    %s%s",
		cursorSave, styleSave.Render("💾 Save"),
		cursorReset, styleReset.Render("🔄 Reset")))
	b.WriteString("\n\n")

	// Help text
	b.WriteString(helpStyle.Render("↑/k up • ↓/j down • enter: edit/select • s: save • r: reset • esc: back"))

	// Status messages
	if m.saved {
		b.WriteString("\n\n")
		b.WriteString(successStyle.Render("✓ Settings saved!"))
	}

	if m.err != nil {
		b.WriteString("\n\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", m.err)))
	}

	// Info box
	b.WriteString("\n\n")
	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#626262")).
		Italic(true)
	b.WriteString(infoStyle.Render("Settings are saved to ~/.marix/settings.json"))

	return boxStyle.Render(b.String())
}
