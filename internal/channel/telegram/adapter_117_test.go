package telegram_test

// TC-117 test suite — Telegram message-aware wiring (task 117, ADR 054 §2).
//
// Tests verified by this file:
//   TC-117-01 — Adapter satisfies MessageSource; Next() emits typed Message
//   TC-117-02 — Kind/GoalID derivation at adapter edge (table-driven)
//   TC-117-03 — ReplyAdapter carries acks/status/results unchanged
//   TC-117-05 — Per-message goal IDs derived from chat/message identity (no collision)
//   TC-117-06 — Envelope-verify + armor pipeline unchanged; derivation on plaintext only
//
// TC-117-04 (assembleOrchestrate inbound selector) lives in internal/cli/orchestrate_117_test.go
// because it tests the assembler in the cli package.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/ingestion"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// --------------------------------------------------------------------------
// TC-117-01 — Compile-time: Adapter satisfies supervisor.MessageSource (L2)
// --------------------------------------------------------------------------

// Verify at compile time that telegram.Adapter satisfies supervisor.MessageSource.
// This is the load-bearing interface assertion (REQ-117-01).
var _ supervisor.MessageSource = (*telegram.Adapter)(nil)

// TestTC117_01_AdapterNextEmitsTypedMessage verifies that Adapter.Next() emits a
// well-typed supervisor.Message (MsgNewGoal for bare text) after the full
// envelope-verify + armor pipeline.
func TestTC117_01_AdapterNextEmitsTypedMessage(t *testing.T) {
	opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv := tc117KeyMaterial(t)

	plaintext := []byte("add rate limiting")
	envJSON := tc117SealEnvelope(t, plaintext, opEdPriv, opXPriv, orchXPub)

	srv := tc117GetUpdatesServer(t, []map[string]interface{}{
		{
			"update_id": 200,
			"message": map[string]interface{}{
				"message_id": 1,
				"text":       string(envJSON),
				"chat":       map[string]interface{}{"id": 42},
			},
		},
	})

	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "test-token-117",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: opEdPub,
		TrustedX25519Pub:  opXPub,
		OrchestratorPriv:  orchXPriv,
		ContentGuard:      &tc117AllowGuard{},
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         audit.NewFakeSink(),
	})

	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("TC-117-01: Next() returned error: %v", err)
	}
	if !ok {
		t.Fatal("TC-117-01: Next() returned ok=false, want true")
	}
	// TC-117-01: kind is MsgNewGoal for bare text
	if msg.Kind != supervisor.MsgNewGoal {
		t.Errorf("TC-117-01: msg.Kind = %v, want MsgNewGoal", msg.Kind)
	}
	// TC-117-01: Goal.Spec carries the plaintext
	if msg.Goal.Spec != string(plaintext) {
		t.Errorf("TC-117-01: msg.Goal.Spec = %q, want %q", msg.Goal.Spec, string(plaintext))
	}
	// TC-117-01: GoalID is set (derived from chat/msg identity)
	if msg.GoalID == "" {
		t.Error("TC-117-01: msg.GoalID is empty, want non-empty derived goalID")
	}
	// GoalID should encode chat and message IDs
	if !strings.Contains(msg.GoalID, "tg-") {
		t.Errorf("TC-117-01: msg.GoalID = %q, expected prefix tg-", msg.GoalID)
	}

	_ = opXPub
	_ = opXPriv
}

// --------------------------------------------------------------------------
// TC-117-02 — Kind/GoalID derivation at adapter edge (table-driven, L2)
// --------------------------------------------------------------------------

// TestTC117_02_KindDerivation verifies the four derivation rules at the adapter
// edge: bare text → MsgNewGoal; status → MsgStatus; info → MsgInfo; cancel → MsgCancel.
// Reply-to threading is also tested: a subsequent status/info/cancel reply-to a
// prior new-goal message carries the same goalID.
func TestTC117_02_KindDerivation(t *testing.T) {
	opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv := tc117KeyMaterial(t)

	// For reply-to threading we need two calls on the same adapter.
	// First call: new-goal (bare text) — records goalID for msgID 10 in the cache.
	// Second call: status reply-to msgID 10 — should return that goalID.
	// We build separate adapters for the non-threading subtests.

	t.Run("bare text → MsgNewGoal", func(t *testing.T) {
		msg := tc117OneUpdate(t, opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv,
			50, 1, 42, nil, "build the auth module")
		if msg.Kind != supervisor.MsgNewGoal {
			t.Errorf("Kind = %v, want MsgNewGoal", msg.Kind)
		}
		if msg.Goal.Spec != "build the auth module" {
			t.Errorf("Goal.Spec = %q, want %q", msg.Goal.Spec, "build the auth module")
		}
		if msg.GoalID == "" {
			t.Error("GoalID is empty, want non-empty")
		}
		wantGoalID := "tg-42-1"
		if msg.GoalID != wantGoalID {
			t.Errorf("GoalID = %q, want %q", msg.GoalID, wantGoalID)
		}
	})

	t.Run("status bare (no reply-to) → MsgStatus, empty GoalID (fleet)", func(t *testing.T) {
		msg := tc117OneUpdate(t, opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv,
			51, 2, 42, nil, "status")
		if msg.Kind != supervisor.MsgStatus {
			t.Errorf("Kind = %v, want MsgStatus", msg.Kind)
		}
		// bare status with no reply-to → fleet query → empty GoalID
		if msg.GoalID != "" {
			t.Errorf("GoalID = %q, want empty for fleet status", msg.GoalID)
		}
	})

	t.Run("reply-to threading: new-goal then status reply → threaded goalID", func(t *testing.T) {
		opEdPub2, opEdPriv2, opXPub2, opXPriv2, orchXPub2, orchXPriv2 := tc117KeyMaterial(t)

		// Update 1: new-goal (msgID=10) — seeds the cache
		update1 := tc117MakeUpdateJSON(t, opEdPriv2, opXPriv2, orchXPub2, 60, 10, 99, nil, "add rate limiting")
		// Update 2: status reply-to msgID=10
		replyTo := map[string]interface{}{"message_id": 10}
		update2 := tc117MakeUpdateJSON(t, opEdPriv2, opXPriv2, orchXPub2, 61, 11, 99, replyTo, "status")

		srv := tc117GetUpdatesServerMulti(t, update1, update2)
		adapter := telegram.NewAdapter(telegram.Config{
			Ctx:               tc157Done(),
			BotToken:          "test-token-117b",
			BaseURL:           srv.URL,
			HTTPClient:        srv.Client(),
			TrustedSigningKey: opEdPub2,
			TrustedX25519Pub:  opXPub2,
			OrchestratorPriv:  orchXPriv2,
			ContentGuard:      &tc117AllowGuard{},
			ReplayCache:       envelope.NewReplayCache(60 * time.Second),
			AuditSink:         audit.NewFakeSink(),
		})

		msg1, ok1, err1 := adapter.Next()
		if err1 != nil || !ok1 {
			t.Fatalf("first Next() failed: ok=%v err=%v", ok1, err1)
		}
		if msg1.Kind != supervisor.MsgNewGoal {
			t.Fatalf("first msg Kind = %v, want MsgNewGoal", msg1.Kind)
		}
		wantGoalID := "tg-99-10"
		if msg1.GoalID != wantGoalID {
			t.Fatalf("first msg GoalID = %q, want %q", msg1.GoalID, wantGoalID)
		}

		msg2, ok2, err2 := adapter.Next()
		if err2 != nil || !ok2 {
			t.Fatalf("second Next() failed: ok=%v err=%v", ok2, err2)
		}
		if msg2.Kind != supervisor.MsgStatus {
			t.Errorf("second msg Kind = %v, want MsgStatus", msg2.Kind)
		}
		// reply-to threads the goalID from the original new-goal message
		if msg2.GoalID != wantGoalID {
			t.Errorf("second msg GoalID = %q, want %q (threaded)", msg2.GoalID, wantGoalID)
		}
	})

	t.Run("info reply-to → MsgInfo with threaded goalID and Text", func(t *testing.T) {
		opEdPub3, opEdPriv3, opXPub3, opXPriv3, orchXPub3, orchXPriv3 := tc117KeyMaterial(t)

		update1 := tc117MakeUpdateJSON(t, opEdPriv3, opXPriv3, orchXPub3, 70, 20, 55, nil, "fix the login bug")
		replyTo := map[string]interface{}{"message_id": 20}
		update2 := tc117MakeUpdateJSON(t, opEdPriv3, opXPriv3, orchXPub3, 71, 21, 55, replyTo, "info also handle retries")

		srv := tc117GetUpdatesServerMulti(t, update1, update2)
		adapter := telegram.NewAdapter(telegram.Config{
			Ctx:               tc157Done(),
			BotToken:          "test-token-117c",
			BaseURL:           srv.URL,
			HTTPClient:        srv.Client(),
			TrustedSigningKey: opEdPub3,
			TrustedX25519Pub:  opXPub3,
			OrchestratorPriv:  orchXPriv3,
			ContentGuard:      &tc117AllowGuard{},
			ReplayCache:       envelope.NewReplayCache(60 * time.Second),
			AuditSink:         audit.NewFakeSink(),
		})

		msg1, ok1, _ := adapter.Next()
		if !ok1 || msg1.Kind != supervisor.MsgNewGoal {
			t.Fatalf("first msg: ok=%v kind=%v", ok1, msg1.Kind)
		}

		msg2, ok2, err2 := adapter.Next()
		if err2 != nil || !ok2 {
			t.Fatalf("second Next() failed: ok=%v err=%v", ok2, err2)
		}
		if msg2.Kind != supervisor.MsgInfo {
			t.Errorf("Kind = %v, want MsgInfo", msg2.Kind)
		}
		if msg2.GoalID != "tg-55-20" {
			t.Errorf("GoalID = %q, want %q", msg2.GoalID, "tg-55-20")
		}
		if msg2.Text != "also handle retries" {
			t.Errorf("Text = %q, want %q", msg2.Text, "also handle retries")
		}
	})

	t.Run("cancel reply-to → MsgCancel with threaded goalID", func(t *testing.T) {
		opEdPub4, opEdPriv4, opXPub4, opXPriv4, orchXPub4, orchXPriv4 := tc117KeyMaterial(t)

		update1 := tc117MakeUpdateJSON(t, opEdPriv4, opXPriv4, orchXPub4, 80, 30, 77, nil, "refactor the parser")
		replyTo := map[string]interface{}{"message_id": 30}
		update2 := tc117MakeUpdateJSON(t, opEdPriv4, opXPriv4, orchXPub4, 81, 31, 77, replyTo, "cancel")

		srv := tc117GetUpdatesServerMulti(t, update1, update2)
		adapter := telegram.NewAdapter(telegram.Config{
			Ctx:               tc157Done(),
			BotToken:          "test-token-117d",
			BaseURL:           srv.URL,
			HTTPClient:        srv.Client(),
			TrustedSigningKey: opEdPub4,
			TrustedX25519Pub:  opXPub4,
			OrchestratorPriv:  orchXPriv4,
			ContentGuard:      &tc117AllowGuard{},
			ReplayCache:       envelope.NewReplayCache(60 * time.Second),
			AuditSink:         audit.NewFakeSink(),
		})

		msg1, ok1, _ := adapter.Next()
		if !ok1 || msg1.Kind != supervisor.MsgNewGoal {
			t.Fatalf("first msg: ok=%v kind=%v", ok1, msg1.Kind)
		}

		msg2, ok2, err2 := adapter.Next()
		if err2 != nil || !ok2 {
			t.Fatalf("second Next() failed: ok=%v err=%v", ok2, err2)
		}
		if msg2.Kind != supervisor.MsgCancel {
			t.Errorf("Kind = %v, want MsgCancel", msg2.Kind)
		}
		if msg2.GoalID != "tg-77-30" {
			t.Errorf("GoalID = %q, want %q", msg2.GoalID, "tg-77-30")
		}
	})

	t.Run("confirm/go/proceed reply-to → MsgConfirm with threaded goalID (case-insensitive)", func(t *testing.T) {
		opEdPub5, opEdPriv5, opXPub5, opXPriv5, orchXPub5, orchXPriv5 := tc117KeyMaterial(t)

		// We will test 'Confirm', 'go', and 'PROCEED'
		update1 := tc117MakeUpdateJSON(t, opEdPriv5, opXPriv5, orchXPub5, 100, 40, 66, nil, "do chore A")
		replyTo := map[string]interface{}{"message_id": 40}
		update2 := tc117MakeUpdateJSON(t, opEdPriv5, opXPriv5, orchXPub5, 101, 41, 66, replyTo, "Confirm")
		update3 := tc117MakeUpdateJSON(t, opEdPriv5, opXPriv5, orchXPub5, 102, 42, 66, replyTo, "go")
		update4 := tc117MakeUpdateJSON(t, opEdPriv5, opXPriv5, orchXPub5, 103, 43, 66, replyTo, "PROCEED")

		srv := tc117GetUpdatesServerMulti(t, update1, update2, update3, update4)
		adapter := telegram.NewAdapter(telegram.Config{
			Ctx:               tc157Done(),
			BotToken:          "test-token-117-confirm",
			BaseURL:           srv.URL,
			HTTPClient:        srv.Client(),
			TrustedSigningKey: opEdPub5,
			TrustedX25519Pub:  opXPub5,
			OrchestratorPriv:  orchXPriv5,
			ContentGuard:      &tc117AllowGuard{},
			ReplayCache:       envelope.NewReplayCache(60 * time.Second),
			AuditSink:         audit.NewFakeSink(),
		})

		msg1, ok1, _ := adapter.Next()
		if !ok1 || msg1.Kind != supervisor.MsgNewGoal {
			t.Fatalf("first msg: ok=%v kind=%v", ok1, msg1.Kind)
		}
		wantGoalID := "tg-66-40"

		// Test 'Confirm'
		msg2, ok2, err2 := adapter.Next()
		if err2 != nil || !ok2 {
			t.Fatalf("second Next() failed: ok=%v err=%v", ok2, err2)
		}
		if msg2.Kind != supervisor.MsgConfirm {
			t.Errorf("Kind = %v, want MsgConfirm", msg2.Kind)
		}
		if msg2.GoalID != wantGoalID {
			t.Errorf("GoalID = %q, want %q", msg2.GoalID, wantGoalID)
		}

		// Test 'go'
		msg3, ok3, err3 := adapter.Next()
		if err3 != nil || !ok3 {
			t.Fatalf("third Next() failed: ok=%v err=%v", ok3, err3)
		}
		if msg3.Kind != supervisor.MsgConfirm {
			t.Errorf("Kind = %v, want MsgConfirm", msg3.Kind)
		}
		if msg3.GoalID != wantGoalID {
			t.Errorf("GoalID = %q, want %q", msg3.GoalID, wantGoalID)
		}

		// Test 'PROCEED'
		msg4, ok4, err4 := adapter.Next()
		if err4 != nil || !ok4 {
			t.Fatalf("fourth Next() failed: ok=%v err=%v", ok4, err4)
		}
		if msg4.Kind != supervisor.MsgConfirm {
			t.Errorf("Kind = %v, want MsgConfirm", msg4.Kind)
		}
		if msg4.GoalID != wantGoalID {
			t.Errorf("GoalID = %q, want %q", msg4.GoalID, wantGoalID)
		}
	})

	t.Run("confirm/go/proceed bare (no reply-to) → MsgNewGoal", func(t *testing.T) {
		// Bare 'confirm', 'go', or 'proceed' without a reply-to should fall back to MsgNewGoal.
		opEdPub6, opEdPriv6, opXPub6, opXPriv6, orchXPub6, orchXPriv6 := tc117KeyMaterial(t)

		msgConfirm := tc117OneUpdate(t, opEdPub6, opEdPriv6, opXPub6, opXPriv6, orchXPub6, orchXPriv6,
			201, 51, 88, nil, "confirm")
		if msgConfirm.Kind != supervisor.MsgNewGoal {
			t.Errorf("confirm no reply-to: Kind = %v, want MsgNewGoal", msgConfirm.Kind)
		}

		msgGo := tc117OneUpdate(t, opEdPub6, opEdPriv6, opXPub6, opXPriv6, orchXPub6, orchXPriv6,
			202, 52, 88, nil, "go build standard-agent")
		if msgGo.Kind != supervisor.MsgNewGoal {
			t.Errorf("go no reply-to: Kind = %v, want MsgNewGoal", msgGo.Kind)
		}
		if msgGo.Goal.Spec != "go build standard-agent" {
			t.Errorf("go no reply-to spec: Spec = %q, want 'go build standard-agent'", msgGo.Goal.Spec)
		}
	})

	t.Run("no panic on empty text (falls back to MsgNewGoal)", func(t *testing.T) {
		// Edge case: empty plaintext — derivation must not panic; falls back to MsgNewGoal.
		msg := tc117OneUpdate(t, opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv,
			90, 40, 11, nil, "")
		// Empty verb → default MsgNewGoal
		if msg.Kind != supervisor.MsgNewGoal {
			t.Errorf("empty text: Kind = %v, want MsgNewGoal (no panic, no silent drop)", msg.Kind)
		}
	})
}

// --------------------------------------------------------------------------
// TC-117-03 — ReplyAdapter carries acks/status/results unchanged (L2)
// --------------------------------------------------------------------------

// TestTC117_03_ReplyAdapterCarriesAcksUnchanged is a smoke regression: Report()
// posts a sealed envelope (not plaintext). The full assertion is in task-098 tests;
// this test confirms the ReplyAdapter is not broken by the adapter refactor.
func TestTC117_03_ReplyAdapterCarriesAcksUnchanged(t *testing.T) {
	orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, opXPriv := tc117ReplyKeyMaterial(t)
	_ = orchEdPub
	_ = orchXPub
	_ = opXPriv

	var capturedBody []byte
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		capturedBody = buf.Bytes()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()

	adapter := telegram.NewReplyAdapter(telegram.ReplyConfig{
		BotToken:   "test-token-117-reply",
		BaseURL:    srv.URL,
		ChatID:     "9999",
		HTTPClient: srv.Client(),
		OrchEdPriv: orchEdPriv,
		OrchXPriv:  orchXPriv,
		OpXPub:     opXPub,
	})

	const statusText = "goal-7: Dispatching (1/2 sub-goals)"
	if err := adapter.Report(context.Background(), statusText); err != nil {
		t.Fatalf("TC-117-03: Report returned error: %v", err)
	}

	// TC-117-03: exactly one POST to sendMessage
	if callCount != 1 {
		t.Errorf("TC-117-03: callCount = %d, want 1", callCount)
	}

	// TC-117-03: body carries a sealed envelope (not plaintext)
	if bytes.Contains(capturedBody, []byte(statusText)) {
		t.Errorf("TC-117-03: plaintext %q found in POST body — must be sealed on the wire", statusText)
	}

	// TC-117-03: body is parseable as an outer sendMessage JSON containing an envelope
	var outer struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(capturedBody, &outer); err != nil {
		t.Fatalf("TC-117-03: cannot parse POST body as JSON: %v — body: %s", err, capturedBody)
	}
	var env envelope.Envelope
	if err := json.Unmarshal([]byte(outer.Text), &env); err != nil {
		t.Fatalf("TC-117-03: Text field is not an envelope: %v — text: %s", err, outer.Text)
	}
	if env.Sig == "" || env.Payload == "" || env.Nonce == "" {
		t.Errorf("TC-117-03: envelope fields are empty — Sig=%q Payload=%q Nonce=%q",
			env.Sig, env.Payload, env.Nonce)
	}
}

// --------------------------------------------------------------------------
// TC-117-05 — Per-message goal IDs (no collision across chat/msg identities)
// --------------------------------------------------------------------------

// TestTC117_05_PerMessageGoalIDs verifies that two updates from distinct message
// identities produce distinct goalIDs, and that a reply-to the FIRST goal threads
// the first goalID (not the second).
func TestTC117_05_PerMessageGoalIDs(t *testing.T) {
	opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv := tc117KeyMaterial(t)

	// Update A: chatID=100 msgID=1 — first goal
	updateA := tc117MakeUpdateJSON(t, opEdPriv, opXPriv, orchXPub, 301, 1, 100, nil, "first goal")
	// Update B: chatID=100 msgID=2 — second goal (same chat, different message)
	updateB := tc117MakeUpdateJSON(t, opEdPriv, opXPriv, orchXPub, 302, 2, 100, nil, "second goal")
	// Update C: status reply-to msgID=1 (should thread goal A's ID)
	replyToA := map[string]interface{}{"message_id": 1}
	updateC := tc117MakeUpdateJSON(t, opEdPriv, opXPriv, orchXPub, 303, 3, 100, replyToA, "status")

	srv := tc117GetUpdatesServerMulti(t, updateA, updateB, updateC)
	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "test-token-117e",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: opEdPub,
		TrustedX25519Pub:  opXPub,
		OrchestratorPriv:  orchXPriv,
		ContentGuard:      &tc117AllowGuard{},
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         audit.NewFakeSink(),
	})

	msgA, okA, errA := adapter.Next()
	if errA != nil || !okA {
		t.Fatalf("TC-117-05: first Next() failed: ok=%v err=%v", okA, errA)
	}
	msgB, okB, errB := adapter.Next()
	if errB != nil || !okB {
		t.Fatalf("TC-117-05: second Next() failed: ok=%v err=%v", okB, errB)
	}
	msgC, okC, errC := adapter.Next()
	if errC != nil || !okC {
		t.Fatalf("TC-117-05: third Next() failed: ok=%v err=%v", okC, errC)
	}

	// TC-117-05: distinct new-goal IDs for distinct messages
	if msgA.GoalID == msgB.GoalID {
		t.Errorf("TC-117-05: goalID collision: msgA.GoalID = msgB.GoalID = %q", msgA.GoalID)
	}
	if msgA.GoalID != "tg-100-1" {
		t.Errorf("TC-117-05: msgA.GoalID = %q, want tg-100-1", msgA.GoalID)
	}
	if msgB.GoalID != "tg-100-2" {
		t.Errorf("TC-117-05: msgB.GoalID = %q, want tg-100-2", msgB.GoalID)
	}

	// TC-117-05: status reply-to msgID=1 threads goal A's ID (not B's)
	if msgC.Kind != supervisor.MsgStatus {
		t.Errorf("TC-117-05: msgC.Kind = %v, want MsgStatus", msgC.Kind)
	}
	if msgC.GoalID != msgA.GoalID {
		t.Errorf("TC-117-05: msgC.GoalID = %q, want %q (threaded to goal A)", msgC.GoalID, msgA.GoalID)
	}
	if msgC.GoalID == msgB.GoalID {
		t.Errorf("TC-117-05: status reply routed to goal B instead of goal A")
	}
}

// --------------------------------------------------------------------------
// TC-117-06 — Envelope-verify + armor pipeline unchanged (regression, L2)
// --------------------------------------------------------------------------

// TestTC117_06_PipelineUnchangedTamperedDropped verifies that a tampered signature
// causes the update to be dropped before kind derivation. The emitted result must
// not be MsgCancel (the command encoded in the plaintext).
func TestTC117_06_PipelineUnchangedTamperedDropped(t *testing.T) {
	opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv := tc117KeyMaterial(t)
	sink := audit.NewFakeSink()

	// Build a valid envelope then tamper the signature.
	plaintext := []byte("cancel goal-42")
	envJSON := tc117SealEnvelope(t, plaintext, opEdPriv, opXPriv, orchXPub)

	var env envelope.Envelope
	if err := json.Unmarshal(envJSON, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Tamper: flip one byte in the signature
	sigBytes, err := hex.DecodeString(env.Sig)
	if err != nil || len(sigBytes) == 0 {
		t.Fatalf("decode sig: %v", err)
	}
	sigBytes[0] ^= 0xFF
	env.Sig = hex.EncodeToString(sigBytes)
	tamperedJSON, _ := json.Marshal(env)

	srv := tc117GetUpdatesServer(t, []map[string]interface{}{
		{
			"update_id": 400,
			"message": map[string]interface{}{
				"message_id": 1,
				"text":       string(tamperedJSON),
				"chat":       map[string]interface{}{"id": 1},
			},
		},
	})

	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "test-token-117f",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: opEdPub,
		TrustedX25519Pub:  opXPub,
		OrchestratorPriv:  orchXPriv,
		ContentGuard:      &tc117AllowGuard{},
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         sink,
	})

	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("TC-117-06: Next() returned error: %v", err)
	}
	// TC-117-06: tampered update must be dropped — ok=false or not MsgCancel
	if ok && msg.Kind == supervisor.MsgCancel {
		t.Errorf("TC-117-06: tampered update reached kind derivation as MsgCancel — pipeline is broken")
	}

	// TC-117-06: audit sink must record a rejection event (same path as TC-080-02)
	events := sink.Events()
	if len(events) == 0 {
		t.Error("TC-117-06: no audit events for tampered update; expected envelope rejection event")
	}
	hasRejection := false
	for _, ev := range events {
		if strings.Contains(ev.Detail.Reason, "unknown_key") ||
			strings.Contains(ev.Detail.Reason, "envelope_rejected") {
			hasRejection = true
			break
		}
	}
	if !hasRejection {
		t.Errorf("TC-117-06: no envelope rejection audit event; events: %v", events)
	}
}

// TestTC117_06_ArmorBlockDropped verifies that an armor-blocked update is dropped
// before kind derivation — the "cancel" command must not reach the control plane.
func TestTC117_06_ArmorBlockDropped(t *testing.T) {
	opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv := tc117KeyMaterial(t)
	sink := audit.NewFakeSink()

	plaintext := []byte("cancel goal-99")
	envJSON := tc117SealEnvelope(t, plaintext, opEdPriv, opXPriv, orchXPub)

	srv := tc117GetUpdatesServer(t, []map[string]interface{}{
		{
			"update_id": 401,
			"message": map[string]interface{}{
				"message_id": 2,
				"text":       string(envJSON),
				"chat":       map[string]interface{}{"id": 2},
			},
		},
	})

	// Armor blocks everything
	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "test-token-117g",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: opEdPub,
		TrustedX25519Pub:  opXPub,
		OrchestratorPriv:  orchXPriv,
		ContentGuard:      &tc117BlockGuard{},
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         sink,
	})

	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("TC-117-06 armor: Next() returned error: %v", err)
	}
	// TC-117-06: armor-blocked update must be dropped
	if ok && msg.Kind == supervisor.MsgCancel {
		t.Errorf("TC-117-06: armor-blocked update reached kind derivation as MsgCancel — pipeline is broken")
	}

	// TC-117-06: audit sink must record an armor_blocked event
	events := sink.Events()
	hasArmorBlock := false
	for _, ev := range events {
		if strings.Contains(ev.Detail.Reason, "armor_blocked") {
			hasArmorBlock = true
			break
		}
	}
	if !hasArmorBlock {
		t.Errorf("TC-117-06 armor: no armor_blocked audit event; events: %v", events)
	}
}

// ==========================================================================
// Helpers
// ==========================================================================

// tc117KeyMaterial generates fresh Ed25519 and X25519 key pairs for inbound tests.
// Returns: opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv.
func tc117KeyMaterial(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, [32]byte, [32]byte, [32]byte, [32]byte) {
	t.Helper()
	opEdPub, opEdPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("tc117KeyMaterial: ed25519 keygen: %v", err)
	}
	opXPub, opXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("tc117KeyMaterial: opX keygen: %v", err)
	}
	orchXPub, orchXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("tc117KeyMaterial: orchX keygen: %v", err)
	}
	return opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv
}

// tc117ReplyKeyMaterial generates key material for ReplyAdapter tests.
// Returns: orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, opXPriv.
func tc117ReplyKeyMaterial(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, [32]byte, [32]byte, [32]byte, [32]byte) {
	t.Helper()
	orchEdPub, orchEdPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("tc117ReplyKeyMaterial: ed25519 keygen: %v", err)
	}
	orchXPub, orchXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("tc117ReplyKeyMaterial: orchX keygen: %v", err)
	}
	opXPub, opXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("tc117ReplyKeyMaterial: opX keygen: %v", err)
	}
	return orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, opXPriv
}

// tc117SealEnvelope creates a signed, sealed envelope JSON for the given plaintext.
// opEdPriv signs; opXPriv + orchXPub seal (operator sends to orchestrator).
func tc117SealEnvelope(t *testing.T, plaintext []byte, opEdPriv ed25519.PrivateKey, opXPriv, orchXPub [32]byte) []byte {
	t.Helper()
	ciphertext, nonce, err := envelope.Seal(plaintext, opXPriv, orchXPub)
	if err != nil {
		t.Fatalf("tc117SealEnvelope: Seal: %v", err)
	}
	env := envelope.Envelope{
		From:    "operator",
		To:      "orchestrator",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
	}
	env, err = envelope.Sign(env, opEdPriv)
	if err != nil {
		t.Fatalf("tc117SealEnvelope: Sign: %v", err)
	}
	j, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("tc117SealEnvelope: Marshal: %v", err)
	}
	return j
}

// tc117MakeUpdateJSON builds a single Telegram update map (as JSON bytes) with the
// given IDs and plaintext. replyTo, if non-nil, is the reply_to_message object.
func tc117MakeUpdateJSON(t *testing.T, opEdPriv ed25519.PrivateKey, opXPriv, orchXPub [32]byte,
	updateID, msgID, chatID int64, replyTo map[string]interface{}, plaintext string) map[string]interface{} {
	t.Helper()
	envJSON := tc117SealEnvelope(t, []byte(plaintext), opEdPriv, opXPriv, orchXPub)
	msg := map[string]interface{}{
		"message_id": msgID,
		"text":       string(envJSON),
		"chat":       map[string]interface{}{"id": chatID},
	}
	if replyTo != nil {
		msg["reply_to_message"] = replyTo
	}
	return map[string]interface{}{
		"update_id": updateID,
		"message":   msg,
	}
}

// tc117GetUpdatesServer builds a stub Telegram getUpdates server that serves the
// given updates in order, returning empty on subsequent calls.
func tc117GetUpdatesServer(t *testing.T, updates []map[string]interface{}) *httptest.Server {
	t.Helper()
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var result interface{}
		if callCount == 1 {
			result = updates
		} else {
			result = []interface{}{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// tc117GetUpdatesServerMulti serves one update per call (each call returns the next update).
func tc117GetUpdatesServerMulti(t *testing.T, updates ...map[string]interface{}) *httptest.Server {
	t.Helper()
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result interface{}
		if callCount < len(updates) {
			result = []interface{}{updates[callCount]}
		} else {
			result = []interface{}{}
		}
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// tc117OneUpdate is a helper that runs one update through a fresh adapter and
// returns the resulting Message. It creates fresh key material for each call.
func tc117OneUpdate(t *testing.T,
	opEdPub ed25519.PublicKey, opEdPriv ed25519.PrivateKey,
	opXPub, opXPriv, orchXPub, orchXPriv [32]byte,
	updateID, msgID, chatID int64, replyTo map[string]interface{}, plaintext string,
) supervisor.Message {
	t.Helper()
	update := tc117MakeUpdateJSON(t, opEdPriv, opXPriv, orchXPub, updateID, msgID, chatID, replyTo, plaintext)
	srv := tc117GetUpdatesServer(t, []map[string]interface{}{update})
	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "test-token-one-update",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: opEdPub,
		TrustedX25519Pub:  opXPub,
		OrchestratorPriv:  orchXPriv,
		ContentGuard:      &tc117AllowGuard{},
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         audit.NewFakeSink(),
	})
	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("tc117OneUpdate: Next() error: %v", err)
	}
	if !ok {
		t.Fatalf("tc117OneUpdate: Next() ok=false for plaintext %q", plaintext)
	}
	return msg
}

// --------------------------------------------------------------------------
// Test doubles
// --------------------------------------------------------------------------

// tc117AllowGuard always allows content through (armor pass-through for adapter tests).
type tc117AllowGuard struct{}

func (tc117AllowGuard) DecideContent(_ context.Context, candidate ingestion.ContentCandidate) (ingestion.Decision, error) {
	return ingestion.Decision{
		CandidateID: candidate.ID,
		Kind:        ingestion.CandidateKindContent,
		Outcome:     ingestion.DecisionAllow,
	}, nil
}

// tc117BlockGuard always blocks content (armor block for TC-117-06).
type tc117BlockGuard struct{}

func (tc117BlockGuard) DecideContent(_ context.Context, candidate ingestion.ContentCandidate) (ingestion.Decision, error) {
	return ingestion.Decision{
		CandidateID: candidate.ID,
		Kind:        ingestion.CandidateKindContent,
		Outcome:     ingestion.DecisionBlock,
		Reason:      "injected-test-block",
	}, nil
}
