package sftp

import (
	"context"
	"log"
	"os/exec"

	"github.com/quocson95/marix/pkg/ssh"
	"github.com/quocson95/marix/pkg/storage"
)

// TransferEngine abstracts the file transfer mechanism
type TransferEngine interface {
	// UploadFile uploads a single file
	UploadFile(ctx context.Context, localPath, remotePath string, progress func(int64, string) error) error

	// DownloadFile downloads a single file
	DownloadFile(ctx context.Context, remotePath, localPath string, progress func(int64, string) error) error

	// ScanRemoteDirectory recursively scans a remote directory
	ScanRemoteDirectory(ctx context.Context, path string) ([]FileJob, error)

	// ScanLocalDirectory recursively scans a local directory
	ScanLocalDirectory(ctx context.Context, path string) ([]FileJob, error)
}

// NewTransferEngine creates the appropriate engine based on settings and availability
func NewTransferEngine(client *Client, sshConfig *ssh.SSHConfig, settings *storage.Settings) TransferEngine {
	// Check if rsync is enabled AND available
	rsyncPath, err := exec.LookPath("rsync")
	if !settings.DisableRsync {
		if err == nil {
			log.Printf("[INFO] Selecting Rsync Engine (Path: %s)", rsyncPath)
			// Rsync is available, use it
			return NewRsyncEngine(client, sshConfig)
		} else {
			log.Printf("[WARN] Rsync enabled but not found in PATH. Falling back to Internal Engine.")
		}
	} else {
		log.Printf("[INFO] Rsync disabled in settings. Using Internal Engine.")
	}

	// Fallback to internal SFTP engine
	return NewInternalEngine(client)
}
