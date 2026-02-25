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
	sharedFS  := flag.String("shared-fs",  "./shared/db",     "Shared filesystem path (ignored when -s3-endpoint is set)")
	grpcAddr  := flag.String("grpc-addr",  "127.0.0.1:0",     "Processing Worker gRPC listen address (host:port)")
	metricsAddr := flag.String("metrics-addr", "",            "Prometheus metrics server address (e.g. :9091); empty = disabled")
	cacheSize := flag.Int("cache-size",    256,               "Maximum number of entries in the in-memory cache")
	cacheTTL  := flag.Duration("cache-ttl", 0,                "TTL for cached entries (0 = LRU-only eviction, no time-based expiry)")
	grpcMaxRecvMsgSize := flag.Int("grpc-max-recv-msg-size", 4*1024*1024, "Maximum gRPC message size in bytes that this worker will accept (default 4 MiB)")

	// S3-compatible storage flags (all optional; when -s3-endpoint is provided
	// the shared filesystem backend is replaced with S3-compatible object storage).
	s3Endpoint  := flag.String("s3-endpoint",   "", "S3-compatible endpoint, e.g. minio:9000 or s3.amazonaws.com (enables S3 backend)")
	s3AccessKey := flag.String("s3-access-key", "", "S3 access key ID")
	s3SecretKey := flag.String("s3-secret-key", "", "S3 secret access key")
	s3Bucket    := flag.String("s3-bucket",     "deltadatabase", "S3 bucket name")
	s3UseSSL    := flag.Bool("s3-use-ssl",      false,           "Use TLS for S3 connection (set to true for AWS S3)")
	s3Region    := flag.String("s3-region",     "",              "S3 region (optional, leave empty for MinIO / SeaweedFS)")

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

	// Build the storage backend and matching lock backend.
	var storage   fs.StorageBackend
	var lockMgr   fs.LockBackend

	if *s3Endpoint != "" {
		// S3-compatible backend (MinIO, RustFS, SeaweedFS, AWS S3, …).
		log.Printf("Storage backend: S3-compatible  endpoint=%s  bucket=%s", *s3Endpoint, *s3Bucket)

		// Allow environment-variable overrides for credentials so that
		// secrets are not exposed in the process argument list.
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
		storage = s3
		lockMgr = fs.NewMemoryLockManager()
	} else {
		// Local shared-filesystem backend (default).
		log.Printf("Storage backend: local shared filesystem  path=%s", *sharedFS)

		localStorage, err := fs.NewStorage(*sharedFS)
		if err != nil {
			log.Fatalf("Failed to initialise storage: %v", err)
		}
		storage = localStorage
		lockMgr = fs.NewLockManager(localStorage)
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
		MainAddr:       *mainAddr,
		WorkerID:       *workerID,
		SharedFSPath:   *sharedFS,
		MetricsAddr:    *metricsAddr,
		Tags:           map[string]string{"grpc_addr": *grpcAddr},
		MaxRecvMsgSize: *grpcMaxRecvMsgSize,
	}

	// Create the Processing Worker.
	worker, err := NewProcWorker(config)
	if err != nil {
		log.Fatalf("Failed to create Processing Worker: %v", err)
	}

	// Create the gRPC server that handles Process/GET requests.
	srv := NewProcWorkerServer(worker, storage, lockMgr, c)

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
		if err := srv.Serve(*grpcAddr, *metricsAddr); err != nil {
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
	fmt.Println("  # Local shared filesystem (default):")
	fmt.Println("  proc-worker -main-addr=127.0.0.1:50051 -worker-id=proc-1 -grpc-addr=127.0.0.1:50052")
	fmt.Println()
	fmt.Println("  # S3-compatible backend (MinIO):")
	fmt.Println("  proc-worker -main-addr=127.0.0.1:50051 -worker-id=proc-1 -grpc-addr=127.0.0.1:50052 \\")
	fmt.Println("    -s3-endpoint=minio:9000 -s3-bucket=deltadatabase -s3-access-key=minioadmin -s3-secret-key=minioadmin")
	fmt.Println()
}

