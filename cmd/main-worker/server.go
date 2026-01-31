package main

import (
	"context"
	"crypto/rsa"
	"fmt"
	"log"
	"net"
	"time"

	"delta-db/api/proto"
	"delta-db/internal/auth"
	"delta-db/pkg/crypto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MainWorkerServer implements the gRPC MainWorker service.
type MainWorkerServer struct {
	proto.UnimplementedMainWorkerServer

	// Authentication and token management
	tokenManager  *auth.TokenManager
	workerAuth    *auth.WorkerAuthenticator
	
	// Encryption key management
	masterKey     []byte
	masterKeyID   string
	
	// Configuration
	config *Config
}

// Config holds the Main Worker configuration.
type Config struct {
	// gRPC server configuration
	GRPCAddr string
	
	// REST API configuration
	RESTAddr string
	
	// Shared filesystem path
	SharedFSPath string
	
	// Token TTLs
	WorkerTokenTTL time.Duration
	ClientTokenTTL time.Duration
	
	// Master encryption key (32 bytes for AES-256)
	MasterKey []byte
	KeyID     string
}

// NewMainWorkerServer creates a new Main Worker server instance.
func NewMainWorkerServer(config *Config) (*MainWorkerServer, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	
	// Validate master key
	if len(config.MasterKey) == 0 {
		// Generate a new master key if not provided
		key, err := crypto.GenerateKey(32)
		if err != nil {
			return nil, fmt.Errorf("failed to generate master key: %w", err)
		}
		config.MasterKey = key
		log.Printf("Generated new master encryption key")
	}
	
	if len(config.MasterKey) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(config.MasterKey))
	}
	
	if config.KeyID == "" {
		config.KeyID = "main-key-v1"
	}
	
	// Initialize token manager
	tokenManager := auth.NewTokenManager(config.WorkerTokenTTL, config.ClientTokenTTL)
	
	// Initialize worker authenticator
	workerAuth := auth.NewWorkerAuthenticator()
	
	server := &MainWorkerServer{
		tokenManager:  tokenManager,
		workerAuth:    workerAuth,
		masterKey:     config.MasterKey,
		masterKeyID:   config.KeyID,
		config:        config,
	}
	
	return server, nil
}

// Subscribe implements the Subscribe RPC for Processing Worker registration.
func (s *MainWorkerServer) Subscribe(ctx context.Context, req *proto.SubscribeRequest) (*proto.SubscribeResponse, error) {
	// Validate request
	if req.GetWorkerId() == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id is required")
	}
	
	if len(req.GetPubkey()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "pubkey is required")
	}
	
	log.Printf("Subscribe request from worker: %s", req.GetWorkerId())
	
	// Parse worker's public key
	pubkey, err := crypto.ParsePublicKeyFromPEM(req.GetPubkey())
	if err != nil {
		log.Printf("Failed to parse worker public key: %v", err)
		return nil, status.Error(codes.InvalidArgument, "invalid public key format")
	}
	
	// Authenticate worker (for now, accept all workers with valid public keys)
	// In production, you would verify against a whitelist or use mutual TLS
	workerID := req.GetWorkerId()
	
	// Check if worker is registered (optional authentication step)
	// For Task 5, we'll use a simple approach: auto-register workers
	if !s.isWorkerRegistered(workerID) {
		// Auto-register the worker for development
		err := s.workerAuth.RegisterWorker(workerID, "default-password", req.GetTags())
		if err != nil {
			log.Printf("Warning: Could not auto-register worker %s: %v", workerID, err)
			// Continue anyway for development
		} else {
			log.Printf("Auto-registered worker: %s", workerID)
		}
	}
	
	// Wrap the master key with the worker's public key
	wrappedKey, err := crypto.WrapKey(pubkey, s.masterKey)
	if err != nil {
		log.Printf("Failed to wrap key for worker %s: %v", workerID, err)
		return nil, status.Error(codes.Internal, "failed to wrap encryption key")
	}
	
	// Generate a session token for the worker
	token, err := s.tokenManager.GenerateWorkerToken(workerID, s.masterKeyID, req.GetTags())
	if err != nil {
		log.Printf("Failed to generate token for worker %s: %v", workerID, err)
		return nil, status.Error(codes.Internal, "failed to generate token")
	}
	
	log.Printf("Successfully subscribed worker %s with token (expires: %v)", 
		workerID, token.ExpiresAt.Format(time.RFC3339))
	
	// Return the subscription response
	return &proto.SubscribeResponse{
		Token:      token.Token,
		WrappedKey: wrappedKey,
		KeyId:      s.masterKeyID,
	}, nil
}

// Process implements the Process RPC for handling entity operations.
// This is a placeholder for now - will be implemented in later tasks.
func (s *MainWorkerServer) Process(ctx context.Context, req *proto.ProcessRequest) (*proto.ProcessResponse, error) {
	// Validate token
	if req.GetToken() == "" {
		return nil, status.Error(codes.Unauthenticated, "token is required")
	}
	
	// Validate the token
	_, err := s.tokenManager.ValidateWorkerToken(req.GetToken())
	if err != nil {
		log.Printf("Invalid token in Process request: %v", err)
		return nil, status.Error(codes.Unauthenticated, "invalid or expired token")
	}
	
	// Placeholder response
	return &proto.ProcessResponse{
		Status: "not_implemented",
		Error:  "Process endpoint not yet implemented - will be completed in Task 6-8",
	}, status.Error(codes.Unimplemented, "Process not yet implemented")
}

// isWorkerRegistered checks if a worker is already registered.
func (s *MainWorkerServer) isWorkerRegistered(workerID string) bool {
	_, err := s.workerAuth.GetWorkerCredentials(workerID)
	return err == nil
}

// RegisterWorker manually registers a worker with credentials.
func (s *MainWorkerServer) RegisterWorker(workerID, password string, tags map[string]string) error {
	return s.workerAuth.RegisterWorker(workerID, password, tags)
}

// Run starts the Main Worker gRPC server.
func (s *MainWorkerServer) Run() error {
	// Create gRPC server
	grpcServer := grpc.NewServer()
	
	// Register the MainWorker service
	proto.RegisterMainWorkerServer(grpcServer, s)
	
	// Listen on the configured address
	listener, err := net.Listen("tcp", s.config.GRPCAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.config.GRPCAddr, err)
	}
	
	log.Printf("Main Worker gRPC server listening on %s", s.config.GRPCAddr)
	log.Printf("Master Key ID: %s", s.masterKeyID)
	
	// Start serving
	if err := grpcServer.Serve(listener); err != nil {
		return fmt.Errorf("gRPC server error: %w", err)
	}
	
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *MainWorkerServer) Shutdown() {
	log.Println("Main Worker shutting down...")
	// Cleanup resources if needed
}

// GetStats returns server statistics.
func (s *MainWorkerServer) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"active_worker_tokens": s.tokenManager.GetWorkerTokenCount(),
		"active_client_tokens": s.tokenManager.GetClientTokenCount(),
		"registered_workers":   len(s.workerAuth.ListWorkers()),
		"master_key_id":        s.masterKeyID,
	}
}

// Helper function to generate RSA key pair for testing
func GenerateTestKeyPair() (*rsa.PrivateKey, *rsa.PublicKey, error) {
	return crypto.GenerateRSAKeyPair(2048)
}
