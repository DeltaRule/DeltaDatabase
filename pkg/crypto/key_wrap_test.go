package crypto

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapUnwrapKey(t *testing.T) {
	// Generate RSA key pair
	privateKey, publicKey, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)
	require.NotNil(t, privateKey)
	require.NotNil(t, publicKey)

	// Generate an AES key to wrap
	aesKey, err := GenerateKey(32)
	require.NoError(t, err)

	// Wrap the key
	wrappedKey, err := WrapKey(publicKey, aesKey)
	require.NoError(t, err)
	assert.NotEmpty(t, wrappedKey)

	// Unwrap the key
	unwrappedKey, err := UnwrapKey(privateKey, wrappedKey)
	require.NoError(t, err)
	assert.Equal(t, aesKey, unwrappedKey)
}

func TestWrapKeyWithNilPublicKey(t *testing.T) {
	aesKey, err := GenerateKey(32)
	require.NoError(t, err)

	_, err = WrapKey(nil, aesKey)
	assert.ErrorIs(t, err, ErrInvalidPublicKey)
}

func TestUnwrapKeyWithNilPrivateKey(t *testing.T) {
	wrappedKey := []byte("dummy wrapped key")

	_, err := UnwrapKey(nil, wrappedKey)
	assert.ErrorIs(t, err, ErrInvalidPrivateKey)
}

func TestUnwrapKeyWithWrongPrivateKey(t *testing.T) {
	// Generate two different key pairs
	_, publicKey1, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	privateKey2, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	// Generate an AES key to wrap
	aesKey, err := GenerateKey(32)
	require.NoError(t, err)

	// Wrap with publicKey1
	wrappedKey, err := WrapKey(publicKey1, aesKey)
	require.NoError(t, err)

	// Try to unwrap with privateKey2 (wrong key)
	_, err = UnwrapKey(privateKey2, wrappedKey)
	assert.ErrorIs(t, err, ErrKeyUnwrapFailed)
}

func TestWrapUnwrapDifferentAESKeySizes(t *testing.T) {
	keySizes := []int{16, 24, 32}

	for _, size := range keySizes {
		t.Run(string(rune(size)), func(t *testing.T) {
			privateKey, publicKey, err := GenerateRSAKeyPair(2048)
			require.NoError(t, err)

			aesKey, err := GenerateKey(size)
			require.NoError(t, err)

			wrappedKey, err := WrapKey(publicKey, aesKey)
			require.NoError(t, err)

			unwrappedKey, err := UnwrapKey(privateKey, wrappedKey)
			require.NoError(t, err)
			assert.Equal(t, aesKey, unwrappedKey)
		})
	}
}

func TestGenerateRSAKeyPairValidSizes(t *testing.T) {
	validSizes := []int{2048, 3072, 4096}

	for _, size := range validSizes {
		t.Run(string(rune(size)), func(t *testing.T) {
			privateKey, publicKey, err := GenerateRSAKeyPair(size)
			require.NoError(t, err)
			require.NotNil(t, privateKey)
			require.NotNil(t, publicKey)

			// Verify key size
			assert.Equal(t, size, privateKey.N.BitLen())
			assert.Equal(t, size, publicKey.N.BitLen())
		})
	}
}

func TestGenerateRSAKeyPairInvalidSize(t *testing.T) {
	invalidSizes := []int{512, 1024, 1536}

	for _, size := range invalidSizes {
		t.Run(string(rune(size)), func(t *testing.T) {
			_, _, err := GenerateRSAKeyPair(size)
			assert.Error(t, err)
		})
	}
}

func TestGenerateRSAKeyPairProducesUniqueKeys(t *testing.T) {
	// Generate multiple key pairs and ensure they're unique
	keys := make(map[string]bool)

	for i := 0; i < 5; i++ {
		privateKey, _, err := GenerateRSAKeyPair(2048)
		require.NoError(t, err)

		// Use private key's N as uniqueness check
		keyStr := privateKey.N.String()
		assert.False(t, keys[keyStr], "Duplicate key detected")
		keys[keyStr] = true
	}
}

func TestMarshalParsePublicKeyPEM(t *testing.T) {
	_, publicKey, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	// Marshal to PEM
	pemData, err := MarshalPublicKeyToPEM(publicKey)
	require.NoError(t, err)
	assert.NotEmpty(t, pemData)
	assert.Contains(t, string(pemData), "BEGIN PUBLIC KEY")
	assert.Contains(t, string(pemData), "END PUBLIC KEY")

	// Parse from PEM
	parsedKey, err := ParsePublicKeyFromPEM(pemData)
	require.NoError(t, err)
	assert.Equal(t, publicKey.N, parsedKey.N)
	assert.Equal(t, publicKey.E, parsedKey.E)
}

func TestMarshalPublicKeyToPEMWithNilKey(t *testing.T) {
	_, err := MarshalPublicKeyToPEM(nil)
	assert.ErrorIs(t, err, ErrInvalidPublicKey)
}

func TestParsePublicKeyFromInvalidPEM(t *testing.T) {
	invalidPEM := []byte("This is not a valid PEM")

	_, err := ParsePublicKeyFromPEM(invalidPEM)
	assert.ErrorIs(t, err, ErrInvalidPEMBlock)
}

func TestParsePublicKeyFromWrongKeyType(t *testing.T) {
	// Create a PEM with wrong key type (private key instead of public)
	privateKey, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	privatePEM, err := MarshalPrivateKeyToPEM(privateKey)
	require.NoError(t, err)

	_, err = ParsePublicKeyFromPEM(privatePEM)
	assert.Error(t, err)
}

func TestMarshalParsePrivateKeyPEM(t *testing.T) {
	privateKey, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	// Marshal to PEM
	pemData, err := MarshalPrivateKeyToPEM(privateKey)
	require.NoError(t, err)
	assert.NotEmpty(t, pemData)
	assert.Contains(t, string(pemData), "BEGIN RSA PRIVATE KEY")
	assert.Contains(t, string(pemData), "END RSA PRIVATE KEY")

	// Parse from PEM
	parsedKey, err := ParsePrivateKeyFromPEM(pemData)
	require.NoError(t, err)
	assert.Equal(t, privateKey.N, parsedKey.N)
	assert.Equal(t, privateKey.D, parsedKey.D)
	assert.Equal(t, privateKey.E, parsedKey.E)
}

func TestMarshalPrivateKeyToPEMWithNilKey(t *testing.T) {
	_, err := MarshalPrivateKeyToPEM(nil)
	assert.ErrorIs(t, err, ErrInvalidPrivateKey)
}

func TestParsePrivateKeyFromInvalidPEM(t *testing.T) {
	invalidPEM := []byte("This is not a valid PEM")

	_, err := ParsePrivateKeyFromPEM(invalidPEM)
	assert.ErrorIs(t, err, ErrInvalidPEMBlock)
}

func TestWrapUnwrapWithPEMEncodedKeys(t *testing.T) {
	// Generate key pair
	privateKey, publicKey, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	// Encode to PEM
	publicPEM, err := MarshalPublicKeyToPEM(publicKey)
	require.NoError(t, err)

	privatePEM, err := MarshalPrivateKeyToPEM(privateKey)
	require.NoError(t, err)

	// Parse from PEM
	parsedPublicKey, err := ParsePublicKeyFromPEM(publicPEM)
	require.NoError(t, err)

	parsedPrivateKey, err := ParsePrivateKeyFromPEM(privatePEM)
	require.NoError(t, err)

	// Generate AES key
	aesKey, err := GenerateKey(32)
	require.NoError(t, err)

	// Wrap with parsed public key
	wrappedKey, err := WrapKey(parsedPublicKey, aesKey)
	require.NoError(t, err)

	// Unwrap with parsed private key
	unwrappedKey, err := UnwrapKey(parsedPrivateKey, wrappedKey)
	require.NoError(t, err)
	assert.Equal(t, aesKey, unwrappedKey)
}

func TestWrapKeyProducesDifferentCiphertexts(t *testing.T) {
	privateKey, publicKey, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	aesKey, err := GenerateKey(32)
	require.NoError(t, err)

	// Wrap the same key multiple times
	wrappedKey1, err := WrapKey(publicKey, aesKey)
	require.NoError(t, err)

	wrappedKey2, err := WrapKey(publicKey, aesKey)
	require.NoError(t, err)

	// Due to OAEP padding with random data, wrapped keys should differ
	assert.NotEqual(t, wrappedKey1, wrappedKey2)

	// Both should unwrap to the same key
	unwrappedKey1, err := UnwrapKey(privateKey, wrappedKey1)
	require.NoError(t, err)

	unwrappedKey2, err := UnwrapKey(privateKey, wrappedKey2)
	require.NoError(t, err)

	assert.Equal(t, aesKey, unwrappedKey1)
	assert.Equal(t, aesKey, unwrappedKey2)
}

func TestWrapUnwrapEmptyKey(t *testing.T) {
	privateKey, publicKey, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	emptyKey := []byte{}

	// Wrap empty key
	wrappedKey, err := WrapKey(publicKey, emptyKey)
	require.NoError(t, err)

	// Unwrap empty key
	unwrappedKey, err := UnwrapKey(privateKey, wrappedKey)
	require.NoError(t, err)
	assert.Equal(t, emptyKey, unwrappedKey)
}

func TestWrapUnwrapLargeData(t *testing.T) {
	privateKey, publicKey, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	// Generate 100 bytes of random data (should be within RSA-OAEP limits for 2048-bit key)
	largeData := make([]byte, 100)
	_, err = rand.Read(largeData)
	require.NoError(t, err)

	// Wrap
	wrappedKey, err := WrapKey(publicKey, largeData)
	require.NoError(t, err)

	// Unwrap
	unwrappedKey, err := UnwrapKey(privateKey, wrappedKey)
	require.NoError(t, err)
	assert.Equal(t, largeData, unwrappedKey)
}

func TestWrapKeyTooLargeForRSA(t *testing.T) {
	_, publicKey, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	// For RSA-OAEP with SHA-256 and 2048-bit key, max plaintext is:
	// (2048/8) - 2*32 - 2 = 256 - 64 - 2 = 190 bytes
	// Try to wrap 300 bytes (too large)
	tooLargeData := make([]byte, 300)
	_, err = rand.Read(tooLargeData)
	require.NoError(t, err)

	_, err = WrapKey(publicKey, tooLargeData)
	assert.ErrorIs(t, err, ErrKeyWrapFailed)
}

func TestUnwrapCorruptedWrappedKey(t *testing.T) {
	privateKey, publicKey, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	aesKey, err := GenerateKey(32)
	require.NoError(t, err)

	// Wrap the key
	wrappedKey, err := WrapKey(publicKey, aesKey)
	require.NoError(t, err)

	// Corrupt the wrapped key
	if len(wrappedKey) > 0 {
		wrappedKey[0] ^= 0xFF
	}

	// Try to unwrap
	_, err = UnwrapKey(privateKey, wrappedKey)
	assert.ErrorIs(t, err, ErrKeyUnwrapFailed)
}

// Integration test: Wrap/unwrap and then use for encryption
func TestWrapUnwrapIntegrationWithEncryption(t *testing.T) {
	// Generate RSA key pair
	privateKey, publicKey, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	// Generate AES key
	aesKey, err := GenerateKey(32)
	require.NoError(t, err)

	// Wrap the AES key
	wrappedKey, err := WrapKey(publicKey, aesKey)
	require.NoError(t, err)

	// Unwrap the AES key
	unwrappedKey, err := UnwrapKey(privateKey, wrappedKey)
	require.NoError(t, err)

	// Use the unwrapped key for encryption
	plaintext := []byte("Test message for integration")
	result, err := Encrypt(unwrappedKey, plaintext)
	require.NoError(t, err)

	// Decrypt with original key
	decrypted, err := Decrypt(aesKey, result.Ciphertext, result.Nonce, result.Tag)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

// Benchmark tests
func BenchmarkWrapKey(b *testing.B) {
	_, publicKey, _ := GenerateRSAKeyPair(2048)
	aesKey, _ := GenerateKey(32)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = WrapKey(publicKey, aesKey)
	}
}

func BenchmarkUnwrapKey(b *testing.B) {
	privateKey, publicKey, _ := GenerateRSAKeyPair(2048)
	aesKey, _ := GenerateKey(32)
	wrappedKey, _ := WrapKey(publicKey, aesKey)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = UnwrapKey(privateKey, wrappedKey)
	}
}

func BenchmarkGenerateRSAKeyPair2048(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _, _ = GenerateRSAKeyPair(2048)
	}
}

func BenchmarkGenerateRSAKeyPair4096(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _, _ = GenerateRSAKeyPair(4096)
	}
}

func BenchmarkMarshalPublicKeyToPEM(b *testing.B) {
	_, publicKey, _ := GenerateRSAKeyPair(2048)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = MarshalPublicKeyToPEM(publicKey)
	}
}

func BenchmarkParsePublicKeyFromPEM(b *testing.B) {
	_, publicKey, _ := GenerateRSAKeyPair(2048)
	pemData, _ := MarshalPublicKeyToPEM(publicKey)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ParsePublicKeyFromPEM(pemData)
	}
}

// Test concurrent key wrapping
func TestConcurrentWrapUnwrap(t *testing.T) {
	privateKey, publicKey, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	// Run multiple wraps/unwraps concurrently
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			aesKey, err := GenerateKey(32)
			require.NoError(t, err)

			wrappedKey, err := WrapKey(publicKey, aesKey)
			require.NoError(t, err)

			unwrappedKey, err := UnwrapKey(privateKey, wrappedKey)
			require.NoError(t, err)
			assert.Equal(t, aesKey, unwrappedKey)

			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}
