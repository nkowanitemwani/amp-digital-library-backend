package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// =============================================================
// STORAGE INTERFACE
// This is the only thing the rest of the application knows about.
// The processor, book service, and handlers all receive a Storage
// and call Save/URL — they never know whether files are on disk or S3.
// Swapping implementations is a one-line change in main.go.
// =============================================================

// Storage defines the two operations the application needs from any
// file store. Every implementation must satisfy both methods.
type Storage interface {
	// Save writes data to the store under the given key.
	// key is a relative path used as the storage identifier,
	// e.g. "pdfs/abc-123.pdf" or "audio/abc-123.mp3".
	Save(ctx context.Context, key string, data []byte, contentType string) error

	// Delete removes a file from the store by its key.
	// Returns nil if the file does not exist — deletion is idempotent.
	Delete(ctx context.Context, key string) error

	// URL returns the full URL or file path for a given key.
	// For local disk this is a relative path the server can serve.
	// For S3 this is a public HTTPS URL.
	URL(key string) string
}

// =============================================================
// LOCAL DISK IMPLEMENTATION
// Used during development — files are written to a folder on disk.
// The folder is created automatically if it does not exist.
// Swap this for S3Storage in main.go when deploying.
// =============================================================

// LocalStorage writes files to a directory on the local filesystem.
// baseDir is the root folder, e.g. "./storage/files".
// baseURL is the URL prefix the HTTP server uses to serve those files,
// e.g. "http://localhost:8080/files".
type LocalStorage struct {
	baseDir string
	baseURL string
}

// NewLocalStorage creates a LocalStorage and ensures the base directory
// exists. Returns an error if the directory cannot be created.
func NewLocalStorage(baseDir, baseURL string) (*LocalStorage, error) {
	// MkdirAll is safe to call even if the directory already exists —
	// it only creates what is missing, including parent directories.
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create storage directory %q: %w", baseDir, err)
	}

	return &LocalStorage{
		baseDir: baseDir,
		baseURL: baseURL,
	}, nil
}

// Save writes data to baseDir/key, creating any subdirectories needed.
// The key is used as a relative path, so "pdfs/abc.pdf" creates
// baseDir/pdfs/abc.pdf. contentType is accepted to satisfy the interface
// but is not used by local disk — the file extension carries that info.
func (s *LocalStorage) Save(_ context.Context, key string, data []byte, _ string) error {
	fullPath := filepath.Join(s.baseDir, key)

	// Create the subdirectory (e.g. baseDir/pdfs/) if it does not exist.
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("create directory for %q: %w", key, err)
	}

	// WriteFile creates the file if it does not exist and truncates it
	// if it does — safe for repeated saves of the same key.
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return fmt.Errorf("write file %q: %w", key, err)
	}

	return nil
}

// Delete removes a file from disk. Returns nil if the file does not
// exist — os.Remove returns an error for missing files but deletion
// should be idempotent from the caller's perspective.
func (s *LocalStorage) Delete(_ context.Context, key string) error {
	fullPath := filepath.Join(s.baseDir, key)
	err := os.Remove(fullPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete file %q: %w", key, err)
	}
	return nil
}

// URL returns the full URL for a key by joining the baseURL and the key.
// e.g. key "audio/abc.mp3" → "http://localhost:8080/files/audio/abc.mp3".
func (s *LocalStorage) URL(key string) string {
	return s.baseURL + "/" + key
}

// ReadFile retrieves the raw bytes of a stored file by its key.
// Used by the processor to read back a PDF after it has been saved,
// so it can extract text and generate audio from it.
// This method is intentionally not on the Storage interface — only
// the processor needs to read files back, and only from local disk.
func (s *LocalStorage) ReadFile(key string) ([]byte, error) {
	fullPath := filepath.Join(s.baseDir, key)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("read file %q: %w", key, err)
	}
	return data, nil
}

// =============================================================
// S3 IMPLEMENTATION
// Used in production — files are stored in an AWS S3 bucket.
// The bucket must be configured for public read access if you want
// URL() to return directly accessible links without signed URLs.
// To switch: replace NewLocalStorage with NewS3Storage in main.go.
// =============================================================

// S3Storage writes files to an AWS S3 bucket.
type S3Storage struct {
	client     *s3.Client
	bucket     string
	region     string
}

// NewS3Storage creates an S3Storage using explicit AWS credentials
// from config rather than relying on environment variable magic.
// This makes the dependency clear and keeps credential handling
// in one place (config) rather than scattered across the codebase.
func NewS3Storage(ctx context.Context, region, bucket, accessKeyID, secretAccessKey string) (*S3Storage, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(
			// StaticCredentialsProvider uses the values from our config struct
			// directly — no ambient environment variables needed.
			credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	return &S3Storage{
		client: s3.NewFromConfig(cfg),
		bucket: bucket,
		region: region,
	}, nil
}

// Save uploads data to S3 under the given key.
// contentType is set on the S3 object so browsers and players handle
// the file correctly (e.g. audio/mpeg for MP3, application/pdf for PDF).
func (s *S3Storage) Save(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("s3 upload %q: %w", key, err)
	}

	return nil
}

// Delete removes an object from S3. Returns nil if the object does not
// exist — S3 DeleteObject is idempotent by design.
func (s *S3Storage) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("s3 delete %q: %w", key, err)
	}
	return nil
}

// URL returns the public S3 URL for a key.
// This assumes the bucket is configured for public read access.
// If your bucket is private, replace this with a presigned URL call —
// the interface signature stays the same, only this method body changes.
func (s *S3Storage) URL(key string) string {
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s.bucket, s.region, key)
}

// =============================================================
// KEY HELPERS
// Centralised key generation keeps the naming convention in one place.
// If you ever change the folder structure, you change it here only —
// not in every handler or service that constructs a path.
// =============================================================

// PDFKey returns the storage key for a book's PDF file.
// e.g. PDFKey("abc-123") → "pdfs/abc-123.pdf"
func PDFKey(bookID string) string {
	return fmt.Sprintf("pdfs/%s.pdf", bookID)
}

// AudioKey returns the storage key for a book's generated audio file.
// e.g. AudioKey("abc-123") → "audio/abc-123.mp3"
func AudioKey(bookID string) string {
	return fmt.Sprintf("audio/%s.mp3", bookID)
}

// =============================================================
// PDF VALIDATION HELPER
// Kept here because it is tightly related to the storage concern —
// we validate before we store. The magic bytes check confirms the
// file is actually a PDF regardless of what the client claims.
// =============================================================

// ReadAndValidatePDF reads all bytes from r and confirms the content
// starts with the PDF magic bytes (%PDF). Returns the raw bytes so
// the caller does not need to read the stream a second time.
func ReadAndValidatePDF(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	// A valid PDF always begins with "%PDF". Checking the raw bytes
	// catches cases where a client sends a non-PDF with a .pdf extension.
	if len(data) < 4 || string(data[:4]) != "%PDF" {
		return nil, fmt.Errorf("file is not a valid PDF")
	}

	return data, nil
}