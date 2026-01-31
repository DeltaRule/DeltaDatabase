package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncryptDecrypt(t *testing.T) {
	// Generate a 256-bit key
	key, err := GenerateKey(32)
	require.NoError(t, err)
	require.Len(t, key, 32)

	plaintext := []byte("Hello, World! This is a test message.")

	// Encrypt
	result, err := Encrypt(key, plaintext)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.Ciphertext)
	assert.Len(t, result.Nonce, NonceSize)
	assert.NotEmpty(t, result.Tag)

	// Decrypt
	decrypted, err := Decrypt(key, result.Ciphertext, result.Nonce, result.Tag)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestEncryptDecryptEmptyPlaintext(t *testing.T) {
	key, err := GenerateKey(32)
	require.NoError(t, err)

	plaintext := []byte{}

	// Encrypt empty data
	result, err := Encrypt(key, plaintext)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Decrypt empty data
	decrypted, err := Decrypt(key, result.Ciphertext, result.Nonce, result.Tag)
	require.NoError(t, err)
	assert.Empty(t, decrypted)
}

func TestEncryptDecryptLargeData(t *testing.T) {
	key, err := GenerateKey(32)
	require.NoError(t, err)

	// Create 1MB of random data
	plaintext := make([]byte, 1024*1024)
	_, err = rand.Read(plaintext)
	require.NoError(t, err)

	// Encrypt
	result, err := Encrypt(key, plaintext)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Decrypt
	decrypted, err := Decrypt(key, result.Ciphertext, result.Nonce, result.Tag)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestEncryptWithDifferentKeySizes(t *testing.T) {
	testCases := []struct {
		name    string
		keySize int
	}{
		{"AES-128", 16},
		{"AES-192", 24},
		{"AES-256", 32},
	}

	plaintext := []byte("Test message")

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			key, err := GenerateKey(tc.keySize)
			require.NoError(t, err)

			result, err := Encrypt(key, plaintext)
			require.NoError(t, err)

			decrypted, err := Decrypt(key, result.Ciphertext, result.Nonce, result.Tag)
			require.NoError(t, err)
			assert.Equal(t, plaintext, decrypted)
		})
	}
}

func TestEncryptInvalidKeySize(t *testing.T) {
	invalidKeySizes := []int{8, 15, 17, 23, 25, 31, 33, 64}

	plaintext := []byte("Test message")

	for _, keySize := range invalidKeySizes {
		t.Run(string(rune(keySize)), func(t *testing.T) {
			key := make([]byte, keySize)
			_, err := Encrypt(key, plaintext)
			assert.ErrorIs(t, err, ErrInvalidKeySize)
		})
	}
}

func TestDecryptInvalidKeySize(t *testing.T) {
	key := make([]byte, 15) // Invalid size
	ciphertext := []byte("dummy")
	nonce := make([]byte, NonceSize)
	tag := make([]byte, 16)

	_, err := Decrypt(key, ciphertext, nonce, tag)
	assert.ErrorIs(t, err, ErrInvalidKeySize)
}

func TestDecryptInvalidNonceSize(t *testing.T) {
	key, err := GenerateKey(32)
	require.NoError(t, err)

	ciphertext := []byte("dummy")
	invalidNonce := make([]byte, 8) // Wrong size
	tag := make([]byte, 16)

	_, err = Decrypt(key, ciphertext, invalidNonce, tag)
	assert.ErrorIs(t, err, ErrInvalidNonceSize)
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	key, err := GenerateKey(32)
	require.NoError(t, err)

	plaintext := []byte("Original message")

	// Encrypt
	result, err := Encrypt(key, plaintext)
	require.NoError(t, err)

	// Tamper with ciphertext
	if len(result.Ciphertext) > 0 {
		result.Ciphertext[0] ^= 0xFF
	}

	// Attempt to decrypt tampered data
	_, err = Decrypt(key, result.Ciphertext, result.Nonce, result.Tag)
	assert.ErrorIs(t, err, ErrDecryptionFailed)
}

func TestDecryptTamperedTag(t *testing.T) {
	key, err := GenerateKey(32)
	require.NoError(t, err)

	plaintext := []byte("Original message")

	// Encrypt
	result, err := Encrypt(key, plaintext)
	require.NoError(t, err)

	// Tamper with tag
	if len(result.Tag) > 0 {
		result.Tag[0] ^= 0xFF
	}

	// Attempt to decrypt with tampered tag
	_, err = Decrypt(key, result.Ciphertext, result.Nonce, result.Tag)
	assert.ErrorIs(t, err, ErrDecryptionFailed)
}

func TestDecryptTamperedNonce(t *testing.T) {
	key, err := GenerateKey(32)
	require.NoError(t, err)

	plaintext := []byte("Original message")

	// Encrypt
	result, err := Encrypt(key, plaintext)
	require.NoError(t, err)

	// Tamper with nonce
	if len(result.Nonce) > 0 {
		result.Nonce[0] ^= 0xFF
	}

	// Attempt to decrypt with tampered nonce
	_, err = Decrypt(key, result.Ciphertext, result.Nonce, result.Tag)
	assert.ErrorIs(t, err, ErrDecryptionFailed)
}

func TestDecryptWrongKey(t *testing.T) {
	key1, err := GenerateKey(32)
	require.NoError(t, err)

	key2, err := GenerateKey(32)
	require.NoError(t, err)

	plaintext := []byte("Secret message")

	// Encrypt with key1
	result, err := Encrypt(key1, plaintext)
	require.NoError(t, err)

	// Try to decrypt with key2
	_, err = Decrypt(key2, result.Ciphertext, result.Nonce, result.Tag)
	assert.ErrorIs(t, err, ErrDecryptionFailed)
}

func TestEncryptProducesUniqueNonces(t *testing.T) {
	key, err := GenerateKey(32)
	require.NoError(t, err)

	plaintext := []byte("Same message")
	nonces := make(map[string]bool)

	// Encrypt the same message multiple times
	for i := 0; i < 100; i++ {
		result, err := Encrypt(key, plaintext)
		require.NoError(t, err)

		nonceStr := string(result.Nonce)
		assert.False(t, nonces[nonceStr], "Duplicate nonce detected")
		nonces[nonceStr] = true
	}
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	key, err := GenerateKey(32)
	require.NoError(t, err)

	plaintext := []byte("Same message")

	// Encrypt the same message twice
	result1, err := Encrypt(key, plaintext)
	require.NoError(t, err)

	result2, err := Encrypt(key, plaintext)
	require.NoError(t, err)

	// Ciphertexts should differ due to different nonces
	assert.NotEqual(t, result1.Ciphertext, result2.Ciphertext)
	assert.NotEqual(t, result1.Nonce, result2.Nonce)

	// Both should decrypt to the same plaintext
	decrypted1, err := Decrypt(key, result1.Ciphertext, result1.Nonce, result1.Tag)
	require.NoError(t, err)

	decrypted2, err := Decrypt(key, result2.Ciphertext, result2.Nonce, result2.Tag)
	require.NoError(t, err)

	assert.Equal(t, plaintext, decrypted1)
	assert.Equal(t, plaintext, decrypted2)
}

func TestGenerateKeyValidSizes(t *testing.T) {
	validSizes := []int{16, 24, 32}

	for _, size := range validSizes {
		t.Run(string(rune(size)), func(t *testing.T) {
			key, err := GenerateKey(size)
			require.NoError(t, err)
			assert.Len(t, key, size)

			// Ensure key is not all zeros
			allZeros := true
			for _, b := range key {
				if b != 0 {
					allZeros = false
					break
				}
			}
			assert.False(t, allZeros, "Generated key should not be all zeros")
		})
	}
}

func TestGenerateKeyInvalidSizes(t *testing.T) {
	invalidSizes := []int{0, 8, 15, 17, 64}

	for _, size := range invalidSizes {
		t.Run(string(rune(size)), func(t *testing.T) {
			_, err := GenerateKey(size)
			assert.ErrorIs(t, err, ErrInvalidKeySize)
		})
	}
}

func TestGenerateKeyProducesUniqueKeys(t *testing.T) {
	keys := make(map[string]bool)

	// Generate multiple keys and ensure they're unique
	for i := 0; i < 100; i++ {
		key, err := GenerateKey(32)
		require.NoError(t, err)

		keyStr := string(key)
		assert.False(t, keys[keyStr], "Duplicate key detected")
		keys[keyStr] = true
	}
}

func TestEncryptDecryptJSONData(t *testing.T) {
	key, err := GenerateKey(32)
	require.NoError(t, err)

	// Simulate JSON data
	jsonData := []byte(`{"Chat_id": {"chat": [{"type": "assistant", "text": "Hello"}]}}`)

	// Encrypt
	result, err := Encrypt(key, jsonData)
	require.NoError(t, err)

	// Decrypt
	decrypted, err := Decrypt(key, result.Ciphertext, result.Nonce, result.Tag)
	require.NoError(t, err)
	assert.Equal(t, jsonData, decrypted)
}

func TestDecryptWithInvalidTagSize(t *testing.T) {
	key, err := GenerateKey(32)
	require.NoError(t, err)

	plaintext := []byte("Test message")

	// Encrypt
	result, err := Encrypt(key, plaintext)
	require.NoError(t, err)

	// Use invalid tag size
	invalidTag := result.Tag[:len(result.Tag)-1]

	_, err = Decrypt(key, result.Ciphertext, result.Nonce, invalidTag)
	assert.ErrorIs(t, err, ErrDecryptionFailed)
}

// Benchmark tests
func BenchmarkEncrypt(b *testing.B) {
	key, _ := GenerateKey(32)
	plaintext := []byte("This is a benchmark test message for encryption performance.")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Encrypt(key, plaintext)
	}
}

func BenchmarkDecrypt(b *testing.B) {
	key, _ := GenerateKey(32)
	plaintext := []byte("This is a benchmark test message for decryption performance.")
	result, _ := Encrypt(key, plaintext)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Decrypt(key, result.Ciphertext, result.Nonce, result.Tag)
	}
}

func BenchmarkEncryptLargeData(b *testing.B) {
	key, _ := GenerateKey(32)
	plaintext := make([]byte, 1024*1024) // 1MB
	rand.Read(plaintext)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Encrypt(key, plaintext)
	}
}

func BenchmarkDecryptLargeData(b *testing.B) {
	key, _ := GenerateKey(32)
	plaintext := make([]byte, 1024*1024) // 1MB
	rand.Read(plaintext)
	result, _ := Encrypt(key, plaintext)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Decrypt(key, result.Ciphertext, result.Nonce, result.Tag)
	}
}

// Test edge case: decrypt with nil inputs
func TestDecryptNilInputs(t *testing.T) {
	key, err := GenerateKey(32)
	require.NoError(t, err)

	// Test nil ciphertext
	_, err = Decrypt(key, nil, make([]byte, NonceSize), make([]byte, 16))
	assert.Error(t, err)

	// Test nil nonce
	_, err = Decrypt(key, []byte("test"), nil, make([]byte, 16))
	assert.Error(t, err)

	// Test nil tag
	_, err = Decrypt(key, []byte("test"), make([]byte, NonceSize), nil)
	assert.Error(t, err)
}

// Test that ciphertext and tag are independent byte slices
func TestEncryptResultIndependence(t *testing.T) {
	key, err := GenerateKey(32)
	require.NoError(t, err)

	plaintext := []byte("Independence test")

	result, err := Encrypt(key, plaintext)
	require.NoError(t, err)

	// Modify the result fields and ensure they're independent
	originalCiphertext := make([]byte, len(result.Ciphertext))
	copy(originalCiphertext, result.Ciphertext)

	originalNonce := make([]byte, len(result.Nonce))
	copy(originalNonce, result.Nonce)

	originalTag := make([]byte, len(result.Tag))
	copy(originalTag, result.Tag)

	// Modify result fields
	if len(result.Ciphertext) > 0 {
		result.Ciphertext[0] ^= 0xFF
	}
	if len(result.Nonce) > 0 {
		result.Nonce[0] ^= 0xFF
	}
	if len(result.Tag) > 0 {
		result.Tag[0] ^= 0xFF
	}

	// Decrypt with original values should still work
	decrypted, err := Decrypt(key, originalCiphertext, originalNonce, originalTag)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

// Test concurrent encryption and decryption
func TestConcurrentEncryptDecrypt(t *testing.T) {
	key, err := GenerateKey(32)
	require.NoError(t, err)

	plaintext := []byte("Concurrent test message")

	// Run multiple encryptions concurrently
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			result, err := Encrypt(key, plaintext)
			require.NoError(t, err)

			decrypted, err := Decrypt(key, result.Ciphertext, result.Nonce, result.Tag)
			require.NoError(t, err)
			assert.True(t, bytes.Equal(plaintext, decrypted))

			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}
