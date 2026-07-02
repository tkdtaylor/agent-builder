package telegram_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram/authz"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/ingestion"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// --- shared helpers for the ADR 063 mode tests ---------------------------------------

// modeKeys is a full set of envelope keypairs for building signed+sealed envelopes and
// constructing an adapter that can verify them.
type modeKeys struct {
	opEdPub   ed25519.PublicKey
	opEdPriv  ed25519.PrivateKey
	opXPub    [32]byte
	opXPriv   [32]byte
	orchXPub  [32]byte
	orchXPriv [32]byte
}

func newModeKeys(t *testing.T) modeKeys {
	t.Helper()
	opEdPub, opEdPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen operator Ed25519: %v", err)
	}
	opXPub, opXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("gen operator X25519: %v", err)
	}
	orchXPub, orchXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("gen orchestrator X25519: %v", err)
	}
	return modeKeys{
		opEdPub: opEdPub, opEdPriv: opEdPriv,
		opXPub: opXPub, opXPriv: opXPriv,
		orchXPub: orchXPub, orchXPriv: orchXPriv,
	}
}

// buildEnvelopeJSON produces the marshaled JSON of a signed+sealed envelope carrying
// plaintext, exactly as the operator CLI / existing envelope-path tests do.
func buildEnvelopeJSON(t *testing.T, k modeKeys, plaintext []byte) string {
	t.Helper()
	ct, nonce, err := envelope.Seal(plaintext, k.opXPriv, k.orchXPub)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	env := envelope.Envelope{
		From:    "operator",
		To:      "orchestrator",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ct),
	}
	env, err = envelope.Sign(env, k.opEdPriv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return string(b)
}

// singleUpdateServer returns a stub Telegram server that yields exactly one update on
// the first getUpdates poll (with the given text + sender id), then empty batches.
func singleUpdateServer(t *testing.T, text string, senderID int64) *httptest.Server {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		var result []any
		if calls == 1 {
			msg := map[string]any{
				"message_id": 7,
				"text":       text,
			}
			if senderID != 0 {
				msg["from"] = map[string]any{"id": senderID}
				msg["chat"] = map[string]any{"id": senderID}
			}
			result = []any{map[string]any{"update_id": 100, "message": msg}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- TC-151-01 -----------------------------------------------------------------------

// TC-151-01: unset AUTH_MODE reproduces today's envelope behavior byte-for-byte, and an
// explicit "envelope" mode produces the identical supervisor.Message. No authz store is
// consulted (nil store), proving the sender ID is never checked on this path.
func TestTC151_01_UnsetAndEnvelopeModeIdenticalToPreTask(t *testing.T) {
	k := newModeKeys(t)
	plaintext := []byte("build the auth module")
	envJSON := buildEnvelopeJSON(t, k, plaintext)

	run := func(mode authz.Mode) supervisor.Message {
		srv := singleUpdateServer(t, envJSON, 12345)
		guard := &mode151Guard{}
		adapter := telegram.NewAdapter(telegram.Config{
			Ctx:               tc157Done(),
			BotToken:          "test-token",
			BaseURL:           srv.URL,
			HTTPClient:        srv.Client(),
			TrustedSigningKey: k.opEdPub,
			TrustedX25519Pub:  k.opXPub,
			OrchestratorPriv:  k.orchXPriv,
			ContentGuard:      guard,
			ReplayCache:       envelope.NewReplayCache(60 * time.Second),
			AuditSink:         audit.NewFakeSink(),
			AuthMode:          mode,
			AuthStore:         nil, // no store built — sender ID must never be consulted
		})
		msg, ok, err := adapter.Next()
		if err != nil {
			t.Fatalf("Next(%q): %v", mode, err)
		}
		if !ok {
			t.Fatalf("Next(%q): ok=false, want a derived message", mode)
		}
		if guard.calls != 1 {
			t.Errorf("armor calls under %q = %d, want 1", mode, guard.calls)
		}
		return msg
	}

	// Unset (zero value "") and explicit "envelope" must produce the identical message.
	unsetMsg := run("")
	envMsg := run(authz.ModeEnvelope)

	if unsetMsg.Kind != supervisor.MsgNewGoal || envMsg.Kind != supervisor.MsgNewGoal {
		t.Errorf("kinds: unset=%v envelope=%v, want MsgNewGoal", unsetMsg.Kind, envMsg.Kind)
	}
	if unsetMsg.Goal.Spec != string(plaintext) {
		t.Errorf("unset Goal.Spec = %q, want %q", unsetMsg.Goal.Spec, plaintext)
	}
	if unsetMsg.Kind != envMsg.Kind || unsetMsg.GoalID != envMsg.GoalID || unsetMsg.Goal.Spec != envMsg.Goal.Spec {
		t.Errorf("unset message %+v != envelope message %+v", unsetMsg, envMsg)
	}
}

// --- TC-151-02 -----------------------------------------------------------------------

// TC-151-02: envelope mode still rejects plaintext exactly as today (envelope_parse_failed),
// no message derived, sender ID never consulted (nil store).
func TestTC151_02_EnvelopeModeRejectsPlaintext(t *testing.T) {
	k := newModeKeys(t)
	srv := singleUpdateServer(t, "status", 12345)
	guard := &mode151Guard{}
	sink := audit.NewFakeSink()

	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "test-token",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: k.opEdPub,
		TrustedX25519Pub:  k.opXPub,
		OrchestratorPriv:  k.orchXPriv,
		ContentGuard:      guard,
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         sink,
		AuthMode:          authz.ModeEnvelope,
	})

	_, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ok {
		t.Fatal("plaintext accepted in envelope mode, want rejection")
	}
	if guard.calls != 0 {
		t.Errorf("armor invoked %d times on rejected plaintext, want 0", guard.calls)
	}
	assertAuditReasonPrefix(t, sink, "envelope_parse_failed")
	// Must NOT be an authz-mode reason.
	assertNoAuditReason(t, sink, string(authz.ReasonSenderNotApproved))
	assertNoAuditReason(t, sink, string(authz.ReasonPlaintextAccepted))
}

// --- TC-151-03 -----------------------------------------------------------------------

// TC-151-03: allowlist mode accepts a plaintext command from an approved sender,
// invokes ContentGuard with the plaintext, and derives the correct message.
func TestTC151_03_AllowlistAcceptsApprovedSender(t *testing.T) {
	k := newModeKeys(t)
	store := seededStore(t, "42")
	srv := singleUpdateServer(t, "status", 42)
	guard := &mode151Guard{}
	sink := audit.NewFakeSink()

	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "test-token",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: k.opEdPub,
		TrustedX25519Pub:  k.opXPub,
		OrchestratorPriv:  k.orchXPriv,
		ContentGuard:      guard,
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         sink,
		AuthMode:          authz.ModeAllowlist,
		AuthStore:         store,
	})

	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !ok {
		t.Fatal("approved sender plaintext rejected, want acceptance")
	}
	if msg.Kind != supervisor.MsgStatus {
		t.Errorf("msg.Kind = %v, want MsgStatus", msg.Kind)
	}
	// Armor was invoked exactly once with the plaintext as the candidate — NOT bypassed.
	if guard.calls != 1 {
		t.Fatalf("armor calls = %d, want 1 (armor must run on accepted plaintext)", guard.calls)
	}
	if string(guard.lastContent) != "status" {
		t.Errorf("armor candidate content = %q, want %q", guard.lastContent, "status")
	}
	assertHasAuditReason(t, sink, string(authz.ReasonPlaintextAccepted))
}

// --- TC-151-04 -----------------------------------------------------------------------

// TC-151-04: allowlist mode rejects a plaintext command from an unapproved sender, emits
// a sender_not_approved audit event, and never invokes ContentGuard.
func TestTC151_04_AllowlistRejectsUnapprovedSender(t *testing.T) {
	k := newModeKeys(t)
	store := seededStore(t, "42")
	srv := singleUpdateServer(t, "status", 99) // 99 not approved
	guard := &mode151Guard{}
	sink := audit.NewFakeSink()

	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "test-token",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: k.opEdPub,
		TrustedX25519Pub:  k.opXPub,
		OrchestratorPriv:  k.orchXPriv,
		ContentGuard:      guard,
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         sink,
		AuthMode:          authz.ModeAllowlist,
		AuthStore:         store,
	})

	_, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ok {
		t.Fatal("unapproved sender accepted, want rejection")
	}
	if guard.calls != 0 {
		t.Errorf("armor invoked %d times for unapproved sender, want 0 (rejected before armor)", guard.calls)
	}
	assertHasAuditReason(t, sink, string(authz.ReasonSenderNotApproved))
}

// TC-151-04 edge: an unapproved sender with an envelope-shaped payload is still rejected
// in allowlist mode (plaintext-only modes do not fall back to envelope verification).
func TestTC151_04_AllowlistUnapprovedEnvelopeShapedStillRejected(t *testing.T) {
	k := newModeKeys(t)
	store := seededStore(t, "42")
	envJSON := buildEnvelopeJSON(t, k, []byte("build it"))
	srv := singleUpdateServer(t, envJSON, 99) // valid envelope, unapproved sender
	guard := &mode151Guard{}
	sink := audit.NewFakeSink()

	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "test-token",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: k.opEdPub,
		TrustedX25519Pub:  k.opXPub,
		OrchestratorPriv:  k.orchXPriv,
		ContentGuard:      guard,
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         sink,
		AuthMode:          authz.ModeAllowlist,
		AuthStore:         store,
	})

	_, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ok {
		t.Fatal("unapproved sender with envelope payload accepted, want rejection (no opportunistic fallback)")
	}
	if guard.calls != 0 {
		t.Errorf("armor invoked %d times, want 0", guard.calls)
	}
	assertHasAuditReason(t, sink, string(authz.ReasonSenderNotApproved))
}

// --- TC-151-05 -----------------------------------------------------------------------

// TC-151-05: allowlist mode oversized plaintext is rejected with SEC-001/002 caps
// retained (rejected before armor), audited with the size-cap reason.
func TestTC151_05_AllowlistOversizedPlaintextRejected(t *testing.T) {
	k := newModeKeys(t)
	store := seededStore(t, "42")
	big := strings.Repeat("x", 200) // exceeds MaxMessageBytes below
	srv := singleUpdateServer(t, big, 42)
	guard := &mode151Guard{}
	sink := audit.NewFakeSink()

	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "test-token",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: k.opEdPub,
		TrustedX25519Pub:  k.opXPub,
		OrchestratorPriv:  k.orchXPriv,
		ContentGuard:      guard,
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         sink,
		MaxMessageBytes:   100, // small cap so 200 bytes is oversized
		AuthMode:          authz.ModeAllowlist,
		AuthStore:         store,
	})

	_, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ok {
		t.Fatal("oversized plaintext accepted, want rejection")
	}
	if guard.calls != 0 {
		t.Errorf("armor invoked %d times on oversized plaintext, want 0 (size cap before armor)", guard.calls)
	}
	assertHasAuditReason(t, sink, "text_too_long")
}

// --- TC-151-06 -----------------------------------------------------------------------

// TC-151-06: disabled mode rejects both an envelope-shaped and a plaintext update with
// a distinct channel_disabled audit event, never invoking VerifyAndOpen or ContentGuard.
func TestTC151_06_DisabledModeRejectsEverything(t *testing.T) {
	k := newModeKeys(t)

	newDisabledAdapter := func(text string, sink *audit.FakeSink, guard *mode151Guard) *telegram.Adapter {
		srv := singleUpdateServer(t, text, 42)
		return telegram.NewAdapter(telegram.Config{
			Ctx:               tc157Done(),
			BotToken:          "test-token",
			BaseURL:           srv.URL,
			HTTPClient:        srv.Client(),
			TrustedSigningKey: k.opEdPub,
			TrustedX25519Pub:  k.opXPub,
			OrchestratorPriv:  k.orchXPriv,
			ContentGuard:      guard,
			ReplayCache:       envelope.NewReplayCache(60 * time.Second),
			AuditSink:         sink,
			AuthMode:          authz.ModeDisabled,
		})
	}

	// (a) a real envelope, (b) a plaintext that would be approved under allowlist.
	envJSON := buildEnvelopeJSON(t, k, []byte("build it"))
	for _, tc := range []struct {
		name string
		text string
	}{
		{"envelope-shaped", envJSON},
		{"plaintext", "status"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := audit.NewFakeSink()
			guard := &mode151Guard{}
			adapter := newDisabledAdapter(tc.text, sink, guard)

			_, ok, err := adapter.Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if ok {
				t.Fatalf("%s accepted in disabled mode, want rejection", tc.name)
			}
			if guard.calls != 0 {
				t.Errorf("armor invoked %d times in disabled mode, want 0", guard.calls)
			}
			assertHasAuditReason(t, sink, string(authz.ReasonChannelDisabled))
			// disabled must NOT emit a decryption/parse reason — it rejects before parse.
			assertNoAuditReason(t, sink, "decryption_failed")
			assertNoAuditReason(t, sink, "unknown_key")
		})
	}
}

// --- test doubles + audit helpers ----------------------------------------------------

// mode151Guard is a content guard that counts invocations and records the last content
// it saw. It always allows. (The other _test.go file has allowGuard/countingGuard; this
// one records content for the "armor saw the plaintext" assertion in TC-151-03.)
type mode151Guard struct {
	calls       int
	lastContent []byte
}

func (g *mode151Guard) DecideContent(_ context.Context, candidate ingestion.ContentCandidate) (ingestion.Decision, error) {
	g.calls++
	g.lastContent = append([]byte(nil), candidate.Content...)
	return ingestion.Decision{
		CandidateID: candidate.ID,
		Kind:        ingestion.CandidateKindContent,
		Outcome:     ingestion.DecisionAllow,
	}, nil
}

func seededStore(t *testing.T, ids ...string) *authz.Store {
	t.Helper()
	s := authz.NewStore(filepath.Join(t.TempDir(), "approved.json"))
	for _, id := range ids {
		if err := s.Add(id); err != nil {
			t.Fatalf("seed Add(%s): %v", id, err)
		}
	}
	return s
}

func assertHasAuditReason(t *testing.T, sink *audit.FakeSink, reason string) {
	t.Helper()
	for _, ev := range sink.Events() {
		if ev.Detail.Reason == reason {
			return
		}
	}
	t.Errorf("no audit event with reason %q; got %v", reason, auditReasons(sink))
}

func assertAuditReasonPrefix(t *testing.T, sink *audit.FakeSink, prefix string) {
	t.Helper()
	for _, ev := range sink.Events() {
		if strings.HasPrefix(ev.Detail.Reason, prefix) {
			return
		}
	}
	t.Errorf("no audit event with reason prefix %q; got %v", prefix, auditReasons(sink))
}

func assertNoAuditReason(t *testing.T, sink *audit.FakeSink, reason string) {
	t.Helper()
	for _, ev := range sink.Events() {
		if ev.Detail.Reason == reason {
			t.Errorf("unexpected audit event with reason %q", reason)
		}
	}
}

func auditReasons(sink *audit.FakeSink) []string {
	var rs []string
	for _, ev := range sink.Events() {
		rs = append(rs, ev.Detail.Reason)
	}
	return rs
}
