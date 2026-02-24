package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Config holds the configuration for the S3-compatible storage backend.
type S3Config struct {
	// Endpoint is the S3-compatible endpoint URL.
	// Leave empty to use AWS S3. For self-hosted services supply the full URL,
	// e.g. "http://rustfs:9000" or "http://seaweedfs:8333".
	Endpoint string

	// Bucket is the name of the S3 bucket to use.
	Bucket string

	// Region is the AWS region (or a placeholder value for self-hosted services,
	// e.g. "us-east-1").
	Region string

	// AccessKeyID is the S3 access key.
	AccessKeyID string

	// SecretAccessKey is the S3 secret key.
	SecretAccessKey string

	// ForcePathStyle forces the use of path-style addressing
	// (http://endpoint/bucket/key) instead of virtual-hosted style
	// (http://bucket.endpoint/key). Required by most self-hosted S3-compatible
	// services such as RustFS, SeaweedFS, and MinIO.
	ForcePathStyle bool
}

// S3Storage implements Backend using an S3-compatible object store. It is
// compatible with AWS S3, RustFS, SeaweedFS, MinIO, and any service that
// implements the S3 API.
//
// Object naming convention:
//   - Encrypted blobs:  files/<id>.json.enc
//   - Metadata:         files/<id>.meta.json
//   - Schema templates: templates/<schemaID>.json
type S3Storage struct {
	cfg    S3Config
	client *s3.Client
}

// NewS3Storage creates a new S3Storage and verifies that the bucket is
// accessible by performing a lightweight HeadBucket request.
func NewS3Storage(cfg S3Config) (*S3Storage, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("S3 bucket name cannot be empty")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1" // sensible default for self-hosted services
	}

	// Build the SDK configuration.
	sdkOpts := []func(*config.LoadOptions) error{
		config.WithRegion(cfg.Region),
	}
	if cfg.AccessKeyID != "" {
		sdkOpts = append(sdkOpts,
			config.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
			),
		)
	}

	sdkCfg, err := config.LoadDefaultConfig(context.Background(), sdkOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load S3 SDK config: %w", err)
	}

	// Build client options.
	var s3Opts []func(*s3.Options)
	if cfg.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		})
	}
	if cfg.ForcePathStyle {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(sdkCfg, s3Opts...)

	// Verify connectivity (lightweight check).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(cfg.Bucket),
	}); err != nil {
		return nil, fmt.Errorf("cannot reach S3 bucket %q: %w", cfg.Bucket, err)
	}

	return &S3Storage{cfg: cfg, client: client}, nil
}

// blobKey returns the S3 key for the encrypted blob of entity id.
func (s *S3Storage) blobKey(id string) string {
	return "files/" + id + ".json.enc"
}

// metaKey returns the S3 key for the metadata of entity id.
func (s *S3Storage) metaKey(id string) string {
	return "files/" + id + ".meta.json"
}

// templateKey returns the S3 key for a schema template.
func (s *S3Storage) templateKey(schemaID string) string {
	return "templates/" + schemaID + ".json"
}

// put performs a PutObject request.
func (s *S3Storage) put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.cfg.Bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
	})
	return err
}

// get performs a GetObject request and returns the body bytes.
func (s *S3Storage) get(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	defer out.Body.Close() //nolint:errcheck
	return io.ReadAll(out.Body)
}

// exists reports whether the given key exists in the bucket.
func (s *S3Storage) exists(ctx context.Context, key string) bool {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	return err == nil
}

// WriteFile stores an encrypted blob and its metadata in S3.
//
// The blob is written first. If the metadata write fails, the caller's
// LockManager ensures the entity cannot be read back in an inconsistent state
// until a subsequent successful write completes.
func (s *S3Storage) WriteFile(id string, data []byte, metadata FileMetadata) error {
	if !isValidEntityID(id) {
		return ErrInvalidID
	}

	ctx := context.Background()

	// Marshal metadata.
	metaJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: failed to marshal metadata: %v", ErrWriteFailed, err)
	}

	// Write the encrypted blob.
	if err := s.put(ctx, s.blobKey(id), data, "application/octet-stream"); err != nil {
		return fmt.Errorf("%w: failed to put blob: %v", ErrWriteFailed, err)
	}

	// Write the metadata. On failure, attempt a best-effort delete of the blob
	// to avoid leaving an orphaned object in the bucket.
	if err := s.put(ctx, s.metaKey(id), metaJSON, "application/json"); err != nil {
		// Best-effort cleanup; ignore the delete error.
		_, _ = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.cfg.Bucket),
			Key:    aws.String(s.blobKey(id)),
		})
		return fmt.Errorf("%w: failed to put metadata: %v", ErrWriteFailed, err)
	}

	return nil
}

// ReadFile fetches the encrypted blob and metadata from S3.
func (s *S3Storage) ReadFile(id string) (*FileData, error) {
	if !isValidEntityID(id) {
		return nil, ErrInvalidID
	}

	ctx := context.Background()

	blob, err := s.get(ctx, s.blobKey(id))
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("%w: failed to get blob: %v", ErrReadFailed, err)
	}

	metaJSON, err := s.get(ctx, s.metaKey(id))
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("%w: failed to get metadata: %v", ErrReadFailed, err)
	}

	var metadata FileMetadata
	if err := json.Unmarshal(metaJSON, &metadata); err != nil {
		return nil, fmt.Errorf("%w: failed to unmarshal metadata: %v", ErrReadFailed, err)
	}

	return &FileData{Blob: blob, Metadata: metadata}, nil
}

// FileExists returns true when both the blob and metadata objects exist in S3.
func (s *S3Storage) FileExists(id string) bool {
	if !isValidEntityID(id) {
		return false
	}
	ctx := context.Background()
	return s.exists(ctx, s.blobKey(id)) && s.exists(ctx, s.metaKey(id))
}

// DeleteFile removes the blob and metadata objects from S3.
func (s *S3Storage) DeleteFile(id string) error {
	if !isValidEntityID(id) {
		return ErrInvalidID
	}

	ctx := context.Background()

	for _, key := range []string{s.blobKey(id), s.metaKey(id)} {
		if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.cfg.Bucket),
			Key:    aws.String(key),
		}); err != nil {
			return fmt.Errorf("failed to delete object %s: %w", key, err)
		}
	}
	return nil
}

// ListFiles returns all entity IDs present in the "files/" prefix of the bucket.
func (s *S3Storage) ListFiles() ([]string, error) {
	ctx := context.Background()
	prefix := "files/"

	ids := make(map[string]struct{})
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.cfg.Bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list S3 objects: %w", err)
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			key = strings.TrimPrefix(key, prefix)
			switch {
			case strings.HasSuffix(key, ".json.enc"):
				ids[key[:len(key)-9]] = struct{}{}
			case strings.HasSuffix(key, ".meta.json"):
				ids[key[:len(key)-10]] = struct{}{}
			}
		}
	}

	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	return result, nil
}

// WriteTemplate stores a JSON-schema template in the "templates/" prefix.
func (s *S3Storage) WriteTemplate(schemaID string, schemaData []byte) error {
	if !isValidEntityID(schemaID) {
		return fmt.Errorf("invalid schema ID")
	}
	ctx := context.Background()
	if err := s.put(ctx, s.templateKey(schemaID), schemaData, "application/json"); err != nil {
		return fmt.Errorf("failed to write template: %w", err)
	}
	return nil
}

// ReadTemplate fetches a JSON-schema template from S3.
func (s *S3Storage) ReadTemplate(schemaID string) ([]byte, error) {
	if !isValidEntityID(schemaID) {
		return nil, fmt.Errorf("invalid schema ID")
	}
	ctx := context.Background()
	data, err := s.get(ctx, s.templateKey(schemaID))
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("failed to read template: %w", err)
	}
	return data, nil
}

// GetTemplatesDir returns an empty string because S3Storage does not use a
// local filesystem for templates. The schema Validator will be skipped when
// this returns empty, which is logged as a warning at startup.
func (s *S3Storage) GetTemplatesDir() string {
	return ""
}
