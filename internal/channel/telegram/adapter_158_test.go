package telegram_test

// TC-158-05 — a real (fake-runner) armor.Guard blocks a plaintext-mode message
// end-to-end through Adapter.Next (task 158, ADR 064).
//
// This test is deliberately scoped at the ADAPTER level (not through
// assembleTelegramInbound) so it isolates ONE thing: that a genuine armor.Guard
// (armor.NewGuard, satisfying telegram.ContentGuard via DecideContent) — not merely
// any hand-rolled ContentGuard double — drives the SAME armor_blocked audit reason and
// message-drop behavior processPlaintext already implements for the envelope path
// (task 097). TC-158-01/02/03/04 in internal/cli/orchestrate_158_test.go separately
// prove assembleTelegramInbound actually WIRES armor.NewGuard at the live production
// construction site for all four auth modes — the two concerns are independent and both
// must hold (a correct armor.Guard wired nowhere is as broken as a hardwired
// allowAllContentGuard that happens to look armor-shaped).
//
// Reuses tc157Done()/singleUpdateServer() shared helpers already defined for this
// package's ADR-063 mode tests (adapter_151_test.go / adapter_157_test.go).

import (
	"context"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/armor"
	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram/authz"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// tc158FakeBlockingRunner is an in-process armor.Runner fake (no subprocess dependency)
// that always returns a "block" decision, recording invocation count and the last
// request it saw — proof the REAL armor.Guard genuinely round-trips through it.
type tc158FakeBlockingRunner struct {
	calls       int
	lastRequest armor.Request
}

func (r *tc158FakeBlockingRunner) Run(_ context.Context, req armor.Request) (armor.Response, error) {
	r.calls++
	r.lastRequest = req
	return armor.Response{Decision: "block", Reason: "tc158 fixture block"}, nil
}

// TestTC158_05_RealArmorGuardBlocksPlaintextMessageEndToEnd proves a genuine armor.Guard
// (backed by a fake in-process Runner) wired as the adapter's ContentGuard drops a
// plaintext "open"-mode message and records the SAME armor_blocked audit reason
// processPlaintext already implements for the envelope path — now proven reachable on a
// plaintext-mode path with NO cryptographic auth gate of its own.
func TestTC158_05_RealArmorGuardBlocksPlaintextMessageEndToEnd(t *testing.T) {
	runner := &tc158FakeBlockingRunner{}
	guard := armor.NewGuard(armor.Config{Runner: runner})

	srv := singleUpdateServer(t, "do the thing", 555)
	sink := audit.NewFakeSink()

	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:          tc157Done(),
		BotToken:     "test-token",
		BaseURL:      srv.URL,
		HTTPClient:   srv.Client(),
		ContentGuard: guard,
		ReplayCache:  envelope.NewReplayCache(60 * time.Second),
		AuditSink:    sink,
		AuthMode:     authz.ModeOpen, // no cryptographic auth gate — armor is the sole content defense
	})

	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ok {
		t.Fatalf("armor-blocked message accepted (msg=%+v), want dropped", msg)
	}
	if runner.calls == 0 {
		t.Fatal("armor runner never invoked — guard not genuinely wired")
	}
	if runner.lastRequest.Content != "do the thing" {
		t.Errorf("armor request content = %q, want %q (proves the REAL plaintext reached armor.Guard.DecideContent)", runner.lastRequest.Content, "do the thing")
	}
	assertAuditReasonPrefix(t, sink, "armor_blocked")
	assertHasAuditReason(t, sink, "armor_blocked: tc158 fixture block")
}

// TestTC158_05_RealArmorGuardAllowsPlaintextMessageEndToEnd is the allow-side control:
// the same real armor.Guard, backed by a fake Runner that ALLOWS, lets a plaintext
// "allowlist"-mode message through — proving the block case above is genuinely armor's
// decision, not an adapter-level default that happens to reject everything.
func TestTC158_05_RealArmorGuardAllowsPlaintextMessageEndToEnd(t *testing.T) {
	runner := &tc158FakeAllowingRunner{}
	guard := armor.NewGuard(armor.Config{Runner: runner})

	store := seededStore(t, "42")
	srv := singleUpdateServer(t, "status", 42)
	sink := audit.NewFakeSink()

	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:          tc157Done(),
		BotToken:     "test-token",
		BaseURL:      srv.URL,
		HTTPClient:   srv.Client(),
		ContentGuard: guard,
		ReplayCache:  envelope.NewReplayCache(60 * time.Second),
		AuditSink:    sink,
		AuthMode:     authz.ModeAllowlist,
		AuthStore:    store,
	})

	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !ok {
		t.Fatal("armor-allowed message rejected, want acceptance")
	}
	if runner.calls != 1 {
		t.Errorf("armor calls = %d, want 1", runner.calls)
	}
	if msg.Kind != supervisor.MsgStatus {
		t.Errorf("msg.Kind = %v, want MsgStatus", msg.Kind)
	}
}

type tc158FakeAllowingRunner struct {
	calls int
}

func (r *tc158FakeAllowingRunner) Run(_ context.Context, _ armor.Request) (armor.Response, error) {
	r.calls++
	return armor.Response{Decision: "allow"}, nil
}
