package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config holds the configuration for an S3-compatible storage backend.
// It works with AWS S3, MinIO, RustFS, SeaweedFS, and any other service
// that exposes the S3 API.
type S3Config struct {
	// Endpoint is the S3 service host (and optional port), e.g.:
	//   "s3.amazonaws.com"          – AWS S3 (default region endpoint)
	//   "play.min.io"               – MinIO playground
	//   "minio:9000"                – MinIO in Docker / Kubernetes
	//   "seaweedfs-s3:8333"         – SeaweedFS S3 gateway
	Endpoint string

	// AccessKeyID and SecretAccessKey are the S3 credentials.
	AccessKeyID     string
	SecretAccessKey string

	// Bucket is the S3 bucket that will store all DeltaDatabase objects.
	// It must already exist (or the caller must have permission to create it).
	Bucket string

	// UseSSL enables TLS for the connection (default true for AWS S3;
	// set to false for local MinIO / SeaweedFS without TLS).
	UseSSL bool

	// Region is optional.  Leave empty to use the bucket's default region.
	Region string
}

// S3Storage implements StorageBackend using an S3-compatible object store.
//
// Object layout inside the configured bucket:
//
//	files/<id>.json.enc   – encrypted entity blob
//	files/<id>.meta.json  – entity metadata (key ID, IV, tag, schema, version…)
//	templates/<schemaID>.json – JSON Schema templates
//
// Templates are also mirrored to a local temporary directory so that the
// file-based schema.Validator can load them without modification.
type S3Storage struct {
	client *minio.Client
	bucket string

	// localTemplatesDir is a temporary directory used as a local mirror of
	// S3 templates so that schema.NewValidator (which is file-based) works
	// transparently with S3-backed storage.
	localTemplatesDir     string
	localTemplatesDirOnce sync.Once
	localTemplatesDirErr  error
}

// NewS3Storage creates and validates a new S3-compatible storage backend.
// It connects to the S3 endpoint, ensures the target bucket exists, and
// returns a ready-to-use *S3Storage.
func NewS3Storage(cfg S3Config) (*S3Storage, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("S3 endpoint cannot be empty")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("S3 bucket cannot be empty")
	}

	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Ensure the bucket exists; create it if necessary.
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check S3 bucket existence: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{
			Region: cfg.Region,
		}); err != nil {
			return nil, fmt.Errorf("failed to create S3 bucket %q: %w", cfg.Bucket, err)
		}
	}

	return &S3Storage{
		client: client,
		bucket: cfg.Bucket,
	}, nil
}

// blobKey returns the S3 object key for an entity's encrypted blob.
func (s *S3Storage) blobKey(id string) string {
	return "files/" + id + ".json.enc"
}

// metaKey returns the S3 object key for an entity's metadata.
func (s *S3Storage) metaKey(id string) string {
	return "files/" + id + ".meta.json"
}

// templateKey returns the S3 object key for a schema template.
func (s *S3Storage) templateKey(schemaID string) string {
	return "templates/" + schemaID + ".json"
}

// WriteFile writes an encrypted blob and its metadata to S3.
// Each PutObject call is atomic from the perspective of S3's strong
// read-after-write consistency model, so no temporary key tricks are needed.
func (s *S3Storage) WriteFile(id string, data []byte, metadata FileMetadata) error {
	if !isValidEntityID(id) {
		return ErrInvalidID
	}

	// Marshal metadata.
	metaJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: failed to marshal metadata: %v", ErrWriteFailed, err)
	}

	ctx := context.Background()

	// Upload encrypted blob.
	_, err = s.client.PutObject(ctx, s.bucket, s.blobKey(id),
		bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"})
	if err != nil {
		return fmt.Errorf("%w: failed to put blob: %v", ErrWriteFailed, err)
	}

	// Upload metadata.
	_, err = s.client.PutObject(ctx, s.bucket, s.metaKey(id),
		bytes.NewReader(metaJSON), int64(len(metaJSON)),
		minio.PutObjectOptions{ContentType: "application/json"})
	if err != nil {
		return fmt.Errorf("%w: failed to put metadata: %v", ErrWriteFailed, err)
	}

	return nil
}

// ReadFile reads the encrypted blob and its metadata from S3.
func (s *S3Storage) ReadFile(id string) (*FileData, error) {
	if !isValidEntityID(id) {
		return nil, ErrInvalidID
	}

	ctx := context.Background()

	// Read encrypted blob.
	blobObj, err := s.client.GetObject(ctx, s.bucket, s.blobKey(id), minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("%w: failed to get blob: %v", ErrReadFailed, err)
	}
	defer blobObj.Close()

	blob, err := io.ReadAll(blobObj)
	if err != nil {
		var minioErr minio.ErrorResponse
		if errors.As(err, &minioErr) && minioErr.Code == "NoSuchKey" {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("%w: failed to read blob: %v", ErrReadFailed, err)
	}

	// Read metadata.
	metaObj, err := s.client.GetObject(ctx, s.bucket, s.metaKey(id), minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("%w: failed to get metadata: %v", ErrReadFailed, err)
	}
	defer metaObj.Close()

	metaJSON, err := io.ReadAll(metaObj)
	if err != nil {
		var minioErr minio.ErrorResponse
		if errors.As(err, &minioErr) && minioErr.Code == "NoSuchKey" {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("%w: failed to read metadata: %v", ErrReadFailed, err)
	}

	var meta FileMetadata
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return nil, fmt.Errorf("%w: failed to unmarshal metadata: %v", ErrReadFailed, err)
	}

	return &FileData{Blob: blob, Metadata: meta}, nil
}

// FileExists reports whether both the blob and metadata objects exist in S3.
func (s *S3Storage) FileExists(id string) bool {
	if !isValidEntityID(id) {
		return false
	}
	ctx := context.Background()
	_, blobErr := s.client.StatObject(ctx, s.bucket, s.blobKey(id), minio.StatObjectOptions{})
	_, metaErr := s.client.StatObject(ctx, s.bucket, s.metaKey(id), minio.StatObjectOptions{})
	return blobErr == nil && metaErr == nil
}

// DeleteFile removes the blob and metadata objects for the given entity ID.
func (s *S3Storage) DeleteFile(id string) error {
	if !isValidEntityID(id) {
		return ErrInvalidID
	}
	ctx := context.Background()

	if err := s.client.RemoveObject(ctx, s.bucket, s.blobKey(id), minio.RemoveObjectOptions{}); err != nil {
		var minioErr minio.ErrorResponse
		if !errors.As(err, &minioErr) || minioErr.Code != "NoSuchKey" {
			return fmt.Errorf("failed to delete blob: %w", err)
		}
	}
	if err := s.client.RemoveObject(ctx, s.bucket, s.metaKey(id), minio.RemoveObjectOptions{}); err != nil {
		var minioErr minio.ErrorResponse
		if !errors.As(err, &minioErr) || minioErr.Code != "NoSuchKey" {
			return fmt.Errorf("failed to delete metadata: %w", err)
		}
	}
	return nil
}

// ListFiles returns the IDs of all stored entities by listing objects under
// the "files/" prefix and stripping the file extensions.
func (s *S3Storage) ListFiles() ([]string, error) {
	ctx := context.Background()
	ids := make(map[string]bool)

	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    "files/",
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", obj.Err)
		}
		name := strings.TrimPrefix(obj.Key, "files/")
		switch {
		case strings.HasSuffix(name, ".json.enc"):
			ids[name[:len(name)-9]] = true
		case strings.HasSuffix(name, ".meta.json"):
			ids[name[:len(name)-10]] = true
		}
	}

	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	return result, nil
}

// WriteTemplate stores a JSON Schema template in S3 and mirrors it to the
// local templates directory so the schema.Validator can use it immediately.
func (s *S3Storage) WriteTemplate(schemaID string, schemaData []byte) error {
	if !isValidEntityID(schemaID) {
		return fmt.Errorf("invalid schema ID")
	}

	ctx := context.Background()

	// Upload to S3.
	_, err := s.client.PutObject(ctx, s.bucket, s.templateKey(schemaID),
		bytes.NewReader(schemaData), int64(len(schemaData)),
		minio.PutObjectOptions{ContentType: "application/json"})
	if err != nil {
		return fmt.Errorf("failed to put template: %w", err)
	}

	// Mirror to local temp directory (best-effort).
	if dir := s.GetTemplatesDir(); dir != "" {
		localPath := filepath.Join(dir, schemaID+".json")
		_ = os.WriteFile(localPath, schemaData, 0644)
	}

	return nil
}

// ReadTemplate retrieves a JSON Schema template from S3.
func (s *S3Storage) ReadTemplate(schemaID string) ([]byte, error) {
	if !isValidEntityID(schemaID) {
		return nil, fmt.Errorf("invalid schema ID")
	}

	ctx := context.Background()
	obj, err := s.client.GetObject(ctx, s.bucket, s.templateKey(schemaID), minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get template: %w", err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		var minioErr minio.ErrorResponse
		if errors.As(err, &minioErr) && minioErr.Code == "NoSuchKey" {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("failed to read template: %w", err)
	}
	return data, nil
}

// GetTemplatesDir returns a local filesystem path to a directory containing
// all schema templates.  On the first call it creates a temporary directory
// and downloads every template from S3 into it so that schema.NewValidator
// (which is file-based) can load them without modification.
//
// Subsequent calls return the same directory.
func (s *S3Storage) GetTemplatesDir() string {
	s.localTemplatesDirOnce.Do(func() {
		dir, err := os.MkdirTemp("", "deltadatabase-templates-*")
		if err != nil {
			s.localTemplatesDirErr = fmt.Errorf("failed to create local templates dir: %w", err)
			return
		}
		s.localTemplatesDir = dir
		s.syncTemplatesFromS3(dir)
	})
	if s.localTemplatesDirErr != nil {
		return ""
	}
	return s.localTemplatesDir
}

// syncTemplatesFromS3 downloads all objects under the "templates/" prefix
// from S3 to the given local directory.  Errors are logged but not fatal —
// templates can be re-uploaded via the REST API.
func (s *S3Storage) syncTemplatesFromS3(localDir string) {
	ctx := context.Background()
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    "templates/",
		Recursive: true,
	}) {
		if obj.Err != nil {
			continue
		}
		name := strings.TrimPrefix(obj.Key, "templates/")
		if !strings.HasSuffix(name, ".json") || name == "" {
			continue
		}
		reader, err := s.client.GetObject(ctx, s.bucket, obj.Key, minio.GetObjectOptions{})
		if err != nil {
			continue
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(localDir, name), data, 0644)
	}
}
