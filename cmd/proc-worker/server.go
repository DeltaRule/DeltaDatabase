package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"

	"delta-db/api/proto"
	"delta-db/pkg/cache"
	pkgcrypto "delta-db/pkg/crypto"
	"delta-db/pkg/fs"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ProcWorkerServer implements the gRPC Process handler for GET operations.
// It wraps a ProcWorker (which holds the in-memory encryption key obtained
// during the Subscribe handshake from Task 7) and uses pkg/cache and pkg/fs
// to fulfil read requests without persisting plaintext to disk.
type ProcWorkerServer struct {
	proto.UnimplementedMainWorkerServer

	worker  *ProcWorker
	storage *fs.Storage
	lockMgr *fs.LockManager
	cache   *cache.Cache
}

// NewProcWorkerServer creates a ProcWorkerServer for the given worker, storage
// path, and cache configuration.
func NewProcWorkerServer(worker *ProcWorker, storage *fs.Storage, c *cache.Cache) *ProcWorkerServer {
	return &ProcWorkerServer{
		worker:  worker,
		storage: storage,
		lockMgr: fs.NewLockManager(storage),
		cache:   c,
	}
}

// Subscribe is not handled by the Processing Worker's own gRPC server;
// all subscription calls go to the Main Worker.
func (s *ProcWorkerServer) Subscribe(_ context.Context, _ *proto.SubscribeRequest) (*proto.SubscribeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "Subscribe is not handled by the Processing Worker")
}

// Process handles GET operations for database entities.
//
// Flow:
//  1. Check pkg/cache; if hit with matching version, return metadata + data.
//  2. On cache miss, obtain a shared lock via pkg/fs.
//  3. Load the .meta.json and .json.enc files from the shared filesystem.
//  4. Decrypt using the memory-resident key received from the Main Worker in Task 7.
//  5. Store the decrypted JSON in cache and release the lock.
//  6. Return the plaintext JSON to the caller.
func (s *ProcWorkerServer) Process(ctx context.Context, req *proto.ProcessRequest) (*proto.ProcessResponse, error) {
	if req.GetOperation() != "GET" {
		return nil, status.Errorf(codes.InvalidArgument,
			"ProcWorkerServer only handles GET operations, got %q", req.GetOperation())
	}

	if req.GetDatabaseName() == "" || req.GetEntityKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "database_name and entity_key are required")
	}

	// Build the entity ID used as the storage and cache key (e.g. "chatdb_Chat_id").
	entityID := req.GetDatabaseName() + "_" + req.GetEntityKey()

	log.Printf("[%s] GET %s", s.worker.config.WorkerID, entityID)

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

	// Step 4: Decrypt using the memory-resident key from Task 7.
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
