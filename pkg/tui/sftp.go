package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/quocson95/marix/pkg/sftp"
	"github.com/quocson95/marix/pkg/ssh"
)

// SFTPModel manages SFTP file browser
type SFTPModel struct {
	sshClient    *ssh.Client
	sftpClient   *sftp.Client
	currentPath  string
	files        []sftp.FileInfo
	cursor       int
	err          error
	width        int
	height       int
	transferring bool
	localPath    string
}

// NewSFTPModel creates a new SFTP model
func NewSFTPModel(sshClient *ssh.Client) (*SFTPModel, error) {
	sftpClient, err := sftp.NewClient(sshClient.GetRawClient())
	if err != nil {
		return nil, fmt.Errorf("failed to create SFTP client: %w", err)
	}

	// Get initial directory
	wd, err := sftpClient.GetWorkingDirectory()
	if err != nil {
		wd = "/"
	}

	m := &SFTPModel{
		sshClient:   sshClient,
		sftpClient:  sftpClient,
		currentPath: wd,
		cursor:      0,
		localPath:   ".",
	}

	// Load initial directory
	m.loadDirectory()

	return m, nil
}

func (m *SFTPModel) loadDirectory() {
	files, err := m.sftpClient.List(m.currentPath)
	if err != nil {
		m.err = err
		m.files = []sftp.FileInfo{}
		return
	}

	m.files = files
	m.err = nil
	if m.cursor >= len(m.files) {
		m.cursor = len(m.files) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *SFTPModel) Init() tea.Cmd {
	return nil
}

func (m *SFTPModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			if m.cursor < len(m.files)-1 {
				m.cursor++
			}

		case "enter":
			// Navigate into directory or download file
			if len(m.files) > 0 && m.cursor < len(m.files) {
				file := m.files[m.cursor]
				if file.IsDir {
					// Change directory
					if file.Name == ".." {
						m.currentPath = filepath.Dir(m.currentPath)
					} else {
						m.currentPath = filepath.Join(m.currentPath, file.Name)
					}
					m.loadDirectory()
					m.cursor = 0
				} else {
					// Download file
					return m, m.downloadFile(file)
				}
			}

		case "d":
			// Download selected file/directory
			if len(m.files) > 0 && m.cursor < len(m.files) {
				file := m.files[m.cursor]
				if file.Name != ".." {
					return m, m.downloadFile(file)
				}
			}

		case "u":
			// Upload file (simplified - asks for path)
			// TODO: Implement file picker
			m.err = fmt.Errorf("upload not yet implemented - use 'd' to download")

		case "delete":
			// Delete selected file
			if len(m.files) > 0 && m.cursor < len(m.files) {
				file := m.files[m.cursor]
				if file.Name != ".." {
					return m, m.deleteFile(file)
				}
			}

		case "r":
			// Refresh directory
			m.loadDirectory()
		}
	}

	return m, nil
}

func (m *SFTPModel) downloadFile(file sftp.FileInfo) tea.Cmd {
	return func() tea.Msg {
		m.transferring = true
		remotePath := filepath.Join(m.currentPath, file.Name)
		localPath := filepath.Join(m.localPath, file.Name)

		var err error
		if file.IsDir {
			err = m.sftpClient.DownloadDirectory(remotePath, localPath)
		} else {
			err = m.sftpClient.Download(remotePath, localPath, nil)
		}

		m.transferring = false
		if err != nil {
			m.err = fmt.Errorf("download failed: %w", err)
		} else {
			m.err = nil
		}
		return nil
	}
}

func (m *SFTPModel) deleteFile(file sftp.FileInfo) tea.Cmd {
	return func() tea.Msg {
		remotePath := filepath.Join(m.currentPath, file.Name)

		var err error
		if file.IsDir {
			err = m.sftpClient.Rmdir(remotePath)
		} else {
			err = m.sftpClient.Delete(remotePath)
		}

		if err != nil {
			m.err = fmt.Errorf("delete failed: %w", err)
		} else {
			m.loadDirectory()
		}
		return nil
	}
}

func (m *SFTPModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(fmt.Sprintf("📁 SFTP Browser: %s", m.currentPath)))
	b.WriteString("\n\n")

	// File list
	if len(m.files) == 0 {
		b.WriteString(helpStyle.Render("Empty directory"))
	} else {
		// Add parent directory entry
		cursor := "  "
		if m.cursor == 0 {
			cursor = "→ "
		}
		style := itemStyle
		if m.cursor == 0 {
			style = selectedItemStyle
		}
		b.WriteString(cursor + style.Render("📁 .."))
		b.WriteString("\n")

		// List files
		displayCount := 15
		if m.height > 20 {
			displayCount = m.height - 10
		}

		startIdx := 0
		if m.cursor > displayCount/2 {
			startIdx = m.cursor - displayCount/2
		}
		endIdx := startIdx + displayCount
		if endIdx > len(m.files) {
			endIdx = len(m.files)
		}

		for i := startIdx; i < endIdx; i++ {
			file := m.files[i]
			cursor := "  "
			style := itemStyle

			if m.cursor == i+1 { // +1 because of ".." entry
				cursor = "→ "
				style = selectedItemStyle
			}

			icon := "📄"
			if file.IsDir {
				icon = "📁"
			}

			sizeStr := formatSize(file.Size)
			name := file.Name
			if len(name) > 40 {
				name = name[:37] + "..."
			}

			line := fmt.Sprintf("%s %s (%s)", icon, name, sizeStr)
			b.WriteString(cursor + style.Render(line))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑/k up • ↓/j down • enter: open/download • d: download • r: refresh • delete: delete • esc: back"))

	if m.transferring {
		b.WriteString("\n\n")
		b.WriteString(successStyle.Render("Transferring..."))
	}

	if m.err != nil {
		b.WriteString("\n\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", m.err)))
	}

	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#626262")).
		Italic(true)
	b.WriteString("\n\n")
	b.WriteString(infoStyle.Render(fmt.Sprintf("Local: %s", m.localPath)))

	return boxStyle.Render(b.String())
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
