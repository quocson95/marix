package sftp

import (
	"fmt"
	"io"
	"os"
	pus "path"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Client wraps SFTP client functionality
type Client struct {
	sshClient  *ssh.Client
	sftpClient *sftp.Client
}

// FileInfo represents a file/directory info
type FileInfo struct {
	Name    string
	Size    int64
	Mode    os.FileMode
	ModTime int64
	IsDir   bool
}

// NewClient creates a new SFTP client from an existing SSH connection
func NewClient(sshClient *ssh.Client) (*Client, error) {
	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create SFTP client: %w", err)
	}

	return &Client{
		sshClient:  sshClient,
		sftpClient: sftpClient,
	}, nil
}

// List lists files in a directory
func (c *Client) List(path string) ([]FileInfo, error) {
	entries, err := c.sftpClient.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}

	files := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		files = append(files, FileInfo{
			Name:    entry.Name(),
			Size:    entry.Size(),
			Mode:    entry.Mode(),
			ModTime: entry.ModTime().Unix(),
			IsDir:   entry.IsDir(),
		})
	}

	return files, nil
}

// ProgressFunc is a callback for tracking transfer progress
type ProgressFunc func(bytes int64) error

// ... (Download and Upload use this type)

// Download downloads a file from remote to local
func (c *Client) Download(remotePath, localPath string, onProgress ProgressFunc) error {
	// Open remote file
	remoteFile, err := c.sftpClient.Open(remotePath)
	if err != nil {
		return fmt.Errorf("failed to open remote file: %w", err)
	}
	defer remoteFile.Close()

	// Create local file
	localFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer localFile.Close()

	// Copy data with progress
	if onProgress != nil {
		_, err = io.Copy(localFile, &progressReader{r: remoteFile, onProgress: onProgress})
	} else {
		_, err = io.Copy(localFile, remoteFile)
	}

	if err != nil {
		return fmt.Errorf("failed to copy data: %w", err)
	}

	return nil
}

// Upload uploads a file from local to remote
func (c *Client) Upload(localPath, remotePath string, onProgress ProgressFunc) error {
	// Open local file
	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer localFile.Close()

	// Create remote file
	remoteFile, err := c.sftpClient.Create(remotePath)
	if err != nil {
		return fmt.Errorf("failed to create remote file: %w", err)
	}
	defer remoteFile.Close()

	// Copy data with progress
	if onProgress != nil {
		_, err = io.Copy(remoteFile, &progressReader{r: localFile, onProgress: onProgress})
	} else {
		_, err = io.Copy(remoteFile, localFile)
	}

	if err != nil {
		return fmt.Errorf("failed to copy data: %w", err)
	}

	return nil
}

// progressReader wraps an io.Reader to track progress
type progressReader struct {
	r          io.Reader
	onProgress ProgressFunc
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 && pr.onProgress != nil {
		if progressErr := pr.onProgress(int64(n)); progressErr != nil {
			return n, progressErr
		}
	}
	return n, err
}

// Delete deletes a file
func (c *Client) Delete(path string) error {
	return c.sftpClient.Remove(path)
}

// RemoveDirectory removes a directory recursively
func (c *Client) RemoveDirectory(path string) error {
	// Try using rm -rf via SSH execution first for performance and robustness
	session, err := c.sshClient.NewSession()
	if err == nil {
		defer session.Close()
		// Use quotes to handle spaces in path. -r for recursive, -f for force (ignore non-existent)
		cmd := fmt.Sprintf("rm -rf %q", path)
		if err := session.Run(cmd); err == nil {
			return nil
		}
		// If SSH command failed, fall back to SFTP recursive delete
	}

	// List files
	files, err := c.List(path)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return err
	}

	for _, file := range files {
		filePath := pus.Join(path, file.Name)
		if file.IsDir {
			if err := c.RemoveDirectory(filePath); err != nil && !isNotExist(err) {
				return err
			}
		} else {
			if err := c.Delete(filePath); err != nil && !isNotExist(err) {
				return err
			}
		}
	}

	// Remove empty directory
	if err := c.sftpClient.RemoveDirectory(path); err != nil && !isNotExist(err) {
		return err
	}
	return nil
}

func isNotExist(err error) bool {
	if os.IsNotExist(err) {
		return true
	}
	// Fallback for SFTP errors that might not unwrap correctly
	return strings.Contains(strings.ToLower(err.Error()), "does not exist")
}

// Mkdir creates a directory
func (c *Client) Mkdir(path string) error {
	return c.sftpClient.Mkdir(path)
}

func (c *Client) Rmdir(path string) error {
	return c.sftpClient.RemoveDirectory(path)
}

// ReadFile reads a file's content
func (c *Client) ReadFile(path string) ([]byte, error) {
	file, err := c.sftpClient.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(file)
}

// WriteFile writes content to a file
func (c *Client) WriteFile(path string, data []byte) error {
	file, err := c.sftpClient.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(data)
	return err
}

// Stat gets file info
func (c *Client) Stat(path string) (*FileInfo, error) {
	info, err := c.sftpClient.Stat(path)
	if err != nil {
		return nil, err
	}

	return &FileInfo{
		Name:    info.Name(),
		Size:    info.Size(),
		Mode:    info.Mode(),
		ModTime: info.ModTime().Unix(),
		IsDir:   info.IsDir(),
	}, nil
}

// Chmod changes file permissions
func (c *Client) Chmod(path string, mode os.FileMode) error {
	return c.sftpClient.Chmod(path, mode)
}

// Rename renames a file
func (c *Client) Rename(oldPath, newPath string) error {
	return c.sftpClient.Rename(oldPath, newPath)
}

// GetWorkingDirectory gets current working directory
func (c *Client) GetWorkingDirectory() (string, error) {
	return c.sftpClient.Getwd()
}

// Close closes the SFTP connection
func (c *Client) Close() error {
	return c.sftpClient.Close()
}

// DownloadDirectory downloads a directory recursively
func (c *Client) DownloadDirectory(remotePath, localPath string) error {
	// Create local directory
	if err := os.MkdirAll(localPath, 0755); err != nil {
		return err
	}

	// List remote directory
	files, err := c.List(remotePath)
	if err != nil {
		return err
	}

	for _, file := range files {
		remoteFilePath := pus.Join(remotePath, file.Name)
		localFilePath := filepath.Join(localPath, file.Name)

		if file.IsDir {
			// Recursively download subdirectory
			if err := c.DownloadDirectory(remoteFilePath, localFilePath); err != nil {
				return err
			}
		} else {
			// Download file
			if err := c.Download(remoteFilePath, localFilePath, nil); err != nil {
				return err
			}
		}
	}

	return nil
}

// UploadDirectory uploads a directory recursively
func (c *Client) UploadDirectory(localPath, remotePath string) error {
	// Create remote directory
	if err := c.Mkdir(remotePath); err != nil {
		// Ignore error if directory already exists
		if !os.IsExist(err) {
			return err
		}
	}

	// List local directory
	entries, err := os.ReadDir(localPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		localFilePath := filepath.Join(localPath, entry.Name())
		remoteFilePath := pus.Join(remotePath, entry.Name())

		if entry.IsDir() {
			// Recursively upload subdirectory
			if err := c.UploadDirectory(localFilePath, remoteFilePath); err != nil {
				return err
			}
		} else {
			// Upload file
			if err := c.Upload(localFilePath, remoteFilePath, nil); err != nil {
				return err
			}
		}
	}

	return nil
}
