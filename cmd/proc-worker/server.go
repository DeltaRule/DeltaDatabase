package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"delta-db/api/proto"
	"delta-db/pkg/cache"
	pkgcrypto "delta-db/pkg/crypto"
	"delta-db/pkg/fs"
	"delta-db/pkg/schema"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ProcWorkerServer implements the gRPC Process handler for GET and PUT operations.
// It wraps a ProcWorker (which holds the in-memory encryption key obtained
// during the Subscribe handshake from Task 7) and uses pkg/cache and pkg/fs
// to fulfil requests without persisting plaintext to disk.
type ProcWorkerServer struct {
	proto.UnimplementedMainWorkerServer

	worker    *ProcWorker
	storage   *fs.Storage
	lockMgr   *fs.LockManager
	cache     *cache.Cache
	validator *schema.Validator
}

// NewProcWorkerServer creates a ProcWorkerServer for the given worker, storage
// path, and cache configuration.
func NewProcWorkerServer(worker *ProcWorker, storage *fs.Storage, c *cache.Cache) *ProcWorkerServer {
	v, err := schema.NewValidator(storage.GetTemplatesDir())
	if err != nil {
		log.Printf("[warn] failed to initialise schema validator: %v — schema validation will be skipped", err)
	}
	return &ProcWorkerServer{
		worker:    worker,
		storage:   storage,
		lockMgr:   fs.NewLockManager(storage),
		cache:     c,
		validator: v,
	}
}

// Subscribe is not handled by the Processing Worker's own gRPC server;
// all subscription calls go to the Main Worker.
func (s *ProcWorkerServer) Subscribe(_ context.Context, _ *proto.SubscribeRequest) (*proto.SubscribeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "Subscribe is not handled by the Processing Worker")
}

// Process handles GET and PUT operations for database entities.
//
// GET flow:
//  1. Check pkg/cache; if hit, return metadata + data.
//  2. On cache miss, obtain a shared lock via pkg/fs.
//  3. Load the .meta.json and .json.enc files from the shared filesystem.
//  4. Decrypt using the memory-resident key received from the Main Worker.
//  5. Store the decrypted JSON in cache and release the lock.
//  6. Return the plaintext JSON to the caller.
//
// PUT flow:
//  1. Validate incoming JSON against the correct schema.
//  2. Obtain exclusive lock via pkg/fs.
//  3. Increment version in metadata.
//  4. Encrypt JSON into a new blob.
//  5. Atomic write to disk (Write temp -> rename).
//  6. Update cache and release lock.
func (s *ProcWorkerServer) Process(ctx context.Context, req *proto.ProcessRequest) (*proto.ProcessResponse, error) {
	if req.GetDatabaseName() == "" || req.GetEntityKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "database_name and entity_key are required")
	}

	// Reject path-traversal characters in database_name and entity_key to prevent
	// an attacker from escaping the shared-filesystem files directory.
	dbName := req.GetDatabaseName()
	entityKey := req.GetEntityKey()
	if strings.ContainsAny(dbName, `/\`) || strings.Contains(dbName, "..") {
		return nil, status.Error(codes.InvalidArgument, "invalid database_name: must not contain path separators or '..'")
	}
	if strings.ContainsAny(entityKey, `/\`) || strings.Contains(entityKey, "..") {
		return nil, status.Error(codes.InvalidArgument, "invalid entity_key: must not contain path separators or '..'")
	}

	// Build the entity ID used as the storage and cache key (e.g. "chatdb_Chat_id").
	entityID := req.GetDatabaseName() + "_" + req.GetEntityKey()

	switch req.GetOperation() {
	case "GET":
		log.Printf("[%s] GET %s", s.worker.config.WorkerID, entityID)
		return s.processGET(ctx, req, entityID)
	case "PUT":
		log.Printf("[%s] PUT %s", s.worker.config.WorkerID, entityID)
		return s.processPUT(ctx, req, entityID)
	default:
		return nil, status.Errorf(codes.InvalidArgument,
			"unsupported operation %q: must be GET or PUT", req.GetOperation())
	}
}

// processGET handles the GET flow: cache check → shared lock → read → decrypt → cache → return.
func (s *ProcWorkerServer) processGET(_ context.Context, _ *proto.ProcessRequest, entityID string) (*proto.ProcessResponse, error) {
	// Step 1: Check cache.
	if entry, ok := s.cache.Get(entityID); ok {
		log.Printf("[%s] cache HIT for %s", s.worker.config.WorkerID, entityID)
		return &proto.ProcessResponse{
			Status:  "OK",
			Result:  entry.Data,
			Version: entry.Version,
		}, nil
	}

	log.Printf("[%s] cache MISS for %s — loading from FS", s.worker.config.WorkerID, entityID)

	// Step 2: Acquire shared (read) lock so other concurrent readers are not blocked
	// while a writer cannot modify the file underneath us.
	if _, err := s.lockMgr.AcquireLock(entityID, fs.LockShared); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to acquire shared lock: %v", err)
	}
	defer s.lockMgr.ReleaseLock(entityID) //nolint:errcheck

	// Step 3: Load .meta.json and .json.enc.
	fileData, err := s.storage.ReadFile(entityID)
	if err != nil {
		if errors.Is(err, fs.ErrFileNotFound) {
			return nil, status.Errorf(codes.NotFound, "entity %q not found", entityID)
		}
		return nil, status.Errorf(codes.Internal, "failed to read file: %v", err)
	}

	// Step 4: Decrypt using the memory-resident key.
	// Security: copy the key under the read-lock; do not log or persist it.
	s.worker.mu.RLock()
	if len(s.worker.encryptionKey) == 0 {
		s.worker.mu.RUnlock()
		return nil, status.Error(codes.Unavailable, "encryption key not available; worker not yet subscribed")
	}
	key := make([]byte, len(s.worker.encryptionKey))
	copy(key, s.worker.encryptionKey)
	s.worker.mu.RUnlock()

	nonce, err := base64.StdEncoding.DecodeString(fileData.Metadata.IV)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "invalid IV in metadata: %v", err)
	}
	tag, err := base64.StdEncoding.DecodeString(fileData.Metadata.Tag)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "invalid tag in metadata: %v", err)
	}

	plaintext, err := pkgcrypto.Decrypt(key, fileData.Blob, nonce, tag)
	if err != nil {
		// Security: do not expose key material or plaintext in error messages.
		log.Printf("[%s] decryption failed for %s", s.worker.config.WorkerID, entityID)
		return nil, status.Error(codes.Internal, "decryption failed")
	}

	// Step 5: Store decrypted data in cache and release the lock (via defer above).
	version := strconv.Itoa(fileData.Metadata.Version)
	s.cache.Set(entityID, plaintext, version)
	log.Printf("[%s] cached %s (version=%s)", s.worker.config.WorkerID, entityID, version)

	// Step 6: Return JSON to caller.
	return &proto.ProcessResponse{
		Status:  "OK",
		Result:  plaintext,
		Version: version,
	}, nil
}

// processPUT handles the PUT flow: validate → exclusive lock → version increment →
// encrypt → atomic write → update cache → release lock.
func (s *ProcWorkerServer) processPUT(_ context.Context, req *proto.ProcessRequest, entityID string) (*proto.ProcessResponse, error) {
	if len(req.GetPayload()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "payload is required for PUT operations")
	}

	// Step 1: Validate incoming JSON against the schema.
	// Security: if validation fails, reject the write (fail closed per guidelines).
	if req.GetSchemaId() != "" && s.validator != nil {
		result, err := s.validator.Validate(req.GetSchemaId(), req.GetPayload())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "schema validation error: %v", err)
		}
		if !result.Valid {
			return nil, status.Errorf(codes.InvalidArgument,
				"payload does not match schema %q: %d error(s)", req.GetSchemaId(), len(result.Errors))
		}
	}

	// Security: copy the key and keyID under the read-lock; do not log or persist them.
	s.worker.mu.RLock()
	if len(s.worker.encryptionKey) == 0 {
		s.worker.mu.RUnlock()
		return nil, status.Error(codes.Unavailable, "encryption key not available; worker not yet subscribed")
	}
	key := make([]byte, len(s.worker.encryptionKey))
	copy(key, s.worker.encryptionKey)
	keyID := s.worker.keyID
	s.worker.mu.RUnlock()

	// Step 2: Acquire exclusive lock to prevent concurrent writes.
	if _, err := s.lockMgr.AcquireLock(entityID, fs.LockExclusive); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to acquire exclusive lock: %v", err)
	}
	defer s.lockMgr.ReleaseLock(entityID) //nolint:errcheck

	// Step 3: Determine next version by reading the current metadata (if any).
	nextVersion := 1
	if existing, err := s.storage.ReadFile(entityID); err == nil {
		nextVersion = existing.Metadata.Version + 1
	}

	// Step 4: Encrypt the JSON payload.
	// Security: if encryption fails, reject the write (fail closed per guidelines).
	encResult, err := pkgcrypto.Encrypt(key, req.GetPayload())
	if err != nil {
		log.Printf("[%s] encryption failed for %s", s.worker.config.WorkerID, entityID)
		return nil, status.Error(codes.Internal, "encryption failed")
	}

	// Step 5: Atomic write to disk (storage.WriteFile uses temp → rename).
	metadata := fs.FileMetadata{
		KeyID:     keyID,
		Algorithm: "AES-GCM",
		IV:        base64.StdEncoding.EncodeToString(encResult.Nonce),
		Tag:       base64.StdEncoding.EncodeToString(encResult.Tag),
		SchemaID:  req.GetSchemaId(),
		Version:   nextVersion,
		WriterID:  s.worker.config.WorkerID,
		Timestamp: time.Now().UTC(),
		Database:  req.GetDatabaseName(),
		EntityKey: req.GetEntityKey(),
	}
	if err := s.storage.WriteFile(entityID, encResult.Ciphertext, metadata); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to write file: %v", err)
	}

	// Step 6: Update cache and release lock (lock released via defer above).
	versionStr := strconv.Itoa(nextVersion)
	s.cache.Set(entityID, req.GetPayload(), versionStr)
	log.Printf("[%s] cached %s (version=%s)", s.worker.config.WorkerID, entityID, versionStr)

	return &proto.ProcessResponse{
		Status:  "OK",
		Version: versionStr,
	}, nil
}

// Serve starts the ProcWorkerServer's gRPC listener on addr and blocks until
// the server exits. Call Handshake on the underlying ProcWorker before Serve
// so that the encryption key is available.
func (s *ProcWorkerServer) Serve(addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	srv := grpc.NewServer()
	proto.RegisterMainWorkerServer(srv, s)

	log.Printf("[%s] Processing Worker gRPC server listening on %s",
		s.worker.config.WorkerID, addr)
	return srv.Serve(lis)
}
