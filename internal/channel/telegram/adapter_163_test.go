package telegram_test

// Task 163 item 2 — Telegram inbound envelope role assertion.
//
// Mirrors internal/channel/worker/transport.go's Receiver.verifyOpen: after a
// successful envelope.VerifyAndOpen, the Telegram adapter must also assert
// env.From == "operator" && env.To == "orchestrator" (task 098 SEC-001 — do not
// rely solely on key separation for role correctness). Before this task the
// Telegram leaf never adopted this check; a validly-signed, validly-decryptable
// envelope with swapped/wrong roles was accepted as a message.
//
// TC-163-03: a role-mismatched envelope that PASSES VerifyAndOpen is rejected
// (Next() does not deliver it as a message).
// TC-163-04: the rejection is audited with the distinct reason "role_mismatch",
// not the generic "envelope_rejected" fallback.
//
// Both tests build a VALIDLY SEALED envelope (real Seal + real Sign, trusted
// keys) with the From/To fields swapped, so the only thing that could reject it
// is the new role-assertion check, not VerifyAndOpen itself. This is the
// mutation-test discipline required by the task: faking the rejection or
// asserting only "ok=false" without confirming the envelope was otherwise valid
// would not prove the new check fired.
//
// TC-163-06 (full regression) is a re-run case, not new assertions: the
// pre-existing internal/runtime, internal/channel/telegram,
// internal/channel/worker, and internal/audit suites continue to pass unchanged
// aside from this task's additions. Verified by `go test -race -count=1
// ./internal/runtime/... ./internal/channel/telegram/... ./internal/channel/worker/...
// ./internal/audit/...` plus `make check` (see task report).

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

var _ = "TC-163-06" // marker anchor for grep

// buildTC163RoleMismatchedEnvelope constructs a fully valid Seal+Sign envelope
// (real crypto, trusted keys) whose From/To fields do NOT match the expected
// "operator"/"orchestrator" pair — everything else about the envelope is valid,
// so it passes VerifyAndOpen and only the new role-assertion check can reject it.
func buildTC163RoleMismatchedEnvelope(t *testing.T, operatorEdPriv ed25519.PrivateKey, operatorX25519Priv, orchX25519Pub [32]byte, plaintext []byte) []byte {
	t.Helper()

	ciphertext, nonce, err := envelope.Seal(plaintext, operatorX25519Priv, orchX25519Pub)
	if err != nil {
		t.Fatalf("failed to seal: %v", err)
	}

	env := envelope.Envelope{
		From:    "orchestrator", // swapped: wrong direction for an inbound message
		To:      "operator",     // swapped
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
		Sig:     "",
	}

	env, err = envelope.Sign(env, operatorEdPriv)
	if err != nil {
		t.Fatalf("failed to sign envelope: %v", err)
	}

	envJSON, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("failed to marshal envelope: %v", err)
	}
	return envJSON
}

// tc163Server serves the single scripted role-mismatched envelope on the first
// getUpdates poll, then empty batches. It is a minimal getUpdates stub mirroring
// the one in adapter_test.go's TestTC080_01, kept local so this file has no
// cross-file symbol dependency beyond the shared allowGuard.
func tc163Server(envJSON []byte) *httptest.Server {
	callCount := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var result []interface{}
		if callCount == 1 {
			result = []interface{}{
				map[string]interface{}{
					"update_id": 200,
					"message": map[string]interface{}{
						"text": string(envJSON),
					},
				},
			}
		}
		response := map[string]interface{}{
			"ok":     true,
			"result": result,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
}

// TC-163-03: a role-mismatched (but otherwise validly signed/decryptable)
// envelope is rejected — Next() does not deliver it as a message.
func TestTC163_03_RoleMismatchedEnvelopeRejected(t *testing.T) {
	operatorEdPub, operatorEdPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate operator Ed25519 key: %v", err)
	}
	operatorX25519Pub, operatorX25519Priv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate operator X25519 keypair: %v", err)
	}
	orchX25519Pub, orchX25519Priv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate orchestrator X25519 keypair: %v", err)
	}

	plaintext := []byte("build the auth module")
	envJSON := buildTC163RoleMismatchedEnvelope(t, operatorEdPriv, operatorX25519Priv, orchX25519Pub, plaintext)

	stubServer := tc163Server(envJSON)
	defer stubServer.Close()

	stubGuard := &allowGuard{}
	stubAudit := audit.NewFakeSink()

	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(), // pre-cancelled: exactly one poll, then ok=false
		BotToken:          "test-token",
		BaseURL:           stubServer.URL,
		HTTPClient:        stubServer.Client(),
		TrustedSigningKey: operatorEdPub,
		TrustedX25519Pub:  operatorX25519Pub,
		OrchestratorPriv:  orchX25519Priv,
		ContentGuard:      stubGuard,
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         stubAudit,
	})

	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("adapter.Next() returned error: %v", err)
	}
	if ok {
		t.Fatalf("adapter.Next() returned ok=true with msg=%+v; want ok=false — a role-mismatched envelope must be rejected despite passing VerifyAndOpen", msg)
	}
	// Armor must never be reached for a role-mismatched envelope — the rejection
	// happens before processPlaintext (mirrors the worker transport's ordering:
	// role check right after VerifyAndOpen, before any further processing).
	if stubGuard.invocationCount != 0 {
		t.Errorf("armor invocation count = %d, want 0 (role check must reject before armor)", stubGuard.invocationCount)
	}
}

// TC-163-04: the role-mismatch rejection is audited with the distinct reason
// "role_mismatch", not the generic "envelope_rejected" fallback.
func TestTC163_04_RoleMismatchAuditedWithDistinctReason(t *testing.T) {
	operatorEdPub, operatorEdPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate operator Ed25519 key: %v", err)
	}
	operatorX25519Pub, operatorX25519Priv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate operator X25519 keypair: %v", err)
	}
	orchX25519Pub, orchX25519Priv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate orchestrator X25519 keypair: %v", err)
	}

	plaintext := []byte("build the auth module")
	envJSON := buildTC163RoleMismatchedEnvelope(t, operatorEdPriv, operatorX25519Priv, orchX25519Pub, plaintext)

	stubServer := tc163Server(envJSON)
	defer stubServer.Close()

	stubGuard := &allowGuard{}
	stubAudit := audit.NewFakeSink()

	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "test-token",
		BaseURL:           stubServer.URL,
		HTTPClient:        stubServer.Client(),
		TrustedSigningKey: operatorEdPub,
		TrustedX25519Pub:  operatorX25519Pub,
		OrchestratorPriv:  orchX25519Priv,
		ContentGuard:      stubGuard,
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         stubAudit,
	})

	_, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("adapter.Next() returned error: %v", err)
	}
	if ok {
		t.Fatal("adapter.Next() returned ok=true; want ok=false for role-mismatched envelope")
	}

	events := stubAudit.Events()
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 audit event for the role-mismatched update, got %d: %+v", len(events), events)
	}
	if events[0].Action != audit.ActionChannelReject {
		t.Errorf("events[0].Action = %v, want ActionChannelReject", events[0].Action)
	}
	if events[0].Detail.Reason != "role_mismatch" {
		t.Errorf("events[0].Detail.Reason = %q, want %q (distinct from the generic envelope_rejected fallback)", events[0].Detail.Reason, "role_mismatch")
	}
	if events[0].Detail.Reason == "envelope_rejected" {
		t.Error("role mismatch was classified as the generic envelope_rejected fallback, want the distinct role_mismatch reason")
	}
}

// TC-163-03/04 negative control: a CORRECTLY-rolled envelope (From=operator,
// To=orchestrator) with the same keys and same plaintext is still accepted and
// delivered — proves the new check does not over-reject valid traffic.
func TestTC163_CorrectlyRolledEnvelopeStillAccepted(t *testing.T) {
	operatorEdPub, operatorEdPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate operator Ed25519 key: %v", err)
	}
	operatorX25519Pub, operatorX25519Priv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate operator X25519 keypair: %v", err)
	}
	orchX25519Pub, orchX25519Priv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate orchestrator X25519 keypair: %v", err)
	}

	plaintext := []byte("build the auth module")
	ciphertext, nonce, err := envelope.Seal(plaintext, operatorX25519Priv, orchX25519Pub)
	if err != nil {
		t.Fatalf("failed to seal: %v", err)
	}
	env := envelope.Envelope{
		From:    "operator",
		To:      "orchestrator",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
		Sig:     "",
	}
	env, err = envelope.Sign(env, operatorEdPriv)
	if err != nil {
		t.Fatalf("failed to sign envelope: %v", err)
	}
	envJSON, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("failed to marshal envelope: %v", err)
	}

	stubServer := tc163Server(envJSON)
	defer stubServer.Close()

	stubGuard := &allowGuard{}
	stubAudit := audit.NewFakeSink()

	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "test-token",
		BaseURL:           stubServer.URL,
		HTTPClient:        stubServer.Client(),
		TrustedSigningKey: operatorEdPub,
		TrustedX25519Pub:  operatorX25519Pub,
		OrchestratorPriv:  orchX25519Priv,
		ContentGuard:      stubGuard,
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         stubAudit,
	})

	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("adapter.Next() returned error: %v", err)
	}
	if !ok {
		t.Fatal("adapter.Next() returned ok=false for a correctly-rolled envelope; want ok=true")
	}
	if msg.Kind != supervisor.MsgNewGoal {
		t.Errorf("msg.Kind = %v, want MsgNewGoal", msg.Kind)
	}
	if len(stubAudit.Events()) != 0 {
		t.Errorf("expected zero audit rejection events for a correctly-rolled envelope, got %d", len(stubAudit.Events()))
	}
}
