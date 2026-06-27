package envelope

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// TestSignVerifyHappyPath tests TC-096-01: Sign + Verify happy path
func TestSignVerifyHappyPath(t *testing.T) {
	// Generate an Ed25519 keypair
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	// Construct an Envelope
	nonce, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce failed: %v", err)
	}

	env := Envelope{
		From:    "operator",
		To:      "orchestrator",
		Nonce:   nonce,
		TS:      NowRFC3339(),
		Payload: "build the auth module",
		Sig:     "", // empty until signed
	}

	// Sign the envelope
	signed, err := Sign(env, priv)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// Verify with the correct public key
	err = Verify(signed, pub)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}

	// TC-096-01: Verify Payload is preserved
	if signed.Payload != "build the auth module" {
		t.Errorf("Payload not preserved: got %q, want %q", signed.Payload, "build the auth module")
	}

	// TC-096-01: Verify Sig is non-empty hex
	if signed.Sig == "" {
		t.Error("Sig is empty")
	}
	if !isValidHex(signed.Sig) {
		t.Errorf("Sig is not valid hex: %q", signed.Sig)
	}
}

// TestVerifyWrongKey tests TC-096-02: Verify rejects signature from wrong key
func TestVerifyWrongKey(t *testing.T) {
	// Generate two keypairs
	_, senderPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	// Sign with senderPriv
	nonce, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce failed: %v", err)
	}

	env := Envelope{
		From:    "operator",
		To:      "orchestrator",
		Nonce:   nonce,
		TS:      NowRFC3339(),
		Payload: "test payload",
		Sig:     "",
	}

	signed, err := Sign(env, senderPriv)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// TC-096-02: Verify with wrong public key should fail
	err = Verify(signed, otherPub)
	if err == nil {
		t.Fatal("Verify with wrong key should have failed but returned nil")
	}

	// TC-096-02: Error string must contain one of the expected substrings
	errStr := err.Error()
	if !strings.Contains(errStr, "unknown_key") &&
		!strings.Contains(errStr, "bad_signature") &&
		!strings.Contains(errStr, "signature") {
		t.Errorf("error message missing expected substring: %q", errStr)
	}
}

// TestVerifyTamperedPayload tests TC-096-03: Verify rejects tampered payload
func TestVerifyTamperedPayload(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	nonce, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce failed: %v", err)
	}

	env := Envelope{
		From:    "operator",
		To:      "orchestrator",
		Nonce:   nonce,
		TS:      NowRFC3339(),
		Payload: "original payload",
		Sig:     "",
	}

	// Sign the envelope
	signed, err := Sign(env, priv)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// Tamper with the payload
	signed.Payload = signed.Payload + "X"

	// TC-096-03: Verify should fail
	err = Verify(signed, pub)
	if err == nil {
		t.Fatal("Verify with tampered payload should have failed but returned nil")
	}

	// TC-096-03: Error string must contain expected substring
	errStr := err.Error()
	if !strings.Contains(errStr, "bad_signature") &&
		!strings.Contains(errStr, "tampered") &&
		!strings.Contains(errStr, "signature") {
		t.Errorf("error message missing expected substring: %q", errStr)
	}
}

// TestVerifyAndOpenSafeOrdering tests SEC-002: VerifyAndOpen enforces verify→check→open ordering
func TestVerifyAndOpenSafeOrdering(t *testing.T) {
	// Setup: keypairs for both sender and recipient
	senderEdPub, senderEdPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	senderX25519Pub, senderX25519Priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	recipX25519Pub, recipX25519Priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	cache := NewReplayCache(60 * time.Second)

	plaintext := []byte("test message")

	// Happy path: VerifyAndOpen should succeed
	ciphertext, nonce, err := Seal(plaintext, senderX25519Priv, recipX25519Pub)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	env := Envelope{
		From:    "sender",
		To:      "recipient",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
		Sig:     "",
	}

	signed, err := Sign(env, senderEdPriv)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// VerifyAndOpen happy path
	decrypted, err := VerifyAndOpen(signed, senderEdPub, cache, recipX25519Priv, senderX25519Pub)
	if err != nil {
		t.Fatalf("VerifyAndOpen failed: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("decrypted plaintext mismatch: got %q, want %q", decrypted, plaintext)
	}

	// Bad signature path: VerifyAndOpen should fail before trying to decrypt
	badSig := signed
	badSig.Sig = "0000000000000000000000000000000000000000000000000000000000000000" +
		"0000000000000000000000000000000000000000000000000000000000000000"

	_, err = VerifyAndOpen(badSig, senderEdPub, cache, recipX25519Priv, senderX25519Pub)
	if err == nil {
		t.Fatal("VerifyAndOpen with bad signature should have failed")
	}

	// The error should be about verification, not decryption
	if !strings.Contains(err.Error(), "verify") {
		t.Errorf("error should mention verify step: %q", err.Error())
	}
}

// TestSigningBytesCollisionResistance tests SEC-001: signing bytes no longer collide
// on field boundaries (the pipe-delimited scheme is replaced with JSON marshalling).
func TestSigningBytesCollisionResistance(t *testing.T) {
	// These two envelopes would have collided under the pipe-delimited scheme:
	// From:"a",To:"b|c" and From:"a|b",To:"c" both produce "a|b|c|ts|payload"
	// With JSON marshalling, they produce different bytes and sign differently.

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	nonce := "collision-test-nonce"
	ts := NowRFC3339()
	payload := "test-payload"

	// Envelope 1: pipe in To field
	env1 := Envelope{
		From:    "a",
		To:      "b|c",
		Nonce:   nonce,
		TS:      ts,
		Payload: payload,
		Sig:     "",
	}

	// Envelope 2: pipe in From field
	env2 := Envelope{
		From:    "a|b",
		To:      "c",
		Nonce:   nonce,
		TS:      ts,
		Payload: payload,
		Sig:     "",
	}

	// Sign both envelopes
	signed1, err := Sign(env1, priv)
	if err != nil {
		t.Fatalf("Sign env1 failed: %v", err)
	}

	signed2, err := Sign(env2, priv)
	if err != nil {
		t.Fatalf("Sign env2 failed: %v", err)
	}

	// The signatures must be different (no collision)
	if signed1.Sig == signed2.Sig {
		t.Fatal("Signatures collided! The pipe-delimited scheme is still in use.")
	}

	// Verify that env2's signature does NOT verify with env1
	signed1.Sig = signed2.Sig // Swap signatures to test cross-verification fails
	err = Verify(signed1, pub)
	if err == nil {
		t.Fatal("Verification should fail when signature is from a different envelope")
	}
}

// isValidHex checks if a string is valid hexadecimal.
func isValidHex(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		isDigit := c >= '0' && c <= '9'
		isLower := c >= 'a' && c <= 'f'
		isUpper := c >= 'A' && c <= 'F'
		if !isDigit && !isLower && !isUpper {
			return false
		}
	}
	return true
}
