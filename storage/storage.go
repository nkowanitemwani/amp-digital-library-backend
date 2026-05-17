package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// =============================================================
// STORAGE INTERFACE
// =============================================================

// Storage defines the operations the application needs from any file store.
// Save and Delete are straightforward. SignedURL generates a URL a client
// can use to stream the file directly — for local disk this is a plain
// HTTP URL served by the backend, for S3 it is a time-limited presigned URL
// that does not require the bucket to be publicly accessible.
type Storage interface {
	// Save writes data under the given key.
	Save(ctx context.Context, key string, data []byte, contentType string) error

	// Delete removes a file. Idempotent — returns nil if the file does not exist.
	Delete(ctx context.Context, key string) error

	// SignedURL returns a URL the client can use to stream or download the file.
	// For S3 the URL expires after urlTTL — pass the duration you want.
	// For local disk the TTL is ignored and a plain server URL is returned.
	SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// =============================================================
// LOCAL DISK IMPLEMENTATION
// =============================================================

// LocalStorage writes files to a directory on the local filesystem.
// Used during development — swap for S3Storage in main.go when deploying.
type LocalStorage struct {
	baseDir string
	baseURL string
}

// NewLocalStorage creates a LocalStorage and ensures the base directory exists.
func NewLocalStorage(baseDir, baseURL string) (*LocalStorage, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create storage directory %q: %w", baseDir, err)
	}
	return &LocalStorage{baseDir: baseDir, baseURL: baseURL}, nil
}

// Save writes data to baseDir/key, creating subdirectories as needed.
func (s *LocalStorage) Save(_ context.Context, key string, data []byte, _ string) error {
	fullPath := filepath.Join(s.baseDir, key)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("create directory for %q: %w", key, err)
	}
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return fmt.Errorf("write file %q: %w", key, err)
	}
	return nil
}

// Delete removes a file from disk. Returns nil if the file does not exist.
func (s *LocalStorage) Delete(_ context.Context, key string) error {
	err := os.Remove(filepath.Join(s.baseDir, key))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete file %q: %w", key, err)
	}
	return nil
}

// SignedURL returns a plain HTTP URL for the given key.
// TTL is ignored for local disk — the backend serves files indefinitely
// via the /files static route registered in main.go.
func (s *LocalStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return s.baseURL + "/" + key, nil
}

// ReadFile retrieves the raw bytes of a file by key.
// Used by the processor to read a PDF back out of storage for text extraction.
// Not on the Storage interface — only local disk needs this; S3 reads
// are handled separately via GetObject when that is implemented.
func (s *LocalStorage) ReadFile(key string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(s.baseDir, key))
	if err != nil {
		return nil, fmt.Errorf("read file %q: %w", key, err)
	}
	return data, nil
}

// =============================================================
// S3 IMPLEMENTATION
// =============================================================

// S3Storage writes files to a private AWS S3 bucket and generates
// presigned URLs for client access. The bucket does NOT need public
// read access — presigned URLs grant temporary access per-request.
type S3Storage struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
	region  string
}

// NewS3Storage creates an S3Storage using explicit credentials from config.
// No environment variables are read here — all values come from the caller.
func NewS3Storage(ctx context.Context, region, bucket, accessKeyID, secretAccessKey string) (*S3Storage, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config for s3: %w", err)
	}

	client := s3.NewFromConfig(cfg)

	return &S3Storage{
		client:  client,
		presign: s3.NewPresignClient(client),
		bucket:  bucket,
		region:  region,
	}, nil
}

// Save uploads data to S3. ContentType is set on the object so that
// browsers and audio players handle the response correctly
// (e.g. audio/mpeg tells the browser it can stream the MP3 directly).
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

// Delete removes an object from S3.
// S3 DeleteObject is idempotent — deleting a non-existent key succeeds.
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

// SignedURL generates a presigned GET URL for the given key.
// The URL is valid for ttl duration (e.g. 1 hour). After that the
// client must call this endpoint again to get a fresh URL.
//
// Presigned URLs are the correct approach for a private bucket —
// they grant temporary access to a specific object without making
// the bucket public or requiring the client to have AWS credentials.
// Audio files are streamed directly from S3 via this URL, so the
// backend does not proxy the audio data — reducing bandwidth costs.
func (s *S3Storage) SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign %q: %w", key, err)
	}
	return req.URL, nil
}

// GetObject fetches the raw bytes of an object from S3.
// Used by the processor to read a PDF back out of S3 for text extraction.
// Not on the Storage interface — the processor calls this directly on
// the concrete *S3Storage type when needed.
func (s *S3Storage) GetObject(ctx context.Context, key string) ([]byte, error) {
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 get object %q: %w", key, err)
	}
	defer result.Body.Close()
	return io.ReadAll(result.Body)
}

// =============================================================
// KEY HELPERS
// Centralised so the folder structure is defined in one place.
// =============================================================

// PDFKey returns the storage key for a book's source PDF.
func PDFKey(bookID string) string {
	return fmt.Sprintf("pdfs/%s.pdf", bookID)
}

// AudioKey returns the storage key for a book's generated audio file.
func AudioKey(bookID string) string {
	return fmt.Sprintf("audio/%s.mp3", bookID)
}

// DialogueKey returns the storage key for a book's two-voice teaching dialogue.
func DialogueKey(bookID string) string {
	return fmt.Sprintf("dialogue/%s.mp3", bookID)
}

// QuestionAudioKey returns the storage key for a single question's audio.
func QuestionAudioKey(questionID string) string {
	return fmt.Sprintf("questions/%s.mp3", questionID)
}

// =============================================================
// PDF VALIDATION
// =============================================================

// ReadAndValidatePDF reads all bytes from r and checks the PDF magic bytes.
// Catches files that have a .pdf extension but are not actually PDFs,
// regardless of what Content-Type the client declared.
func ReadAndValidatePDF(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if len(data) < 4 || string(data[:4]) != "%PDF" {
		return nil, fmt.Errorf("file is not a valid PDF")
	}
	return data, nil
}

// AudioSignedURLTTL is how long a presigned audio URL stays valid.
// 1 hour is generous for a single listening session — students
// won't be listening to one unit for longer than that.
const AudioSignedURLTTL = 1 * time.Hour