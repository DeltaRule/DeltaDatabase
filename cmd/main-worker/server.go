package main

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"delta-db/api/proto"
	"delta-db/internal/auth"
	"delta-db/internal/routing"
	"delta-db/pkg/cache"
	"delta-db/pkg/crypto"
	"delta-db/pkg/schema"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// MainWorkerServer implements the gRPC MainWorker service.
type MainWorkerServer struct {
	proto.UnimplementedMainWorkerServer

	// Authentication and token management
	tokenManager  *auth.TokenManager
	workerAuth    *auth.WorkerAuthenticator

	// Worker registry tracks Processing Worker status.
	registry *routing.WorkerRegistry

	// entityStore is an LRU-bounded in-memory store for REST/gRPC entity data.
	// Entries are cached on every write and served directly on reads; the LRU
	// algorithm evicts the least-recently-used entry when the cache is full.
	// No time-based TTL is applied — data stays in memory until evicted.
	entityStore *cache.Cache

	// Schema validator for managing JSON Schema templates.
	validator *schema.Validator

	// Encryption key management
	masterKey   []byte
	masterKeyID string

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

	// EntityCacheSize is the maximum number of entities held in the
	// in-memory LRU store.  Defaults to 1024 when zero or negative.
	EntityCacheSize int
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

	if config.EntityCacheSize <= 0 {
		config.EntityCacheSize = 1024
	}
	
	// Initialize token manager
	tokenManager := auth.NewTokenManager(config.WorkerTokenTTL, config.ClientTokenTTL)
	
	// Initialize worker authenticator
	workerAuth := auth.NewWorkerAuthenticator()

	// Initialise LRU entity store.
	// TTL = 0: entries are kept until LRU eviction — no time-based expiry.
	entityStore, err := cache.NewCache(cache.CacheConfig{
		MaxSize:    config.EntityCacheSize,
		DefaultTTL: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create entity store: %w", err)
	}
	
	server := &MainWorkerServer{
		tokenManager:  tokenManager,
		workerAuth:    workerAuth,
		registry:      routing.NewWorkerRegistry(),
		entityStore:   entityStore,
		masterKey:     config.MasterKey,
		masterKeyID:   config.KeyID,
		config:        config,
	}

	// Initialize schema validator (non-fatal: schema endpoints disabled if it fails).
	if config.SharedFSPath != "" {
		templatesPath := filepath.Join(config.SharedFSPath, "templates")
		if v, err := schema.NewValidator(templatesPath); err != nil {
			log.Printf("Warning: failed to initialize schema validator: %v", err)
		} else {
			server.validator = v
		}
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
	
	// Mark the worker as Available in the registry.
	if err := s.registry.Register(workerID, s.masterKeyID, req.GetTags()); err != nil {
		log.Printf("Warning: failed to register worker %s in registry: %v", workerID, err)
	}
	
	// Return the subscription response
	return &proto.SubscribeResponse{
		Token:      token.Token,
		WrappedKey: wrappedKey,
		KeyId:      s.masterKeyID,
	}, nil
}

// Process implements the Process RPC for handling entity operations.
// For GET requests it forwards the call to an available Processing Worker.
// PUT requests are cached immediately in the LRU entity store (full
// encrypted persistence is handled by a Processing Worker).
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

	// Validate operation
	op := req.GetOperation()
	if op != "GET" && op != "PUT" {
		return nil, status.Error(codes.InvalidArgument, "operation must be GET or PUT")
	}

	if op == "GET" {
		return s.routeGETToProcWorker(ctx, req)
	}

	// PUT: cache immediately (LRU evicts the least-recently-used entry if full).
	// Full encrypted persistence is handled by a Processing Worker.
	storeKey := req.GetDatabaseName() + "/" + req.GetEntityKey()
	s.entityStore.Set(storeKey, req.GetPayload(), "1")

	return &proto.ProcessResponse{
		Status:  "OK",
		Version: "1",
	}, nil
}

// routeGETToProcWorker forwards a GET Process request to an available
// Processing Worker.  The worker's gRPC address must have been advertised as
// the "grpc_addr" tag during Subscribe.  If no worker is available the
// entity is served from the LRU entity store (cached by a previous PUT).
func (s *MainWorkerServer) routeGETToProcWorker(ctx context.Context, req *proto.ProcessRequest) (*proto.ProcessResponse, error) {
	// Find a proc-worker that advertised its gRPC address.
	var procAddr string
	for _, w := range s.registry.ListAvailableWorkers() {
		if addr, ok := w.Tags["grpc_addr"]; ok && addr != "" {
			procAddr = addr
			break
		}
	}

	if procAddr != "" {
		// Forward to the Processing Worker.
		conn, err := grpc.NewClient(
			procAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(grpc.ForceCodec(proto.JSONCodec{})),
		)
		if err == nil {
			defer conn.Close()
			resp, err := proto.NewMainWorkerClient(conn).Process(ctx, req)
			if err == nil {
				return resp, nil
			}
			log.Printf("Process forwarding to %s failed: %v — falling back", procAddr, err)
		}
	}

	// Fallback: serve from the LRU entity store.
	storeKey := req.GetDatabaseName() + "/" + req.GetEntityKey()
	entry, found := s.entityStore.Get(storeKey)
	if !found {
		return nil, status.Errorf(codes.NotFound, "entity %q/%q not found",
			req.GetDatabaseName(), req.GetEntityKey())
	}

	return &proto.ProcessResponse{
		Status:  "OK",
		Result:  entry.Data,
		Version: entry.Version,
	}, nil
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

// Run starts both the gRPC server and the REST HTTP server.
func (s *MainWorkerServer) Run() error {
	// Start REST HTTP server in a separate goroutine.
	go func() {
		mux := http.NewServeMux()
		s.registerFrontendRoutes(mux)
		mux.HandleFunc("/health", s.handleHealth)
		mux.HandleFunc("/admin/workers", s.handleAdminWorkers)
		mux.HandleFunc("/admin/schemas", s.handleAdminSchemas)
		mux.HandleFunc("/schema/", s.handleSchema)
		mux.HandleFunc("/entity/", s.handleEntity)

		log.Printf("Main Worker REST server listening on %s", s.config.RESTAddr)
		if err := http.ListenAndServe(s.config.RESTAddr, mux); err != nil && err != http.ErrServerClosed {
			log.Printf("REST server error: %v", err)
		}
	}()

	// Create gRPC server.
	grpcServer := grpc.NewServer()

	// Register the MainWorker service.
	proto.RegisterMainWorkerServer(grpcServer, s)

	// Listen on the configured address.
	listener, err := net.Listen("tcp", s.config.GRPCAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.config.GRPCAddr, err)
	}

	log.Printf("Main Worker gRPC server listening on %s", s.config.GRPCAddr)
	log.Printf("Master Key ID: %s", s.masterKeyID)

	// Start serving.
	if err := grpcServer.Serve(listener); err != nil {
		return fmt.Errorf("gRPC server error: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the server.
func (s *MainWorkerServer) Shutdown() {
	log.Println("Main Worker shutting down...")
}

// handleHealth serves the GET /health endpoint.
func (s *MainWorkerServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
}

// handleAdminWorkers serves the GET /admin/workers endpoint.
// It returns the list of all registered Processing Workers and their status.
// Requires a valid Bearer token.
func (s *MainWorkerServer) handleAdminWorkers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if _, ok := s.extractBearerToken(w, r); !ok {
		return
	}

	workers := s.registry.ListWorkers()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(workers) //nolint:errcheck
}

// extractBearerToken extracts and validates a client Bearer token from the
// Authorization header.  It returns the token string on success, or writes a
// 401 response and returns ("", false) on failure.
func (s *MainWorkerServer) extractBearerToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	authHeader := r.Header.Get("Authorization")
	bearerToken := strings.TrimPrefix(authHeader, "Bearer ")
	if !strings.HasPrefix(authHeader, "Bearer ") || bearerToken == "" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return "", false
	}
	if _, err := s.tokenManager.ValidateClientToken(bearerToken); err != nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return "", false
	}
	return bearerToken, true
}

// handleEntity handles GET and PUT requests for /entity/{db}[?key=...].
func (s *MainWorkerServer) handleEntity(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.extractBearerToken(w, r); !ok {
		return
	}

	// Extract database name from path: /entity/{db}
	pathParts := strings.TrimPrefix(r.URL.Path, "/entity/")
	db := strings.Split(pathParts, "?")[0]
	if db == "" {
		http.Error(w, `{"error":"missing database"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, `{"error":"missing key"}`, http.StatusBadRequest)
			return
		}
		storeKey := db + "/" + key
		entry, found := s.entityStore.Get(storeKey)
		if !found {
			http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(entry.Data) //nolint:errcheck

	case http.MethodPut:
		// Limit request body to 1 MiB to prevent resource exhaustion.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, `{"error":"bad_json"}`, http.StatusBadRequest)
			return
		}
		if len(payload) == 0 {
			http.Error(w, `{"error":"empty"}`, http.StatusBadRequest)
			return
		}
		// Cache each key-value pair immediately.  LRU eviction keeps memory
		// bounded: the least-recently-used entry is dropped when the cache is full.
		// json.RawMessage is defined as []byte, so a direct slice conversion is safe.
		for key, value := range payload {
			s.entityStore.Set(db+"/"+key, value, "1")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAdminSchemas serves GET /admin/schemas.
// It returns the list of all available schema IDs (no authentication required).
func (s *MainWorkerServer) handleAdminSchemas(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.validator == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]string{}) //nolint:errcheck
		return
	}
	schemas, err := s.validator.ListAvailableTemplates()
	if err != nil {
		http.Error(w, `{"error":"failed to list schemas"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(schemas) //nolint:errcheck
}

// handleSchema serves GET and PUT requests for /schema/{id}.
//
//   GET  /schema/{id}  — retrieve a schema JSON (no authentication required).
//   PUT  /schema/{id}  — create or replace a schema (authentication required).
func (s *MainWorkerServer) handleSchema(w http.ResponseWriter, r *http.Request) {
	schemaID := strings.TrimPrefix(r.URL.Path, "/schema/")
	// Reject empty or path-traversal schema IDs.
	if schemaID == "" || strings.ContainsAny(schemaID, `/\`) || strings.Contains(schemaID, "..") {
		http.Error(w, `{"error":"invalid schema id"}`, http.StatusBadRequest)
		return
	}

	if s.validator == nil {
		http.Error(w, `{"error":"schema management unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		data, err := s.validator.GetTemplateData(schemaID)
		if err != nil {
			http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data) //nolint:errcheck

	case http.MethodPut:
		if _, ok := s.extractBearerToken(w, r); !ok {
			return
		}
		// Limit schema body to 1 MiB.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"bad_json"}`, http.StatusBadRequest)
			return
		}
		if err := s.validator.SaveTemplate(schemaID, []byte(body)); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// GetStats returns server statistics.
func (s *MainWorkerServer) GetStats() map[string]interface{} {
	cacheStats := s.entityStore.Stats()
	return map[string]interface{}{
		"active_worker_tokens":    s.tokenManager.GetWorkerTokenCount(),
		"active_client_tokens":    s.tokenManager.GetClientTokenCount(),
		"registered_workers":      len(s.workerAuth.ListWorkers()),
		"available_workers":       len(s.registry.ListAvailableWorkers()),
		"master_key_id":           s.masterKeyID,
		"entity_cache_size":       cacheStats.Size,
		"entity_cache_max":        cacheStats.MaxSize,
		"entity_cache_hits":       cacheStats.Hits,
		"entity_cache_misses":     cacheStats.Misses,
		"entity_cache_evictions":  cacheStats.Evicts,
	}
}

// Helper function to generate RSA key pair for testing
func GenerateTestKeyPair() (*rsa.PrivateKey, *rsa.PublicKey, error) {
	return crypto.GenerateRSAKeyPair(2048)
}
