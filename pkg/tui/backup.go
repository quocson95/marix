package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/quocson95/marix/pkg/s3"
	"github.com/quocson95/marix/pkg/storage"
)

// BackupModel manages backup and restore operations
type BackupModel struct {
	settingsStore       *storage.SettingsStore
	inputs              []textinput.Model
	cursor              int
	focused             int
	width               int
	height              int
	s3Host              string
	s3AccessKey         string
	s3SecretKey         string
	dataDir             string
	s3BackupInProgress  bool
	s3RestoreInProgress bool
	statusMsg           string
	err                 error
}

const (
	backupS3Host      = 0
	backupS3AccessKey = 1
	backupS3SecretKey = 2
	backupPassword    = 3
)

// NewBackupModel creates a new backup model
func NewBackupModel(settingsStore *storage.SettingsStore) *BackupModel {
	settings := settingsStore.Get()

	// 4 inputs: S3Host, S3AccessKey, S3SecretKey, Password
	inputs := make([]textinput.Model, 4)

	inputs[backupS3Host] = textinput.New()
	inputs[backupS3Host].Placeholder = "https://s3.amazonaws.com"
	inputs[backupS3Host].CharLimit = 256
	inputs[backupS3Host].Width = 60
	inputs[backupS3Host].Prompt = "S3 Host: "
	inputs[backupS3Host].SetValue(settings.S3Host)

	inputs[backupS3AccessKey] = textinput.New()
	inputs[backupS3AccessKey].Placeholder = "Access Key ID"
	inputs[backupS3AccessKey].CharLimit = 128
	inputs[backupS3AccessKey].Width = 40
	inputs[backupS3AccessKey].Prompt = "S3 Access Key: "
	inputs[backupS3AccessKey].SetValue(settings.S3AccessKey)

	inputs[backupS3SecretKey] = textinput.New()
	inputs[backupS3SecretKey].Placeholder = "Secret Access Key"
	inputs[backupS3SecretKey].CharLimit = 128
	inputs[backupS3SecretKey].Width = 40
	inputs[backupS3SecretKey].Prompt = "S3 Secret Key: "
	inputs[backupS3SecretKey].EchoMode = textinput.EchoPassword
	inputs[backupS3SecretKey].EchoCharacter = '•'
	inputs[backupS3SecretKey].SetValue(settings.S3SecretKey)

	inputs[backupPassword] = textinput.New()
	inputs[backupPassword].Placeholder = "Encryption password"
	inputs[backupPassword].CharLimit = 128
	inputs[backupPassword].Width = 40
	inputs[backupPassword].Prompt = "Backup Password: "
	inputs[backupPassword].EchoMode = textinput.EchoPassword
	inputs[backupPassword].EchoCharacter = '•'
	inputs[backupPassword].SetValue("backup-password") // Default for now

	return &BackupModel{
		settingsStore: settingsStore,
		inputs:        inputs,
		cursor:        0,
		focused:       -1,
		dataDir:       settingsStore.GetDataDir(),
	}
}

func (m *BackupModel) Init() tea.Cmd {
	return nil
}

func (m *BackupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Handle Tab navigation
		if msg.String() == "tab" || msg.String() == "shift+tab" {
			if m.focused >= 0 {
				m.inputs[m.focused].Blur()
				m.focused = -1
			}

			direction := 1
			if msg.String() == "shift+tab" {
				direction = -1
			}

			m.cursor += direction
			maxIndex := len(m.inputs) + 1 // inputs + backup + restore

			if m.cursor > maxIndex {
				m.cursor = 0
			} else if m.cursor < 0 {
				m.cursor = maxIndex
			}

			if m.cursor < len(m.inputs) {
				m.focused = m.cursor
				m.inputs[m.focused].Focus()
			}

			return m, nil
		}

		// If input focused, handle input
		if m.focused >= 0 && m.focused < len(m.inputs) {
			switch msg.String() {
			case "enter", "esc":
				m.inputs[m.focused].Blur()
				m.focused = -1
				return m, nil
			default:
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
			maxCursor := len(m.inputs) + 1
			if m.cursor < maxCursor {
				m.cursor++
			}

		case "enter", " ":
			if m.cursor < len(m.inputs) {
				m.focused = m.cursor
				m.inputs[m.focused].Focus()
			} else if m.cursor == len(m.inputs) {
				// Backup
				return m, m.performBackup()
			} else if m.cursor == len(m.inputs)+1 {
				// Restore
				return m, m.performRestore()
			}

		case "b":
			// Quick backup
			return m, m.performBackup()

		case "r":
			// Quick restore
			return m, m.performRestore()
		}

	case BackupMsg:
		m.s3BackupInProgress = false
		if msg.err != nil {
			m.err = msg.err
			m.statusMsg = ""
		} else {
			m.err = nil
			m.statusMsg = "✓ Backup successful! 🎉"
		}
		return m, nil

	case RestoreMsg:
		m.s3RestoreInProgress = false
		if msg.err != nil {
			m.err = msg.err
			m.statusMsg = ""
		} else {
			m.err = nil
			m.statusMsg = "✓ Restore successful! Restart app. ♻️"
		}
		return m, nil
	}

	return m, nil
}

func (m *BackupModel) performBackup() tea.Cmd {
	return func() tea.Msg {
		host := m.inputs[backupS3Host].Value()
		access := m.inputs[backupS3AccessKey].Value()
		secret := m.inputs[backupS3SecretKey].Value()
		password := m.inputs[backupPassword].Value()

		if host == "" || access == "" || secret == "" {
			return BackupMsg{err: fmt.Errorf("missing S3 configuration")}
		}

		if password == "" {
			return BackupMsg{err: fmt.Errorf("backup password is required")}
		}

		m.s3BackupInProgress = true
		m.statusMsg = "Creating encrypted backup..."

		client, err := s3.NewClient(host, access, secret)
		if err != nil {
			return BackupMsg{err: fmt.Errorf("S3 connection failed: %w", err)}
		}

		err = client.Backup(m.dataDir, password)
		if err != nil {
			return BackupMsg{err: err}
		}

		return BackupMsg{nil}
	}
}

func (m *BackupModel) performRestore() tea.Cmd {
	return func() tea.Msg {
		host := m.inputs[backupS3Host].Value()
		access := m.inputs[backupS3AccessKey].Value()
		secret := m.inputs[backupS3SecretKey].Value()
		password := m.inputs[backupPassword].Value()

		if host == "" || access == "" || secret == "" {
			return RestoreMsg{err: fmt.Errorf("missing S3 configuration")}
		}

		if password == "" {
			return RestoreMsg{err: fmt.Errorf("backup password is required")}
		}

		m.s3RestoreInProgress = true
		m.statusMsg = "Restoring from encrypted backup..."

		client, err := s3.NewClient(host, access, secret)
		if err != nil {
			return RestoreMsg{err: fmt.Errorf("S3 connection failed: %w", err)}
		}

		err = client.Restore(m.dataDir, password)
		if err != nil {
			return RestoreMsg{err: err}
		}

		return RestoreMsg{nil}
	}
}

func (m *BackupModel) View() string {
	var s string

	s += titleStyle.Render("☁️ Backup & Restore") + "\n\n"

	// S3 Config inputs
	for i := 0; i < len(m.inputs); i++ {
		input := m.inputs[i]
		cursor := "  "
		if m.cursor == i && m.focused < 0 {
			cursor = "→ "
		}
		s += cursor + input.View() + "\n"
	}

	s += "\n"

	// Actions
	cursorBackup := " "
	styleBackup := itemStyle
	if m.cursor == len(m.inputs) {
		cursorBackup = "→"
		styleBackup = selectedItemStyle
	}

	cursorRestore := " "
	styleRestore := itemStyle
	if m.cursor == len(m.inputs)+1 {
		cursorRestore = "→"
		styleRestore = selectedItemStyle
	}

	s += fmt.Sprintf("%s%s    %s%s\n\n",
		cursorBackup, styleBackup.Render("⬆️  Backup to S3"),
		cursorRestore, styleRestore.Render("⬇️  Restore from S3"))

	// Help
	s += helpStyle.Render("↑/k up • ↓/j down • enter: edit/select • b: backup • r: restore • esc: back") + "\n"

	// Status/Progress
	if m.s3BackupInProgress {
		s += "\n" + successStyle.Render("⏳ Backing up...")
	} else if m.s3RestoreInProgress {
		s += "\n" + successStyle.Render("⏳ Restoring...")
	} else if m.statusMsg != "" {
		s += "\n" + successStyle.Render(m.statusMsg)
	}

	if m.err != nil {
		s += "\n" + errorStyle.Render(fmt.Sprintf("Error: %v", m.err))
	}

	// Info
	s += "\n\n"
	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#626262")).
		Italic(true)
	s += infoStyle.Render("🔐 Backups are encrypted with Argon2id + AES-256-GCM")

	return boxStyle.Render(s)
}
