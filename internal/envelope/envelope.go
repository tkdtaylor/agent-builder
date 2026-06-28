// Package envelope provides cryptographic primitives for agent-mesh-compatible
// message signing, encryption, and replay prevention.
//
// # MANDATORY ORDERING FOR INBOUND MESSAGES
//
// When processing inbound envelopes, the following order MUST be observed:
//
//	1. Verify(env, senderPub) — reject forged/tampered signatures before any decryption
//	2. ReplayCache.Check(env.Nonce, env.TS) — reject stale/replayed messages
//	3. Open(ciphertext, nonce, recipPriv, senderPub) — decrypt only after authenticity is proven
//
// WHY THIS ORDER:
// - Never decrypt unverified input: a forged ciphertext could exploit the AEAD implementation
// - Verify is cheap (~100ns); replay check is fast (hash lookup); decrypt is expensive (~1us)
// - This prevents an attacker from triggering decryption cost amplification
//
// CONVENIENCE: Call VerifyAndOpen(env, signPub, cache, recipPriv, senderPub) for the safe path
// that enforces this ordering and returns plaintext in one call.
//
// # NONCE MANAGEMENT
//
// For SEALED (confidential) envelopes: the nonce returned by Seal() is authoritative and
// MUST be placed in Envelope.Nonce before signing. The same nonce value is used for both
// AEAD (encryption/decryption) and replay prevention.
//
// For signing-only (authenticity) envelopes: call GenerateNonce() to create a fresh nonce
// for the Nonce field before signing.
package envelope

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Sentinel errors for classification of envelope verification failures.
// These are exported so that consumers can use errors.Is() to determine
// the specific reason an envelope was rejected.
var (
	ErrUnknownKey       = errors.New("unknown_key")
	ErrBadSignature     = errors.New("bad_signature")
	ErrReplay           = errors.New("replay")
	ErrStaleTimestamp   = errors.New("stale_timestamp")
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
// Returns nil if the signature is valid; returns a wrapped sentinel error for unknown key,
// bad signature, or other failures.
func Verify(env Envelope, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key size: %w", ErrUnknownKey)
	}

	// Decode the signature from hex.
	sig, err := hex.DecodeString(env.Sig)
	if err != nil {
		return fmt.Errorf("invalid hex encoding: %w", ErrBadSignature)
	}

	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("invalid signature size: expected %d, got %d: %w", ed25519.SignatureSize, len(sig), ErrBadSignature)
	}

	// Compute the canonical signing body (reconstructed from the envelope without Sig).
	canonical := signingBytes(env)

	// Verify the signature.
	if !ed25519.Verify(pub, canonical, sig) {
		return fmt.Errorf("verification failed: %w", ErrBadSignature)
	}

	return nil
}

// signingBytes returns the canonical form of the Envelope that is signed.
// It mirrors agent-mesh's canonicalization: JSON marshalling of the Envelope
// with Sig set to empty string. The struct field order (From, To, Nonce, TS,
// Payload, Sig) ensures the marshalled bytes are deterministic and byte-identical
// to agent-mesh for the same field values, restoring wire interoperability.
// Using canonical JSON prevents field-confusion attacks (e.g., payloads
// containing the field separator character).
func signingBytes(env Envelope) []byte {
	// Marshal with Sig empty to get the canonical signing bytes.
	// The struct field order is stable due to Go's JSON marshaller behavior.
	body := Envelope{From: env.From, To: env.To, Nonce: env.Nonce, TS: env.TS, Payload: env.Payload, Sig: ""}
	b, _ := json.Marshal(body)
	return b
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

// VerifyAndOpen is a convenience function that enforces the MANDATORY ordering for inbound
// messages: Verify signature → Check replay cache → Open (decrypt). Returns the decrypted
// plaintext or an error if any step fails.
//
// This makes the safe path the easy path for task 080/083 consumers.
// Errors are wrapped with sentinel errors so callers can use errors.Is() for classification.
func VerifyAndOpen(env Envelope, signPub ed25519.PublicKey, cache *ReplayCache, recipX25519Priv [32]byte, senderX25519Pub [32]byte) ([]byte, error) {
	// Step 1: Verify signature (cheap, rejects forgeries before decryption)
	if err := Verify(env, signPub); err != nil {
		return nil, fmt.Errorf("verify failed: %w", err)
	}

	// Decode the nonce from hex for replay check and decryption
	nonceBytes, err := hex.DecodeString(env.Nonce)
	if err != nil {
		return nil, fmt.Errorf("invalid nonce hex: %w", err)
	}
	if len(nonceBytes) != 24 {
		return nil, fmt.Errorf("nonce must be 24 bytes, got %d", len(nonceBytes))
	}
	var nonce [24]byte
	copy(nonce[:], nonceBytes)

	// Parse timestamp for replay check
	ts, err := time.Parse(time.RFC3339, env.TS)
	if err != nil {
		// Try Unix timestamp format as fallback
		var ts64 int64
		_, errUnix := fmt.Sscanf(env.TS, "%d", &ts64)
		if errUnix != nil {
			return nil, fmt.Errorf("invalid timestamp format: %w", err)
		}
		ts = time.Unix(ts64, 0).UTC()
	}

	// Step 2: Check replay cache (moderate cost, rejects replayed messages before decryption)
	if err := cache.Check(env.Nonce, ts); err != nil {
		return nil, fmt.Errorf("replay check failed: %w", err)
	}

	// Step 3: Open (decrypt) — only after authenticity and freshness are proven
	ciphertext, err := hex.DecodeString(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("invalid payload hex: %w", err)
	}

	plaintext, err := Open(ciphertext, nonce, recipX25519Priv, senderX25519Pub)
	if err != nil {
		return nil, fmt.Errorf("open failed: %w", err)
	}

	return plaintext, nil
}
