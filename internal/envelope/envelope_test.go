package envelope

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
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
