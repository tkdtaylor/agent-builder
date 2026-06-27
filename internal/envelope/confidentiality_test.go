package envelope

import (
	"bytes"
	"strings"
	"testing"
)

// TestSealCiphertextNotEqual tests TC-096-04: Seal produces ciphertext != plaintext
func TestSealCiphertextNotEqual(t *testing.T) {
	// Generate X25519 keypairs
	_, senderPriv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	recipPub, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	plaintext := []byte("run the diagnostics task")

	// TC-096-04: Seal should return non-nil ciphertext and nonce
	ciphertext, nonce, err := Seal(plaintext, senderPriv, recipPub)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	// TC-096-04: Ciphertext must not be equal to plaintext
	if bytes.Equal(ciphertext, plaintext) {
		t.Error("ciphertext equals plaintext (should differ due to AEAD overhead)")
	}

	// TC-096-04: Ciphertext length must be > plaintext length (due to 16-byte Poly1305 tag)
	if len(ciphertext) <= len(plaintext) {
		t.Errorf("ciphertext length %d must be > plaintext length %d", len(ciphertext), len(plaintext))
	}

	// Verify nonce was generated (type [24]byte guarantees length, this confirms it's not zero-initialized)
	if nonce == [24]byte{} {
		t.Error("nonce should not be zero-initialized")
	}
}

// TestOpenRoundTrip tests TC-096-05: Open correctly decrypts ciphertext
func TestOpenRoundTrip(t *testing.T) {
	// Generate X25519 keypairs for both sides
	senderPub, senderPriv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	recipPub, recipPriv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	plaintext := []byte("run the diagnostics task")

	// Seal the plaintext
	ciphertext, nonce, err := Seal(plaintext, senderPriv, recipPub)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	// TC-096-05 (happy path): Open should decrypt correctly
	decrypted, err := Open(ciphertext, nonce, recipPriv, senderPub)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// TC-096-05: Decrypted must equal original plaintext
	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("decrypted plaintext does not match original: got %q, want %q", decrypted, plaintext)
	}
}

// TestOpenTamperedCiphertext tests TC-096-05: Open fails on tampered ciphertext
func TestOpenTamperedCiphertext(t *testing.T) {
	// Generate X25519 keypairs
	senderPub, senderPriv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	recipPub, recipPriv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	plaintext := []byte("run the diagnostics task")

	// Seal the plaintext
	ciphertext, nonce, err := Seal(plaintext, senderPriv, recipPub)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	// Tamper with the ciphertext by flipping a byte
	ciphertext[0] ^= 0xFF

	// TC-096-05 (tamper path): Open should fail
	decrypted, err := Open(ciphertext, nonce, recipPriv, senderPub)
	if err == nil {
		t.Fatal("Open on tampered ciphertext should have failed but returned nil")
	}

	// TC-096-05: Error must contain expected substring
	errStr := err.Error()
	if !strings.Contains(errStr, "authentication failed") &&
		!strings.Contains(errStr, "decrypt") &&
		!strings.Contains(errStr, "open") {
		t.Errorf("error message missing expected substring: %q", errStr)
	}

	// TC-096-05: Decrypted must be nil (no partial plaintext)
	if decrypted != nil {
		t.Errorf("Open should return nil plaintext on AEAD failure, got %q", decrypted)
	}
}
