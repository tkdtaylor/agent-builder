package envelope

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

// TestRoundTripSignSealVerifyOpen tests TC-096-09: Full end-to-end round-trip
func TestRoundTripSignSealVerifyOpen(t *testing.T) {
	// Setup: Generate both Ed25519 and X25519 keypairs for sender and recipient.

	// Sender side
	senderEdPub, senderEdPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	senderX25519Pub, senderX25519Priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Recipient side
	_, _, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	recipX25519Pub, recipX25519Priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// TC-096-09 (sender side):
	// 1. Create plaintext goal
	plaintext := "deploy the new monitoring agent"

	// 2. Seal the plaintext with sender's X25519 private and recipient's X25519 public
	ciphertext, nonce, err := Seal([]byte(plaintext), senderX25519Priv, recipX25519Pub)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	// 3. Encode ciphertext as base64 for the envelope
	payloadEncoded := base64.StdEncoding.EncodeToString(ciphertext)

	// 4. Construct the Envelope
	nonceStr := hex.EncodeToString(nonce[:])
	env := Envelope{
		From:    "operator",
		To:      "orchestrator",
		Nonce:   nonceStr,
		TS:      time.Now().UTC().Format(time.RFC3339),
		Payload: payloadEncoded,
		Sig:     "",
	}

	// 5. Sign the envelope with sender's Ed25519 private key
	signedEnv, err := Sign(env, senderEdPriv)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// 6. Marshal the signed envelope to JSON (the wire representation)
	jsonBytes, err := json.Marshal(signedEnv)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// TC-096-09 (receiver side):
	// 7. Unmarshal the JSON bytes
	var received Envelope
	err = json.Unmarshal(jsonBytes, &received)
	if err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	// 8. Verify the signature with sender's Ed25519 public key
	err = Verify(received, senderEdPub)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}

	// 9. Decode the ciphertext from base64
	decodedCipher, err := base64.StdEncoding.DecodeString(received.Payload)
	if err != nil {
		t.Fatalf("base64.DecodeString failed: %v", err)
	}

	// Decode the nonce from hex
	nonceBytes, err := hex.DecodeString(received.Nonce)
	if err != nil {
		t.Fatalf("hex.DecodeString failed: %v", err)
	}
	var decodedNonce [24]byte
	copy(decodedNonce[:], nonceBytes)

	// 10. Open the ciphertext with recipient's X25519 private and sender's X25519 public
	got, err := Open(decodedCipher, decodedNonce, recipX25519Priv, senderX25519Pub)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// TC-096-09: Verify the original plaintext is recovered
	if string(got) != plaintext {
		t.Errorf("plaintext mismatch: got %q, want %q", string(got), plaintext)
	}

	// TC-096-09: Verify all six agent-mesh keys are present in the JSON
	keys := []string{"from", "to", "nonce", "ts", "payload", "sig"}
	jsonStr := string(jsonBytes)
	for _, key := range keys {
		if !bytes.Contains([]byte(jsonStr), []byte(`"`+key+`"`)) {
			t.Errorf("JSON missing key %q", key)
		}
	}

	// Verify the envelope contains the expected values
	if received.From != "operator" {
		t.Errorf("From mismatch: got %q, want %q", received.From, "operator")
	}
	if received.To != "orchestrator" {
		t.Errorf("To mismatch: got %q, want %q", received.To, "orchestrator")
	}
	if received.Nonce == "" {
		t.Error("Nonce is empty")
	}
	if received.TS == "" {
		t.Error("TS is empty")
	}
	if received.Sig == "" {
		t.Error("Sig is empty")
	}
}
