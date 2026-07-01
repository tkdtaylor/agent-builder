package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/tkdtaylor/agent-builder/internal/envelope"
)

// generateEd25519KeyPair generates an Ed25519 keypair.
// Returns (public key, private key, error).
func generateEd25519KeyPair() ([]byte, []byte, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate Ed25519 key: %w", err)
	}
	return pub, priv, nil
}

// hexEncode converts bytes to hex string.
func hexEncode(b []byte) string {
	return hex.EncodeToString(b)
}

// hexDecode converts hex string to bytes.
func hexDecode(s string) ([]byte, error) {
	return hex.DecodeString(s)
}

// marshalJSON marshals a value to JSON.
func marshalJSON(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// unmarshalJSON unmarshals JSON into a value.
func unmarshalJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// BuildEnvelope seals a command with the operator's X25519 private key and
// orchestrator's X25519 public key, then signs the envelope with the operator's
// Ed25519 private key. The result is an Envelope ready to marshal and POST.
//
// This mirrors the inbound path in telegram.Adapter.VerifyAndOpen, but in reverse:
// - Seal encrypts with (operatorXPriv → orchestratorXPub)
// - Sign signs with operatorEdPriv
// - The result encodes Nonce and Payload as hex (not base64, despite the struct doc comment)
func BuildEnvelope(
	operatorEdPriv ed25519.PrivateKey,
	operatorXPriv [32]byte,
	orchestratorXPub [32]byte,
	cmdText []byte,
) (*envelope.Envelope, error) {
	// Seal the plaintext using envelope.Seal
	ciphertext, nonce, err := envelope.Seal(cmdText, operatorXPriv, orchestratorXPub)
	if err != nil {
		return nil, fmt.Errorf("failed to seal: %w", err)
	}

	// Build the Envelope struct with hex-encoded Nonce and Payload
	env := envelope.Envelope{
		From:    "operator",
		To:      "orchestrator",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
		Sig:     "", // Will be filled by Sign
	}

	// Sign the envelope
	signedEnv, err := envelope.Sign(env, operatorEdPriv)
	if err != nil {
		return nil, fmt.Errorf("failed to sign: %w", err)
	}

	return &signedEnv, nil
}
