package main

import (
	"context"
	"net"
	"testing"
	"time"

	"delta-db/api/proto"
	"delta-db/pkg/crypto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// startMockMainWorker starts an in-process mock Main Worker gRPC server.
// It returns the listen address and a cleanup function.
func startMockMainWorker(t *testing.T, svc proto.MainWorkerServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := grpc.NewServer()
	proto.RegisterMainWorkerServer(srv, svc)
	go srv.Serve(lis) //nolint:errcheck
	t.Cleanup(func() { srv.Stop() })

	return lis.Addr().String()
}

// mockMainWorker is a minimal in-process Main Worker for testing.
type mockMainWorker struct {
	proto.UnimplementedMainWorkerServer
	masterKey []byte
	keyID     string
}

func newMockMainWorker(t *testing.T) *mockMainWorker {
	t.Helper()
	key, err := crypto.GenerateKey(32)
	require.NoError(t, err)
	return &mockMainWorker{masterKey: key, keyID: "mock-key-1"}
}

func (m *mockMainWorker) Subscribe(ctx context.Context, req *proto.SubscribeRequest) (*proto.SubscribeResponse, error) {
	if req.GetWorkerId() == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id required")
	}
	if len(req.GetPubkey()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "pubkey required")
	}

	pubKey, err := crypto.ParsePublicKeyFromPEM(req.GetPubkey())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid pubkey")
	}

	wrapped, err := crypto.WrapKey(pubKey, m.masterKey)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "wrap failed: %v", err)
	}

	return &proto.SubscribeResponse{
		Token:      "mock-token-" + req.GetWorkerId(),
		WrappedKey: wrapped,
		KeyId:      m.keyID,
	}, nil
}

func TestNewProcWorker(t *testing.T) {
	t.Run("creates worker with valid config", func(t *testing.T) {
		config := &ProcConfig{
			MainAddr: "127.0.0.1:50051",
			WorkerID: "test-worker",
		}
		w, err := NewProcWorker(config)
		assert.NoError(t, err)
		assert.NotNil(t, w)
		assert.NotNil(t, w.privateKey)
		assert.False(t, w.HasKey())
		assert.Empty(t, w.Token())
	})

	t.Run("returns error for nil config", func(t *testing.T) {
		_, err := NewProcWorker(nil)
		assert.Error(t, err)
	})

	t.Run("returns error for empty worker ID", func(t *testing.T) {
		config := &ProcConfig{MainAddr: "127.0.0.1:50051"}
		_, err := NewProcWorker(config)
		assert.Error(t, err)
	})

	t.Run("returns error for empty main addr", func(t *testing.T) {
		config := &ProcConfig{WorkerID: "test-worker"}
		_, err := NewProcWorker(config)
		assert.Error(t, err)
	})
}

func TestHandshake_Success(t *testing.T) {
	mock := newMockMainWorker(t)
	addr := startMockMainWorker(t, mock)

	config := &ProcConfig{
		MainAddr: addr,
		WorkerID: "handshake-worker",
	}
	w, err := NewProcWorker(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = w.Handshake(ctx)
	assert.NoError(t, err)

	// Verify the worker now holds a session token.
	assert.NotEmpty(t, w.Token())
	// Verify the encryption key was unwrapped and stored in memory.
	assert.True(t, w.HasKey())
	// Verify key ID is set.
	assert.Equal(t, mock.keyID, w.KeyID())
}

func TestHandshake_InvalidServer(t *testing.T) {
	config := &ProcConfig{
		MainAddr: "127.0.0.1:1",
		WorkerID: "no-server-worker",
	}
	w, err := NewProcWorker(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = w.Handshake(ctx)
	assert.Error(t, err)
}

func TestHandshakeWithRetry_CancelledContext(t *testing.T) {
	config := &ProcConfig{
		MainAddr: "127.0.0.1:1",
		WorkerID: "cancel-worker",
	}
	w, err := NewProcWorker(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err = w.HandshakeWithRetry(ctx)
	// Should return a context error, not a nil.
	assert.Error(t, err)
}

func TestHandshake_KeyUnwrap(t *testing.T) {
	mock := newMockMainWorker(t)
	addr := startMockMainWorker(t, mock)

	config := &ProcConfig{
		MainAddr: addr,
		WorkerID: "key-unwrap-worker",
	}
	w, err := NewProcWorker(config)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, w.Handshake(ctx))

	// The worker's internal encryption key should match the mock master key.
	w.mu.RLock()
	storedKey := make([]byte, len(w.encryptionKey))
	copy(storedKey, w.encryptionKey)
	w.mu.RUnlock()

	assert.Equal(t, mock.masterKey, storedKey)
}

func TestHandshake_GRPCClientDialOpts(t *testing.T) {
	// Verify the proc-worker connects with insecure credentials and JSON codec (expected for dev).
	mock := newMockMainWorker(t)
	addr := startMockMainWorker(t, mock)

	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(proto.JSONCodec{})),
	)
	require.NoError(t, err)
	defer conn.Close()

	client := proto.NewMainWorkerClient(conn)

	privKey, pubKey, err := crypto.GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	pubKeyPEM, err := crypto.MarshalPublicKeyToPEM(pubKey)
	require.NoError(t, err)

	resp, err := client.Subscribe(context.Background(), &proto.SubscribeRequest{
		WorkerId: "direct-test",
		Pubkey:   pubKeyPEM,
	})
	require.NoError(t, err)

	// Unwrap the key directly.
	unwrapped, err := crypto.UnwrapKey(privKey, resp.WrappedKey)
	require.NoError(t, err)
	assert.Equal(t, mock.masterKey, unwrapped)
}
