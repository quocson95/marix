package sftp

import (
	"context"
	"os"
	"path/filepath"
)

// InternalEngine implements TransferEngine using the internal SFTP client
type InternalEngine struct {
	client *Client
}

// NewInternalEngine creates a new internal transfer engine
func NewInternalEngine(client *Client) *InternalEngine {
	return &InternalEngine{
		client: client,
	}
}

// UploadFile uploads a single file using SFTP
func (e *InternalEngine) UploadFile(ctx context.Context, localPath, remotePath string, progress func(int64, string) error) error {
	// Adapter for client.ProgressFunc (func(int64) error)
	return e.client.Upload(localPath, remotePath, func(bytes int64) error {
		if progress != nil {
			return progress(bytes, "")
		}
		return nil
	})
}

// DownloadFile downloads a single file using SFTP
func (e *InternalEngine) DownloadFile(ctx context.Context, remotePath, localPath string, progress func(int64, string) error) error {
	return e.client.Download(remotePath, localPath, func(bytes int64) error {
		if progress != nil {
			return progress(bytes, "")
		}
		return nil
	})
}

// ScanRemoteDirectory scans a remote directory for files
func (e *InternalEngine) ScanRemoteDirectory(ctx context.Context, path string) ([]FileJob, error) {
	var jobs []FileJob

	walker := e.client.sftpClient.Walk(path)
	for walker.Step() {
		if err := walker.Err(); err != nil {
			continue
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		path := walker.Path()
		stat := walker.Stat()

		// relPath and err were unused
		// _ = relPath

		jobs = append(jobs, FileJob{
			Path:  path,
			Size:  stat.Size(),
			IsDir: stat.IsDir(),
		})
	}

	return jobs, nil
}

// ScanLocalDirectory scans a local directory for files
func (e *InternalEngine) ScanLocalDirectory(ctx context.Context, path string) ([]FileJob, error) {
	var jobs []FileJob

	err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		jobs = append(jobs, FileJob{
			Path:  p,
			Size:  info.Size(),
			IsDir: info.IsDir(),
		})
		return nil
	})

	return jobs, err
}
