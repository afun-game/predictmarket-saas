// Package credentials encrypts merchant HMAC credentials at rest.
package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const encryptionKeySize = 32

var ErrInvalidKey = errors.New("credential encryption key must be a base64-encoded 32-byte value")

// Protector encrypts and decrypts secrets with AES-256-GCM. Ciphertexts are
// base64url(nonce || ciphertext), so they can be stored in a text column.
type Protector struct {
	aead cipher.AEAD
}

// NewProtector creates a protector from a base64url-encoded 32-byte key.
func NewProtector(encodedKey string) (*Protector, error) {
	key, err := decodeKey(encodedKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return &Protector{aead: aead}, nil
}

// Encrypt returns an authenticated ciphertext for plaintext.
func (p *Protector) Encrypt(plaintext string) (string, error) {
	if p == nil {
		return "", errors.New("credential protector is not configured")
	}
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate credential nonce: %w", err)
	}
	ciphertext := p.aead.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, ciphertext...)
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

// Decrypt verifies and returns the plaintext in ciphertext.
func (p *Protector) Decrypt(ciphertext string) (string, error) {
	if p == nil {
		return "", errors.New("credential protector is not configured")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(ciphertext))
	if err != nil {
		return "", fmt.Errorf("decode encrypted credential: %w", err)
	}
	nonceSize := p.aead.NonceSize()
	if len(payload) <= nonceSize {
		return "", errors.New("encrypted credential is malformed")
	}
	plaintext, err := p.aead.Open(nil, payload[:nonceSize], payload[nonceSize:], nil)
	if err != nil {
		return "", errors.New("encrypted credential could not be authenticated")
	}
	return string(plaintext), nil
}

func decodeKey(encodedKey string) ([]byte, error) {
	encodedKey = strings.TrimSpace(encodedKey)
	key, err := base64.RawURLEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, ErrInvalidKey
	}
	if len(key) != encryptionKeySize {
		return nil, ErrInvalidKey
	}
	return key, nil
}
