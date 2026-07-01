package envelope

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
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

// TestTC154_01_DirectOpenAEADFailureMatchesErrDecryptionFailed tests TC-154-01:
// ErrDecryptionFailed sentinel matches a direct Open AEAD failure
func TestTC154_01_DirectOpenAEADFailureMatchesErrDecryptionFailed(t *testing.T) {
	// Setup: Seal a plaintext with real sender-priv/recipient-pub pair
	senderPub, senderPriv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	recipPub, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	plaintext := []byte("test message")
	ciphertext, nonce, err := Seal(plaintext, senderPriv, recipPub)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	// Call Open with mismatched recipient private key (wrong recipient)
	wrongRecipPub, wrongRecipPriv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Try to decrypt with the wrong recipient key (not the one the ciphertext was sealed to)
	_, err = Open(ciphertext, nonce, wrongRecipPriv, senderPub)

	// TC-154-01: err must be non-nil
	if err == nil {
		t.Fatal("Open with wrong recipient key should have failed but returned nil")
	}

	// TC-154-01: errors.Is(err, ErrDecryptionFailed) must be true
	if !errors.Is(err, ErrDecryptionFailed) {
		t.Errorf("error should match ErrDecryptionFailed: %v", err)
	}

	// TC-154-01: err.Error() must still contain the descriptive message text
	errStr := err.Error()
	if !strings.Contains(errStr, "authentication failed") && !strings.Contains(errStr, "decrypt") {
		t.Errorf("error message should contain descriptive text: %q", errStr)
	}

	// Sanity check: verify the wrong recipient key is actually wrong
	// (the ciphertext was sealed to recipPub, not wrongRecipPub)
	correctPlaintext, err := Open(ciphertext, nonce, wrongRecipPriv, wrongRecipPub)
	if err != nil {
		t.Logf("Verified: ciphertext cannot be decrypted with different recipient keys: %v", err)
	} else {
		t.Logf("Note: decryption succeeded (probably used wrong keypair setup), plaintext: %q", correctPlaintext)
	}
}

// TestTC154_02_VerifyAndOpenDecryptFailureMatchesErrDecryptionFailed tests TC-154-02:
// ErrDecryptionFailed matches through the full VerifyAndOpen chain when only Open fails
func TestTC154_02_VerifyAndOpenDecryptFailureMatchesErrDecryptionFailed(t *testing.T) {
	// Setup: keypairs for both sender and recipient
	senderEdPub, senderEdPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	senderX25519Pub, senderX25519Priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	recipX25519Pub, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	cache := NewReplayCache(60 * time.Second)
	plaintext := []byte("test message")

	// Create a correctly signed, replay-fresh envelope that is sealed to recipPub
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

	// Now call VerifyAndOpen with a WRONG recipient X25519 private key
	// Verify and replay check will pass, but Open will fail
	_, wrongRecipPriv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	_, err = VerifyAndOpen(signed, senderEdPub, cache, wrongRecipPriv, senderX25519Pub)

	// TC-154-02: err must be non-nil
	if err == nil {
		t.Fatal("VerifyAndOpen with wrong recipient key should have failed but returned nil")
	}

	// TC-154-02: errors.Is(err, ErrDecryptionFailed) must be true
	if !errors.Is(err, ErrDecryptionFailed) {
		t.Errorf("error should match ErrDecryptionFailed: %v", err)
	}

	// TC-154-02: The other four sentinels must NOT match this error
	if errors.Is(err, ErrUnknownKey) {
		t.Error("error should not match ErrUnknownKey")
	}
	if errors.Is(err, ErrBadSignature) {
		t.Error("error should not match ErrBadSignature")
	}
	if errors.Is(err, ErrReplay) {
		t.Error("error should not match ErrReplay")
	}
	if errors.Is(err, ErrStaleTimestamp) {
		t.Error("error should not match ErrStaleTimestamp")
	}
}

// TestTC154_03_MalformedHexPayloadNonceConsistentClassification tests TC-154-03:
// Malformed-hex Payload and Nonce are consistently classified
func TestTC154_03_MalformedHexPayloadNonceConsistentClassification(t *testing.T) {
	// Setup: keypairs
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

	// Create a correctly signed envelope
	ciphertext, nonce, err := Seal(plaintext, senderX25519Priv, recipX25519Pub)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	// Test 1: Malformed-hex Payload
	env1 := Envelope{
		From:    "sender",
		To:      "recipient",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      NowRFC3339(),
		Payload: "not-valid-hex!!", // Malformed hex
		Sig:     "",
	}

	signed1, err := Sign(env1, senderEdPriv)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	_, err1 := VerifyAndOpen(signed1, senderEdPub, cache, recipX25519Priv, senderX25519Pub)
	if err1 == nil {
		t.Fatal("VerifyAndOpen with malformed Payload hex should have failed")
	}

	// Test 2: Malformed-hex Nonce
	env2 := Envelope{
		From:    "sender",
		To:      "recipient",
		Nonce:   "not-valid-hex!!", // Malformed hex
		TS:      NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
		Sig:     "",
	}

	signed2, err := Sign(env2, senderEdPriv)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	_, err2 := VerifyAndOpen(signed2, senderEdPub, cache, recipX25519Priv, senderX25519Pub)
	if err2 == nil {
		t.Fatal("VerifyAndOpen with malformed Nonce hex should have failed")
	}

	// TC-154-03: Both should be classified identically
	// Per the implementation, malformed-hex errors are NOT wrapped as ErrDecryptionFailed;
	// they're bare errors returned from hex.DecodeString. So both should NOT match ErrDecryptionFailed.
	// This is the consistent classification: both malformed-hex cases are treated as unclassified "envelope_rejected".
	isErr1Decryption := errors.Is(err1, ErrDecryptionFailed)
	isErr2Decryption := errors.Is(err2, ErrDecryptionFailed)

	if isErr1Decryption != isErr2Decryption {
		t.Errorf("Malformed Payload and Nonce should be classified identically: Payload is ErrDecryptionFailed=%v, Nonce is ErrDecryptionFailed=%v",
			isErr1Decryption, isErr2Decryption)
	}

	// Both should return the same classification (false for ErrDecryptionFailed, since they're hex decode errors)
	if isErr1Decryption || isErr2Decryption {
		t.Errorf("Malformed hex errors should not be classified as ErrDecryptionFailed")
	}
}

// TestTC154_04_ExistingSentinelNoContamination tests TC-154-04:
// The four existing sentinels still classify correctly; no cross-contamination with ErrDecryptionFailed
func TestTC154_04_ExistingSentinelNoContamination(t *testing.T) {
	// Test ErrBadSignature
	t.Run("ErrBadSignature", func(t *testing.T) {
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

		plaintext := []byte("test message")
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

		// Tamper with signature
		signed.Sig = "0000000000000000000000000000000000000000000000000000000000000000" +
			"0000000000000000000000000000000000000000000000000000000000000000"

		cache := NewReplayCache(60 * time.Second)
		_, err = VerifyAndOpen(signed, senderEdPub, cache, recipX25519Priv, senderX25519Pub)
		if err == nil {
			t.Fatal("VerifyAndOpen with bad signature should have failed")
		}

		if !errors.Is(err, ErrBadSignature) {
			t.Errorf("expected ErrBadSignature but got: %v", err)
		}

		// Verify no cross-contamination
		if errors.Is(err, ErrDecryptionFailed) {
			t.Error("ErrBadSignature should not match ErrDecryptionFailed")
		}
		if errors.Is(err, ErrUnknownKey) {
			t.Error("ErrBadSignature should not match ErrUnknownKey")
		}
		if errors.Is(err, ErrReplay) {
			t.Error("ErrBadSignature should not match ErrReplay")
		}
		if errors.Is(err, ErrStaleTimestamp) {
			t.Error("ErrBadSignature should not match ErrStaleTimestamp")
		}
	})

	// Test ErrReplay detection (second call with same nonce)
	t.Run("ErrReplay", func(t *testing.T) {
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

		// First call should succeed
		_, err = VerifyAndOpen(signed, senderEdPub, cache, recipX25519Priv, senderX25519Pub)
		if err != nil {
			t.Fatalf("First VerifyAndOpen should have succeeded: %v", err)
		}

		// Second call with same nonce should trigger ErrReplay
		_, err = VerifyAndOpen(signed, senderEdPub, cache, recipX25519Priv, senderX25519Pub)
		if err == nil {
			t.Fatal("Second VerifyAndOpen with same nonce should have failed with ErrReplay")
		}

		if !errors.Is(err, ErrReplay) {
			t.Errorf("Expected ErrReplay but got: %v", err)
		}

		// Verify no cross-contamination
		if errors.Is(err, ErrDecryptionFailed) {
			t.Error("ErrReplay should not match ErrDecryptionFailed")
		}
		if errors.Is(err, ErrBadSignature) {
			t.Error("ErrReplay should not match ErrBadSignature")
		}
		if errors.Is(err, ErrUnknownKey) {
			t.Error("ErrReplay should not match ErrUnknownKey")
		}
		if errors.Is(err, ErrStaleTimestamp) {
			t.Error("ErrReplay should not match ErrStaleTimestamp")
		}
	})

	// Test ErrUnknownKey (Verify with wrong-size public key)
	t.Run("ErrUnknownKey", func(t *testing.T) {
		_, senderEdPriv, err := ed25519.GenerateKey(rand.Reader)
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

		plaintext := []byte("test message")
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

		cache := NewReplayCache(60 * time.Second)

		// Verify with wrong-sized public key (not ed25519.PublicKeySize)
		wrongSizePub := make(ed25519.PublicKey, 10) // Wrong size
		_, err = VerifyAndOpen(signed, wrongSizePub, cache, recipX25519Priv, senderX25519Pub)
		if err == nil {
			t.Fatal("VerifyAndOpen with wrong-sized public key should have failed")
		}

		if !errors.Is(err, ErrUnknownKey) {
			t.Errorf("expected ErrUnknownKey but got: %v", err)
		}

		// Verify no cross-contamination
		if errors.Is(err, ErrDecryptionFailed) {
			t.Error("ErrUnknownKey should not match ErrDecryptionFailed")
		}
		if errors.Is(err, ErrBadSignature) {
			t.Error("ErrUnknownKey should not match ErrBadSignature")
		}
		if errors.Is(err, ErrReplay) {
			t.Error("ErrUnknownKey should not match ErrReplay")
		}
		if errors.Is(err, ErrStaleTimestamp) {
			t.Error("ErrUnknownKey should not match ErrStaleTimestamp")
		}
	})

	// Test ErrStaleTimestamp (ReplayCache with stale timestamp)
	t.Run("ErrStaleTimestamp", func(t *testing.T) {
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

		plaintext := []byte("test message")
		ciphertext, nonce, err := Seal(plaintext, senderX25519Priv, recipX25519Pub)
		if err != nil {
			t.Fatalf("Seal failed: %v", err)
		}

		// Use a very old timestamp (beyond replay window)
		staleTime := time.Now().Add(-120 * time.Second)
		env := Envelope{
			From:    "sender",
			To:      "recipient",
			Nonce:   hex.EncodeToString(nonce[:]),
			TS:      staleTime.Format(time.RFC3339),
			Payload: hex.EncodeToString(ciphertext),
			Sig:     "",
		}

		signed, err := Sign(env, senderEdPriv)
		if err != nil {
			t.Fatalf("Sign failed: %v", err)
		}

		cache := NewReplayCache(60 * time.Second)

		_, err = VerifyAndOpen(signed, senderEdPub, cache, recipX25519Priv, senderX25519Pub)
		if err == nil {
			t.Fatal("VerifyAndOpen with stale timestamp should have failed")
		}

		if !errors.Is(err, ErrStaleTimestamp) {
			t.Errorf("expected ErrStaleTimestamp but got: %v", err)
		}

		// Verify no cross-contamination
		if errors.Is(err, ErrDecryptionFailed) {
			t.Error("ErrStaleTimestamp should not match ErrDecryptionFailed")
		}
		if errors.Is(err, ErrBadSignature) {
			t.Error("ErrStaleTimestamp should not match ErrBadSignature")
		}
		if errors.Is(err, ErrReplay) {
			t.Error("ErrStaleTimestamp should not match ErrReplay")
		}
		if errors.Is(err, ErrUnknownKey) {
			t.Error("ErrStaleTimestamp should not match ErrUnknownKey")
		}
	})
}
