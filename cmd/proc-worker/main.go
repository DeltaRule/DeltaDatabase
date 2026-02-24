package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"delta-db/pkg/cache"
	"delta-db/pkg/fs"
)

func main() {
	// Command-line flags.
	mainAddr  := flag.String("main-addr",  "127.0.0.1:50051", "Main Worker gRPC address (host:port)")
	workerID  := flag.String("worker-id",  "",                "Unique ID for this Processing Worker")
	sharedFS  := flag.String("shared-fs",  "./shared/db",     "Shared filesystem path")
	grpcAddr  := flag.String("grpc-addr",  "127.0.0.1:0",     "Processing Worker gRPC listen address (host:port)")
	cacheSize := flag.Int("cache-size",    256,               "Maximum number of entries in the in-memory cache")
	cacheTTL  := flag.Duration("cache-ttl", 5*time.Minute,   "Time-to-live for cached entries")

	flag.Usage = PrintUsage
	flag.Parse()

	log.Println("=== DeltaDatabase Processing Worker ===")
	log.Printf("Main Worker address: %s", *mainAddr)
	log.Printf("Shared FS: %s", *sharedFS)

	if *workerID == "" {
		// Default to hostname-based ID if not specified.
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "proc-worker"
		}
		*workerID = fmt.Sprintf("proc-%s", hostname)
	}

	log.Printf("Worker ID: %s", *workerID)

	// Initialise shared filesystem storage.
	storage, err := fs.NewStorage(*sharedFS)
	if err != nil {
		log.Fatalf("Failed to initialise storage: %v", err)
	}

	// Initialise in-memory LRU + TTL cache.
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
	fmt.Println("Example:")
	fmt.Println("  proc-worker -main-addr=127.0.0.1:50051 -worker-id=proc-1 -grpc-addr=127.0.0.1:50052")
	fmt.Println()
}
