package telegram_test

// Task 157 — Adapter.Next no longer terminates on an empty/rejected poll.
//
// These L2 tests drive telegram.Adapter directly against a scripted stub getUpdates
// server. They prove the task-157 termination contract: Next() re-polls INTERNALLY on
// an empty batch (TC-157-02) or a fully-rejected batch (TC-157-03), returns ok=false
// ONLY when the shutdown context fires (TC-157-04), retries a hard transport failure
// rather than surfacing it as a fatal error (TC-157-05), and retains/defaults the
// shutdown context correctly (TC-157-01).
//
// The tests are NON-VACUOUS: each survival test asserts the adapter POLLED MULTIPLE
// TIMES (via an atomic call counter) before delivering the eventual real message —
// proving the loop survived the idle/rejected/failed polls rather than merely not
// panicking on a single poll.
//
// TC-157-09 (full regression) is a re-run case, not new assertions: the pre-existing
// internal/channel/telegram and internal/cli suites (tasks 080/097/098/117/151/152/153)
// continue to pass unchanged in security/audit behavior after this task's diff — every
// pre-task adapter that expected Next() to return ok=false on an empty/rejected batch now
// constructs with a pre-cancelled shutdown context (tc157Done), reproducing the one-poll
// observable behavior. Verified by `go test -race -count=1 ./internal/channel/telegram/...
// ./internal/cli/...` plus `make check`.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// tc157Done returns a context that is ALREADY cancelled. Pre-task-157 adapter tests
// whose assertions expect Next() to return ok=false once a batch yields no deliverable
// message use it as the adapter's shutdown context: under the task-157 re-poll contract
// Next() re-polls until its shutdown context fires, and a pre-cancelled context makes it
// perform exactly ONE poll (so offset advances and audit events still emit) and then
// return ok=false — reproducing the single-poll observable behavior those tests were
// written against. New task-157 tests below instead use live cancellable contexts.
func tc157Done() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// tc157Resp is one scripted getUpdates response.
type tc157Resp struct {
	updates       []map[string]interface{} // nil/empty ⇒ an empty (idle) batch
	transportFail bool                     // ⇒ an ok:false API error (a hard transport failure)
}

// tc157Server serves the scripted responses in order (response i on the i-th poll),
// returning an empty batch once the script is exhausted. It records the total poll
// count into calls (atomic) so a test can assert the adapter re-polled.
func tc157Server(t *testing.T, calls *int64, script ...tc157Resp) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		idx := atomic.AddInt64(calls, 1) - 1 // 0-based index of THIS poll
		var resp tc157Resp
		if int(idx) < len(script) {
			resp = script[idx]
		}
		w.Header().Set("Content-Type", "application/json")
		if resp.transportFail {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error_description": "simulated transport failure"})
			return
		}
		result := make([]interface{}, 0, len(resp.updates))
		for _, u := range resp.updates {
			result = append(result, u)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// tc157EmptyResp is a convenience empty-batch response.
func tc157EmptyResp() tc157Resp { return tc157Resp{} }

// TC-157-01 — Adapter accepts a shutdown context at construction and defaults it safely.
//
// Two observable checks:
//
//	(a) An OMITTED Ctx does not panic and the adapter still delivers a message — proving
//	    NewAdapter defaults the nil context to context.Background() rather than storing
//	    a nil that would panic at first select.
//	(b) A SUPPLIED (pre-cancelled) Ctx is retained and observed: over an always-empty
//	    server the adapter performs exactly one poll and returns ok=false — proving the
//	    passed context reaches the internal poll loop (verified jointly with TC-157-04).
func TestTC157_01_AdapterAcceptsAndDefaultsShutdownContext(t *testing.T) {
	// (a) Omitted Ctx: no panic, message delivered.
	opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv := tc117KeyMaterial(t)
	update := tc117MakeUpdateJSON(t, opEdPriv, opXPriv, orchXPub, 500, 1, 9, nil, "build the thing")
	var callsA int64
	srvA := tc157Server(t, &callsA, tc157Resp{updates: []map[string]interface{}{update}})
	adapterA := telegram.NewAdapter(telegram.Config{
		// Ctx deliberately OMITTED — must default to context.Background(), never nil.
		BotToken:          "tc157-01a",
		BaseURL:           srvA.URL,
		HTTPClient:        srvA.Client(),
		TrustedSigningKey: opEdPub,
		TrustedX25519Pub:  opXPub,
		OrchestratorPriv:  orchXPriv,
		ContentGuard:      &tc117AllowGuard{},
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         audit.NewFakeSink(),
		PollBackoff:       time.Millisecond,
	})
	msg, ok, err := adapterA.Next()
	if err != nil || !ok {
		t.Fatalf("omitted Ctx: Next() ok=%v err=%v, want a delivered message", ok, err)
	}
	if msg.Kind != supervisor.MsgNewGoal || msg.Goal.Spec != "build the thing" {
		t.Fatalf("omitted Ctx: msg = %+v, want MsgNewGoal spec %q", msg, "build the thing")
	}

	// (b) Supplied pre-cancelled Ctx over an always-empty server: exactly one poll, false.
	var callsB int64
	srvB := tc157Server(t, &callsB) // no script ⇒ always empty
	adapterB := telegram.NewAdapter(telegram.Config{
		Ctx:               tc157Done(),
		BotToken:          "tc157-01b",
		BaseURL:           srvB.URL,
		HTTPClient:        srvB.Client(),
		TrustedSigningKey: opEdPub,
		TrustedX25519Pub:  opXPub,
		OrchestratorPriv:  orchXPriv,
		ContentGuard:      &tc117AllowGuard{},
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         audit.NewFakeSink(),
		PollBackoff:       time.Second, // must NOT be waited: cancelled ctx short-circuits
	})
	done := make(chan struct{})
	var okB bool
	var errB error
	go func() {
		_, okB, errB = adapterB.Next()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supplied pre-cancelled Ctx: Next() did not return — context not observed")
	}
	if okB || errB != nil {
		t.Fatalf("supplied pre-cancelled Ctx: Next() = (ok=%v, err=%v), want (false, nil)", okB, errB)
	}
	if got := atomic.LoadInt64(&callsB); got != 1 {
		t.Fatalf("supplied pre-cancelled Ctx: poll count = %d, want exactly 1 (one poll then shutdown)", got)
	}
}

// TC-157-02 — An empty poll batch does not terminate Next(); it re-polls until a real
// message arrives. The caller observes ONE Next() call that returns the real message —
// never an intermediate ok=false.
func TestTC157_02_EmptyBatchRepolls(t *testing.T) {
	opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv := tc117KeyMaterial(t)
	update := tc117MakeUpdateJSON(t, opEdPriv, opXPriv, orchXPub, 600, 2, 9, nil, "ship the feature")

	const emptyPolls = 3
	var calls int64
	script := []tc157Resp{tc157EmptyResp(), tc157EmptyResp(), tc157EmptyResp(), {updates: []map[string]interface{}{update}}}
	srv := tc157Server(t, &calls, script...)

	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               context.Background(), // long-lived: only a real message ends Next()
		BotToken:          "tc157-02",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: opEdPub,
		TrustedX25519Pub:  opXPub,
		OrchestratorPriv:  orchXPriv,
		ContentGuard:      &tc117AllowGuard{},
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         audit.NewFakeSink(),
		PollBackoff:       time.Millisecond,
	})

	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("Next() err = %v, want nil", err)
	}
	if !ok {
		t.Fatal("Next() ok=false — an empty batch wrongly surfaced as source-exhausted")
	}
	if msg.Kind != supervisor.MsgNewGoal || msg.Goal.Spec != "ship the feature" {
		t.Fatalf("msg = %+v, want MsgNewGoal spec %q", msg, "ship the feature")
	}
	// Non-vacuous: the adapter must have polled through the idle batches (survived them),
	// then delivered on the message poll.
	if got := atomic.LoadInt64(&calls); got <= emptyPolls {
		t.Fatalf("poll count = %d, want > %d (must have re-polled through the idle batches)", got, emptyPolls)
	}
}

// TC-157-03 — An all-rejected batch does not terminate Next(); it re-polls until an
// accepted message arrives, and the rejection still emits its audit event.
func TestTC157_03_AllRejectedBatchRepolls(t *testing.T) {
	opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv := tc117KeyMaterial(t)

	// First batch: a single update whose text is NOT a valid envelope (parse-fail reject).
	rejected := map[string]interface{}{
		"update_id": int64(700),
		"message": map[string]interface{}{
			"message_id": int64(3),
			"text":       "this is not an envelope at all",
			"chat":       map[string]interface{}{"id": int64(9)},
		},
	}
	// Second batch: a valid sealed envelope.
	valid := tc117MakeUpdateJSON(t, opEdPriv, opXPriv, orchXPub, 701, 4, 9, nil, "do the work")

	var calls int64
	sink := audit.NewFakeSink()
	srv := tc157Server(t, &calls,
		tc157Resp{updates: []map[string]interface{}{rejected}},
		tc157Resp{updates: []map[string]interface{}{valid}},
	)
	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               context.Background(),
		BotToken:          "tc157-03",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: opEdPub,
		TrustedX25519Pub:  opXPub,
		OrchestratorPriv:  orchXPriv,
		ContentGuard:      &tc117AllowGuard{},
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         sink,
		PollBackoff:       time.Millisecond,
	})

	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("Next() err = %v, want nil", err)
	}
	if !ok {
		t.Fatal("Next() ok=false — an all-rejected batch wrongly surfaced as source-exhausted (the DoS bug)")
	}
	if msg.Kind != supervisor.MsgNewGoal || msg.Goal.Spec != "do the work" {
		t.Fatalf("msg = %+v, want MsgNewGoal spec %q (the SECOND batch's valid message)", msg, "do the work")
	}
	// Non-vacuous: it re-polled past the rejected batch.
	if got := atomic.LoadInt64(&calls); got < 2 {
		t.Fatalf("poll count = %d, want >= 2 (must have re-polled past the rejected batch)", got)
	}
	// The rejection's audit event is unaffected (regression): exactly one reject recorded.
	if got := len(sink.Events()); got != 1 {
		t.Fatalf("audit reject events = %d, want exactly 1 (the parse-fail rejection)", got)
	}
}

// TC-157-04 — Next() returns ok=false ONLY when the shutdown context fires. Over an
// always-empty server the adapter polls indefinitely; once the test observes a poll it
// cancels the context and Next() must return (false, nil) within a bounded time.
func TestTC157_04_ReturnsFalseOnlyOnShutdown(t *testing.T) {
	opEdPub, _, opXPub, _, _, orchXPriv := tc117KeyMaterial(t)

	var calls int64
	srv := tc157Server(t, &calls) // always empty
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               ctx,
		BotToken:          "tc157-04",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: opEdPub,
		TrustedX25519Pub:  opXPub,
		OrchestratorPriv:  orchXPriv,
		ContentGuard:      &tc117AllowGuard{},
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         audit.NewFakeSink(),
		PollBackoff:       5 * time.Millisecond,
	})

	type result struct {
		ok  bool
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		_, ok, err := adapter.Next()
		resCh <- result{ok, err}
	}()

	// Wait until the adapter has actually polled at least once (proving it is looping,
	// not already returned).
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt64(&calls) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("adapter never polled — Next() did not enter its re-poll loop")
		}
		time.Sleep(time.Millisecond)
	}
	// It must still be running (not returned) before the cancel.
	select {
	case r := <-resCh:
		t.Fatalf("Next() returned (ok=%v err=%v) BEFORE shutdown — an empty poll wrongly terminated it", r.ok, r.err)
	default:
	}

	cancel()

	select {
	case r := <-resCh:
		if r.ok || r.err != nil {
			t.Fatalf("Next() after shutdown = (ok=%v, err=%v), want (false, nil)", r.ok, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Next() did not return within 2s of shutdown — cancel not observed")
	}
}

// TC-157-05 — A hard transport failure is retried internally, not immediately fatal.
// The stub returns an ok:false API error for its first M polls, then a valid message;
// Next() must deliver that message without ever returning a fatal error.
func TestTC157_05_TransportFailureRetried(t *testing.T) {
	opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv := tc117KeyMaterial(t)
	valid := tc117MakeUpdateJSON(t, opEdPriv, opXPriv, orchXPub, 800, 5, 9, nil, "recovered goal")

	const failCalls = 3
	var calls int64
	srv := tc157Server(t, &calls,
		tc157Resp{transportFail: true},
		tc157Resp{transportFail: true},
		tc157Resp{transportFail: true},
		tc157Resp{updates: []map[string]interface{}{valid}},
	)
	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:               context.Background(),
		BotToken:          "tc157-05",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		TrustedSigningKey: opEdPub,
		TrustedX25519Pub:  opXPub,
		OrchestratorPriv:  orchXPriv,
		ContentGuard:      &tc117AllowGuard{},
		ReplayCache:       envelope.NewReplayCache(60 * time.Second),
		AuditSink:         audit.NewFakeSink(),
		PollBackoff:       time.Millisecond,
	})

	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("Next() err = %v — a transient transport failure was wrongly fatal", err)
	}
	if !ok {
		t.Fatal("Next() ok=false — transport failure wrongly surfaced as source-exhausted")
	}
	if msg.Kind != supervisor.MsgNewGoal || msg.Goal.Spec != "recovered goal" {
		t.Fatalf("msg = %+v, want MsgNewGoal spec %q", msg, "recovered goal")
	}
	// Non-vacuous: it retried past the failing polls (survived them).
	if got := atomic.LoadInt64(&calls); got <= failCalls {
		t.Fatalf("poll count = %d, want > %d (must have retried past the transport failures)", got, failCalls)
	}
}
