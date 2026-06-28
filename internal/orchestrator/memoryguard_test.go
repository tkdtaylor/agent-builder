package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/memoryguard"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// --- stub ExecRunner for orchestrator-level tests ---------------------------

// mgStubRunner is a test double for memoryguard.ExecRunner that returns
// configurable per-op responses without spawning a real subprocess.
type mgStubRunner struct {
	calls     []mgCall
	responses map[string][]byte
}

type mgCall struct {
	op  string
	req map[string]any
}

func newMGStubRunner() *mgStubRunner {
	return &mgStubRunner{responses: make(map[string][]byte)}
}

func (r *mgStubRunner) setResponse(op string, v any) {
	b, _ := json.Marshal(v)
	r.responses[op] = b
}

func (r *mgStubRunner) Run(_ string, reqJSON []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(reqJSON, &req); err != nil {
		return nil, fmt.Errorf("mgStub: bad request JSON: %w", err)
	}
	op, _ := req["op"].(string)
	r.calls = append(r.calls, mgCall{op: op, req: req})
	if resp, ok := r.responses[op]; ok {
		return resp, nil
	}
	return nil, fmt.Errorf("mgStub: no response for op %q", op)
}

func (r *mgStubRunner) opsInOrder() []string {
	out := make([]string, len(r.calls))
	for i, c := range r.calls {
		out[i] = c.op
	}
	return out
}

// --- TC-084-01: write goes through validate_write; stored_id held -----------

// TestTC084_01_WriteGoalThroughWriteGate asserts that when the orchestrator
// stores a plan under require_approval, it calls validate_write on the
// MemoryGuardPlanStore, the stub returns allow=true + stored_id, and the
// stored_id is held internally (accessible via StoredID).
func TestTC084_01_WriteGoalThroughWriteGate(t *testing.T) {
	stub := newMGStubRunner()
	stub.setResponse("validate_write", map[string]any{
		"allow": true, "stored_id": "stub-id-1", "flags": nil,
	})

	store := orchestrator.NewMemoryGuardPlanStoreWithRunner(
		"/stub/mg", "agent-builder/orchestrator", stub,
	)

	spy := newDispatchSpy()
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionRequireApproval}

	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
		orchestrator.WithPlanStore(store),
	)

	goal := supervisor.Task{ID: "g1", Spec: "coding-agent: do the thing"}
	if _, err := o.Handle(context.Background(), goal); err != nil {
		t.Fatalf("TC-084-01: Handle: unexpected error: %v", err)
	}

	// validate_write must have been called exactly once.
	ops := stub.opsInOrder()
	if len(ops) != 1 || ops[0] != "validate_write" {
		t.Fatalf("TC-084-01: want [validate_write], got %v", ops)
	}

	// The IPC entry must be a non-empty JSON string containing the plan.
	entry, _ := stub.calls[0].req["entry"].(string)
	if entry == "" {
		t.Errorf("TC-084-01: IPC entry must be non-empty")
	}
	if !strings.Contains(entry, "do the thing") {
		t.Errorf("TC-084-01: IPC entry must contain plan content, got %q", entry)
	}

	// stored_id must be held in the store.
	sid, ok := store.StoredID("g1")
	if !ok || sid != "stub-id-1" {
		t.Errorf("TC-084-01: stored_id: want (stub-id-1, true), got (%q, %v)", sid, ok)
	}

	// No dispatch yet (require_approval pauses).
	if spy.count() != 0 {
		t.Errorf("TC-084-01: want 0 dispatches before approval, got %d", spy.count())
	}
}

// TestTC084_01_WriteGateDenied asserts that when validate_write returns allow=false,
// Handle returns a non-nil error and does not proceed to report an approval request.
func TestTC084_01_WriteGateDenied(t *testing.T) {
	stub := newMGStubRunner()
	stub.setResponse("validate_write", map[string]any{
		"allow": false, "stored_id": "", "flags": []string{"injection_detected"},
	})

	store := orchestrator.NewMemoryGuardPlanStoreWithRunner(
		"/stub/mg", "agent-builder/orchestrator", stub,
	)
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionRequireApproval}

	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithPlanStore(store),
	)

	goal := supervisor.Task{ID: "g1", Spec: "coding-agent: bad plan"}
	_, err := o.Handle(context.Background(), goal)
	if err == nil {
		t.Fatal("TC-084-01: write-gate denied: want error, got nil")
	}
	if !errors.Is(err, memoryguard.ErrWriteGateDenied) {
		t.Errorf("TC-084-01: want errors.Is(err, ErrWriteGateDenied), got: %v", err)
	}
}

// --- TC-084-02: delete-bypass → tamper on Resume ----------------------------

// TestTC084_02_DeleteBypassTamperDetected asserts that when verify_delete returns
// confirmed=false/residue_detected=true, Resume returns ErrTamperDetected and
// no dispatch occurs.
func TestTC084_02_DeleteBypassTamperDetected(t *testing.T) {
	stub := newMGStubRunner()
	stub.setResponse("validate_write", map[string]any{
		"allow": true, "stored_id": "stub-id-2", "flags": nil,
	})
	stub.setResponse("verify_delete", map[string]any{
		"confirmed":        false,
		"residue_detected": true,
		"residue_summary":  "unexpected residue",
		"deletion_hash":    "abc123",
	})

	store := orchestrator.NewMemoryGuardPlanStoreWithRunner(
		"/stub/mg", "agent-builder/orchestrator", stub,
	)
	spy := newDispatchSpy()
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionRequireApproval}

	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
		orchestrator.WithPlanStore(store),
	)

	goal := supervisor.Task{ID: "g2", Spec: "coding-agent: implement X"}
	if _, err := o.Handle(context.Background(), goal); err != nil {
		t.Fatalf("TC-084-02: Handle: unexpected error: %v", err)
	}

	// Now Resume — verify_delete fires and returns tamper.
	_, err := o.Resume(context.Background(), orchestrator.Approval{
		From: "operator", To: "orchestrator", GoalID: "g2", Approved: true,
	})
	if err == nil {
		t.Fatal("TC-084-02: Resume with tamper: want error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "tamper") {
		t.Errorf("TC-084-02: error must contain 'tamper', got: %q", err.Error())
	}
	// No sub-goal dispatch.
	if spy.count() != 0 {
		t.Errorf("TC-084-02: want 0 dispatches on tamper, got %d", spy.count())
	}

	// After tamper, the plan is no longer pending.
	if o.HasPendingPlan("g2") {
		t.Errorf("TC-084-02: plan must not be pending after tamper Delete")
	}

	// ops: validate_write (Put) + verify_delete (TryDelete in Resume).
	ops := stub.opsInOrder()
	if len(ops) != 2 || ops[0] != "validate_write" || ops[1] != "verify_delete" {
		t.Errorf("TC-084-02: want ops [validate_write, verify_delete], got %v", ops)
	}
}

// --- TC-084-04: AGENT_BUILDER_MEMORY_GUARD_BIN unset → in-memory + warning --

// TestTC084_04_DegradedModeWarning asserts that NewPlanStoreFromEnv with no
// env var set returns a non-nil store and calls logFn with a warning naming
// both the missing config key and "memory-guard".
func TestTC084_04_DegradedModeWarning(t *testing.T) {
	// Ensure the env var is unset.
	t.Setenv(orchestrator.EnvVarMemoryGuardBin, "")

	var logCalls []struct {
		msg string
		kvs []any
	}
	logFn := func(msg string, kvs ...any) {
		logCalls = append(logCalls, struct {
			msg string
			kvs []any
		}{msg, kvs})
	}

	store := orchestrator.NewPlanStoreFromEnv(logFn)
	if store == nil {
		t.Fatal("TC-084-04: NewPlanStoreFromEnv returned nil store")
	}

	// Exactly one warning emitted.
	if len(logCalls) != 1 {
		t.Fatalf("TC-084-04: want 1 log call, got %d", len(logCalls))
	}
	call := logCalls[0]

	// Warning names the missing config key.
	found := false
	for _, kv := range call.kvs {
		if s, ok := kv.(string); ok && s == orchestrator.EnvVarMemoryGuardBin {
			found = true
		}
	}
	if !found {
		t.Errorf("TC-084-04: warning must name %q; got msg=%q kvs=%v",
			orchestrator.EnvVarMemoryGuardBin, call.msg, call.kvs)
	}

	// Warning names the disabled component ("memory-guard" or "memoryguard").
	combined := call.msg
	for _, kv := range call.kvs {
		combined += fmt.Sprintf(" %v", kv)
	}
	if !strings.Contains(strings.ToLower(combined), "memory-guard") &&
		!strings.Contains(strings.ToLower(combined), "memoryguard") {
		t.Errorf("TC-084-04: warning must name 'memory-guard' or 'memoryguard'; got %q", combined)
	}

	// The store must be functional (Put/Get/Delete work in-process, no IPC).
	plan := orchestrator.Plan{GoalID: "g-degraded", Goal: "test", SubGoals: nil}
	store.Put(plan)
	got, ok := store.Get("g-degraded")
	if !ok || got.Goal != "test" {
		t.Errorf("TC-084-04: degraded store Put/Get: want plan, got (%+v, %v)", got, ok)
	}
	store.Delete("g-degraded")
	if _, ok := store.Get("g-degraded"); ok {
		t.Errorf("TC-084-04: degraded store Delete: want ok=false after delete")
	}
}

// --- TC-084-05: tamper → halt + audit event ---------------------------------

// TestTC084_05_TamperHaltsAndEmitsAuditEvent asserts that when verify_delete
// returns tamper, Resume halts (returns error), no dispatch occurs, and the
// audit.FakeSink receives an event with Detail.TamperDetected=true.
func TestTC084_05_TamperHaltsAndEmitsAuditEvent(t *testing.T) {
	stub := newMGStubRunner()
	stub.setResponse("validate_write", map[string]any{
		"allow": true, "stored_id": "stub-id-3", "flags": nil,
	})
	stub.setResponse("verify_delete", map[string]any{
		"confirmed":        false,
		"residue_detected": true,
		"residue_summary":  "injected",
		"deletion_hash":    "deadbeef",
	})

	store := orchestrator.NewMemoryGuardPlanStoreWithRunner(
		"/stub/mg", "agent-builder/orchestrator", stub,
	)
	fakeSink := audit.NewFakeSink()
	spy := newDispatchSpy()
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionRequireApproval}

	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
		orchestrator.WithPlanStore(store),
		orchestrator.WithAuditSink(fakeSink),
	)

	goal := supervisor.Task{ID: "g3", Spec: "coding-agent: implement Z"}
	if _, err := o.Handle(context.Background(), goal); err != nil {
		t.Fatalf("TC-084-05: Handle: unexpected error: %v", err)
	}

	// Resume triggers verify_delete → tamper.
	_, err := o.Resume(context.Background(), orchestrator.Approval{
		From: "operator", To: "orchestrator", GoalID: "g3", Approved: true,
	})
	if err == nil {
		t.Fatal("TC-084-05: Resume with tamper: want error, got nil")
	}

	// No dispatch (plan halted).
	if spy.count() != 0 {
		t.Errorf("TC-084-05: want 0 dispatches on tamper, got %d", spy.count())
	}

	// FakeSink must have received a tamper event with TamperDetected=true.
	events := fakeSink.Events()
	var tamperEvent *audit.AuditEvent
	for i := range events {
		ev := events[i]
		if ev.Detail.TamperDetected {
			tamperEvent = &ev
			break
		}
	}
	if tamperEvent == nil {
		t.Fatalf("TC-084-05: FakeSink must receive an event with Detail.TamperDetected=true; got events=%+v", events)
	}
	if tamperEvent.Action != audit.ActionTamper {
		t.Errorf("TC-084-05: tamper event Action: want %q, got %q", audit.ActionTamper, tamperEvent.Action)
	}
	if tamperEvent.TaskID != "g3" {
		t.Errorf("TC-084-05: tamper event TaskID: want %q, got %q", "g3", tamperEvent.TaskID)
	}
}
