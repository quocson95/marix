package tui

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/quocson95/marix/pkg/sftp"
	"github.com/quocson95/marix/pkg/ssh"
	"github.com/quocson95/marix/pkg/storage"
)

// PaneType represents which pane is active
type PaneType int

const (
	LocalPane PaneType = iota
	RemotePane
)

// LocalFileInfo represents local file information
type LocalFileInfo struct {
	Name  string
	Size  int64
	IsDir bool
}

// SFTPDualModel is the model for dual-pane SFTP browser
type SFTPDualModel struct {
	sshClient  *ssh.Client
	sftpClient *sftp.Client
	store      *storage.SettingsStore

	// Local pane
	// Local pane
	localPath         string
	localFiles        []LocalFileInfo
	localCursor       int
	displayLocalFiles []LocalFileInfo // Display/Filtered files (for search)

	// Remote pane
	remotePath         string
	remoteFiles        []sftp.FileInfo
	remoteCursor       int
	displayRemoteFiles []sftp.FileInfo // Display/Filtered files (for search)

	// UI state
	activePane PaneType
	err        error
	width      int
	height     int
	statusMsg  string

	// Input state
	creatingFolder bool
	input          textinput.Model

	// Search state
	searching   bool
	searchInput textinput.Model

	// Confirmation state
	confirmingDownload bool
	confirmingDelete   bool
	pendingFile        *sftp.FileInfo

	// Task Queue (new system)
	taskQueue    *sftp.TaskQueue
	taskUpdate   chan sftp.TaskProgress
	currentTasks []sftp.TaskProgress // Track active task progress
	logHistory   []string            // Last 10 lines of output

	// Refresh status
	refreshStatus     string
	refreshStatusTime int64
}

// NewSFTPDualModel creates a new dual-pane SFTP model
func NewSFTPDualModel(sshClient *ssh.Client, store *storage.SettingsStore) (*SFTPDualModel, error) {
	sftpClient, err := sftp.NewClient(sshClient.GetRawClient())
	if err != nil {
		return nil, fmt.Errorf("failed to create SFTP client: %w", err)
	}

	// Get initial directories
	remoteWd, err := sftpClient.GetWorkingDirectory()
	if err != nil {
		remoteWd = "/"
	}

	localWd, err := os.Getwd()
	if err != nil {
		localWd = os.Getenv("HOME")
	}

	// Create task update channel
	taskUpdateChan := make(chan sftp.TaskProgress, 10)

	// Get SSH config for engine creation
	sshConfig := sshClient.GetConfig()
	settings := store.Get()

	// Create task queue with max 5 concurrent tasks
	taskQueue := sftp.NewTaskQueue(sftpClient, sshConfig, &settings, 5, taskUpdateChan)

	// Initialize input
	ti := textinput.New()
	ti.Placeholder = "New Folder Name"
	ti.CharLimit = 156
	ti.Width = 40

	// Initialize search input
	si := textinput.New()
	si.Placeholder = "Search..."
	si.CharLimit = 50
	si.Width = 30

	m := &SFTPDualModel{
		sshClient:      sshClient,
		sftpClient:     sftpClient,
		store:          store,
		localPath:      localWd,
		remotePath:     remoteWd,
		activePane:     LocalPane,
		taskQueue:      taskQueue,
		taskUpdate:     taskUpdateChan,
		currentTasks:   make([]sftp.TaskProgress, 0),
		logHistory:     make([]string, 0, 10),
		input:          ti,
		creatingFolder: false,
		searchInput:    si,
		searching:      false,
	}

	// Load initial directories
	m.loadLocalDirectory()
	m.loadRemoteDirectory()

	// If remote load failed, try fallbacks
	if m.err != nil {
		m.err = nil
		m.remotePath = "."
		m.loadRemoteDirectory()
	}

	if m.err != nil {
		m.err = nil
		m.remotePath = "/"
		m.loadRemoteDirectory()
	}

	return m, nil
}

// addLog adds a message to the log history, keeping only last 10 lines
func (m *SFTPDualModel) addLog(msg string) {
	m.logHistory = append(m.logHistory, msg)
	maxLog := 5
	if len(m.logHistory) > maxLog {
		m.logHistory = m.logHistory[len(m.logHistory)-maxLog:]
	}
}

func (m *SFTPDualModel) loadLocalDirectory() {
	entries, err := os.ReadDir(m.localPath)
	if err != nil {
		log.Printf("Failed to list local directory %s: %v", m.localPath, err)
		m.err = err
		m.localFiles = []LocalFileInfo{}
		return
	}

	m.localFiles = make([]LocalFileInfo, 0, len(entries)+1)

	// Add parent directory entry
	m.localFiles = append(m.localFiles, LocalFileInfo{
		Name:  "..",
		IsDir: true,
	})

	for _, entry := range entries {
		info, err := entry.Info()
		size := int64(0)
		if err == nil {
			size = info.Size()
		}

		m.localFiles = append(m.localFiles, LocalFileInfo{
			Name:  entry.Name(),
			Size:  size,
			IsDir: entry.IsDir(),
		})
	}

	if m.localCursor >= len(m.localFiles) {
		m.localCursor = len(m.localFiles) - 1
	}
	if m.localCursor < 0 {
		m.localCursor = 0
	}

	// Reset display files
	m.displayLocalFiles = m.localFiles
	// If searching, re-apply filter? For now, clear search on reload or keep it?
	// Let's clear search on reload to be simple, OR re-filter.
	// User might want to refresh and keep filter.
	// To keep it simple: if searching, re-filter.
	if m.searching {
		m.updateFilter() // We need to implement this
	}

	m.err = nil
}

func (m *SFTPDualModel) loadRemoteDirectory() {
	files, err := m.sftpClient.List(m.remotePath)
	if err != nil {
		log.Printf("Failed to list remote directory %s: %v", m.remotePath, err)
		m.err = err
		m.remoteFiles = []sftp.FileInfo{}
		return
	}

	// Add parent directory entry at the beginning
	m.remoteFiles = make([]sftp.FileInfo, 0, len(files)+1)
	m.remoteFiles = append(m.remoteFiles, sftp.FileInfo{
		Name:  "..",
		IsDir: true,
	})
	m.remoteFiles = append(m.remoteFiles, files...)

	if m.remoteCursor >= len(m.remoteFiles) {
		m.remoteCursor = len(m.remoteFiles) - 1
	}
	if m.remoteCursor < 0 {
		m.remoteCursor = 0
	}

	// Reset display files
	m.displayRemoteFiles = m.remoteFiles
	if m.searching {
		m.updateFilter()
	}

	m.err = nil
}

// waitForTaskUpdate waits for task progress updates
func (m *SFTPDualModel) waitForTaskUpdate() tea.Msg {
	return <-m.taskUpdate
}

func (m *SFTPDualModel) Init() tea.Cmd {
	return m.waitForTaskUpdate
}

func (m *SFTPDualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case sftp.TaskProgress:
		// Update task progress in our list
		found := false
		for i, task := range m.currentTasks {
			if task.TaskID == msg.TaskID {
				m.currentTasks[i] = msg
				found = true
				break
			}
		}
		if !found {
			m.currentTasks = append(m.currentTasks, msg)
		}

		// Capture log from progress update
		if msg.LastLog != "" {
			m.addLog(fmt.Sprintf("[%d] %s", msg.TaskID, msg.LastLog))
		}

		// Clean up completed/failed/cancelled tasks
		var activeTasks []sftp.TaskProgress
		for _, task := range m.currentTasks {
			switch task.State {
			case sftp.TaskPending, sftp.TaskScanning, sftp.TaskTransferring:
				activeTasks = append(activeTasks, task)
			case sftp.TaskCompleted:
				// Refresh directories on completion
				m.addLog(fmt.Sprintf("[%d] Task completed", task.TaskID))
				m.loadLocalDirectory()
				m.loadRemoteDirectory()
			case sftp.TaskFailed:
				errMsg := "Unknown error"
				if msg.Error != "" {
					errMsg = msg.Error
				}
				m.addLog(fmt.Sprintf("[%d] Task failed: %s", task.TaskID, errMsg))
			case sftp.TaskCancelled:
				m.addLog(fmt.Sprintf("[%d] Task cancelled", task.TaskID))
			}
		}
		m.currentTasks = activeTasks

		return m, m.waitForTaskUpdate

	case tea.KeyMsg:
		// Handle folder creation input
		if m.creatingFolder {
			switch strings.ToLower(msg.String()) {
			case "enter":
				name := m.input.Value()
				if name != "" {
					m.createFolder(name)
				}
				m.creatingFolder = false
				m.input.SetValue("")
				return m, nil
			case "esc":
				m.creatingFolder = false
				m.input.SetValue("")
				return m, nil
			default:
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
		}

		// Handle search input
		if m.searching {
			switch msg.String() {
			case "enter":
				// Confirm selection (stay in search view or exit?)
				// Let's exit search mode but keep filter (so user can navigate)
				// Or better: Enter on search usually essentially "filters" the view.
				// User can then use Up/Down to navigate.
				// But we are capturing keys here.
				// If we want to navigate while searching, we need to handle Up/Down here too?
				// Common pattern: Search box is focused, 'Enter' -> Done searching, focus list (filtered).
				// 'Esc' -> Cancel search (unfilter).

				// Let's say Enter -> Focus List (keep filter)
				// But wait, if we focus list, `m.searching` becomes false?
				// We need a specific "Filtered" state vs "Searching" (typing) state?
				// Simplest: 'Enter' -> Navigate to current top match?

				// Re-reading request: "f for search pannel dir".
				// Let's implement: Type search -> Filter updates live.
				// Press 'Enter' -> Stop typing, focus list (keep filter).
				// Press 'Esc' -> Stop typing, Clear filter.

				m.searching = false
				// m.input.Blur() ? Bubbletea textinput has Focus/Blur.
				m.searchInput.Blur()
				return m, nil

			case "esc":
				m.searching = false
				m.searchInput.SetValue("")
				m.searchInput.Blur()
				m.updateFilter() // Clears filter
				return m, nil

			// Allow navigation while searching?
			case "down":
				// Hack: Allow down/up to move cursor in background list?
				if m.activePane == LocalPane {
					if m.localCursor < len(m.displayLocalFiles)-1 {
						m.localCursor++
					}
				} else {
					if m.remoteCursor < len(m.displayRemoteFiles)-1 {
						m.remoteCursor++
					}
				}
				return m, nil
			case "up":
				if m.activePane == LocalPane {
					if m.localCursor > 0 {
						m.localCursor--
					}
				} else {
					if m.remoteCursor > 0 {
						m.remoteCursor--
					}
				}
				return m, nil

			default:
				var cmd tea.Cmd
				m.searchInput, cmd = m.searchInput.Update(msg)
				m.updateFilter() // Update filter live
				return m, cmd
			}
		}

		// Handle confirmation dialogs
		if m.confirmingDownload {
			switch strings.ToLower(msg.String()) {
			case "y":
				m.confirmingDownload = false
				if m.pendingFile != nil {
					// Queue download for the pending file
					remotePath := filepath.Join(m.remotePath, m.pendingFile.Name)
					localPath := filepath.Join(m.localPath, m.pendingFile.Name)

					taskType := sftp.TaskDownloadFile
					if m.pendingFile.IsDir {
						taskType = sftp.TaskDownloadDirectory
					}

					_, err := m.taskQueue.QueueTask(taskType, remotePath, localPath, m.pendingFile.Name)
					if err != nil {
						m.statusMsg = fmt.Sprintf("Download failed: %v", err)
						m.addLog(fmt.Sprintf("ERROR: %v", err))
					} else {
						m.statusMsg = fmt.Sprintf("Downloading: %s", m.pendingFile.Name)
						shortRemote := shortenPath(remotePath, m.remotePath)
						shortLocal := shortenPath(localPath, m.localPath)
						m.addLog(fmt.Sprintf("Download: %s → %s", shortRemote, shortLocal))
					}
				}
				m.pendingFile = nil
				return m, nil
			case "n", "esc":
				m.confirmingDownload = false
				m.pendingFile = nil
				return m, nil
			}
			return m, nil
		}

		if m.confirmingDelete {
			switch strings.ToLower(msg.String()) {
			case "y":
				m.confirmingDelete = false
				m.deleteSelected()
				return m, nil
			case "n", "esc":
				m.confirmingDelete = false
				return m, nil
			}
			return m, nil
		}

		// Navigation and actions
		switch strings.ToLower(msg.String()) {
		case "tab":
			if m.activePane == LocalPane {
				m.activePane = RemotePane
			} else {
				m.activePane = LocalPane
			}
			return m, nil

		case "up", "k":
			if m.activePane == LocalPane {
				if m.localCursor > 0 {
					m.localCursor--
				}
			} else {
				if m.remoteCursor > 0 {
					m.remoteCursor--
				}
			}
			return m, nil

		case "down", "j":
			if m.activePane == LocalPane {
				if m.localCursor < len(m.displayLocalFiles)-1 {
					m.localCursor++
				}
			} else {
				if m.remoteCursor < len(m.displayRemoteFiles)-1 {
					m.remoteCursor++
				}
			}
			return m, nil

		case "enter":
			m.navigate()
			return m, nil

		case "backspace":
			// Go to parent directory
			if m.activePane == LocalPane {
				m.localPath = filepath.Dir(m.localPath)
				m.localCursor = 0
				m.loadLocalDirectory()
			} else {
				m.remotePath = filepath.Dir(m.remotePath)
				if m.remotePath == "" || m.remotePath == "." {
					m.remotePath = "/"
				}
				m.remoteCursor = 0
				m.loadRemoteDirectory()
			}
			return m, nil

		case "u":
			// Upload
			m.uploadSelected()
			return m, nil

		case "d":
			// Download
			m.downloadSelected()
			return m, nil

		case "delete", "del":
			// Delete with confirmation
			m.confirmingDelete = true
			return m, nil

		case "r":
			// Refresh both directories
			m.loadLocalDirectory()
			m.loadRemoteDirectory()
			m.statusMsg = "Refreshed"
			return m, nil

		case "c":
			// Cancel all transfers
			m.taskQueue.CancelAllTasks()
			m.statusMsg = "All transfers cancelled"
			return m, nil

		case "q", "ctrl+c":
			return m, tea.Quit

		case "alt+r":
			m.toggleRsync()
			return m, nil

		case "esc":
			if m.searchInput.Value() != "" {
				m.searchInput.SetValue("")
				m.updateFilter()
				return m, nil
			}

		case "f":
			m.searching = true
			m.searchInput.Focus()
			m.searchInput.SetValue("")
			// Clear previous filter? Or keep?
			// Usually start fresh
			m.updateFilter()
			return m, nil
		}
	}

	return m, nil
}

func (m *SFTPDualModel) toggleRsync() {
	settings := m.store.Get()
	newState := !settings.DisableRsync

	err := m.store.SetDisableRsync(newState)
	if err != nil {
		m.addLog(fmt.Sprintf("[ERROR] Failed to save settings: %v", err))
		return
	}

	// Update task queue settings reference
	newSettings := m.store.Get()
	m.taskQueue.UpdateSettings(&newSettings)

	if !newState {
		m.addLog("[INFO] Rsync ENABLED")
	} else {
		m.addLog("[INFO] Rsync DISABLED (Using Internal/SFTP)")
	}
}

func (m *SFTPDualModel) updateFilter() {
	term := strings.ToLower(m.searchInput.Value())

	if m.activePane == LocalPane {
		if term == "" {
			m.displayLocalFiles = m.localFiles
		} else {
			var filtered []LocalFileInfo
			// Always keep ".." if present? Usually yes.
			for _, f := range m.localFiles {
				if f.Name == ".." {
					filtered = append(filtered, f)
					continue
				}
				if strings.Contains(strings.ToLower(f.Name), term) {
					filtered = append(filtered, f)
				}
			}
			m.displayLocalFiles = filtered
		}
		// Reset cursor if out of bounds
		if m.localCursor >= len(m.displayLocalFiles) {
			m.localCursor = 0
		}
	} else {
		if term == "" {
			m.displayRemoteFiles = m.remoteFiles
		} else {
			var filtered []sftp.FileInfo
			for _, f := range m.remoteFiles {
				if f.Name == ".." {
					filtered = append(filtered, f)
					continue
				}
				if strings.Contains(strings.ToLower(f.Name), term) {
					filtered = append(filtered, f)
				}
			}
			m.displayRemoteFiles = filtered
		}
		if m.remoteCursor >= len(m.displayRemoteFiles) {
			m.remoteCursor = 0
		}
	}
}

// IsSearchActive returns true if search mode is active or a filter is applied
func (m *SFTPDualModel) IsSearchActive() bool {
	return m.searching || m.searchInput.Value() != ""
}

func (m *SFTPDualModel) navigate() {
	if m.activePane == LocalPane {
		if m.localCursor >= len(m.displayLocalFiles) { // Use Display
			return
		}
		file := m.displayLocalFiles[m.localCursor] // Use Display
		if !file.IsDir {
			return
		}

		if file.Name == ".." {
			m.localPath = filepath.Dir(m.localPath)
		} else {
			m.localPath = filepath.Join(m.localPath, file.Name)
		}
		m.localCursor = 0
		m.loadLocalDirectory()
	} else {
		if m.remoteCursor >= len(m.displayRemoteFiles) { // Use Display
			return
		}
		file := m.displayRemoteFiles[m.remoteCursor] // Use Display
		if !file.IsDir {
			return
		}

		if file.Name == ".." {
			m.remotePath = filepath.Dir(m.remotePath)
			if m.remotePath == "" || m.remotePath == "." {
				m.remotePath = "/"
			}
		} else {
			m.remotePath = filepath.Join(m.remotePath, file.Name)
		}
		m.remoteCursor = 0
		m.loadRemoteDirectory()
	}
}

// shortenPath returns a shortened version of the path for display
func shortenPath(fullPath, basePath string) string {
	if strings.HasPrefix(fullPath, basePath) {
		rel := strings.TrimPrefix(fullPath, basePath)
		rel = strings.TrimPrefix(rel, string(filepath.Separator))
		if rel == "" {
			return filepath.Base(fullPath)
		}
		return rel
	}
	return filepath.Base(fullPath)
}

func (m *SFTPDualModel) uploadSelected() {
	if m.activePane != LocalPane || m.localCursor >= len(m.displayLocalFiles) { // Use display
		return
	}

	file := m.displayLocalFiles[m.localCursor] // Use display
	if file.Name == ".." {
		return
	}

	localPath := filepath.Join(m.localPath, file.Name)
	remotePath := filepath.Join(m.remotePath, file.Name)

	taskType := sftp.TaskUploadFile
	if file.IsDir {
		taskType = sftp.TaskUploadDirectory
	}

	_, err := m.taskQueue.QueueTask(taskType, localPath, remotePath, file.Name)
	if err != nil {
		m.statusMsg = fmt.Sprintf("Upload failed: %v", err)
		m.addLog(fmt.Sprintf("ERROR: %v", err))
	} else {
		m.statusMsg = fmt.Sprintf("Uploading: %s", file.Name)
		shortLocal := shortenPath(localPath, m.localPath)
		shortRemote := shortenPath(remotePath, m.remotePath)
		m.addLog(fmt.Sprintf("Upload: %s → %s", shortLocal, shortRemote))
	}
}

func (m *SFTPDualModel) downloadSelected() {
	if m.activePane != RemotePane || m.remoteCursor >= len(m.displayRemoteFiles) { // Use display
		return
	}

	file := m.displayRemoteFiles[m.remoteCursor] // Use display
	if file.Name == ".." {
		return
	}

	m.pendingFile = &file // Store for confirmation
	m.confirmingDownload = true
}

func (m *SFTPDualModel) deleteSelected() {
	if m.activePane == LocalPane {
		if m.localCursor >= len(m.displayLocalFiles) { // Use display
			return
		}
		file := m.displayLocalFiles[m.localCursor] // Use display
		if file.Name == ".." {
			return
		}

		path := filepath.Join(m.localPath, file.Name)
		var err error
		if file.IsDir {
			err = os.RemoveAll(path)
		} else {
			err = os.Remove(path)
		}

		if err != nil {
			m.statusMsg = fmt.Sprintf("Delete failed: %v", err)
			m.addLog(fmt.Sprintf("ERROR: %v", err))
		} else {
			m.statusMsg = fmt.Sprintf("Deleted: %s", file.Name)
			shortPath := shortenPath(path, m.localPath)
			m.addLog(fmt.Sprintf("Deleted local: %s", shortPath))
			m.loadLocalDirectory()
		}
	} else {
		if m.remoteCursor >= len(m.displayRemoteFiles) { // Use display
			return
		}
		file := m.displayRemoteFiles[m.remoteCursor] // Use display
		if file.Name == ".." {
			return
		}

		path := filepath.Join(m.remotePath, file.Name)
		var err error

		if file.IsDir {
			// Use SSH rm -rf for directories
			cmd := fmt.Sprintf("rm -rf %s", path)
			_, err = m.sshClient.Execute(cmd)
		} else {
			// Use SFTP delete for files
			err = m.sftpClient.Delete(path)
		}

		if err != nil {
			m.statusMsg = fmt.Sprintf("Delete failed: %v", err)
			m.addLog(fmt.Sprintf("ERROR: %v", err))
		} else {
			m.statusMsg = fmt.Sprintf("Deleted: %s", file.Name)
			shortPath := shortenPath(path, m.remotePath)
			m.addLog(fmt.Sprintf("Deleted remote: %s", shortPath))
			m.loadRemoteDirectory()
		}
	}
}

func (m *SFTPDualModel) createFolder(name string) {
	if m.activePane == LocalPane {
		path := filepath.Join(m.localPath, name)
		err := os.MkdirAll(path, 0755)
		if err != nil {
			m.statusMsg = fmt.Sprintf("Create folder failed: %v", err)
		} else {
			m.statusMsg = fmt.Sprintf("Created: %s", name)
			m.loadLocalDirectory()
		}
	} else {
		path := filepath.Join(m.remotePath, name)
		err := m.sftpClient.Mkdir(path)
		if err != nil {
			m.statusMsg = fmt.Sprintf("Create folder failed: %v", err)
		} else {
			m.statusMsg = fmt.Sprintf("Created: %s", name)
			m.loadRemoteDirectory()
		}
	}
}

func (m *SFTPDualModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder

	// Show confirmation dialogs if active
	if m.confirmingDelete {
		fileName := ""
		if m.activePane == LocalPane && m.localCursor < len(m.displayLocalFiles) {
			fileName = m.displayLocalFiles[m.localCursor].Name
		} else if m.activePane == RemotePane && m.remoteCursor < len(m.displayRemoteFiles) {
			fileName = m.displayRemoteFiles[m.remoteCursor].Name
		}

		b.WriteString("\n")
		b.WriteString(strings.Repeat("═", m.width) + "\n")
		b.WriteString(" ⚠️  DELETE CONFIRMATION\n")
		b.WriteString(strings.Repeat("─", m.width) + "\n")
		b.WriteString(fmt.Sprintf(" Are you sure you want to delete: %s\n", fileName))
		b.WriteString(" Press 'Y' to confirm, 'N' or 'Esc' to cancel\n")
		b.WriteString(strings.Repeat("═", m.width) + "\n")
		b.WriteString("\n")
		return b.String()
	}

	if m.confirmingDownload {
		fileName := ""
		if m.pendingFile != nil {
			fileName = m.pendingFile.Name
		}

		b.WriteString("\n")
		b.WriteString(strings.Repeat("═", m.width) + "\n")
		b.WriteString(" 📥 DOWNLOAD CONFIRMATION\n")
		b.WriteString(strings.Repeat("─", m.width) + "\n")
		b.WriteString(fmt.Sprintf(" Download: %s\n", fileName))
		b.WriteString(" Press 'Y' to confirm, 'N' or 'Esc' to cancel\n")
		b.WriteString(strings.Repeat("═", m.width) + "\n")
		b.WriteString("\n")
		return b.String()
	}

	// Height Calculation Strategy
	// We have 3 parts: Panes (Top), Progress (Middle), Logs (Bottom).
	// We want Panes to be dominant.

	// 1. Calculate explicit heights

	// Progress Height: Flexible but reasonable limit
	// Header (3 lines) + Tasks (min 1, max 5) -> 4-8 lines
	currentTasksCount := len(m.currentTasks)
	if currentTasksCount == 0 {
		currentTasksCount = 1
	} // "No active transfers" line
	progressHeight := 3 + currentTasksCount

	// Logs Height: Flexible
	// Header(2) + Spacer(1) + Status(1) + Error(1) + Logs(max 10)
	// Max possible needed = 5 + 10 = 15
	targetLogHeight := 15

	// Available height
	totalHeight := m.height

	// Ensure we don't use too much for bottom panels
	maxBottomHeight := int(float64(totalHeight) * 0.4) // Bottom 40% max for logs+progress

	// Adjust
	if progressHeight+targetLogHeight > maxBottomHeight {
		// Compress if needed
		targetLogHeight = maxBottomHeight - progressHeight
		// if targetLogHeight < 5 { // Minimum practical log height
		// 	targetLogHeight = 5
		// 	// If still too big, eat into panes, but usually 5 is fine
		// }
	}

	// Panes take the rest
	// -2 for newlines between sections
	paneHeight := totalHeight - progressHeight - targetLogHeight - 2

	if paneHeight < 5 {
		// Critical failure case (terminal too small)
		// Try to show just panes? or squish everything
		paneHeight = 5
	}

	paneWidth := m.width / 2

	b.WriteString(m.renderDualPanes(paneHeight, paneWidth))
	b.WriteString("\n")

	// Part 2: Status/Progress
	b.WriteString(m.renderProgress(progressHeight))
	b.WriteString("\n")

	// Part 3: Last output
	b.WriteString(m.renderLastOutput(targetLogHeight))

	return b.String()
}

func (m *SFTPDualModel) renderDualPanes(height, width int) string {
	var b strings.Builder

	// Header
	localHeader := " LOCAL: " + m.localPath
	remoteHeader := " REMOTE: " + m.remotePath

	if m.activePane == LocalPane {
		localHeader = "▶" + localHeader
		if m.searching {
			localHeader += " [Search: " + m.searchInput.View() + "]"
		} else if len(m.displayLocalFiles) != len(m.localFiles) {
			localHeader += " [Filtered]"
		}
	} else {
		remoteHeader = "▶" + remoteHeader
		if m.searching {
			remoteHeader += " [Search: " + m.searchInput.View() + "]"
		} else if len(m.displayRemoteFiles) != len(m.remoteFiles) {
			remoteHeader += " [Filtered]"
		}
	}

	b.WriteString(fmt.Sprintf("%-*s│%-*s\n", width-4, localHeader, width, remoteHeader))
	b.WriteString(strings.Repeat("─", width-4) + "┼" + strings.Repeat("─", width) + "\n")

	// Calculate scrolling offset
	maxItems := height - 2

	// Calculate local scroll offset
	localOffset := 0
	if m.localCursor >= maxItems {
		localOffset = m.localCursor - maxItems + 1
	}

	// Calculate remote scroll offset
	remoteOffset := 0
	if m.remoteCursor >= maxItems {
		remoteOffset = m.remoteCursor - maxItems + 1
	}

	// File lists
	dirStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("33")).Bold(true)

	for i := 0; i < maxItems; i++ {
		localIdx := i + localOffset
		remoteIdx := i + remoteOffset

		// Local pane
		if localIdx < len(m.displayLocalFiles) { // Use Display Files
			file := m.displayLocalFiles[localIdx]
			prefix := "  "
			isSelected := localIdx == m.localCursor && m.activePane == LocalPane
			if isSelected {
				prefix = "► "
			}

			icon := "📄"
			if file.IsDir {
				icon = "📁"
			}

			sizeStr := formatSize(file.Size)
			if file.IsDir {
				sizeStr = "<DIR>"
			}

			// Name truncation
			nameWidth := width - 20
			name := file.Name
			if len(name) > nameWidth {
				name = name[:nameWidth-3] + "..."
			}

			lineContent := fmt.Sprintf("%s%s %-*s %10s", prefix, icon, nameWidth, name, sizeStr)

			// Apply styling
			style := lipgloss.NewStyle()
			if file.IsDir {
				style = dirStyle
			}
			if isSelected {
				style = style.Reverse(true)
			}

			// Render with fixed width to ensure background covers whole line if selected
			// We manually padded above, but lipgloss width is safer for background
			// However, fmt padding is simple. Let's just render.
			// Note: lipgloss.Style.Render() resets colors, so we shouldn't rely on b.WriteString padding after.
			// We already padded in Sprintf.

			b.WriteString(style.Render(lineContent))

			// Fill remaining space if Sprintf padding wasn't enough (it should be)
			// But lipgloss ansi codes don't count towards length in pure string len check
			// Sprintf is fine because input strings were plain text.
		} else {
			b.WriteString(strings.Repeat(" ", width-4))
		}

		b.WriteString("│")

		// Remote pane
		if remoteIdx < len(m.displayRemoteFiles) { // Use Display Files
			file := m.displayRemoteFiles[remoteIdx]
			prefix := "  "
			isSelected := remoteIdx == m.remoteCursor && m.activePane == RemotePane
			if isSelected {
				prefix = "► "
			}

			icon := "📄"
			if file.IsDir {
				icon = "📁"
			}

			sizeStr := formatSize(file.Size)
			if file.IsDir {
				sizeStr = "<DIR>"
			}

			nameWidth := width - 20
			name := file.Name
			if len(name) > nameWidth {
				name = name[:nameWidth-3] + "..."
			}

			lineContent := fmt.Sprintf("%s%s %-*s %10s", prefix, icon, nameWidth, name, sizeStr)

			style := lipgloss.NewStyle()
			if file.IsDir {
				style = dirStyle
			}
			if isSelected {
				style = style.Reverse(true)
			}

			b.WriteString(style.Render(lineContent))
		} else {
			b.WriteString(strings.Repeat(" ", width-3))
		}

		b.WriteString("\n")
	}

	return b.String()
}

func (m *SFTPDualModel) renderProgress(height int) string {
	var b strings.Builder

	b.WriteString("═" + strings.Repeat("═", m.width-2) + "═\n")
	b.WriteString(" TRANSFERS & PROGRESS\n")
	b.WriteString(strings.Repeat("─", m.width) + "\n")

	if len(m.currentTasks) == 0 {
		b.WriteString(" No active transfers\n")
	} else {
		for _, task := range m.currentTasks {
			var stateStr, progressStr string

			switch task.State {
			case sftp.TaskScanning:
				stateStr = "🔍 Scanning"
				progressStr = "..."
			case sftp.TaskTransferring:
				stateStr = "📤 Transfer"
				if task.TotalFiles > 0 {
					progressStr = fmt.Sprintf("%d/%d files (%d%%) %.2f MB/s",
						task.CompletedFiles,
						task.TotalFiles,
						task.Percentage,
						task.CurrentSpeed/1024/1024)
				} else {
					progressStr = "In progress..."
				}
			case sftp.TaskCompleted:
				stateStr = "✓ Done"
				progressStr = fmt.Sprintf("%d files", task.CompletedFiles)
			case sftp.TaskFailed:
				stateStr = "✗ Failed"
				progressStr = "Error"
			case sftp.TaskCancelled:
				stateStr = "⊘ Cancelled"
				progressStr = ""
			default:
				stateStr = "⏳ Pending"
				progressStr = ""
			}

			taskLine := fmt.Sprintf(" Task #%d: %s - %s\n", task.TaskID, stateStr, progressStr)
			b.WriteString(taskLine)
		}
	}

	return b.String()
}

func (m *SFTPDualModel) renderLastOutput(height int) string {
	if height <= 0 {
		return ""
	}
	var b strings.Builder

	// Header takes 2 lines (Divider + Controls)
	// Status/Error take 1-2 lines
	// Spacer takes 1 line

	// Pre-calculate fixed usage
	fixedUsed := 3 // Divider + Controls + Spacer
	if m.statusMsg != "" {
		fixedUsed++
	}
	if m.err != nil {
		fixedUsed++
	}

	availableForLogs := height - fixedUsed
	if availableForLogs < 0 {
		availableForLogs = 0
	}

	b.WriteString(strings.Repeat("─", m.width) + "\n")

	// Status line with controls and mode
	controls := "Controls: [U]pload [D]ownload [R]efresh [Alt+R] Toggle Rsync"

	// Rsync Status
	settings := m.store.Get()
	mode := "SFTP"
	if !settings.DisableRsync {
		mode = "RSYNC"
	}

	b.WriteString(fmt.Sprintf(" %-60s | Mode: [%s]\n", controls, mode))

	// Print status/error first
	if m.statusMsg != "" {
		b.WriteString(fmt.Sprintf(" Status: %s\n", m.statusMsg))
	}
	if m.err != nil {
		b.WriteString(fmt.Sprintf(" Error: %v\n", m.err))
	}

	b.WriteString("\n") // Spacer

	// Print last N logs that fit
	count := len(m.logHistory)
	start := 0
	if count > availableForLogs {
		start = count - availableForLogs
	}

	for i := start; i < count; i++ {
		logLine := m.logHistory[i]
		// Truncate if needed
		if len(logLine) > m.width-2 {
			logLine = logLine[:m.width-5] + "..."
		}
		b.WriteString(fmt.Sprintf(" %s\n", logLine))
	}

	return b.String()
}
