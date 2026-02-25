package main

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"delta-db/api/proto"
	"delta-db/internal/auth"
	"delta-db/internal/routing"
	"delta-db/pkg/cache"
	"delta-db/pkg/crypto"
	"delta-db/pkg/metrics"
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
	keyManager    *auth.KeyManager // persistent API key store with RBAC
	adminKeyHash  string           // SHA-256 of the raw admin key (empty = disabled)

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

	// Prometheus metrics (nil-safe — no-op when nil)
	metrics *metrics.MainWorkerMetrics

	// lastCacheStats tracks the previous cache stats snapshot so we can
	// increment Prometheus counters by the delta on each update.
	lastCacheHits   int64
	lastCacheMisses int64
	lastCacheEvicts int64
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

	// MetricsAddr is the address for the Prometheus /metrics HTTP endpoint
	// (e.g. ":9090").  An empty string disables the metrics server.
	MetricsAddr string

	// AdminKey is the master Bearer key that bypasses all RBAC checks.
	// When empty, access is controlled entirely by RBAC API keys.
	AdminKey string

	// KeyStorePath is the path to the JSON file that persists API keys.
	// When empty the key store lives in memory only (keys are lost on restart).
	// Defaults to <SharedFSPath>/_auth/keys.json when SharedFSPath is set.
	KeyStorePath string
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

	// Derive key store path from SharedFSPath when not explicitly set.
	if config.KeyStorePath == "" && config.SharedFSPath != "" {
		config.KeyStorePath = filepath.Join(config.SharedFSPath, "_auth", "keys.json")
	}

	// Initialize token manager
	tokenManager := auth.NewTokenManager(config.WorkerTokenTTL, config.ClientTokenTTL)

	// Initialize worker authenticator
	workerAuth := auth.NewWorkerAuthenticator()

	// Initialize API key manager (persisted to disk).
	keyManager, err := auth.NewKeyManager(config.KeyStorePath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize key manager: %w", err)
	}

	// Hash the admin key so we can compare without storing the raw value.
	adminKeyHash := ""
	if config.AdminKey != "" {
		adminKeyHash = hashAdminKey(config.AdminKey)
	}

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
		tokenManager: tokenManager,
		workerAuth:   workerAuth,
		keyManager:   keyManager,
		adminKeyHash: adminKeyHash,
		registry:     routing.NewWorkerRegistry(),
		entityStore:  entityStore,
		masterKey:    config.MasterKey,
		masterKeyID:  config.KeyID,
		config:       config,
		metrics:      metrics.NewMainWorkerMetrics(),
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
	start := time.Now()
	// Validate request
	if req.GetWorkerId() == "" {
		s.metrics.SubscribeRequestsTotal.WithLabelValues("error").Inc()
		s.metrics.SubscribeDurationSeconds.WithLabelValues("error").Observe(time.Since(start).Seconds())
		return nil, status.Error(codes.InvalidArgument, "worker_id is required")
	}
	
	if len(req.GetPubkey()) == 0 {
		s.metrics.SubscribeRequestsTotal.WithLabelValues("error").Inc()
		s.metrics.SubscribeDurationSeconds.WithLabelValues("error").Observe(time.Since(start).Seconds())
		return nil, status.Error(codes.InvalidArgument, "pubkey is required")
	}
	
	log.Printf("Subscribe request from worker: %s", req.GetWorkerId())
	
	// Parse worker's public key
	pubkey, err := crypto.ParsePublicKeyFromPEM(req.GetPubkey())
	if err != nil {
		log.Printf("Failed to parse worker public key: %v", err)
		s.metrics.SubscribeRequestsTotal.WithLabelValues("error").Inc()
		s.metrics.SubscribeDurationSeconds.WithLabelValues("error").Observe(time.Since(start).Seconds())
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
		s.metrics.SubscribeRequestsTotal.WithLabelValues("error").Inc()
		s.metrics.SubscribeDurationSeconds.WithLabelValues("error").Observe(time.Since(start).Seconds())
		return nil, status.Error(codes.Internal, "failed to wrap encryption key")
	}
	
	// Generate a session token for the worker
	token, err := s.tokenManager.GenerateWorkerToken(workerID, s.masterKeyID, req.GetTags())
	if err != nil {
		log.Printf("Failed to generate token for worker %s: %v", workerID, err)
		s.metrics.SubscribeRequestsTotal.WithLabelValues("error").Inc()
		s.metrics.SubscribeDurationSeconds.WithLabelValues("error").Observe(time.Since(start).Seconds())
		return nil, status.Error(codes.Internal, "failed to generate token")
	}
	
	log.Printf("Successfully subscribed worker %s with token (expires: %v)", 
		workerID, token.ExpiresAt.Format(time.RFC3339))
	
	// Mark the worker as Available in the registry.
	if err := s.registry.Register(workerID, s.masterKeyID, req.GetTags()); err != nil {
		log.Printf("Warning: failed to register worker %s in registry: %v", workerID, err)
	}

	s.metrics.SubscribeRequestsTotal.WithLabelValues("success").Inc()
	s.metrics.SubscribeDurationSeconds.WithLabelValues("success").Observe(time.Since(start).Seconds())
	s.metrics.RegisteredWorkers.Set(float64(len(s.workerAuth.ListWorkers())))
	s.metrics.AvailableWorkers.Set(float64(len(s.registry.ListAvailableWorkers())))
	s.metrics.ActiveWorkerTokens.Set(float64(s.tokenManager.GetWorkerTokenCount()))
	
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
	start := time.Now()
	op := req.GetOperation()

	// Validate token
	if req.GetToken() == "" {
		s.metrics.ProcessRequestsTotal.WithLabelValues(op, "error").Inc()
		s.metrics.ProcessDurationSeconds.WithLabelValues(op, "error").Observe(time.Since(start).Seconds())
		return nil, status.Error(codes.Unauthenticated, "token is required")
	}

	// Validate the token
	_, err := s.tokenManager.ValidateWorkerToken(req.GetToken())
	if err != nil {
		log.Printf("Invalid token in Process request: %v", err)
		s.metrics.ProcessRequestsTotal.WithLabelValues(op, "error").Inc()
		s.metrics.ProcessDurationSeconds.WithLabelValues(op, "error").Observe(time.Since(start).Seconds())
		return nil, status.Error(codes.Unauthenticated, "invalid or expired token")
	}

	// Validate operation
	if op != "GET" && op != "PUT" {
		s.metrics.ProcessRequestsTotal.WithLabelValues(op, "error").Inc()
		s.metrics.ProcessDurationSeconds.WithLabelValues(op, "error").Observe(time.Since(start).Seconds())
		return nil, status.Error(codes.InvalidArgument, "operation must be GET or PUT")
	}

	if op == "GET" {
		resp, err := s.routeGETToProcWorker(ctx, req)
		statusLabel := "success"
		if err != nil {
			statusLabel = "error"
		}
		s.metrics.ProcessRequestsTotal.WithLabelValues(op, statusLabel).Inc()
		s.metrics.ProcessDurationSeconds.WithLabelValues(op, statusLabel).Observe(time.Since(start).Seconds())
		s.updateCacheMetrics()
		return resp, err
	}

	// PUT: cache immediately (LRU evicts the least-recently-used entry if full).
	// Full encrypted persistence is handled by a Processing Worker.
	storeKey := req.GetDatabaseName() + "/" + req.GetEntityKey()
	s.entityStore.Set(storeKey, req.GetPayload(), "1")

	s.metrics.ProcessRequestsTotal.WithLabelValues(op, "success").Inc()
	s.metrics.ProcessDurationSeconds.WithLabelValues(op, "success").Observe(time.Since(start).Seconds())
	s.updateCacheMetrics()

	return &proto.ProcessResponse{
		Status:  "OK",
		Version: "1",
	}, nil
}

// updateCacheMetrics refreshes the entity cache gauge metrics from live stats.
func (s *MainWorkerServer) updateCacheMetrics() {
	cs := s.entityStore.Stats()
	s.metrics.EntityCacheSize.Set(float64(cs.Size))
	if delta := cs.Hits - s.lastCacheHits; delta > 0 {
		s.metrics.EntityCacheHitsTotal.Add(float64(delta))
		s.lastCacheHits = cs.Hits
	}
	if delta := cs.Misses - s.lastCacheMisses; delta > 0 {
		s.metrics.EntityCacheMissesTotal.Add(float64(delta))
		s.lastCacheMisses = cs.Misses
	}
	if delta := cs.Evicts - s.lastCacheEvicts; delta > 0 {
		s.metrics.EntityCacheEvictionsTotal.Add(float64(delta))
		s.lastCacheEvicts = cs.Evicts
	}
}

// routeGETToProcWorker forwards a GET Process request to a Processing Worker
// using two-tier smart routing:
//
//  1. Cache-aware: prefer the worker that last served this entity — it is
//     likely to have the entity in its LRU cache, avoiding a disk read.
//  2. Load-aware: if the preferred worker is overloaded (deallocating) or no
//     cache-preferred worker exists, fall back to the least-loaded available
//     worker.
//
// If no Processing Worker is reachable the entity is served from the Main
// Worker's own LRU entity store (cached by a previous PUT).
func (s *MainWorkerServer) routeGETToProcWorker(ctx context.Context, req *proto.ProcessRequest) (*proto.ProcessResponse, error) {
	entityID := req.GetDatabaseName() + "/" + req.GetEntityKey()

	// Tier 1: prefer the worker that last served this entity (cache-aware).
	worker := s.registry.FindWorkerForEntity(entityID)

	// Tier 2: fall back to the least-loaded available worker.
	if worker == nil {
		worker = s.registry.FindLeastLoadedWorker()
	}

	if worker != nil {
		addr := worker.Tags["grpc_addr"]
		if addr != "" {
			// Track in-flight load so the registry can detect overloaded workers.
			s.registry.IncrementLoad(worker.WorkerID)
			conn, err := grpc.NewClient(
				addr,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithDefaultCallOptions(grpc.ForceCodec(proto.JSONCodec{})),
			)
			if err == nil {
				defer conn.Close()
				resp, err := proto.NewMainWorkerClient(conn).Process(ctx, req)
				s.registry.DecrementLoad(worker.WorkerID)
				if err == nil {
					// Record that this worker served the entity so future
					// requests benefit from its cache.
					s.registry.UpdateEntityLocation(entityID, worker.WorkerID)
					return resp, nil
				}
				log.Printf("Process forwarding to %s failed: %v — falling back", addr, err)
			} else {
				s.registry.DecrementLoad(worker.WorkerID)
			}
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
		// API key management endpoints
		mux.HandleFunc("/api/keys", s.handleAPIKeys)
		mux.HandleFunc("/api/keys/", s.handleAPIKeyByID)

		log.Printf("Main Worker REST server listening on %s", s.config.RESTAddr)
		if err := http.ListenAndServe(s.config.RESTAddr, s.instrumentHTTP(mux)); err != nil && err != http.ErrServerClosed {
			log.Printf("REST server error: %v", err)
		}
	}()

	// Start Prometheus metrics server if configured.
	if s.config.MetricsAddr != "" {
		go s.metrics.Serve(s.config.MetricsAddr)
	}

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

// instrumentHTTP wraps the given handler to record HTTP request counts and durations.
func (s *MainWorkerServer) instrumentHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rw, r)
		// Normalise path to avoid high-cardinality labels (strip query string,
		// collapse per-entity / per-schema paths to their prefix).
		path := normalisePath(r.URL.Path)
		s.metrics.HTTPRequestsTotal.WithLabelValues(r.Method, path, strconv.Itoa(rw.code)).Inc()
		s.metrics.HTTPDurationSeconds.WithLabelValues(r.Method, path).Observe(time.Since(start).Seconds())
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	code int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.code = code
	rw.ResponseWriter.WriteHeader(code)
}

// normalisePath collapses high-cardinality URL paths into labelled prefixes.
func normalisePath(p string) string {
	switch {
	case p == "/health":
		return "/health"
	case p == "/admin/workers":
		return "/admin/workers"
	case p == "/admin/schemas":
		return "/admin/schemas"
	case p == "/api/keys":
		return "/api/keys"
	case strings.HasPrefix(p, "/api/keys/"):
		return "/api/keys/:id"
	case strings.HasPrefix(p, "/schema/"):
		return "/schema/:id"
	case strings.HasPrefix(p, "/entity/"):
		return "/entity/:db"
	default:
		return p
	}
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
// Requires admin permission.
func (s *MainWorkerServer) handleAdminWorkers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.requirePermission(w, r, auth.PermAdmin) {
		return
	}

	workers := s.registry.ListWorkers()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(workers) //nolint:errcheck
}

// extractBearerToken extracts the raw Bearer token from the Authorization
// header.  It validates the token in the following priority order:
//
//  1. Admin key — bypasses all RBAC checks.
//  2. API key — validated via the KeyManager; RBAC permissions apply.
//  3. Session token — short-lived token issued by POST /api/login.
//
// On success it returns the raw token string and the resolved permissions.
// On failure it writes a 401 response and returns ("", nil, false).
func (s *MainWorkerServer) extractBearerToken(w http.ResponseWriter, r *http.Request) (string, []auth.Permission, bool) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return "", nil, false
	}
	bearerToken := strings.TrimPrefix(authHeader, "Bearer ")
	if bearerToken == "" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return "", nil, false
	}

	// 1. Admin key check — full access.
	if s.adminKeyHash != "" && hashAdminKey(bearerToken) == s.adminKeyHash {
		return bearerToken, auth.AllPermissions, true
	}

	// 2. Persistent API key check — RBAC permissions from the key record.
	if apiKey, err := s.keyManager.ValidateKey(bearerToken); err == nil {
		return bearerToken, apiKey.Permissions, true
	}

	// 3. Session token check — issued by POST /api/login.
	// Use the roles stored on the token at login time so that admin and
	// API-key sessions retain their correct permission set (e.g. admin
	// sessions must reach /admin/workers and /api/keys).
	if ct, err := s.tokenManager.ValidateClientToken(bearerToken); err == nil {
		perms := make([]auth.Permission, 0, len(ct.Roles))
		for _, r := range ct.Roles {
			perms = append(perms, auth.Permission(r))
		}
		if len(perms) == 0 {
			perms = []auth.Permission{auth.PermRead, auth.PermWrite}
		}
		return bearerToken, perms, true
	}

	http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	return "", nil, false
}

// requirePermission is like extractBearerToken but also enforces that the
// caller holds a specific permission.  Returns false (and writes 401/403) on
// any auth or authorisation failure.
func (s *MainWorkerServer) requirePermission(w http.ResponseWriter, r *http.Request, perm auth.Permission) bool {
	_, perms, ok := s.extractBearerToken(w, r)
	if !ok {
		return false
	}
	for _, p := range perms {
		if p == perm || p == auth.PermAdmin {
			return true
		}
	}
	http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
	return false
}

// handleEntity handles GET and PUT requests for /entity/{db}[?key=...].
func (s *MainWorkerServer) handleEntity(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !s.requirePermission(w, r, auth.PermRead) {
			return
		}
	case http.MethodPut:
		if !s.requirePermission(w, r, auth.PermWrite) {
			return
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract database name from path: /entity/{db}
	pathParts := strings.TrimPrefix(r.URL.Path, "/entity/")
	db := strings.Split(pathParts, "?")[0]
	if db == "" || strings.ContainsAny(db, `/\`) || strings.Contains(db, "..") {
		http.Error(w, `{"error":"invalid database name"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, `{"error":"missing key"}`, http.StatusBadRequest)
			return
		}
		// Reject path-traversal characters in the entity key.
		if strings.ContainsAny(key, `/\`) || strings.Contains(key, "..") {
			http.Error(w, `{"error":"invalid key"}`, http.StatusBadRequest)
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
		// Validate entity keys before storing.
		for key := range payload {
			if strings.ContainsAny(key, `/\`) || strings.Contains(key, "..") {
				http.Error(w, `{"error":"invalid key"}`, http.StatusBadRequest)
				return
			}
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
		if !s.requirePermission(w, r, auth.PermWrite) {
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
		"api_keys":                s.keyManager.Count(),
	}
}

// Helper function to generate RSA key pair for testing
func GenerateTestKeyPair() (*rsa.PrivateKey, *rsa.PublicKey, error) {
	return crypto.GenerateRSAKeyPair(2048)
}

// ── API Key Management Endpoints ────────────────────────────────────────────

// createKeyRequest is the JSON body for POST /api/keys.
type createKeyRequest struct {
	Name        string           `json:"name"`
	Permissions []auth.Permission `json:"permissions"`
	// ExpiresIn is an optional duration string (e.g. "24h", "7d").
	// When omitted the key never expires.
	ExpiresIn string `json:"expires_in,omitempty"`
}

// createKeyResponse is the JSON body returned by POST /api/keys.
// The Secret is shown only once.
type createKeyResponse struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Secret      string           `json:"secret"` // raw key — shown only once
	Permissions []auth.Permission `json:"permissions"`
	ExpiresAt   *time.Time       `json:"expires_at,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
}

// handleAPIKeys serves POST /api/keys (create) and GET /api/keys (list).
// Both operations require admin permission.
func (s *MainWorkerServer) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !s.requirePermission(w, r, auth.PermAdmin) {
			return
		}
		keys := s.keyManager.ListKeys()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(keys) //nolint:errcheck

	case http.MethodPost:
		if !s.requirePermission(w, r, auth.PermAdmin) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16) // 64 KiB max
		var req createKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad_json"}`, http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
			return
		}
		if len(req.Permissions) == 0 {
			http.Error(w, `{"error":"permissions are required"}`, http.StatusBadRequest)
			return
		}

		var expiresAt *time.Time
		if req.ExpiresIn != "" {
			d, err := time.ParseDuration(req.ExpiresIn)
			if err != nil {
				// Try plain days notation: "7d"
				if len(req.ExpiresIn) > 1 && req.ExpiresIn[len(req.ExpiresIn)-1] == 'd' {
					var days int
					if _, err2 := fmt.Sscanf(req.ExpiresIn[:len(req.ExpiresIn)-1], "%d", &days); err2 == nil {
						d = time.Duration(days) * 24 * time.Hour
						err = nil
					}
				}
				if err != nil {
					http.Error(w, `{"error":"invalid expires_in (use Go duration e.g. 24h or 7d)"}`, http.StatusBadRequest)
					return
				}
			}
			t := time.Now().Add(d)
			expiresAt = &t
		}

		secret, key, err := s.keyManager.CreateKey(req.Name, req.Permissions, expiresAt)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
			return
		}
		log.Printf("Created API key %q (id=%s permissions=%v)", key.Name, key.ID, key.Permissions)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(createKeyResponse{ //nolint:errcheck
			ID:          key.ID,
			Name:        key.Name,
			Secret:      secret,
			Permissions: key.Permissions,
			ExpiresAt:   key.ExpiresAt,
			CreatedAt:   key.CreatedAt,
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAPIKeyByID serves DELETE /api/keys/{id} (delete key).
// Requires admin permission.
func (s *MainWorkerServer) handleAPIKeyByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/keys/")
	if id == "" {
		http.Error(w, `{"error":"key id is required"}`, http.StatusBadRequest)
		return
	}

	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.requirePermission(w, r, auth.PermAdmin) {
		return
	}

	if err := s.keyManager.DeleteKey(id); err != nil {
		http.Error(w, `{"error":"key not found"}`, http.StatusNotFound)
		return
	}
	log.Printf("Deleted API key id=%s", id)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
}

// hashAdminKey returns the hex-encoded SHA-256 of the raw admin key string.
func hashAdminKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
