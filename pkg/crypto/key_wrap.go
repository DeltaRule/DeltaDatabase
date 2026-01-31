package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

var (
	// ErrInvalidPublicKey is returned when the public key is invalid
	ErrInvalidPublicKey = errors.New("invalid public key")
	// ErrInvalidPrivateKey is returned when the private key is invalid
	ErrInvalidPrivateKey = errors.New("invalid private key")
	// ErrKeyWrapFailed is returned when key wrapping fails
	ErrKeyWrapFailed = errors.New("key wrapping failed")
	// ErrKeyUnwrapFailed is returned when key unwrapping fails
	ErrKeyUnwrapFailed = errors.New("key unwrapping failed")
	// ErrInvalidPEMBlock is returned when PEM decoding fails
	ErrInvalidPEMBlock = errors.New("failed to decode PEM block")
)

// WrapKey wraps (encrypts) a symmetric key using RSA-OAEP with SHA-256.
// This is used by the Main Worker to securely distribute encryption keys to Processing Workers.
// The public key should be in PEM format or raw *rsa.PublicKey.
//
// Security notes:
//   - Uses RSA-OAEP with SHA-256 for secure key encapsulation
//   - The wrapped key can only be unwrapped by the holder of the private key
//   - Suitable for distributing AES keys securely
func WrapKey(publicKey *rsa.PublicKey, keyToWrap []byte) ([]byte, error) {
	if publicKey == nil {
		return nil, ErrInvalidPublicKey
	}

	// Encrypt the key using RSA-OAEP
	wrappedKey, err := rsa.EncryptOAEP(
		sha256.New(),
		rand.Reader,
		publicKey,
		keyToWrap,
		nil, // no label
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyWrapFailed, err)
	}

	return wrappedKey, nil
}

// UnwrapKey unwraps (decrypts) a symmetric key using RSA-OAEP with SHA-256.
// This is used by Processing Workers to decrypt keys received from the Main Worker.
// The private key should be in PEM format or raw *rsa.PrivateKey.
//
// Security notes:
//   - Uses RSA-OAEP with SHA-256 for secure key decapsulation
//   - The private key must match the public key used for wrapping
//   - Returns an error if decryption fails
func UnwrapKey(privateKey *rsa.PrivateKey, wrappedKey []byte) ([]byte, error) {
	if privateKey == nil {
		return nil, ErrInvalidPrivateKey
	}

	// Decrypt the wrapped key using RSA-OAEP
	unwrappedKey, err := rsa.DecryptOAEP(
		sha256.New(),
		rand.Reader,
		privateKey,
		wrappedKey,
		nil, // no label
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyUnwrapFailed, err)
	}

	return unwrappedKey, nil
}

// GenerateRSAKeyPair generates a new RSA key pair for key wrapping/unwrapping.
// The bits parameter specifies the key size (recommended: 2048 or 4096).
//
// Returns:
//   - privateKey: the RSA private key
//   - publicKey: the corresponding RSA public key
func GenerateRSAKeyPair(bits int) (*rsa.PrivateKey, *rsa.PublicKey, error) {
	if bits < 2048 {
		return nil, nil, fmt.Errorf("key size too small: minimum 2048 bits recommended")
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate RSA key pair: %w", err)
	}

	return privateKey, &privateKey.PublicKey, nil
}

// MarshalPublicKeyToPEM encodes an RSA public key to PEM format.
// This is useful for transmitting public keys over the wire or storing them.
func MarshalPublicKeyToPEM(publicKey *rsa.PublicKey) ([]byte, error) {
	if publicKey == nil {
		return nil, ErrInvalidPublicKey
	}

	pubASN1, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal public key: %w", err)
	}

	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubASN1,
	})

	return pubPEM, nil
}

// ParsePublicKeyFromPEM decodes an RSA public key from PEM format.
// This is useful for receiving public keys over the wire or loading them from storage.
func ParsePublicKeyFromPEM(pemData []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, ErrInvalidPEMBlock
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: not an RSA public key", ErrInvalidPublicKey)
	}

	return rsaPub, nil
}

// MarshalPrivateKeyToPEM encodes an RSA private key to PEM format.
// This is useful for securely storing private keys.
//
// Security note: The returned PEM is unencrypted. In production, consider
// encrypting the PEM with a passphrase using x509.EncryptPEMBlock.
func MarshalPrivateKeyToPEM(privateKey *rsa.PrivateKey) ([]byte, error) {
	if privateKey == nil {
		return nil, ErrInvalidPrivateKey
	}

	privASN1 := x509.MarshalPKCS1PrivateKey(privateKey)
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privASN1,
	})

	return privPEM, nil
}

// ParsePrivateKeyFromPEM decodes an RSA private key from PEM format.
// This is useful for loading private keys from secure storage.
//
// Security note: This function expects an unencrypted PEM. If your PEM is
// encrypted, decrypt it first using x509.DecryptPEMBlock.
func ParsePrivateKeyFromPEM(pemData []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, ErrInvalidPEMBlock
	}

	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	return priv, nil
}
