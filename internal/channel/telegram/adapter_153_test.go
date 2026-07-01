package telegram_test

// TC-153-01/02 — Telegram `open` mode: accepts plaintext from ANY sender (task 153,
// ADR 063 Decision 1). Retained controls (armor, size caps, audit) apply unconditionally
// exactly as in allowlist/pairing (ADR 063 Decision 2) — open only removes the sender-ID
// gate, never the retained controls.
//
// Reuses the shared helpers (newModeKeys, singleUpdateServer, mode151Guard, etc.) defined
// in adapter_151_test.go — same package, same test-double conventions.

import (
	"strings"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram/authz"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// TC-153-01: open mode accepts plaintext from a never-before-seen sender ID (no store
// lookup, no owner gate at all). ContentGuard.DecideContent is invoked (armor retained);
// MsgStatus is derived correctly. A second, different unknown sender is likewise accepted
// in a fresh Next() call, with no per-sender state change (open never reads/writes a store
// — AuthStore is nil for the whole test, proving no store consultation occurs).
func TestTC153_01_OpenModeAcceptsAnyNeverSeenSender(t *testing.T) {
	k := newModeKeys(t)

	acceptFrom := func(senderID int64) supervisor.Message {
		srv := singleUpdateServer(t, "status", senderID)
		guard := &mode151Guard{}
		sink := audit.NewFakeSink()

		adapter := telegram.NewAdapter(telegram.Config{
			BotToken:          "test-token",
			BaseURL:           srv.URL,
			HTTPClient:        srv.Client(),
			TrustedSigningKey: k.opEdPub,
			TrustedX25519Pub:  k.opXPub,
			OrchestratorPriv:  k.orchXPriv,
			ContentGuard:      guard,
			ReplayCache:       envelope.NewReplayCache(60 * time.Second),
			AuditSink:         sink,
			AuthMode:          authz.ModeOpen,
			AuthStore:         nil, // open NEVER consults the store — proven by leaving it nil
		})

		msg, ok, err := adapter.Next()
		if err != nil {
			t.Fatalf("Next() sender=%d: %v", senderID, err)
		}
		if !ok {
			t.Fatalf("Next() sender=%d: ok=false, want acceptance (open mode gates no sender)", senderID)
		}
		if msg.Kind != supervisor.MsgStatus {
			t.Errorf("sender=%d msg.Kind = %v, want MsgStatus", senderID, msg.Kind)
		}
		if guard.calls != 1 {
			t.Errorf("sender=%d armor calls = %d, want 1 (armor retained, not bypassed)", senderID, guard.calls)
		}
		assertHasAuditReason(t, sink, string(authz.ReasonPlaintextAccepted))
		return msg
	}

	// First never-before-seen sender.
	acceptFrom(999999)

	// A second, DIFFERENT unknown sender in a fresh adapter/Next() call — also accepted,
	// with no per-sender state carried between them (no store exists in either case).
	acceptFrom(123456)
}

// TC-153-01 edge: open mode with a genuinely fresh goal (not "status") also derives the
// correct message kind for an unknown sender, confirming deriveMessage runs normally.
func TestTC153_01_OpenModeNewGoalFromUnknownSender(t *testing.T) {
	k := newModeKeys(t)
	srv := singleUpdateServer(t, "build the widget", 424242)
	guard := &mode151Guard{}
	sink := audit.NewFakeSink()

	adapter := telegram.NewAdapter(telegram.Config{
		BotToken:          "test-token",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: k.opEdPub,
		TrustedX25519Pub:  k.opXPub,
		OrchestratorPriv:  k.orchXPriv,
		ContentGuard:      guard,
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         sink,
		AuthMode:          authz.ModeOpen,
	})

	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !ok {
		t.Fatal("open mode rejected a never-seen sender's new goal, want acceptance")
	}
	if msg.Kind != supervisor.MsgNewGoal {
		t.Errorf("msg.Kind = %v, want MsgNewGoal", msg.Kind)
	}
	if msg.Goal.Spec != "build the widget" {
		t.Errorf("msg.Goal.Spec = %q, want %q", msg.Goal.Spec, "build the widget")
	}
	if guard.calls != 1 {
		t.Errorf("armor calls = %d, want 1", guard.calls)
	}
}

// TC-153-02: open mode still enforces the retained SEC-001/002 size cap. An oversized
// plaintext is rejected BEFORE armor (mirrors the existing text_too_long branch), an audit
// event is emitted, and ContentGuard is never invoked. Open does not weaken the
// retained-controls list from ADR 063 Decision 2.
func TestTC153_02_OpenModeOversizedPlaintextRejectedBeforeArmor(t *testing.T) {
	k := newModeKeys(t)
	big := strings.Repeat("x", 200) // exceeds the 100-byte cap configured below
	srv := singleUpdateServer(t, big, 777)
	guard := &mode151Guard{}
	sink := audit.NewFakeSink()

	adapter := telegram.NewAdapter(telegram.Config{
		BotToken:          "test-token",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: k.opEdPub,
		TrustedX25519Pub:  k.opXPub,
		OrchestratorPriv:  k.orchXPriv,
		ContentGuard:      guard,
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         sink,
		MaxMessageBytes:   100,
		AuthMode:          authz.ModeOpen,
	})

	_, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ok {
		t.Fatal("oversized plaintext accepted in open mode, want rejection")
	}
	if guard.calls != 0 {
		t.Errorf("armor invoked %d times on oversized plaintext, want 0 (size cap enforced before armor even in open mode)", guard.calls)
	}
	assertHasAuditReason(t, sink, "text_too_long")
	// Must not be misclassified as a plaintext-accepted or sender-gate reason.
	assertNoAuditReason(t, sink, string(authz.ReasonPlaintextAccepted))
	assertNoAuditReason(t, sink, string(authz.ReasonSenderNotApproved))
}
