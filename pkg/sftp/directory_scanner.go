package sftp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// FileJob represents a single file/directory operation
type FileJob struct {
	Path        string // relative path from root of transfer
	AbsPath     string // absolute source path
	DestPath    string // absolute destination path
	Size        int64
	IsDir       bool
	IsRecursive bool // if true, scanner will explode this
}

// DirectoryScanner handles scanning directories to create transfer jobs
type DirectoryScanner struct {
	client *Client
}

// NewDirectoryScanner creates a new scanner
func NewDirectoryScanner(client *Client) *DirectoryScanner {
	return &DirectoryScanner{
		client: client,
	}
}

// ScanLocal scans a local directory and returns a list of jobs
func (s *DirectoryScanner) ScanLocal(ctx context.Context, root string, remoteRoot string, onProgress func(int)) ([]FileJob, int64, error) {
	var jobs []FileJob
	var totalSize int64

	// Ensure root exists
	info, err := os.Stat(root)
	if err != nil {
		return nil, 0, err
	}

	baseDir := filepath.Dir(root)
	if info.IsDir() && !strings.HasSuffix(root, string(filepath.Separator)) {
		// If uploading "folder", we want "folder" to be created in remote.
		// So base is the parent of root.
	}

	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		relPath, _ := filepath.Rel(baseDir, path)
		destPath := filepath.Join(remoteRoot, relPath)

		// Check for symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			// It's a symlink. Check what it points to.
			realPath, err := filepath.EvalSymlinks(path)
			if err != nil {
				// Broken link, skip
				return nil
			}
			realInfo, err := os.Stat(realPath)
			if err != nil {
				return nil
			}
			if realInfo.IsDir() {
				// Symlink to directory.
				// filepath.Walk does not follow it, so we won't see contents.
				// Creating it as a file fails (read error).
				// Creating it as a dir creates an empty dir on remote (not useful).
				// Best to skip for Internal Engine.
				return nil
			}
			// Symlink to file: treated as file, os.Open follows it, data copied. OK.
		}

		// For rsync-like behavior:
		// If "Upload /local/foo to /remote/bar", and foo is dir:
		// We want /remote/bar/foo to exist.

		jobs = append(jobs, FileJob{
			Path:     relPath,
			AbsPath:  path,
			DestPath: destPath,
			Size:     info.Size(),
			IsDir:    info.IsDir(),
		})

		if !info.IsDir() {
			totalSize += info.Size()
		}

		if onProgress != nil && len(jobs)%100 == 0 {
			onProgress(len(jobs))
		}

		return nil
	})

	return jobs, totalSize, err
}

// ScanRemote scans a remote directory and returns a list of jobs
func (s *DirectoryScanner) ScanRemote(ctx context.Context, root string, localRoot string, onProgress func(int)) ([]FileJob, int64, error) {
	var jobs []FileJob
	var totalSize int64

	baseDir := filepath.Dir(root)

	walker := s.client.sftpClient.Walk(root)
	for walker.Step() {
		if err := walker.Err(); err != nil {
			continue // skip errors?
		}
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}

		path := walker.Path()
		stat := walker.Stat()

		relPath, _ := filepath.Rel(baseDir, path)
		destPath := filepath.Join(localRoot, relPath)

		jobs = append(jobs, FileJob{
			Path:     relPath,
			AbsPath:  path,
			DestPath: destPath,
			Size:     stat.Size(),
			IsDir:    stat.IsDir(),
		})

		if !stat.IsDir() {
			totalSize += stat.Size()
		}

		if onProgress != nil && len(jobs)%100 == 0 {
			onProgress(len(jobs))
		}
	}

	return jobs, totalSize, nil
}
