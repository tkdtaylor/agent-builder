// Package worker implements the orchestrator↔worker transport (ADR 048). Work-items
// (a sub-goal's supervisor.Task) and results (supervisor.Result) are carried in
// internal/envelope.Envelope objects: Ed25519-signed, X25519+AEAD-sealed, and
// replay-checked. Per ADR 048 the v1 wire is in-process (matching task 081's
// sequential dispatch), but the envelope is the load-bearing security layer
// regardless of the wire — it gives the orchestrator↔worker trust boundary (ADR 042)
// tamper-evidence, provenance, replay resistance, and a ready seam for a future
// out-of-process worker without a security retrofit.
//
// # Leaf isolation (REQ-083-04, F-011)
//
// This package is a leaf: its only agent-builder/internal/ imports are
// internal/envelope, internal/supervisor, and internal/audit. The crypto/transport
// stays off the supervisor's import graph (F-003) and out of every other package.
//
// # Key roles (mirror internal/channel/telegram exactly)
//
//   - Work-item dispatch (orchestrator → worker): orchestrator signs with its
//     Ed25519 private key, seals with its X25519 private key to the worker's X25519
//     public key. Envelope From="orchestrator", To="worker".
//   - Result return (worker → orchestrator): worker signs with its Ed25519 private
//     key, seals with its X25519 private key to the orchestrator's X25519 public key.
//     Envelope From="worker", To="orchestrator".
//
// # Inbound ordering and role assertion
//
// Receivers follow the MANDATORY ordering from internal/envelope (verify → replay
// check → open) via VerifyAndOpen, then additionally assert the envelope From/To
// match the expected roles for the direction (task 098 SEC-001 carry-forward): key
// separation alone is not relied upon. Plaintext work-items/results never appear on
// any logged surface, and no key material is logged.
package worker

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// Envelope role constants for the two transport directions.
const (
	roleOrchestrator = "orchestrator"
	roleWorker       = "worker"
)

// Sender wraps a payload (work-item or result) in a signed+sealed envelope.
// The two directions are constructed via NewWorkItemSender (orchestrator side) and
// NewResultSender (worker side), which fix the From/To roles and the key material.
type Sender struct {
	from     string
	to       string
	edPriv   ed25519.PrivateKey // signer's Ed25519 private key
	xPriv    [32]byte           // signer's X25519 private key (seal sender)
	recipPub [32]byte           // recipient's X25519 public key (seal recipient)
	logger   *slog.Logger
}

// SenderConfig configures a Sender.
type SenderConfig struct {
	// EdPriv is the sender's Ed25519 private key (signs the envelope).
	EdPriv ed25519.PrivateKey
	// XPriv is the sender's X25519 private key (seal sender).
	XPriv [32]byte
	// RecipPub is the recipient's X25519 public key (seal recipient).
	RecipPub [32]byte
	// Logger is optional; defaults to a discard logger.
	Logger *slog.Logger
}

// NewWorkItemSender constructs the orchestrator-side sender for work-items
// (orchestrator → worker). From="orchestrator", To="worker".
func NewWorkItemSender(cfg SenderConfig) *Sender {
	return newSender(roleOrchestrator, roleWorker, cfg)
}

// NewResultSender constructs the worker-side sender for results
// (worker → orchestrator). From="worker", To="orchestrator".
func NewResultSender(cfg SenderConfig) *Sender {
	return newSender(roleWorker, roleOrchestrator, cfg)
}

func newSender(from, to string, cfg SenderConfig) *Sender {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Sender{
		from:     from,
		to:       to,
		edPriv:   cfg.EdPriv,
		xPriv:    cfg.XPriv,
		recipPub: cfg.RecipPub,
		logger:   logger,
	}
}

// DispatchWorkItem seals+signs a sub-goal's supervisor.Task as an envelope.
// Must be called on a sender constructed via NewWorkItemSender.
func (s *Sender) DispatchWorkItem(task supervisor.Task) (envelope.Envelope, error) {
	payload, err := json.Marshal(task)
	if err != nil {
		return envelope.Envelope{}, fmt.Errorf("worker transport: marshal work-item: %w", err)
	}
	return s.sealSign(payload)
}

// DispatchResult seals+signs a supervisor.Result as an envelope.
// Must be called on a sender constructed via NewResultSender.
func (s *Sender) DispatchResult(result supervisor.Result) (envelope.Envelope, error) {
	payload, err := json.Marshal(result)
	if err != nil {
		return envelope.Envelope{}, fmt.Errorf("worker transport: marshal result: %w", err)
	}
	return s.sealSign(payload)
}

// sealSign seals the plaintext (fresh per-message nonce from crypto/rand, via
// envelope.Seal) and signs the envelope with the sender's Ed25519 private key. The
// plaintext never appears in the returned envelope (Payload is ciphertext, hex-encoded
// to mirror VerifyAndOpen's decode) and never on any logged surface.
func (s *Sender) sealSign(plaintext []byte) (envelope.Envelope, error) {
	ciphertext, nonce, err := envelope.Seal(plaintext, s.xPriv, s.recipPub)
	if err != nil {
		return envelope.Envelope{}, fmt.Errorf("worker transport: seal failed: %w", err)
	}

	env := envelope.Envelope{
		From:    s.from,
		To:      s.to,
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
		Sig:     "",
	}

	env, err = envelope.Sign(env, s.edPriv)
	if err != nil {
		return envelope.Envelope{}, fmt.Errorf("worker transport: sign failed: %w", err)
	}

	s.logger.Debug("worker transport: sealed envelope", "from", s.from, "to", s.to, "nonce_prefix", env.Nonce[:8])
	return env, nil
}

// Receiver verifies+opens an inbound envelope, enforcing the MANDATORY ordering
// (verify → replay check → open) and asserting the envelope roles for the direction.
// The two directions are constructed via NewWorkItemReceiver (worker side) and
// NewResultReceiver (orchestrator side).
type Receiver struct {
	expectFrom string
	expectTo   string
	signPub    ed25519.PublicKey // sender's Ed25519 public key (verify)
	recipPriv  [32]byte          // receiver's X25519 private key (open)
	senderPub  [32]byte          // sender's X25519 public key (open)
	cache      *envelope.ReplayCache
	auditSink  audit.Sink
	logger     *slog.Logger
}

// ReceiverConfig configures a Receiver.
type ReceiverConfig struct {
	// SignPub is the sender's Ed25519 public key (verifies the signature).
	SignPub ed25519.PublicKey
	// RecipPriv is the receiver's X25519 private key (opens the seal).
	RecipPriv [32]byte
	// SenderPub is the sender's X25519 public key (opens the seal).
	SenderPub [32]byte
	// ReplayCache is the shared replay cache; defaults to a 60s-window cache.
	ReplayCache *envelope.ReplayCache
	// AuditSink records rejection events. Optional; if nil, rejections are not audited.
	AuditSink audit.Sink
	// Logger is optional; defaults to a discard logger.
	Logger *slog.Logger
}

// NewWorkItemReceiver constructs the worker-side receiver for work-items
// (orchestrator → worker). Asserts From="orchestrator", To="worker".
func NewWorkItemReceiver(cfg ReceiverConfig) *Receiver {
	return newReceiver(roleOrchestrator, roleWorker, cfg)
}

// NewResultReceiver constructs the orchestrator-side receiver for results
// (worker → orchestrator). Asserts From="worker", To="orchestrator".
func NewResultReceiver(cfg ReceiverConfig) *Receiver {
	return newReceiver(roleWorker, roleOrchestrator, cfg)
}

func newReceiver(expectFrom, expectTo string, cfg ReceiverConfig) *Receiver {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	cache := cfg.ReplayCache
	if cache == nil {
		cache = envelope.NewReplayCache(0)
	}
	return &Receiver{
		expectFrom: expectFrom,
		expectTo:   expectTo,
		signPub:    cfg.SignPub,
		recipPriv:  cfg.RecipPriv,
		senderPub:  cfg.SenderPub,
		cache:      cache,
		auditSink:  cfg.AuditSink,
		logger:     logger,
	}
}

// ReceiveWorkItem verifies+opens an inbound work-item envelope and returns the
// byte-exact supervisor.Task. On any rejection it returns the zero Task plus a
// classified error and emits an audit rejection event.
func (r *Receiver) ReceiveWorkItem(env envelope.Envelope) (supervisor.Task, error) {
	plaintext, err := r.verifyOpen(env)
	if err != nil {
		return supervisor.Task{}, err
	}
	var task supervisor.Task
	if err := json.Unmarshal(plaintext, &task); err != nil {
		r.emitReject("payload_decode_failed")
		return supervisor.Task{}, fmt.Errorf("worker transport: decode work-item: %w", err)
	}
	return task, nil
}

// ReceiveResult verifies+opens an inbound result envelope and returns the byte-exact
// supervisor.Result. On any rejection it returns the zero Result plus a classified
// error and emits an audit rejection event.
func (r *Receiver) ReceiveResult(env envelope.Envelope) (supervisor.Result, error) {
	plaintext, err := r.verifyOpen(env)
	if err != nil {
		return supervisor.Result{}, err
	}
	var result supervisor.Result
	if err := json.Unmarshal(plaintext, &result); err != nil {
		r.emitReject("payload_decode_failed")
		return supervisor.Result{}, fmt.Errorf("worker transport: decode result: %w", err)
	}
	return result, nil
}

// verifyOpen runs VerifyAndOpen (verify → replay check → open) and then asserts the
// declared envelope roles for this direction (task 098 SEC-001). It classifies the
// rejection reason for the audit sink and never logs plaintext or key material.
func (r *Receiver) verifyOpen(env envelope.Envelope) ([]byte, error) {
	plaintext, err := envelope.VerifyAndOpen(env, r.signPub, r.cache, r.recipPriv, r.senderPub)
	if err != nil {
		reason := r.classify(err)
		r.logger.Debug("worker transport: envelope rejected", "reason", reason)
		r.emitReject(reason)
		return nil, err
	}

	// Role assertion (task 098 SEC-001): do not rely solely on key separation.
	if env.From != r.expectFrom || env.To != r.expectTo {
		r.logger.Debug("worker transport: role mismatch", "reason", "role_mismatch")
		r.emitReject("role_mismatch")
		return nil, fmt.Errorf("worker transport: role mismatch: from=%q to=%q (want from=%q to=%q): %w",
			env.From, env.To, r.expectFrom, r.expectTo, ErrRoleMismatch)
	}

	return plaintext, nil
}

// classify maps an envelope error to a short audit reason string, never leaking
// payload or key material.
func (r *Receiver) classify(err error) string {
	switch {
	case errors.Is(err, envelope.ErrReplay):
		return "replay_detected"
	case errors.Is(err, envelope.ErrStaleTimestamp):
		return "replay_detected"
	case errors.Is(err, envelope.ErrUnknownKey):
		return "unknown_key"
	case errors.Is(err, envelope.ErrBadSignature):
		return "bad_signature"
	case errors.Is(err, envelope.ErrDecryptionFailed):
		return "decryption_failed"
	default:
		return "envelope_rejected"
	}
}

// emitReject records a channel-reject audit event with the given reason. Never
// includes payload or key material.
func (r *Receiver) emitReject(reason string) {
	if r.auditSink == nil {
		return
	}
	ev := audit.AuditEvent{
		Action: audit.ActionChannelReject,
		RunID:  "worker-transport",
		TaskID: "worker-inbound",
		Detail: audit.EventDetail{Reason: reason},
	}
	_ = r.auditSink.Append(ev)
}

// ErrRoleMismatch is returned when a verified+opened envelope's From/To roles do not
// match the expected direction (task 098 SEC-001 carry-forward).
var ErrRoleMismatch = errors.New("worker transport: envelope role mismatch")
