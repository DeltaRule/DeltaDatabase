package main

import (
	"context"
	"crypto/rsa"
	"fmt"
	"log"
	"sync"
	"time"

	"delta-db/api/proto"
	"delta-db/pkg/crypto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ProcWorker represents a Processing Worker instance.
// It manages the gRPC connection to the Main Worker, stores the session
// token, and holds the decrypted encryption key in volatile memory only.
type ProcWorker struct {
	config *ProcConfig

	// RSA key pair for key unwrapping.
	privateKey *rsa.PrivateKey

	// mu protects the fields below.
	mu sync.RWMutex

	// encryptionKey is the unwrapped AES key received from the Main Worker.
	// It is kept in memory only and never persisted to disk.
	encryptionKey []byte

	// sessionToken is the token issued by the Main Worker on Subscribe.
	// It is used to authenticate subsequent Process RPCs.
	sessionToken string

	// keyID is the ID of the encryption key currently in use.
	keyID string
}

// ProcConfig holds configuration for the Processing Worker.
type ProcConfig struct {
	// MainAddr is the gRPC address of the Main Worker (host:port).
	MainAddr string

	// WorkerID is the unique identifier of this Processing Worker.
	WorkerID string

	// SharedFSPath is the path to the shared filesystem.
	SharedFSPath string

	// Tags are optional metadata labels sent during subscription.
	Tags map[string]string

	// MetricsAddr is the address for the Prometheus /metrics HTTP endpoint
	// (e.g. ":9091").  An empty string disables the metrics server.
	MetricsAddr string
}

// NewProcWorker creates a new ProcWorker instance and generates an RSA key pair
// for receiving the wrapped encryption key from the Main Worker.
func NewProcWorker(config *ProcConfig) (*ProcWorker, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if config.WorkerID == "" {
		return nil, fmt.Errorf("worker ID cannot be empty")
	}
	if config.MainAddr == "" {
		return nil, fmt.Errorf("main worker address cannot be empty")
	}

	// Generate RSA key pair for key wrapping/unwrapping.
	// The public key is sent to the Main Worker during Subscribe.
	privKey, _, err := crypto.GenerateRSAKeyPair(2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key pair: %w", err)
	}

	return &ProcWorker{
		config:     config,
		privateKey: privKey,
	}, nil
}

// Handshake connects to the Main Worker, calls Subscribe, unwraps the
// encryption key, and stores the session token in memory.
// It should be called once during startup and may be retried on failure.
func (w *ProcWorker) Handshake(ctx context.Context) error {
	log.Printf("[%s] Connecting to Main Worker at %s", w.config.WorkerID, w.config.MainAddr)

	conn, err := grpc.NewClient(
		w.config.MainAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(proto.JSONCodec{})),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to Main Worker: %w", err)
	}
	defer conn.Close()

	client := proto.NewMainWorkerClient(conn)

	// Marshal the worker's public key to PEM for transmission.
	pubKeyPEM, err := crypto.MarshalPublicKeyToPEM(&w.privateKey.PublicKey)
	if err != nil {
		return fmt.Errorf("failed to marshal public key: %w", err)
	}

	req := &proto.SubscribeRequest{
		WorkerId: w.config.WorkerID,
		Pubkey:   pubKeyPEM,
		Tags:     w.config.Tags,
	}

	log.Printf("[%s] Sending Subscribe request", w.config.WorkerID)

	resp, err := client.Subscribe(ctx, req)
	if err != nil {
		return fmt.Errorf("Subscribe RPC failed: %w", err)
	}

	// Unwrap the encryption key using the worker's RSA private key.
	// The key is stored in volatile memory only — never written to disk.
	decryptedKey, err := crypto.UnwrapKey(w.privateKey, resp.WrappedKey)
	if err != nil {
		return fmt.Errorf("failed to unwrap encryption key: %w", err)
	}

	w.mu.Lock()
	w.encryptionKey = decryptedKey
	w.sessionToken = resp.Token
	w.keyID = resp.KeyId
	w.mu.Unlock()

	log.Printf("[%s] Subscribed successfully (key_id=%s)", w.config.WorkerID, resp.KeyId)
	return nil
}

// HandshakeWithRetry performs Handshake with exponential back-off retries.
// It returns only when the handshake succeeds or ctx is cancelled.
func (w *ProcWorker) HandshakeWithRetry(ctx context.Context) error {
	const maxInterval = 30 * time.Second
	interval := time.Second

	for {
		err := w.Handshake(ctx)
		if err == nil {
			return nil
		}

		log.Printf("[%s] Handshake failed: %v — retrying in %s",
			w.config.WorkerID, err, interval)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}

		interval *= 2
		if interval > maxInterval {
			interval = maxInterval
		}
	}
}

// Token returns the session token acquired during the last successful Subscribe.
// Returns an empty string if the worker has not yet subscribed.
func (w *ProcWorker) Token() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.sessionToken
}

// KeyID returns the ID of the encryption key currently held by this worker.
func (w *ProcWorker) KeyID() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.keyID
}

// HasKey returns true when the worker has successfully received and unwrapped
// the encryption key from the Main Worker.
func (w *ProcWorker) HasKey() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.encryptionKey) > 0
}
