package telegram_test

// Task 171 TC-171-03/04: Telegram deriveMessage recognizes approve/deny as reply-to
// commands (goalID threaded from the cache), and a standalone approve is a new goal.
// Mirrors the tc117 confirm-derivation harness exactly.

import (
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

func TestTC171_03_ApproveDenyReplyToDerivation(t *testing.T) {
	opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv := tc117KeyMaterial(t)

	// A new goal (msgID 40, chat 66) → goalID tg-66-40, then two reply-to commands.
	update1 := tc117MakeUpdateJSON(t, opEdPriv, opXPriv, orchXPub, 200, 40, 66, nil, "do chore A")
	replyTo := map[string]interface{}{"message_id": 40}
	update2 := tc117MakeUpdateJSON(t, opEdPriv, opXPriv, orchXPub, 201, 41, 66, replyTo, "approve task-3")
	update3 := tc117MakeUpdateJSON(t, opEdPriv, opXPriv, orchXPub, 202, 42, 66, replyTo, "deny task-9")

	srv := tc117GetUpdatesServerMulti(t, update1, update2, update3)
	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "test-token-171",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: opEdPub,
		TrustedX25519Pub:  opXPub,
		OrchestratorPriv:  orchXPriv,
		ContentGuard:      &tc117AllowGuard{},
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         audit.NewFakeSink(),
	})

	msg1, ok1, _ := adapter.Next()
	if !ok1 || msg1.Kind != supervisor.MsgNewGoal {
		t.Fatalf("first msg: ok=%v kind=%v", ok1, msg1.Kind)
	}
	wantGoalID := "tg-66-40"

	msg2, ok2, err2 := adapter.Next()
	if err2 != nil || !ok2 {
		t.Fatalf("approve Next() failed: ok=%v err=%v", ok2, err2)
	}
	if msg2.Kind != supervisor.MsgApprove || msg2.GoalID != wantGoalID || msg2.TaskID != "task-3" {
		t.Errorf("approve derived %+v, want {MsgApprove %s task-3}", msg2, wantGoalID)
	}

	msg3, ok3, err3 := adapter.Next()
	if err3 != nil || !ok3 {
		t.Fatalf("deny Next() failed: ok=%v err=%v", ok3, err3)
	}
	if msg3.Kind != supervisor.MsgDeny || msg3.GoalID != wantGoalID || msg3.TaskID != "task-9" {
		t.Errorf("deny derived %+v, want {MsgDeny %s task-9}", msg3, wantGoalID)
	}
}

func TestTC171_04_StandaloneApproveIsNewGoal(t *testing.T) {
	opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv := tc117KeyMaterial(t)

	// A standalone "approve task-3" with NO reply-to → MsgNewGoal (mirrors confirm's
	// fallback).
	update1 := tc117MakeUpdateJSON(t, opEdPriv, opXPriv, orchXPub, 210, 50, 77, nil, "approve task-3")
	srv := tc117GetUpdatesServerMulti(t, update1)
	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "test-token-171b",
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
	if err != nil || !ok {
		t.Fatalf("Next() failed: ok=%v err=%v", ok, err)
	}
	if msg.Kind != supervisor.MsgNewGoal {
		t.Errorf("standalone approve derived %v, want MsgNewGoal (no reply-to)", msg.Kind)
	}
	if msg.Goal.Spec != "approve task-3" {
		t.Errorf("new goal spec = %q, want the full text %q", msg.Goal.Spec, "approve task-3")
	}
}
