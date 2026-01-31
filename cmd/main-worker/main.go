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
)

func main() {
	// Command line flags
	grpcAddr := flag.String("grpc-addr", "127.0.0.1:50051", "gRPC server address")
	restAddr := flag.String("rest-addr", "127.0.0.1:8080", "REST API server address")
	sharedFS := flag.String("shared-fs", "./shared/db", "Shared filesystem path")
	masterKeyHex := flag.String("master-key", "", "Master encryption key (hex-encoded 32 bytes)")
	keyID := flag.String("key-id", "main-key-v1", "Master key ID")
	workerTTL := flag.Duration("worker-ttl", 1*time.Hour, "Worker token TTL")
	clientTTL := flag.Duration("client-ttl", 24*time.Hour, "Client token TTL")
	
	flag.Parse()

	log.Println("=== DeltaDatabase Main Worker ===")
	log.Printf("gRPC Address: %s", *grpcAddr)
	log.Printf("REST Address: %s", *restAddr)
	log.Printf("Shared FS: %s", *sharedFS)

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

	// Create configuration
	config := &Config{
		GRPCAddr:       *grpcAddr,
		RESTAddr:       *restAddr,
		SharedFSPath:   *sharedFS,
		MasterKey:      masterKey,
		KeyID:          *keyID,
		WorkerTokenTTL: *workerTTL,
		ClientTokenTTL: *clientTTL,
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
	fmt.Println("Example:")
	fmt.Println("  main-worker -grpc-addr=:50051 -rest-addr=:8080")
	fmt.Println()
}

