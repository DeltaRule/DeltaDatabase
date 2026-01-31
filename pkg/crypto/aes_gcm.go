package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// Standard AES-GCM nonce size (12 bytes recommended for GCM)
const NonceSize = 12

var (
	// ErrInvalidKeySize is returned when the encryption key is not 16, 24, or 32 bytes
	ErrInvalidKeySize = errors.New("invalid key size: must be 16, 24, or 32 bytes for AES")
	// ErrEncryptionFailed is returned when encryption operation fails
	ErrEncryptionFailed = errors.New("encryption failed")
	// ErrDecryptionFailed is returned when decryption operation fails
	ErrDecryptionFailed = errors.New("decryption failed")
	// ErrInvalidNonceSize is returned when nonce size is incorrect
	ErrInvalidNonceSize = errors.New("invalid nonce size")
)

// EncryptResult contains the output of an encryption operation
type EncryptResult struct {
	Ciphertext []byte
	Nonce      []byte
	Tag        []byte
}

// Encrypt encrypts plaintext using AES-GCM with the provided key.
// Returns the ciphertext, nonce, and authentication tag.
// The key must be 16, 24, or 32 bytes for AES-128, AES-192, or AES-256.
//
// Security notes:
//   - A unique nonce is generated for each encryption operation
//   - The authentication tag is embedded in the ciphertext by GCM
//   - This provides both confidentiality and integrity
func Encrypt(key, plaintext []byte) (*EncryptResult, error) {
	// Validate key size
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil, ErrInvalidKeySize
	}

	// Create AES cipher block
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEncryptionFailed, err)
	}

	// Create GCM mode
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEncryptionFailed, err)
	}

	// Generate a unique nonce for this encryption
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("%w: failed to generate nonce: %v", ErrEncryptionFailed, err)
	}

	// Encrypt and authenticate
	// GCM's Seal appends the authentication tag to the ciphertext
	ciphertextWithTag := aesGCM.Seal(nil, nonce, plaintext, nil)

	// Split ciphertext and tag
	// The tag is the last TagSize() bytes
	tagSize := aesGCM.Overhead()
	if len(ciphertextWithTag) < tagSize {
		return nil, fmt.Errorf("%w: output too short", ErrEncryptionFailed)
	}

	ciphertext := ciphertextWithTag[:len(ciphertextWithTag)-tagSize]
	tag := ciphertextWithTag[len(ciphertextWithTag)-tagSize:]

	return &EncryptResult{
		Ciphertext: ciphertext,
		Nonce:      nonce,
		Tag:        tag,
	}, nil
}

// Decrypt decrypts ciphertext using AES-GCM with the provided key, nonce, and tag.
// Returns the original plaintext if authentication succeeds.
// The key must be 16, 24, or 32 bytes for AES-128, AES-192, or AES-256.
//
// Security notes:
//   - Authentication is verified before decryption
//   - If the tag doesn't match, decryption fails (tamper detection)
//   - Returns an error if authentication fails
func Decrypt(key, ciphertext, nonce, tag []byte) ([]byte, error) {
	// Validate key size
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil, ErrInvalidKeySize
	}

	// Validate nonce size
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidNonceSize, NonceSize, len(nonce))
	}

	// Create AES cipher block
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryptionFailed, err)
	}

	// Create GCM mode
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryptionFailed, err)
	}

	// Validate tag size
	if len(tag) != aesGCM.Overhead() {
		return nil, fmt.Errorf("%w: invalid tag size", ErrDecryptionFailed)
	}

	// Reconstruct the ciphertext with tag appended (as GCM expects)
	ciphertextWithTag := append(ciphertext, tag...)

	// Decrypt and verify authentication
	plaintext, err := aesGCM.Open(nil, nonce, ciphertextWithTag, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: authentication failed or corrupted data: %v", ErrDecryptionFailed, err)
	}

	return plaintext, nil
}

// GenerateKey generates a random AES key of the specified size.
// keySize must be 16, 24, or 32 bytes for AES-128, AES-192, or AES-256.
func GenerateKey(keySize int) ([]byte, error) {
	if keySize != 16 && keySize != 24 && keySize != 32 {
		return nil, ErrInvalidKeySize
	}

	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	return key, nil
}
