package credentials

import (
	"encoding/base64"
	"testing"
)

func TestProtectorRoundTrip(t *testing.T) {
	t.Parallel()

	key := make([]byte, encryptionKeySize)
	for index := range key {
		key[index] = byte(index + 1)
	}
	protector, err := NewProtector(base64.RawURLEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("NewProtector() error = %v", err)
	}
	ciphertext, err := protector.Encrypt("sk_live_test")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	plaintext, err := protector.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if plaintext != "sk_live_test" {
		t.Errorf("plaintext = %q, want %q", plaintext, "sk_live_test")
	}
}

func TestProtectorRejectsTamperedCiphertext(t *testing.T) {
	t.Parallel()

	key := make([]byte, encryptionKeySize)
	protector, err := NewProtector(base64.RawURLEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("NewProtector() error = %v", err)
	}
	ciphertext, err := protector.Encrypt("sk_live_test")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	// Flip a character inside the nonce region, guaranteeing a different
	// ciphertext. The final base64 character can carry ignored low bits
	// (payload length mod 3), and a fixed replacement could equal the
	// original character, so choose a replacement that always differs.
	replacement := byte('A')
	if ciphertext[8] == 'A' {
		replacement = 'B'
	}
	tampered := ciphertext[:8] + string(replacement) + ciphertext[9:]
	if _, err := protector.Decrypt(tampered); err == nil {
		t.Fatal("Decrypt() error = nil, want authenticated-ciphertext error")
	}
}
