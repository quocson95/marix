package sftp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/quocson95/marix/pkg/ssh"
)

// RsyncEngine implements TransferEngine using rsync
type RsyncEngine struct {
	client    *Client
	sshConfig *ssh.SSHConfig
}

// NewRsyncEngine creates a new rsync transfer engine
func NewRsyncEngine(client *Client, sshConfig *ssh.SSHConfig) *RsyncEngine {
	return &RsyncEngine{
		client:    client,
		sshConfig: sshConfig,
	}
}

// UploadFile uploads a single file using rsync
func (e *RsyncEngine) UploadFile(ctx context.Context, localPath, remotePath string, progress func(int64, string) error) error {
	// For single files, rsync overhead might be high, but we use it if selected.
	return e.runRsync(ctx, localPath, remotePath, true, progress)
}

// DownloadFile downloads a single file using rsync
func (e *RsyncEngine) DownloadFile(ctx context.Context, remotePath, localPath string, progress func(int64, string) error) error {
	return e.runRsync(ctx, remotePath, localPath, false, progress)
}

func (e *RsyncEngine) runRsync(ctx context.Context, src, dest string, upload bool, progress func(int64, string) error) error {
	keyPath := e.sshConfig.PrivateKey

	// Handle key content by writing to temp file
	if len(e.sshConfig.KeyContent) > 0 {
		tmpFile, err := os.CreateTemp("", "marix-rsync-*.pem")
		if err != nil {
			return fmt.Errorf("temp key creation failed: %w", err)
		}
		defer os.Remove(tmpFile.Name())
		defer tmpFile.Close()

		if err := os.Chmod(tmpFile.Name(), 0600); err != nil {
			return fmt.Errorf("failed to set temp key permissions: %w", err)
		}

		if _, err := tmpFile.Write(e.sshConfig.KeyContent); err != nil {
			return fmt.Errorf("temp key write failed: %w", err)
		}
		tmpFile.Close()
		keyPath = tmpFile.Name()
	}

	sshOpts := fmt.Sprintf("ssh -p %d -i '%s' -o StrictHostKeyChecking=no", e.sshConfig.Port, keyPath)
	if runtime.GOOS == "windows" {
		keyPath = filepath.ToSlash(keyPath)
		sshOpts = fmt.Sprintf("ssh -p %d -i \"%s\" -o StrictHostKeyChecking=no", e.sshConfig.Port, keyPath)
	}

	var source, destination string
	if upload {
		source = src
		destination = fmt.Sprintf("%s@%s:%s", e.sshConfig.Username, e.sshConfig.Host, dest)
		if runtime.GOOS == "windows" {
			source = filepath.ToSlash(source)
		}
	} else {
		source = fmt.Sprintf("%s@%s:%s", e.sshConfig.Username, e.sshConfig.Host, src)
		destination = dest
		if runtime.GOOS == "windows" {
			destination = filepath.ToSlash(destination)
		}
	}

	cmd := exec.CommandContext(ctx, "rsync", "-avz", "--info=progress2", "-e", sshOpts, source, destination)

	// Stream output
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// rsync often sends info to stderr too? or --info=progress2 sends to stdout?
	cmd.Stderr = cmd.Stdout // Merge stderr to stdout for simplicity

	if err := cmd.Start(); err != nil {
		return err
	}

	// Read output in goroutine
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				line := string(buf[:n])
				// Send to progress
				if progress != nil {
					progress(0, line)
				}
			}
			if err != nil {
				break
			}
		}
	}()

	return cmd.Wait()
}

// ScanRemoteDirectory - Rsync doesn't efficiently scan for us to build a job list for the internal queue.
// So for scanning, we might still fallback to SFTP or use rsync list-only mode.
// For consistency and ease, let's assume we use SFTP for scanning even if using rsync for transfer,
// OR we just use rsync recursive transfer directly (which bypasses the file queue).
//
// In our architecture, the TaskQueue decides strategy.
// If Engine is Rsync, we might bypass the FileQueue explosion and just run one big rsync command for directories.
// That was the design: TaskQueue splits, but for RsyncEngine we likely want to delegate the WHOLE directory task.
// But the Interface defines "Scan".
//
// Let's implement Scan using SFTP client since we have it, as it's more reliable for structural data than parsing rsync output.
func (e *RsyncEngine) ScanRemoteDirectory(ctx context.Context, path string) ([]FileJob, error) {
	// Re-use internal engine for scanning, as it has the SFTP client
	internal := NewInternalEngine(e.client)
	return internal.ScanRemoteDirectory(ctx, path)
}

func (e *RsyncEngine) ScanLocalDirectory(ctx context.Context, path string) ([]FileJob, error) {
	internal := NewInternalEngine(e.client)
	return internal.ScanLocalDirectory(ctx, path)
}
