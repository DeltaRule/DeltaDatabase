package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"delta-db/pkg/crypto"
	"delta-db/pkg/fs"
)

func main() {
	// Command line flags
	grpcAddr := flag.String("grpc-addr", "127.0.0.1:50051", "gRPC server address")
	restAddr := flag.String("rest-addr", "127.0.0.1:8080", "REST API server address")
	metricsAddr := flag.String("metrics-addr", "", "Prometheus metrics server address (e.g. :9090); empty = disabled")
	sharedFS := flag.String("shared-fs", "./shared/db", "Shared filesystem path (ignored when -s3-endpoint is set)")
	masterKeyHex := flag.String("master-key", "", "Master encryption key (hex-encoded 32 bytes)")
	keyID := flag.String("key-id", "main-key-v1", "Master key ID")
	workerTTL := flag.Duration("worker-ttl", 1*time.Hour, "Worker token TTL")
	clientTTL := flag.Duration("client-ttl", 24*time.Hour, "Client token TTL")
	entityCacheSize := flag.Int("entity-cache-size", 1024, "Max number of entities in the in-memory LRU cache")
	adminKey := flag.String("admin-key", os.Getenv("ADMIN_KEY"), "Master admin Bearer key (bypasses all RBAC); defaults to $ADMIN_KEY")
	keyStorePath := flag.String("key-store", "", "Path to the API key JSON store (default: <shared-fs>/_auth/keys.json)")
	grpcMaxRecvMsgSize := flag.Int("grpc-max-recv-msg-size", 4*1024*1024, "Maximum gRPC message size in bytes that the server will accept (default 4 MiB)")
	restMaxBodySize := flag.Int64("rest-max-body-size", 1*1024*1024, "Maximum HTTP request body size in bytes for entity and schema PUT endpoints (default 1 MiB)")

	// S3-compatible storage flags (optional).
	s3Endpoint  := flag.String("s3-endpoint",   "", "S3-compatible endpoint (enables S3 backend for templates)")
	s3AccessKey := flag.String("s3-access-key", "", "S3 access key ID")
	s3SecretKey := flag.String("s3-secret-key", "", "S3 secret access key")
	s3Bucket    := flag.String("s3-bucket",     "deltadatabase", "S3 bucket name")
	s3UseSSL    := flag.Bool("s3-use-ssl",      false,           "Use TLS for S3 connection")
	s3Region    := flag.String("s3-region",     "",              "S3 region (optional)")

	flag.Parse()

	log.Println("=== DeltaDatabase Main Worker ===")
	log.Printf("gRPC Address: %s", *grpcAddr)
	log.Printf("REST Address: %s", *restAddr)

	// Parse or generate master key
	var masterKey []byte
	var err error

	if *masterKeyHex != "" {
		// Use provided key
		masterKey, err = hex.DecodeString(*masterKeyHex)
		if err != nil {
			log.Fatalf("Failed to decode master key: %v", err)
		}
		if len(masterKey) != 32 {
			log.Fatalf("Master key must be 32 bytes (64 hex chars), got %d bytes", len(masterKey))
		}
		log.Println("Using provided master encryption key")
	} else {
		// Generate new key
		masterKey, err = crypto.GenerateKey(32)
		if err != nil {
			log.Fatalf("Failed to generate master key: %v", err)
		}
		log.Println("Generated new master encryption key")
		log.Printf("Key (hex): %s", hex.EncodeToString(masterKey))
		log.Println("IMPORTANT: Save this key securely! It is needed to decrypt data.")
	}

	// Resolve the templates directory.  When S3 is configured the Main Worker
	// uses the S3Storage backend so that templates are persisted in the same
	// bucket as entity data and are available to all Processing Workers.
	templatesDir := ""
	if *s3Endpoint != "" {
		log.Printf("Storage backend: S3-compatible  endpoint=%s  bucket=%s", *s3Endpoint, *s3Bucket)

		accessKey := *s3AccessKey
		if accessKey == "" {
			accessKey = os.Getenv("S3_ACCESS_KEY")
		}
		secretKey := *s3SecretKey
		if secretKey == "" {
			secretKey = os.Getenv("S3_SECRET_KEY")
		}

		s3, err := fs.NewS3Storage(fs.S3Config{
			Endpoint:        *s3Endpoint,
			AccessKeyID:     accessKey,
			SecretAccessKey: secretKey,
			Bucket:          *s3Bucket,
			UseSSL:          *s3UseSSL,
			Region:          *s3Region,
		})
		if err != nil {
			log.Fatalf("Failed to initialise S3 storage: %v", err)
		}
		// GetTemplatesDir syncs all existing templates from S3 to a local
		// temp dir so the file-based schema.Validator can read them.
		templatesDir = s3.GetTemplatesDir()
		if templatesDir == "" {
			log.Fatalf("Failed to initialise S3 templates directory")
		}
	} else {
		log.Printf("Storage backend: local shared filesystem  path=%s", *sharedFS)
		templatesDir = *sharedFS
	}

	// Create configuration
	config := &Config{
		GRPCAddr:           *grpcAddr,
		RESTAddr:           *restAddr,
		MetricsAddr:        *metricsAddr,
		SharedFSPath:       templatesDir,
		MasterKey:          masterKey,
		KeyID:              *keyID,
		WorkerTokenTTL:     *workerTTL,
		ClientTokenTTL:     *clientTTL,
		EntityCacheSize:    *entityCacheSize,
		AdminKey:           *adminKey,
		KeyStorePath:       *keyStorePath,
		MaxRecvMsgSize:     *grpcMaxRecvMsgSize,
		MaxRequestBodySize: *restMaxBodySize,
	}

	// Create and start Main Worker server
	server, err := NewMainWorkerServer(config)
	if err != nil {
		log.Fatalf("Failed to create Main Worker server: %v", err)
	}

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start server in goroutine
	go func() {
		if err := server.Run(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	log.Println("Main Worker started successfully")
	log.Println("Press Ctrl+C to shutdown")

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutdown signal received")

	server.Shutdown()
	log.Println("Main Worker stopped")
}

// PrintUsage prints usage information.
func PrintUsage() {
	fmt.Println("DeltaDatabase Main Worker")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  main-worker [options]")
	fmt.Println()
	fmt.Println("Options:")
	flag.PrintDefaults()
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  # Local shared filesystem with admin key:")
	fmt.Println("  main-worker -grpc-addr=:50051 -rest-addr=:8080 -admin-key=mysecretkey")
	fmt.Println()
	fmt.Println("  # Using environment variable for admin key:")
	fmt.Println("  ADMIN_KEY=mysecretkey main-worker -grpc-addr=:50051 -rest-addr=:8080")
	fmt.Println()
	fmt.Println("  # S3-compatible backend (MinIO):")
	fmt.Println("  main-worker -grpc-addr=:50051 -rest-addr=:8080 \\")
	fmt.Println("    -s3-endpoint=minio:9000 -s3-bucket=deltadatabase -s3-access-key=minioadmin -s3-secret-key=minioadmin")
	fmt.Println()
}

