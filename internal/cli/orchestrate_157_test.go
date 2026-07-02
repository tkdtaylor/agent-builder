package cli

// Task 157 (L5) — the REAL control loop (runControlLoop / orchestrate machinery) built
// over a scripted stub Telegram getUpdates server via the production assembleOrchestrate
// → inboundFromEnv → assembleTelegramInbound → telegram.NewAdapter wire.
//
// These tests prove the fix holds END-TO-END, not just inside Adapter.Next in isolation:
//   - TC-157-06: the loop SURVIVES several idle polls and later routes the real message
//     as MsgNewGoal (it never breaks on an empty poll).
//   - TC-157-07: the loop SURVIVES an all-rejected batch (the one-junk-message DoS) and
//     still routes a subsequent valid message.
//   - TC-157-08: cancelling the loop's top-level context — the SAME context wired into
//     the adapter — still cleanly terminates both the adapter and the loop.
//
// The producer→consumer wire is genuine: the test creates ONE context, passes it as
// assembleOverrides.ctx (which assembleOrchestrate threads through inboundFromEnv into
// the adapter's shutdown seam) AND hands the SAME context to runControlLoop.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
)

// tc157Keys bundles the operator/orchestrator key material for a Telegram inbound test.
type tc157Keys struct {
	opEdPub    ed25519.PublicKey
	opEdPriv   ed25519.PrivateKey
	opXPub     [32]byte
	opXPriv    [32]byte
	orchXPub   [32]byte
	orchXPriv  [32]byte
	orchEdPriv ed25519.PrivateKey
	opReplyPub [32]byte
}

func tc157GenKeys(t *testing.T) tc157Keys {
	t.Helper()
	opEdPub, opEdPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("opEd keygen: %v", err)
	}
	opXPub, opXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("opX keygen: %v", err)
	}
	orchXPub, orchXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("orchX keygen: %v", err)
	}
	_, orchEdPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("orchEd keygen: %v", err)
	}
	opReplyPub, _, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("opReply keygen: %v", err)
	}
	return tc157Keys{opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv, orchEdPriv, opReplyPub}
}

// tc157Env builds the AGENT_BUILDER_* getenv for envelope-mode Telegram inbound pointed
// at the given stub server, with a fast poll backoff and approval disabled.
func tc157Env(k tc157Keys, baseURL string) func(string) string {
	m := map[string]string{
		EnvInbound:             "telegram",
		EnvTelegramBotToken:    "tc157-token",
		EnvTelegramBaseURL:     baseURL,
		EnvTelegramChatID:      "9",
		EnvTelegramSigningKey:  hex.EncodeToString(k.opEdPub),
		EnvTelegramX25519Pub:   hex.EncodeToString(k.opXPub[:]),
		EnvTelegramOrchPriv:    hex.EncodeToString(k.orchXPriv[:]),
		EnvTelegramOrchEdPriv:  hex.EncodeToString(k.orchEdPriv),
		EnvTelegramOpX25519Pub: hex.EncodeToString(k.opReplyPub[:]),
		EnvTelegramPollBackoff: "1ms",
		EnvRequireApproval:     "false",
	}
	return func(key string) string { return m[key] }
}

// tc157SealedUpdate builds a Telegram update JSON map carrying a valid sealed+signed
// envelope for the given plaintext, addressed as chat 9 / message msgID.
func tc157SealedUpdate(t *testing.T, k tc157Keys, updateID, msgID int64, plaintext string) map[string]interface{} {
	t.Helper()
	ciphertext, nonce, err := envelope.Seal([]byte(plaintext), k.opXPriv, k.orchXPub)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	env := envelope.Envelope{
		From:    "operator",
		To:      "orchestrator",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
	}
	env, err = envelope.Sign(env, k.opEdPriv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	j, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return map[string]interface{}{
		"update_id": updateID,
		"message": map[string]interface{}{
			"message_id": msgID,
			"text":       string(j),
			"chat":       map[string]interface{}{"id": int64(9)},
		},
	}
}

// tc157CliServer serves scripted getUpdates batches (batch i on poll i, empty after),
// recording the poll count atomically.
func tc157CliServer(t *testing.T, calls *int64, script ...[]map[string]interface{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		idx := atomic.AddInt64(calls, 1) - 1
		var batch []map[string]interface{}
		if int(idx) < len(script) {
			batch = script[idx]
		}
		result := make([]interface{}, 0, len(batch))
		for _, u := range batch {
			result = append(result, u)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// tc157AssembleLoop builds a real orchestrateConfig over the Telegram inbound env path
// (no messageSource override), threading ctx into the adapter via assembleOverrides.ctx.
func tc157AssembleLoop(t *testing.T, ctx context.Context, getenv func(string) string, reg *orchestrator.StatusRegistry) orchestrateConfig {
	t.Helper()
	setBaseConfigEnv(t)
	oc, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
		ctx:          ctx,
		getenv:       getenv,
		policyClient: &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}},
		dispatch:     (&spyDispatch{}).fn,
		auditSink:    audit.NewFakeSink(),
		planner:      newPerGoalPlanner(),
		reporter:     &spyReporter{},
		signingKey:   testSigningKey(t),
		registry:     reg,
		maxWorkers:   4,
		maxGoals:     8,
	})
	if err != nil {
		t.Fatalf("assembleOrchestrate: %v", err)
	}
	t.Cleanup(cleanup)
	return oc
}

// TC-157-06 — the real control loop survives idle polls and later delivers a message.
func TestTC157_06_ControlLoopSurvivesIdlePoll(t *testing.T) {
	k := tc157GenKeys(t)
	const idleBatches = 3
	update := tc157SealedUpdate(t, k, 1000, 2, "please build the feature")
	var calls int64
	srv := tc157CliServer(t, &calls,
		nil, nil, nil, // three idle polls
		[]map[string]interface{}{update},
	)
	getenv := tc157Env(k, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := orchestrator.NewStatusRegistry()
	oc := tc157AssembleLoop(t, ctx, getenv, reg)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(ctx, oc) }()

	// The message derives goalID "tg-9-2". Its appearance in the registry proves the loop
	// kept polling through the idle batches and routed the eventual message as MsgNewGoal.
	if !tc157WaitRegistered(t, reg, "tg-9-2", 5*time.Second) {
		t.Fatal("goal tg-9-2 never registered — loop terminated on an idle poll or never routed the message")
	}
	// Non-vacuous survival evidence: more polls than idle batches occurred.
	if got := atomic.LoadInt64(&calls); got <= idleBatches {
		t.Fatalf("poll count = %d, want > %d (loop must have re-polled through the idle batches)", got, idleBatches)
	}

	cancel()
	select {
	case <-loopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("runControlLoop did not return after cancel")
	}
}

// TC-157-07 — the real control loop survives an all-rejected batch (the DoS scenario)
// and still delivers a subsequent valid message.
func TestTC157_07_ControlLoopSurvivesRejectedBatch(t *testing.T) {
	k := tc157GenKeys(t)
	// First batch: a single update whose text is NOT a valid envelope → rejected.
	rejected := map[string]interface{}{
		"update_id": int64(2000),
		"message": map[string]interface{}{
			"message_id": int64(1),
			"text":       "junk not-an-envelope",
			"chat":       map[string]interface{}{"id": int64(9)},
		},
	}
	valid := tc157SealedUpdate(t, k, 2001, 2, "do the real work")
	var calls int64
	srv := tc157CliServer(t, &calls,
		[]map[string]interface{}{rejected},
		[]map[string]interface{}{valid},
	)
	getenv := tc157Env(k, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := orchestrator.NewStatusRegistry()
	oc := tc157AssembleLoop(t, ctx, getenv, reg)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(ctx, oc) }()

	if !tc157WaitRegistered(t, reg, "tg-9-2", 5*time.Second) {
		t.Fatal("goal tg-9-2 never registered — the rejected batch terminated the loop (DoS reproduced)")
	}
	if got := atomic.LoadInt64(&calls); got < 2 {
		t.Fatalf("poll count = %d, want >= 2 (loop must have re-polled past the rejected batch)", got)
	}

	cancel()
	select {
	case <-loopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("runControlLoop did not return after cancel")
	}
}

// TC-157-08 — a genuine shutdown still cleanly terminates the control loop. The stub
// never yields a message; cancelling the shared top-level context must unblock both the
// adapter's re-poll loop and runControlLoop within a bounded time.
func TestTC157_08_ControlLoopTerminatesOnShutdown(t *testing.T) {
	k := tc157GenKeys(t)
	var calls int64
	srv := tc157CliServer(t, &calls) // always empty
	getenv := tc157Env(k, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := orchestrator.NewStatusRegistry()
	oc := tc157AssembleLoop(t, ctx, getenv, reg)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(ctx, oc) }()

	// Wait until the adapter has actually polled (loop is live), then confirm it has NOT
	// terminated on its own before shutdown.
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt64(&calls) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("adapter never polled — loop did not start")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-loopDone:
		t.Fatalf("runControlLoop returned (%v) BEFORE shutdown — an empty poll wrongly terminated it", err)
	default:
	}

	cancel()
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("runControlLoop returned error on clean shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runControlLoop did not return within 5s of shutdown — cancel not wired through to the adapter")
	}
}

// tc157WaitRegistered polls the registry until goalID appears (any state) or timeout.
func tc157WaitRegistered(t *testing.T, reg *orchestrator.StatusRegistry, goalID string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, ok := reg.Get(goalID); ok {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}
