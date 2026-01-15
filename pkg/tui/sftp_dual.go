package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/quocson95/marix/pkg/sftp"
	"github.com/quocson95/marix/pkg/ssh"
)

// Large file threshold (10MB)
const MaxEditSize = 10 * 1024 * 1024

// PaneType represents which pane is active
type PaneType int

const (
	LocalPane PaneType = iota
	RemotePane
)

// SFTPDualModel manages dual-pane SFTP file browser
type SFTPDualModel struct {
	sshClient  *ssh.Client
	sftpClient *sftp.Client

	// Local pane
	localPath   string
	localFiles  []LocalFileInfo
	localCursor int

	// Remote pane
	remotePath   string
	remoteFiles  []sftp.FileInfo
	remoteCursor int

	// UI state
	activePane PaneType
	err        error
	width      int
	height     int
	statusMsg  string

	// Input state
	creatingFolder bool
	input          textinput.Model

	// Confirmation state
	confirmingDownload bool
	confirmingDelete   bool
	pendingFile        *sftp.FileInfo

	// Transfer Queue
	transferQueue  *TransferQueue
	transferUpdate chan TransferStatusMsg

	// Cancellation
	mu sync.Mutex
	// ctx    context.Context
	// cancel context.CancelFunc
	ctxMap    map[int]context.Context
	cancelMap map[int]context.CancelFunc
	serial    int

	// Debug: rsync output display
	rsyncOutputLines []string // Last 10 lines of rsync output

	// Refresh status
	refreshStatus     string
	refreshStatusTime time.Time
}

// TransferType distinguishes between upload and download
type TransferType int

const (
	TransferUpload TransferType = iota
	TransferDownload
)

// TransferJob represents a single file transfer operation
type TransferJob struct {
	Type        TransferType
	SourcePath  string
	DestPath    string
	FileName    string
	Size        int64
	Serial      int
	IsDirectory bool // true if transferring entire directory with rsync
}

// TransferStatusMsg updates the UI about queue progress
type TransferStatusMsg struct {
	Total     int
	Pending   int
	Active    int
	Completed int
	Failed    int
	Last      string // Name of last completed file
	Err       error
	BytesSec  float64 // Speed in bytes/sec
}

// TransferQueue manages concurrent transfers
type TransferQueue struct {
	jobs      chan TransferJob
	results   chan TransferStatusMsg
	total     int
	pending   int
	active    int
	completed int
	failed    int
	maxActive int

	// Speed calculation
	bytesTransferred int64
	lastMeasured     int64 // timestamp
	currentSpeed     float64
	currentPercent   int       // Current progress percentage for directory transfers
	mu               chan bool // rudimentary mutex via channel
}

// NewTransferQueue creates a new transfer queue
func NewTransferQueue(updateChan chan TransferStatusMsg) *TransferQueue {
	return &TransferQueue{
		jobs:      make(chan TransferJob, 1000), // Buffer for many files
		results:   updateChan,
		maxActive: 5,
		mu:        make(chan bool, 1),
	}
}

// LocalFileInfo represents local file information
type LocalFileInfo struct {
	Name  string
	Size  int64
	IsDir bool
}

// NewSFTPDualModel creates a new dual-pane SFTP model
func NewSFTPDualModel(sshClient *ssh.Client) (*SFTPDualModel, error) {
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

	updateChan := make(chan TransferStatusMsg)
	queue := NewTransferQueue(updateChan)

	// Initialize input
	ti := textinput.New()
	ti.Placeholder = "New Folder Name"
	ti.CharLimit = 156
	ti.Width = 40

	//

	m := &SFTPDualModel{
		sshClient:      sshClient,
		sftpClient:     sftpClient,
		localPath:      localWd,
		remotePath:     remoteWd,
		activePane:     LocalPane,
		transferQueue:  queue,
		transferUpdate: updateChan,
		input:          ti,
		creatingFolder: false,
		// ctx:            ctx,
		// cancel:         cancel,
		serial:    0,
		ctxMap:    make(map[int]context.Context),
		cancelMap: make(map[int]context.CancelFunc),
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.ctxMap[m.serial] = ctx
	m.cancelMap[m.serial] = cancel

	// Start worker pool
	for i := 0; i < queue.maxActive; i++ {
		go m.transferWorker()
	}

	// Start speed ticker
	go m.speedTicker()

	// Load initial directories
	m.loadLocalDirectory()
	m.loadRemoteDirectory()

	// If remote load failed, try fallbacks
	if m.err != nil {
		// Try current directory "."
		m.err = nil
		m.remotePath = "."
		m.loadRemoteDirectory()
	}

	if m.err != nil {
		// Try root "/"
		m.err = nil
		m.remotePath = "/"
		m.loadRemoteDirectory()
	}

	return m, nil
}

func (m *SFTPDualModel) speedTicker() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var lastBytes int64
	for range ticker.C {
		m.transferQueue.mu <- true
		current := m.transferQueue.bytesTransferred
		diff := current - lastBytes
		lastBytes = current
		<-m.transferQueue.mu

		if diff > 0 || m.transferQueue.active > 0 {
			m.transferUpdate <- TransferStatusMsg{
				Active:   m.transferQueue.active,
				BytesSec: float64(diff),
			}
		}
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
	m.err = nil
}

func (m *SFTPDualModel) cancelAllTransfers() {
	m.mu.Lock()
	// if m.cancel != nil {
	// 	m.cancel()
	// }
	oldSerial := m.serial
	defer func() {
		delete(m.cancelMap, oldSerial)
		delete(m.ctxMap, oldSerial)
	}()
	m.serial++
	ctx, cancel := context.WithCancel(context.Background())
	m.ctxMap[m.serial] = ctx
	m.cancelMap[m.serial] = cancel
	defer m.mu.Unlock()
	if cancel, ok := m.cancelMap[oldSerial]; ok {
		cancel()
	}

	// Drain pending jobs and mark as cancelled
	cancelledCount := 0
Loop:
	for {
		select {
		case <-m.transferQueue.jobs:
			m.transferQueue.pending--
			cancelledCount++
		default:
			break Loop
		}
	}

	// Update failed counter for cancelled jobs
	if cancelledCount > 0 {
		m.transferQueue.failed += cancelledCount
		m.notifyTransferUpdate(fmt.Errorf("cancelled %d pending transfers", cancelledCount), "")
	}

	// Clear rsync output when cancelling
	m.rsyncOutputLines = nil

	// Reset active counter (running transfers will fail with context.Canceled)
	m.transferQueue.active = 0
}

func (m *SFTPDualModel) transferWorker() {
	for job := range m.transferQueue.jobs {
		// drop if serial is not match
		if job.Serial != m.serial {
			continue
		}
		// Signal job started (active)
		m.transferQueue.active++
		m.transferQueue.pending--
		m.notifyTransferUpdate(nil, "")

		// Get current context
		ctx := m.ctxMap[m.serial]

		var err error

		// Handle directory transfers with rsync directly
		if job.IsDirectory {
			// Check for rsync availability
			if _, pathErr := exec.LookPath("rsync"); pathErr == nil {
				err = m.rsyncTransferDir(job, ctx)
			} else {
				log.Printf("Rsync not available for directory transfer: %s", job.FileName)
				err = fmt.Errorf("rsync not available")
			}
		} else {
			// Define progress callback for file transfers
			progress := func(bytes int64) error {
				// Check for cancellation
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				// Update queue stats securely
				m.transferQueue.mu <- true
				m.transferQueue.bytesTransferred += bytes
				<-m.transferQueue.mu
				return nil
			}

			// Check for rsync availability
			useRsync := false
			if _, pathErr := exec.LookPath("rsync"); pathErr == nil {
				useRsync = true
			}

			// Check if already cancelled before starting
			if ctx.Err() != nil {
				err = ctx.Err()
			} else if useRsync {
				err = m.rsyncTransfer(job, ctx, progress)
			} else {
				if job.Type == TransferUpload {
					err = m.sftpClient.Upload(job.SourcePath, job.DestPath, progress)
				} else {
					err = m.sftpClient.Download(job.SourcePath, job.DestPath, progress)
				}
			}
		}

		m.transferQueue.active--
		if err != nil {
			if err == context.Canceled {
				// Don't log as error, just status
				m.notifyTransferUpdate(nil, "")
			} else {
				log.Printf("Transfer failed for %s: %v", job.FileName, err)
				m.transferQueue.failed++
				m.notifyTransferUpdate(err, "")
			}
		} else {
			m.transferQueue.completed++
			m.notifyTransferUpdate(nil, job.FileName)

			// Refresh directory listings after directory transfer completes
			if job.IsDirectory {
				m.loadLocalDirectory()
				m.loadRemoteDirectory()

				// Clear rsync output after directory transfer completes
				m.mu.Lock()
				m.rsyncOutputLines = nil
				m.mu.Unlock()
			}
		}
	}
}

func (m *SFTPDualModel) rsyncTransfer(job TransferJob, ctx context.Context, progress func(int64) error) error {
	config := m.sshClient.GetConfig()

	// Handle key
	keyPath := config.PrivateKey
	if len(config.KeyContent) > 0 {
		// Write temp file
		tmpFile, err := os.CreateTemp("", "marix-rsync-*.pem")
		if err != nil {
			return fmt.Errorf("temp key creation failed: %w", err)
		}
		defer os.Remove(tmpFile.Name())
		defer tmpFile.Close()

		// Set secure permissions (user-only read/write)
		if err := os.Chmod(tmpFile.Name(), 0600); err != nil {
			return fmt.Errorf("failed to set temp key permissions: %w", err)
		}

		if _, err := tmpFile.Write(config.KeyContent); err != nil {
			return fmt.Errorf("temp key write failed: %w", err)
		}
		tmpFile.Close()
		keyPath = tmpFile.Name()
	}

	// Build Rsync Command
	// rsync -avz -e "ssh -p PORT -i KEY -o StrictHostKeyChecking=no" SRC DEST

	sshOpts := fmt.Sprintf("ssh -p %d -i '%s' -o StrictHostKeyChecking=no", config.Port, keyPath)
	if runtime.GOOS == "windows" {
		// Windows paths for rsync (assuming Git Bash/Cygwin style) can be tricky.
		// escape path backslashes?
		keyPath = filepath.ToSlash(keyPath)
		sshOpts = fmt.Sprintf("ssh -p %d -i \"%s\" -o StrictHostKeyChecking=no", config.Port, keyPath)
	}

	var source, dest string
	if job.Type == TransferUpload {
		// Local -> Remote
		source = job.SourcePath
		dest = fmt.Sprintf("%s@%s:%s", config.Username, config.Host, job.DestPath)
	} else {
		// Remote -> Local
		source = fmt.Sprintf("%s@%s:%s", config.Username, config.Host, job.SourcePath)
		dest = job.DestPath
	}

	if runtime.GOOS == "windows" {
		// Convert local paths for rsync (heuristic)
		if job.Type == TransferUpload {
			source = filepath.ToSlash(source)
		} else {
			dest = filepath.ToSlash(dest)
		}
	}

	// Use CommandContext to support cancellation
	cmd := exec.CommandContext(ctx, "rsync", "-az", "--info=progress2", "-e", sshOpts, source, dest)

	// Capture output?
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if it was cancelled
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("Rsync error: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("rsync failed: %s, output: %s", err, string(output))
	}

	// Update progress (full completion)
	// For rsync, we don't get granular progress easily without parsing stdout.
	// Just mark complete at end.
	progress(job.Size)
	return nil
}

func scanLinesAndCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[0:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// rsyncTransferDir handles directory transfer using rsync
func (m *SFTPDualModel) rsyncTransferDir(job TransferJob, ctx context.Context) error {
	config := m.sshClient.GetConfig()

	// Handle key
	keyPath := config.PrivateKey
	if len(config.KeyContent) > 0 {
		tmpFile, err := os.CreateTemp("", "marix-rsync-*.pem")
		if err != nil {
			return fmt.Errorf("temp key creation failed: %w", err)
		}
		defer os.Remove(tmpFile.Name())
		defer tmpFile.Close()

		// Set secure permissions (user-only read/write)
		if err := os.Chmod(tmpFile.Name(), 0600); err != nil {
			return fmt.Errorf("failed to set temp key permissions: %w", err)
		}

		if _, err := tmpFile.Write(config.KeyContent); err != nil {
			return fmt.Errorf("temp key write failed: %w", err)
		}
		tmpFile.Close()
		keyPath = tmpFile.Name()
	}

	// Build SSH options
	sshOpts := fmt.Sprintf("ssh -p %d -i '%s' -o StrictHostKeyChecking=no", config.Port, keyPath)
	if runtime.GOOS == "windows" {
		keyPath = filepath.ToSlash(keyPath)
		sshOpts = fmt.Sprintf("ssh -p %d -i \"%s\" -o StrictHostKeyChecking=no", config.Port, keyPath)
	}

	var source, dest string
	sourcePath := job.SourcePath
	destPath := job.DestPath

	if job.Type == TransferUpload {
		// Ensure local path ends with separator for directory sync
		if !strings.HasSuffix(sourcePath, "/") && !strings.HasSuffix(sourcePath, "\\") {
			sourcePath += string(filepath.Separator)
		}
		source = sourcePath
		dest = fmt.Sprintf("%s@%s:%s", config.Username, config.Host, destPath)
		if runtime.GOOS == "windows" {
			source = filepath.ToSlash(source)
		}
	} else {
		// Download: Ensure remote path ends with /
		if !strings.HasSuffix(sourcePath, "/") {
			sourcePath += "/"
		}
		source = fmt.Sprintf("%s@%s:%s", config.Username, config.Host, sourcePath)
		dest = destPath
		if runtime.GOOS == "windows" {
			dest = filepath.ToSlash(dest)
		}
		// Create local directory
		os.MkdirAll(dest, 0755)
	}

	// Use CommandContext to support cancellation
	cmd := exec.CommandContext(ctx, "rsync", "-avz", "--info=progress2", "-e", sshOpts, source, dest)

	// Create pipes to capture output
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	log.Printf("Running rsync directory transfer: %s -> %s", source, dest)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start rsync: %w", err)
	}

	// Read and parse progress output in real-time
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Split(scanLinesAndCR)
		lastUpdate := time.Now()

		for scanner.Scan() {
			line := scanner.Text()

			// Store last 10 lines for display
			m.mu.Lock()
			m.rsyncOutputLines = append(m.rsyncOutputLines, line)
			if len(m.rsyncOutputLines) > 10 {
				m.rsyncOutputLines = m.rsyncOutputLines[len(m.rsyncOutputLines)-10:]
			}
			m.mu.Unlock()

			if strings.Contains(line, "/s") {
				fields := strings.Fields(line)
				var speed float64
				var percentVal int

				for _, field := range fields {
					// Parse speed (e.g., "69.68MB/s")
					if strings.HasSuffix(field, "/s") {
						speedStr := strings.TrimSuffix(field, "/s")
						if s := parseRsyncSpeed(speedStr); s > 0 {
							speed = s
						}
					}

					// Parse byte percentage (e.g., "49%") for large file transfers
					if strings.HasSuffix(field, "%") {
						percentStr := strings.TrimSuffix(field, "%")
						if p, err := strconv.Atoi(percentStr); err == nil && p > 0 {
							percentVal = p
						}
					}

					// Parse file counter from ir-chk=remaining/total
					// ir-chk=1625/1649 means 1649-1625=24 files done out of 1649 total
					if strings.HasPrefix(field, "ir-chk=") {
						parts := strings.TrimPrefix(field, "ir-chk=")
						parts = strings.TrimSuffix(parts, ")")
						counts := strings.Split(parts, "/")
						if len(counts) == 2 {
							if remaining, err := strconv.Atoi(counts[0]); err == nil {
								if total, err := strconv.Atoi(counts[1]); err == nil {
									if total > 0 {
										filesCompleted := total - remaining
										percentVal = (filesCompleted * 100) / total
									}
								}
							}
						}
					}
				}

				// Throttle updates - only update every 100ms to avoid UI spam
				now := time.Now()
				if speed > 0 && now.Sub(lastUpdate) >= 100*time.Millisecond {
					lastUpdate = now

					m.transferQueue.mu <- true
					m.transferQueue.currentSpeed = speed
					if percentVal > 0 {
						m.transferQueue.currentPercent = percentVal
					}
					<-m.transferQueue.mu

					// Send status update
					m.notifyTransferUpdate(nil, "")
				}
			}
		}
	}()

	// Capture stderr for error messages
	stderrBytes, _ := io.ReadAll(stderr)

	// Wait for command to complete
	err = cmd.Wait()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("Rsync directory error: %v\nStderr: %s", err, string(stderrBytes))
		return fmt.Errorf("rsync failed: %v", err)
	}

	return nil
}

// parseRsyncSpeed converts rsync speed string (e.g., "123.45MB", "1.23GB") to bytes/sec
func parseRsyncSpeed(speedStr string) float64 {
	speedStr = strings.TrimSpace(speedStr)
	var multiplier float64 = 1

	// Remove unit suffix and determine multiplier
	if strings.HasSuffix(speedStr, "GB") {
		multiplier = 1024 * 1024 * 1024
		speedStr = strings.TrimSuffix(speedStr, "GB")
	} else if strings.HasSuffix(speedStr, "MB") {
		multiplier = 1024 * 1024
		speedStr = strings.TrimSuffix(speedStr, "MB")
	} else if strings.HasSuffix(speedStr, "KB") {
		multiplier = 1024
		speedStr = strings.TrimSuffix(speedStr, "KB")
	} else if strings.HasSuffix(speedStr, "kB") {
		multiplier = 1000
		speedStr = strings.TrimSuffix(speedStr, "kB")
	} else if strings.HasSuffix(speedStr, "B") {
		multiplier = 1
		speedStr = strings.TrimSuffix(speedStr, "B")
	}

	// Parse the numeric part
	speed, err := strconv.ParseFloat(strings.ReplaceAll(speedStr, ",", ""), 64)
	if err != nil {
		return 0
	}

	return speed * multiplier
}

func (m *SFTPDualModel) notifyTransferUpdate(err error, file string) {
	// Calculate rudimentary speed
	// In a real implementation we'd use time-windowed average
	m.transferUpdate <- TransferStatusMsg{
		Total:     m.transferQueue.total,
		Pending:   m.transferQueue.pending,
		Active:    m.transferQueue.active,
		Completed: m.transferQueue.completed,
		Failed:    m.transferQueue.failed,
		Last:      file,
		Err:       err,
	}
}

// waitForTransferUpdate is a command that waits for updates from workers
func (m *SFTPDualModel) waitForTransferUpdate() tea.Msg {
	return <-m.transferUpdate
}

func (m *SFTPDualModel) Init() tea.Cmd {
	return m.waitForTransferUpdate
}

func (m *SFTPDualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Handle confirmation dialog (download or delete)
		if m.confirmingDownload {
			switch msg.String() {
			case "y", "Y":
				// Confirm download
				m.confirmingDownload = false
				file := m.pendingFile
				remotePath := path.Join(m.remotePath, file.Name)
				localPath := filepath.Join(m.localPath, file.Name)

				// Queue download
				return m, func() tea.Msg {
					m.queueJob(TransferJob{
						Type:       TransferDownload,
						SourcePath: remotePath,
						DestPath:   localPath,
						FileName:   file.Name,
						Size:       file.Size,
						Serial:     m.serial,
					})
					return nil
				}
			case "n", "N", "esc":
				// Cancel
				m.confirmingDownload = false
				m.pendingFile = nil
				return m, nil
			}
			return m, nil
		}

		if m.confirmingDelete {
			switch msg.String() {
			case "y", "Y":
				m.confirmingDelete = false
				return m, m.deletePendingItem()
			case "n", "N", "esc":
				m.confirmingDelete = false
				m.pendingFile = nil
				return m, nil
			}
			return m, nil
		}

		// Handle input if creating folder
		if m.creatingFolder {
			switch msg.String() {
			case "enter":
				name := m.input.Value()
				if name != "" {
					return m, m.createFolder(name)
				}
				m.creatingFolder = false
				m.input.Blur()
				return m, nil
			case "esc":
				m.creatingFolder = false
				m.input.Blur()
				m.input.Reset()
				return m, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "tab":
			// Switch active pane
			if m.activePane == LocalPane {
				m.activePane = RemotePane
			} else {
				m.activePane = LocalPane
			}

		case "n":
			// Start folder creation
			m.creatingFolder = true
			m.input.Reset()
			m.input.Focus()
			return m, textinput.Blink

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

		case "down", "j":
			if m.activePane == LocalPane {
				if m.localCursor < len(m.localFiles)-1 {
					m.localCursor++
				}
			} else {
				if m.remoteCursor < len(m.remoteFiles)-1 {
					m.remoteCursor++
				}
			}

		case "enter":
			// Navigate into directory or transfer file
			if m.activePane == LocalPane {
				return m, m.handleLocalEnter()
			} else {
				return m, m.handleRemoteEnter()
			}

		case "d":
			// Download: remote to local
			if m.activePane == RemotePane && len(m.remoteFiles) > 0 {
				ctx := m.ctxMap[m.serial]
				return m, m.downloadFile(ctx)
			}

		case "u":
			// Upload: local to remote
			if m.activePane == LocalPane && len(m.localFiles) > 0 {
				ctx := m.ctxMap[m.serial]
				return m, m.uploadFile(ctx)
			}

		case "delete", "x":
			// Delete selected item
			return m, m.confirmDelete()

		case "r":
			// Refresh both panes
			m.loadLocalDirectory()
			m.loadRemoteDirectory()
			m.refreshStatus = "✓ Refreshed"
			m.refreshStatusTime = time.Now()

		case "C":
			// Cancel all transfers
			m.cancelAllTransfers()
			m.statusMsg = "Cancellation requested..."
			m.loadLocalDirectory()
			m.loadRemoteDirectory()
		}

	case TransferStatusMsg:
		if msg.BytesSec > 0 {
			m.transferQueue.currentSpeed = msg.BytesSec
		}

		if msg.Total > 0 {
			// standard update
		}

		// m.statusMsg = fmt.Sprintf("Queue: %d pending, %d active, %d done", msg.Pending, msg.Active, msg.Completed)
		if msg.Err != nil {
			m.err = fmt.Errorf("Transfer error (%s): %v", msg.Last, msg.Err)
		} else if msg.Last != "" {
			// Auto refresh on completion
			if m.transferQueue.active == 0 && m.transferQueue.pending == 0 {
				m.statusMsg = "Transfer queue completed!"
				m.transferQueue.currentSpeed = 0
				m.transferQueue.currentPercent = 0 // Reset percentage
				m.loadLocalDirectory()
				m.loadRemoteDirectory()
			}
		}
		// Continue waiting for updates
		return m, m.waitForTransferUpdate
	}

	return m, nil
}

func (m *SFTPDualModel) handleLocalEnter() tea.Cmd {
	if len(m.localFiles) == 0 || m.localCursor >= len(m.localFiles) {
		return nil
	}

	file := m.localFiles[m.localCursor]
	path := filepath.Join(m.localPath, file.Name)

	if file.IsDir {
		// Change directory
		m.localPath = path
		m.localCursor = 0
		m.loadLocalDirectory()
		return nil
	}

	// Open file in editor
	return m.openInEditor(path)
}

func (m *SFTPDualModel) handleRemoteEnter() tea.Cmd {
	if len(m.remoteFiles) == 0 || m.remoteCursor >= len(m.remoteFiles) {
		return nil
	}

	file := m.remoteFiles[m.remoteCursor]
	path := path.Join(m.remotePath, file.Name)

	if file.IsDir {
		// Change directory
		m.remotePath = path
		m.remoteCursor = 0
		m.loadRemoteDirectory()
		return nil
	}

	// Check file size
	if file.Size > MaxEditSize {
		// Prompt to download
		m.confirmingDownload = true
		m.pendingFile = &file
		return nil
	}

	// For smaller files, download to temp and open
	return func() tea.Msg {
		tempDir := os.TempDir()
		tempPath := filepath.Join(tempDir, file.Name)

		err := m.sftpClient.Download(path, tempPath, nil)
		if err != nil {
			return TransferStatusMsg{Err: fmt.Errorf("failed to download temp file: %w", err), Last: file.Name}
		}

		// Open in editor
		return m.openInEditor(tempPath)()
	}
}

// openInEditor returns a command that opens the file in $EDITOR
func (m *SFTPDualModel) openInEditor(path string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nano" // Fallback
	}

	c := exec.Command(editor, path)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return TransferStatusMsg{Err: fmt.Errorf("failed to open editor: %w", err), Last: filepath.Base(path)}
		}
		return nil
	})
}

func (m *SFTPDualModel) downloadFile(ctx context.Context) tea.Cmd {
	if m.remoteCursor >= len(m.remoteFiles) || m.remoteFiles[m.remoteCursor].Name == ".." {
		return nil
	}

	file := m.remoteFiles[m.remoteCursor]
	remotePath := path.Join(m.remotePath, file.Name)
	localPath := filepath.Join(m.localPath, file.Name)

	// Reset queue stats for new batch if empty
	if m.transferQueue.pending == 0 && m.transferQueue.active == 0 {
		m.transferQueue.total = 0
		m.transferQueue.completed = 0
		m.transferQueue.failed = 0
	}

	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if file.IsDir {
			m.queueDownloadDir(ctx, remotePath, localPath)
		} else {

			m.queueJob(TransferJob{
				Type:       TransferDownload,
				SourcePath: remotePath,
				DestPath:   localPath,
				FileName:   file.Name,
				Size:       file.Size,
				Serial:     m.serial,
			})
		}
		return nil
	}
}

func (m *SFTPDualModel) createFolder(name string) tea.Cmd {
	m.creatingFolder = false
	m.input.Blur()
	m.input.Reset()

	return func() tea.Msg {
		var err error
		if m.activePane == LocalPane {
			path := filepath.Join(m.localPath, name)
			err = os.Mkdir(path, 0755)
			if err == nil {
				m.loadLocalDirectory()
				m.statusMsg = fmt.Sprintf("Created local folder: %s", name)
			}
		} else {
			path := path.Join(m.remotePath, name)
			err = m.sftpClient.Mkdir(path)
			if err == nil {
				m.loadRemoteDirectory()
				m.statusMsg = fmt.Sprintf("Created remote folder: %s", name)
			}
		}

		if err != nil {
			m.err = fmt.Errorf("failed to create folder: %w", err)
			return nil
		}
		return nil
	}
}

func (m *SFTPDualModel) confirmDelete() tea.Cmd {
	var file *sftp.FileInfo

	if m.activePane == LocalPane {
		if m.localCursor >= len(m.localFiles) || m.localFiles[m.localCursor].Name == ".." {
			return nil
		}
		f := m.localFiles[m.localCursor]
		// Convert to standard FileInfo
		file = &sftp.FileInfo{Name: f.Name, IsDir: f.IsDir, Size: f.Size}
	} else {
		if m.remoteCursor >= len(m.remoteFiles) || m.remoteFiles[m.remoteCursor].Name == ".." {
			return nil
		}
		f := m.remoteFiles[m.remoteCursor]
		file = &f
	}

	m.pendingFile = file
	m.confirmingDelete = true
	return nil
}

func (m *SFTPDualModel) deletePendingItem() tea.Cmd {
	return func() tea.Msg {
		name := m.pendingFile.Name
		var err error
		var fullPath string

		if m.activePane == LocalPane {
			fullPath = filepath.Join(m.localPath, name)
			// Recursive local delete
			err = os.RemoveAll(fullPath)
			m.loadLocalDirectory()
		} else {
			fullPath = path.Join(m.remotePath, name)
			// Recursive remote delete
			if m.pendingFile.IsDir {
				err = m.sftpClient.RemoveDirectory(fullPath)
			} else {
				err = m.sftpClient.Delete(fullPath)
			}
			m.loadRemoteDirectory()
		}

		m.pendingFile = nil

		if err != nil {
			log.Printf("Failed to delete item: %s. Error: %v", fullPath, err)
			return TransferStatusMsg{Err: fmt.Errorf("failed to delete %s: %w", name, err)}
		} else {
			log.Printf("Successfully deleted item: %s", fullPath)
			return TransferStatusMsg{Last: fmt.Sprintf("Deleted %s", name)} // Reuse message for generic status
		}
	}
}

func (m *SFTPDualModel) queueDownloadDir(ctx context.Context, remotePath, localPath string) {
	// Check for rsync availability
	if _, err := exec.LookPath("rsync"); err == nil {
		// Queue a single directory transfer job
		m.queueJob(TransferJob{
			Type:        TransferDownload,
			SourcePath:  remotePath,
			DestPath:    localPath,
			FileName:    filepath.Base(localPath),
			Size:        0, // Size unknown for directories
			Serial:      m.serial,
			IsDirectory: true,
		})
		return
	}

	// Fallback to individual file queueing if rsync is not available
	// Create local dir
	os.MkdirAll(localPath, 0755)

	files, err := m.sftpClient.List(remotePath)
	if err != nil {
		m.err = err
		return
	}

	for _, f := range files {
		select {
		case <-ctx.Done():
			return
		default:
		}
		rPath := path.Join(remotePath, f.Name)
		lPath := filepath.Join(localPath, f.Name)
		if f.IsDir {
			m.queueDownloadDir(ctx, rPath, lPath)
		} else {
			// Skip non-regular files on download too (e.g. remote symlinks)
			if !f.Mode.IsRegular() {
				log.Printf("Skipping remote non-regular file: %s (%v)", rPath, f.Mode)
				continue
			}

			m.queueJob(TransferJob{
				Type:        TransferDownload,
				SourcePath:  rPath,
				DestPath:    lPath,
				FileName:    f.Name,
				Size:        f.Size,
				Serial:      m.serial,
				IsDirectory: false,
			})
		}
	}
}

func (m *SFTPDualModel) queueJob(job TransferJob) {
	if m.serial != job.Serial {
		return
	}
	m.transferQueue.total++
	m.transferQueue.pending++
	m.transferQueue.jobs <- job
	m.notifyTransferUpdate(nil, "")
}

func (m *SFTPDualModel) uploadFile(ctx context.Context) tea.Cmd {
	if m.localCursor >= len(m.localFiles) || m.localFiles[m.localCursor].Name == ".." {
		return nil
	}

	file := m.localFiles[m.localCursor]
	localPath := filepath.Join(m.localPath, file.Name)
	remotePath := path.Join(m.remotePath, file.Name)

	// Reset queue stats for new batch
	if m.transferQueue.pending == 0 && m.transferQueue.active == 0 {
		m.transferQueue.total = 0
		m.transferQueue.completed = 0
		m.transferQueue.failed = 0
	}

	return func() tea.Msg {
		if file.IsDir {
			m.queueUploadDir(ctx, localPath, remotePath)
		} else {
			// Check if single file is regular
			// We need to Lstat to be sure, but we only have LocalFileInfo (which comes from ReadDir usually)
			// LocalFileInfo in sftp_dual.go struct definition is not standard os.FileInfo.
			// Let's rely on os.Lstat for safety here.
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			info, err := os.Lstat(localPath)
			if err == nil && !info.Mode().IsRegular() {
				log.Printf("Skipping single non-regular file: %s", localPath)
				return nil
			}

			m.queueJob(TransferJob{
				Type:       TransferUpload,
				SourcePath: localPath,
				DestPath:   remotePath,
				FileName:   file.Name,
				Size:       file.Size,
				Serial:     m.serial,
			})
		}
		return nil
	}
}

func (m *SFTPDualModel) queueUploadDir(ctx context.Context, localPath, remotePath string) {
	// Check for rsync availability
	if _, err := exec.LookPath("rsync"); err == nil {
		// Queue a single directory transfer job
		m.queueJob(TransferJob{
			Type:        TransferUpload,
			SourcePath:  localPath,
			DestPath:    remotePath,
			FileName:    filepath.Base(localPath),
			Size:        0, // Size unknown for directories
			Serial:      m.serial,
			IsDirectory: true,
		})
		return
	}

	// Fallback to individual file queueing if rsync is not available
	// Create remote dir
	m.sftpClient.Mkdir(remotePath)

	entries, err := os.ReadDir(localPath)
	if err != nil {
		m.err = err
		return
	}

	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return
		default:
		}
		lPath := filepath.Join(localPath, entry.Name())
		rPath := path.Join(remotePath, entry.Name())
		info, _ := entry.Info()

		if entry.IsDir() {
			m.queueUploadDir(ctx, lPath, rPath)
		} else {
			// Skip non-regular files (symlinks, junctions, devices) to avoid "Incorrect function" errors
			if !entry.Type().IsRegular() {
				log.Printf("Skipping non-regular file: %s (Type: %v)", lPath, entry.Type())
				continue
			}

			m.queueJob(TransferJob{
				Type:        TransferUpload,
				SourcePath:  lPath,
				DestPath:    rPath,
				FileName:    entry.Name(),
				Size:        info.Size(),
				Serial:      m.serial,
				IsDirectory: false,
			})
		}
	}
}

func (m *SFTPDualModel) View() string {
	var b strings.Builder

	// Title
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7D56F4")).
		Padding(0, 1)
	b.WriteString(titleStyle.Render("📁 SFTP File Manager"))
	b.WriteString("\n\n")

	// ===== RESPONSIVE HEIGHT CALCULATION =====
	const (
		titleLines         = 2  // Title + spacing
		helpLines          = 1  // Help text
		statusLines        = 2  // Status messages (can be 1-3 lines but reserve 2)
		outputHeaderLines  = 1  // "Rsync Output:" header
		outputContentLines = 10 // 10 lines of output
		spacing            = 2  // Extra spacing
	)

	bottomHeight := outputHeaderLines + outputContentLines
	midHeight := statusLines
	topHeight := m.height - titleLines - helpLines - midHeight - bottomHeight - spacing
	if topHeight < 15 {
		topHeight = 15 // Minimum height for panels
	}

	// ===== TOP SECTION: DUAL PANELS =====
	paneWidth := (m.width - 4) / 2 // -4 for spacing between panels
	if paneWidth < 30 {
		paneWidth = 30
	}

	// Panel styles
	activeBorderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7D56F4")).
		Padding(0, 1).
		Height(topHeight)

	inactiveBorderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#626262")).
		Padding(0, 1).
		Height(topHeight)

	// Render panels
	localPane := m.renderLocalPane(paneWidth, topHeight)
	if m.activePane == LocalPane {
		localPane = activeBorderStyle.Render(localPane)
	} else {
		localPane = inactiveBorderStyle.Render(localPane)
	}

	remotePane := m.renderRemotePane(paneWidth, topHeight)
	if m.activePane == RemotePane {
		remotePane = activeBorderStyle.Render(remotePane)
	} else {
		remotePane = inactiveBorderStyle.Render(remotePane)
	}

	// Join panels horizontally
	panes := lipgloss.JoinHorizontal(lipgloss.Top, localPane, "  ", remotePane)
	b.WriteString(panes)
	b.WriteString("\n\n")

	// ===== MIDDLE SECTION: STATUS/PROGRESS =====
	// Show refresh status if recent (within 2 seconds)
	if m.refreshStatus != "" && time.Since(m.refreshStatusTime) < 2*time.Second {
		b.WriteString(successStyle.Render(m.refreshStatus))
		b.WriteString("\n")
	}

	// Help text (always visible)
	b.WriteString(helpStyle.Render("tab: switch • enter: open • u: upload • d: download • r: refresh • C: cancel • esc: back"))

	// Status/progress (if active)
	if m.transferQueue.active > 0 || m.transferQueue.pending > 0 {
		b.WriteString("\n")
		speed := formatSpeed(m.transferQueue.currentSpeed)

		var status string
		if m.transferQueue.currentPercent > 0 {
			status = fmt.Sprintf("🚀 %d%% | %s | Active: %d | Pending: %d | Done: %d/%d",
				m.transferQueue.currentPercent,
				speed,
				m.transferQueue.active,
				m.transferQueue.pending,
				m.transferQueue.completed,
				m.transferQueue.total)
		} else {
			status = fmt.Sprintf("🚀 %s | Active: %d | Pending: %d | Done: %d/%d",
				speed,
				m.transferQueue.active,
				m.transferQueue.pending,
				m.transferQueue.completed,
				m.transferQueue.total)
		}
		b.WriteString(successStyle.Render(status))
	} else if m.statusMsg != "" {
		b.WriteString("\n")
		b.WriteString(successStyle.Render("✓ " + m.statusMsg))
	}

	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", m.err)))
	}

	b.WriteString("\n")

	// ===== BOTTOM SECTION: COMMAND OUTPUT =====
	outputHeaderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#666")).
		Bold(true)
	b.WriteString(outputHeaderStyle.Render("Command Output:"))

	m.mu.Lock()
	lineCount := len(m.rsyncOutputLines)
	for i := 0; i < 10; i++ {
		b.WriteString("\n")
		if i < lineCount {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#888")).Render(m.rsyncOutputLines[i]))
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#333")).Render("—"))
		}
	}
	m.mu.Unlock()

	// ===== POPUPS (overlays) =====
	// Input popup
	if m.creatingFolder {
		popupStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7D56F4")).
			Padding(1, 2).
			Width(50)

		inputView := popupStyle.Render(
			fmt.Sprintf("Create New Folder\n\n%s", m.input.View()),
		)
		b.WriteString("\n\n")
		b.WriteString(inputView)
	}

	// Download confirmation
	if m.confirmingDownload {
		popupStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#FFA500")).
			Padding(1, 2).
			Width(60)

		msg := fmt.Sprintf("⚠️  File is too large to edit directly (>10MB).\n\nDownload '%s' to local folder instead?\n\n(y/n)", m.pendingFile.Name)
		popupView := popupStyle.Render(msg)
		b.WriteString("\n\n")
		b.WriteString(popupView)
	}

	// Delete confirmation
	if m.confirmingDelete {
		popupStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#FF0000")).
			Padding(1, 2).
			Width(60)

		msg := fmt.Sprintf("🗑️  Are you sure you want to PERMANENTLY delete:\n\n'%s'\n\n(y/n)", m.pendingFile.Name)
		popupView := popupStyle.Render(msg)
		b.WriteString("\n\n")
		b.WriteString(popupView)
	}

	return b.String()
}

func formatSpeed(bytesSec float64) string {
	if bytesSec < 1024 {
		return fmt.Sprintf("%.0f B/s", bytesSec)
	} else if bytesSec < 1024*1024 {
		return fmt.Sprintf("%.1f KB/s", bytesSec/1024)
	} else {
		return fmt.Sprintf("%.1f MB/s", bytesSec/(1024*1024))
	}
}

func (m *SFTPDualModel) renderLocalPane(width, height int) string {
	var b strings.Builder

	// Pane title
	paneTitle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#04B575"))
	b.WriteString(paneTitle.Render("💻 Local"))
	b.WriteString("\n")

	// Current path
	pathStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#626262")).
		Italic(true)
	truncatedPath := m.localPath
	if len(truncatedPath) > width-4 {
		truncatedPath = "..." + truncatedPath[len(truncatedPath)-(width-7):]
	}
	b.WriteString(pathStyle.Render(truncatedPath))
	b.WriteString("\n\n")

	// Files - calculate display count based on provided height
	// Overhead: Title(1) + Path(1) + Spacing(1) = 3 lines
	displayCount := height - 3
	if displayCount < 5 {
		displayCount = 5
	}

	startIdx := 0
	if m.localCursor > displayCount/2 && len(m.localFiles) > displayCount {
		startIdx = m.localCursor - displayCount/2
	}
	endIdx := startIdx + displayCount
	if endIdx > len(m.localFiles) {
		endIdx = len(m.localFiles)
	}

	for i := startIdx; i < endIdx; i++ {
		file := m.localFiles[i]
		cursor := "  "
		style := itemStyle

		if m.localCursor == i {
			cursor = "→ "
			style = selectedItemStyle
		}

		icon := "📄"
		if file.IsDir {
			icon = "📁"
		}

		name := file.Name
		if len(name) > width-15 {
			name = name[:width-18] + "..."
		}

		line := fmt.Sprintf("%s %s", icon, name)
		b.WriteString(cursor + style.Render(line))
		b.WriteString("\n")
	}

	return b.String()
}

func (m *SFTPDualModel) renderRemotePane(width, height int) string {
	var b strings.Builder

	// Pane title
	paneTitle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFA500"))
	b.WriteString(paneTitle.Render("🌐 Remote"))
	b.WriteString("\n")

	// Current path
	pathStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#626262")).
		Italic(true)
	truncatedPath := m.remotePath
	if len(truncatedPath) > width-4 {
		truncatedPath = "..." + truncatedPath[len(truncatedPath)-(width-7):]
	}
	b.WriteString(pathStyle.Render(truncatedPath))
	b.WriteString("\n\n")

	// Files - calculate display count based on provided height
	// Overhead: Title(1) + Path(1) + Spacing(1) = 3 lines
	displayCount := height - 3
	if displayCount < 5 {
		displayCount = 5
	}

	startIdx := 0
	if m.remoteCursor > displayCount/2 && len(m.remoteFiles) > displayCount {
		startIdx = m.remoteCursor - displayCount/2
	}
	endIdx := startIdx + displayCount
	if endIdx > len(m.remoteFiles) {
		endIdx = len(m.remoteFiles)
	}

	for i := startIdx; i < endIdx; i++ {
		file := m.remoteFiles[i]
		cursor := "  "
		style := itemStyle

		if m.remoteCursor == i {
			cursor = "→ "
			style = selectedItemStyle
		}

		icon := "📄"
		if file.IsDir {
			icon = "📁"
		}

		name := file.Name
		if len(name) > width-15 {
			name = name[:width-18] + "..."
		}

		line := fmt.Sprintf("%s %s", icon, name)
		b.WriteString(cursor + style.Render(line))
		b.WriteString("\n")
	}

	return b.String()
}
