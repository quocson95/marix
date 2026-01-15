package s3

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/quocson95/marix/pkg/backup"
)

const (
	BucketName = "matrixdb"
)

// Client handles S3 operations
type Client struct {
	s3Client      *s3.Client
	presignClient *s3.PresignClient
	bucket        string
}

// NewClient creates a new S3 client
func NewClient(host, accessKey, secretKey string) (*Client, error) {
	if host == "" || accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("missing S3 configuration")
	}

	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
		config.WithRegion("us-east-1"), // Default region
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(host)
		o.UsePathStyle = true // Force path-style for better compatibility with S3 implementations (MinIO, etc)
	})
	presignClient := s3.NewPresignClient(client)

	return &Client{
		s3Client:      client,
		presignClient: presignClient,
		bucket:        BucketName,
	}, nil
}

// EnsureBucket checks if bucket exists, creates if not
func (c *Client) EnsureBucket(ctx context.Context) error {
	_, err := c.s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err == nil {
		return nil // Bucket exists
	}

	// Assume 404 or similar means missing, try creating
	// Note: checking error type strictly is better but basic check works for many S3-compat
	_, err = c.s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err != nil {
		// Check for 409 Conflict (BucketAlreadyOwnedByYou or BucketAlreadyExists)
		// We treat it as success if we can access it (which subsequent ops will determine),
		// essentially making CreateBucket idempotent.
		if strings.Contains(err.Error(), "StatusCode: 409") ||
			strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") {
			return nil
		}
		return fmt.Errorf("failed to create bucket %s: %w", c.bucket, err)
	}

	return nil
}

// Backup zips the data directory, encrypts it, and uploads to S3
func (c *Client) Backup(dataDir, password string) error {
	ctx := context.TODO()

	// 1. Ensure bucket
	if err := c.EnsureBucket(ctx); err != nil {
		return err
	}

	// 2. Create Zip
	timestamp := time.Now().Format("20060102-150405")
	fileName := fmt.Sprintf("backup-%s.enc", timestamp) // .enc extension for encrypted
	tempZip := filepath.Join(os.TempDir(), "backup-temp.zip")

	if err := zipDirectory(tempZip, dataDir); err != nil {
		return fmt.Errorf("failed to zip directory: %w", err)
	}
	defer os.Remove(tempZip)

	// 3. Read zip file
	zipData, err := os.ReadFile(tempZip)
	if err != nil {
		return fmt.Errorf("failed to read zip: %w", err)
	}

	// 4. Encrypt the zip data
	encryptedBackup, err := backup.Encrypt(zipData, password)
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	// 5. Marshal encrypted backup to JSON
	backupJSON, err := json.Marshal(encryptedBackup)
	if err != nil {
		return fmt.Errorf("failed to marshal backup: %w", err)
	}

	// 6. Generate Presigned PUT
	presignedReq, err := c.presignClient.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(fileName),
	}, s3.WithPresignExpires(15*time.Minute))
	if err != nil {
		return fmt.Errorf("failed to generate presigned PUT: %w", err)
	}

	// 7. Upload encrypted backup using HTTP PUT
	req, err := http.NewRequest("PUT", presignedReq.URL, strings.NewReader(string(backupJSON)))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(backupJSON))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload backup: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("upload failed with status: %s", resp.Status)
	}

	return nil
}

// Restore downloads the latest encrypted backup, decrypts it, and restores
func (c *Client) Restore(dataDir, password string) error {
	ctx := context.TODO()

	// 1. List objects to find latest backup
	output, err := c.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(c.bucket),
		Prefix: aws.String("backup-"),
	})
	if err != nil {
		return fmt.Errorf("failed to list backups: %w", err)
	}

	if len(output.Contents) == 0 {
		return fmt.Errorf("no backups found")
	}

	// Sort by LastModified (descending)
	sort.Slice(output.Contents, func(i, j int) bool {
		return output.Contents[i].LastModified.After(*output.Contents[j].LastModified)
	})
	latestKey := *output.Contents[0].Key

	// 2. Generate Presigned GET
	presignedReq, err := c.presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(latestKey),
	}, s3.WithPresignExpires(15*time.Minute))
	if err != nil {
		return fmt.Errorf("failed to generate presigned GET: %w", err)
	}

	// 3. Download using HTTP GET
	resp, err := http.Get(presignedReq.URL)
	if err != nil {
		return fmt.Errorf("failed to download backup: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %s", resp.Status)
	}

	// 4. Read encrypted backup JSON
	encryptedData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read backup: %w", err)
	}

	// 5. Parse encrypted backup
	var encryptedBackup backup.BackupFile
	if err := json.Unmarshal(encryptedData, &encryptedBackup); err != nil {
		return fmt.Errorf("invalid backup format: %w", err)
	}

	// 6. Decrypt the backup
	zipData, err := backup.Decrypt(&encryptedBackup, password)
	if err != nil {
		return fmt.Errorf("decryption failed (wrong password?): %w", err)
	}

	// 7. Save decrypted zip to temp file
	tempZip := filepath.Join(os.TempDir(), "restore.zip")
	if err := os.WriteFile(tempZip, zipData, 0600); err != nil {
		return fmt.Errorf("failed to write temp zip: %w", err)
	}
	defer os.Remove(tempZip)

	// 8. Unzip and restore
	return unzipToDirectory(tempZip, dataDir)
}

// Helpers

func zipDirectory(zipPath, sourceDir string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		// Use unix-style slashes
		relPath = filepath.ToSlash(relPath)

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = relPath
		header.Method = zip.Deflate

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})
}

func unzipToDirectory(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		path := filepath.Join(destDir, f.Name)

		// Guard against Zip Slip
		if !strings.HasPrefix(path, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path: %s", path)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(path, f.Mode())
			continue
		}

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}

		outFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)

		outFile.Close()
		rc.Close()

		if err != nil {
			return err
		}
	}
	return nil
}
