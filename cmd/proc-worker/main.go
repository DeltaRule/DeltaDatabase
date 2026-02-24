package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"delta-db/pkg/cache"
	"delta-db/pkg/fs"
)

func main() {
	// Command-line flags.
	mainAddr  := flag.String("main-addr",  "127.0.0.1:50051", "Main Worker gRPC address (host:port)")
	workerID  := flag.String("worker-id",  "",                "Unique ID for this Processing Worker")
	sharedFS  := flag.String("shared-fs",  "./shared/db",     "Shared filesystem path (used when S3 is not configured)")
	grpcAddr  := flag.String("grpc-addr",  "127.0.0.1:0",     "Processing Worker gRPC listen address (host:port)")
	cacheSize := flag.Int("cache-size",    256,               "Maximum number of entries in the in-memory cache")
	cacheTTL  := flag.Duration("cache-ttl", 0,                "TTL for cached entries (0 = LRU-only eviction, no time-based expiry)")

	// S3-compatible object-store flags (optional).
	// When -s3-endpoint is set the worker stores data in the S3 bucket instead
	// of the local filesystem. Compatible with AWS S3, RustFS, SeaweedFS, MinIO,
	// and any service that implements the S3 API.
	s3Endpoint  := flag.String("s3-endpoint",   "", "S3-compatible endpoint URL (e.g. http://rustfs:9000). Empty = use local FS.")
	s3Bucket    := flag.String("s3-bucket",      "", "S3 bucket name")
	s3Region    := flag.String("s3-region",      "us-east-1", "S3 region (or placeholder for self-hosted services)")
	s3AccessKey := flag.String("s3-access-key",  "", "S3 access key ID (falls back to AWS credential chain if empty)")
	s3SecretKey := flag.String("s3-secret-key",  "", "S3 secret access key")
	s3PathStyle := flag.Bool("s3-path-style",    true, "Use S3 path-style addressing (required for RustFS, SeaweedFS, MinIO)")

	flag.Usage = PrintUsage
	flag.Parse()

	log.Println("=== DeltaDatabase Processing Worker ===")
	log.Printf("Main Worker address: %s", *mainAddr)

	if *workerID == "" {
		// Default to hostname-based ID if not specified.
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "proc-worker"
		}
		*workerID = fmt.Sprintf("proc-%s", hostname)
	}

	log.Printf("Worker ID: %s", *workerID)

	// Initialise storage backend: S3 when an endpoint is provided, otherwise
	// the local shared filesystem.
	var storage fs.Backend
	if *s3Endpoint != "" {
		log.Printf("Storage backend: S3-compatible (%s, bucket=%s)", *s3Endpoint, *s3Bucket)
		s3Storage, err := fs.NewS3Storage(fs.S3Config{
			Endpoint:        *s3Endpoint,
			Bucket:          *s3Bucket,
			Region:          *s3Region,
			AccessKeyID:     *s3AccessKey,
			SecretAccessKey: *s3SecretKey,
			ForcePathStyle:  *s3PathStyle,
		})
		if err != nil {
			log.Fatalf("Failed to initialise S3 storage: %v", err)
		}
		storage = s3Storage
	} else {
		log.Printf("Storage backend: local filesystem (%s)", *sharedFS)
		localStorage, err := fs.NewStorage(*sharedFS)
		if err != nil {
			log.Fatalf("Failed to initialise storage: %v", err)
		}
		storage = localStorage
	}

	// Initialise in-memory LRU cache.
	// DefaultTTL = 0: entries are kept until the LRU algorithm evicts them
	// when the cache is full.  No time-based eviction is used.
	c, err := cache.NewCache(cache.CacheConfig{
		MaxSize:    *cacheSize,
		DefaultTTL: *cacheTTL,
	})
	if err != nil {
		log.Fatalf("Failed to initialise cache: %v", err)
	}
	defer c.Close()

	// Build configuration.
	config := &ProcConfig{
		MainAddr:     *mainAddr,
		WorkerID:     *workerID,
		SharedFSPath: *sharedFS,
		Tags:         map[string]string{"grpc_addr": *grpcAddr},
	}

	// Create the Processing Worker.
	worker, err := NewProcWorker(config)
	if err != nil {
		log.Fatalf("Failed to create Processing Worker: %v", err)
	}

	// Create the gRPC server that handles Process/GET requests.
	srv := NewProcWorkerServer(worker, storage, c)

	// Set up cancellable context for the handshake goroutine.
	ctx, cancel := context.WithCancel(context.Background())

	// Set up signal handling for graceful shutdown.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Run the subscribe handshake in the background with automatic retry.
	go func() {
		if err := worker.HandshakeWithRetry(ctx); err != nil {
			log.Printf("[%s] Handshake loop exited: %v", config.WorkerID, err)
		}
	}()

	// Start the Processing Worker gRPC server.
	go func() {
		if err := srv.Serve(*grpcAddr); err != nil {
			log.Printf("[%s] gRPC server exited: %v", config.WorkerID, err)
		}
	}()

	log.Println("Processing Worker started successfully")
	log.Println("Press Ctrl+C to shutdown")

	// Block until a shutdown signal is received.
	<-sigChan
	log.Println("Shutdown signal received")
	cancel()
	log.Printf("[%s] Processing Worker stopped", config.WorkerID)
}

// PrintUsage prints usage information.
func PrintUsage() {
	fmt.Println("DeltaDatabase Processing Worker")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  proc-worker [options]")
	fmt.Println()
	fmt.Println("Options:")
	flag.PrintDefaults()
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  # Local filesystem backend:")
	fmt.Println("  proc-worker -main-addr=127.0.0.1:50051 -worker-id=proc-1 -grpc-addr=127.0.0.1:50052")
	fmt.Println()
	fmt.Println("  # S3-compatible backend (RustFS / SeaweedFS / MinIO):")
	fmt.Println("  proc-worker -main-addr=127.0.0.1:50051 -worker-id=proc-1 -grpc-addr=127.0.0.1:50052 \\")
	fmt.Println("    -s3-endpoint=http://rustfs:9000 -s3-bucket=delta-db \\")
	fmt.Println("    -s3-access-key=minioadmin -s3-secret-key=minioadmin")
	fmt.Println()
}
