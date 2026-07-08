// Package cryptobox provides authenticated encryption-at-rest for small
// secrets (A-3: TOTP shared secrets). AES-256-GCM with a key derived via
// HKDF-SHA256, so operators supply one seed instead of a raw key, and
// distinct info strings yield independent keys from the same seed.
package cryptobox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

const keySize = 32 // AES-256

var (
	ErrSeedTooShort = errors.New("cryptobox: seed must be at least 32 bytes")
	ErrCiphertext   = errors.New("cryptobox: invalid ciphertext")
)

// Box seals and opens small secrets with one derived AEAD key.
type Box struct {
	aead cipher.AEAD
}

// New derives an AES-256-GCM key from seed via HKDF-SHA256. The info string
// domain-separates keys derived from a shared seed (e.g. JWT_SECRET fallback).
func New(seed, info string) (*Box, error) {
	if len(seed) < keySize {
		return nil, ErrSeedTooShort
	}

	key, err := hkdf.Key(sha256.New, []byte(seed), nil, info, keySize)
	if err != nil {
		return nil, fmt.Errorf("cryptobox: derive key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cryptobox: cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cryptobox: gcm: %w", err)
	}

	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext and returns base64(nonce || ciphertext).
func (b *Box) Seal(plaintext []byte) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("cryptobox: nonce: %w", err)
	}

	sealed := b.aead.Seal(nonce, nonce, plaintext, nil)

	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Open decrypts a Seal output. Tampered or foreign-key ciphertexts fail with
// ErrCiphertext.
func (b *Box) Open(encoded string) ([]byte, error) {
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrCiphertext
	}

	nonceSize := b.aead.NonceSize()
	if len(raw) < nonceSize {
		return nil, ErrCiphertext
	}

	plaintext, err := b.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return nil, ErrCiphertext
	}

	return plaintext, nil
}
