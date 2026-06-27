package envelope

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Envelope is the agent-mesh-compatible wire format for signed/encrypted messages.
// It carries sender identity, recipient, a nonce for replay prevention, a timestamp,
// the payload (plaintext or ciphertext), and an Ed25519 signature over the canonical
// body (signingBytes).
type Envelope struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Nonce   string `json:"nonce"`    // 24-byte random value, hex-encoded
	TS      string `json:"ts"`       // RFC3339 or Unix timestamp
	Payload string `json:"payload"`  // base64-encoded ciphertext or plaintext
	Sig     string `json:"sig"`      // hex-encoded Ed25519 signature
}

// Sign computes an Ed25519 signature over the canonical signingBytes and populates
// the Sig field. It returns the signed Envelope (with the original fields intact)
// or an error if signing fails (e.g., invalid key format).
func Sign(env Envelope, priv ed25519.PrivateKey) (Envelope, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return Envelope{}, fmt.Errorf("invalid private key size: expected %d, got %d", ed25519.PrivateKeySize, len(priv))
	}

	// Compute the canonical signing body (all fields except Sig).
	canonical := signingBytes(env)

	// Sign with the Ed25519 private key.
	sig := ed25519.Sign(priv, canonical)

	// Populate the Sig field with hex encoding.
	env.Sig = hex.EncodeToString(sig)
	return env, nil
}

// Verify checks the Ed25519 signature against the provided public key.
// Returns nil if the signature is valid; returns a named error for unknown key,
// bad signature, or other failures.
func Verify(env Envelope, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("unknown_key: invalid public key size")
	}

	// Decode the signature from hex.
	sig, err := hex.DecodeString(env.Sig)
	if err != nil {
		return fmt.Errorf("bad_signature: invalid hex encoding: %w", err)
	}

	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("bad_signature: invalid signature size: expected %d, got %d", ed25519.SignatureSize, len(sig))
	}

	// Compute the canonical signing body (reconstructed from the envelope without Sig).
	canonical := signingBytes(env)

	// Verify the signature.
	if !ed25519.Verify(pub, canonical, sig) {
		return errors.New("bad_signature: verification failed")
	}

	return nil
}

// signingBytes returns the canonical form of the Envelope that is signed.
// It follows agent-mesh's canonicalization: a deterministic encoding of all
// fields except Sig, using pipe-delimited concatenation: from|to|nonce|ts|payload
func signingBytes(env Envelope) []byte {
	// Use pipe-delimited format matching agent-mesh.
	canonical := fmt.Sprintf("%s|%s|%s|%s|%s",
		env.From,
		env.To,
		env.Nonce,
		env.TS,
		env.Payload,
	)
	return []byte(canonical)
}

// GenerateNonce generates a 24-byte cryptographically random nonce for AEAD,
// returning it as a hex-encoded string suitable for the Nonce field.
func GenerateNonce() (string, error) {
	nonce := make([]byte, 24)
	_, err := rand.Read(nonce)
	if err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	return hex.EncodeToString(nonce), nil
}

// NowRFC3339 returns the current time formatted as RFC3339, suitable for the TS field.
func NowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
