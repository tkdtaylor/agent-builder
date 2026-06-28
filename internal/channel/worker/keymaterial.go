package worker

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// EnvWorkerSigningKey is the environment variable naming the file that holds the
// orchestrator's Ed25519 signing key for the worker transport. The file content is
// the hex-encoded 64-byte Ed25519 private key (ed25519.PrivateKeySize). When this
// variable is unset, or names a file that is absent/unreadable/malformed, the
// transport constructor fails loudly at startup before any work-item is dispatched
// or received (REQ-083-05).
const EnvWorkerSigningKey = "AGENT_BUILDER_WORKER_SIGNING_KEY"

// ErrMissingSigningKey is the NAMED sentinel returned when the worker-transport
// signing key material is unset or unreadable at startup. Callers use errors.Is to
// distinguish a missing-key startup failure from other configuration errors. The
// wrapped error message always names EnvWorkerSigningKey so the operator knows which
// configuration to set.
var ErrMissingSigningKey = errors.New("worker transport: missing signing key material")

// LoadSigningKey reads the orchestrator's Ed25519 signing key from the file named by
// AGENT_BUILDER_WORKER_SIGNING_KEY. It fails loudly at startup (not at first message
// receipt) when:
//   - the variable is unset,
//   - the file is absent or unreadable, or
//   - the content is not a valid hex-encoded ed25519.PrivateKeySize key.
//
// Every error wraps ErrMissingSigningKey and names AGENT_BUILDER_WORKER_SIGNING_KEY.
// The key bytes are never logged.
func LoadSigningKey() (ed25519.PrivateKey, error) {
	path := os.Getenv(EnvWorkerSigningKey)
	if path == "" {
		return nil, fmt.Errorf("%s is not set: %w", EnvWorkerSigningKey, ErrMissingSigningKey)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s=%q could not be read: %v: %w", EnvWorkerSigningKey, path, err, ErrMissingSigningKey)
	}

	decoded, err := hex.DecodeString(string(bytes.TrimSpace(raw)))
	if err != nil {
		return nil, fmt.Errorf("%s=%q is not valid hex: %w", EnvWorkerSigningKey, path, ErrMissingSigningKey)
	}

	if len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s=%q is not a %d-byte Ed25519 private key (got %d bytes): %w",
			EnvWorkerSigningKey, path, ed25519.PrivateKeySize, len(decoded), ErrMissingSigningKey)
	}

	return ed25519.PrivateKey(decoded), nil
}

// NewWorkItemSenderFromEnv constructs the orchestrator-side work-item Sender, loading
// the orchestrator's Ed25519 signing key from AGENT_BUILDER_WORKER_SIGNING_KEY at
// startup. The X25519 seal keys (the orchestrator's own X25519 private key and the
// worker's X25519 public key) are supplied by the caller — only the long-lived signing
// key is file-backed and subject to the startup check (REQ-083-05).
//
// On a missing/unreadable/malformed signing key it returns (nil, error) where the
// error satisfies errors.Is(err, ErrMissingSigningKey) and names the configuration —
// the failure happens here, at construction, before any dispatch.
func NewWorkItemSenderFromEnv(orchXPriv [32]byte, workerXPub [32]byte) (*Sender, error) {
	edPriv, err := LoadSigningKey()
	if err != nil {
		return nil, err
	}
	return NewWorkItemSender(SenderConfig{
		EdPriv:   edPriv,
		XPriv:    orchXPriv,
		RecipPub: workerXPub,
	}), nil
}
