package orchestrator

import (
	"os"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/memoryguard"
)

// EnvVarMemoryGuardBin is the environment variable that configures the path to
// the memory-guard binary. When unset the orchestrator degrades to MemoryPlanStore
// with a structured warning (ADR 049 §3).
const EnvVarMemoryGuardBin = memoryguard.EnvVarMemoryGuardBin

// MemoryGuardPlanStore is the memory-guard-backed PlanStore adapter for the
// orchestrator. It wraps memoryguard.MemoryGuardStore[Plan] and implements the
// orchestrator.PlanStore interface by keying on plan.GoalID.
//
// This type lives in internal/orchestrator (not in internal/memoryguard) so the
// leaf package never imports internal/orchestrator. The F-012 fitness check asserts
// internal/memoryguard has no other agent-builder/internal imports.
type MemoryGuardPlanStore struct {
	inner *memoryguard.MemoryGuardStore[Plan]
}

// NewMemoryGuardPlanStore constructs a MemoryGuardPlanStore backed by the given
// memory-guard binary path. identity is the actor label sent to memory-guard
// (e.g. "agent-builder/orchestrator").
func NewMemoryGuardPlanStore(binPath, identity string) *MemoryGuardPlanStore {
	client := memoryguard.NewClient(binPath)
	return &MemoryGuardPlanStore{
		inner: memoryguard.NewMemoryGuardStore[Plan](client, identity),
	}
}

// NewMemoryGuardPlanStoreWithRunner constructs a MemoryGuardPlanStore with an
// injectable ExecRunner (for tests — substitutes a spy/stub for the real binary).
func NewMemoryGuardPlanStoreWithRunner(binPath, identity string, runner memoryguard.ExecRunner) *MemoryGuardPlanStore {
	client := memoryguard.NewClientWithRunner(binPath, runner)
	return &MemoryGuardPlanStore{
		inner: memoryguard.NewMemoryGuardStore[Plan](client, identity),
	}
}

// TryPut serialises plan as JSON, sends it through the memory-guard write-gate,
// and on success records the plan and its stored_id. Returns ErrWriteGateDenied
// when the guard rejects the write, or any transport/parse error. Called by the
// orchestrator's Handle method via the TamperAwarePlanStore type assertion.
func (s *MemoryGuardPlanStore) TryPut(plan Plan) error {
	return s.inner.Put(plan.GoalID, plan)
}

// Put calls TryPut and silently drops any error, preserving the void PlanStore
// interface. Callers that need the error must use TryPut via TamperAwarePlanStore.
func (s *MemoryGuardPlanStore) Put(plan Plan) {
	_ = s.inner.Put(plan.GoalID, plan)
}

// Get returns the plan held for goalID and whether one was found.
func (s *MemoryGuardPlanStore) Get(goalID string) (Plan, bool) {
	return s.inner.Get(goalID)
}

// Delete removes the plan for goalID after calling verify_delete. It returns
// memoryguard.ErrTamperDetected (wrapped) when the block reports tamper.
// IMPORTANT: MemoryGuardPlanStore.Delete is the only PlanStore implementation that
// returns an error — the orchestrator's Resume method checks for this error and
// halts the plan, emitting a tamper audit event.
func (s *MemoryGuardPlanStore) Delete(goalID string) {
	// NOTE: Delete on PlanStore is void (matches MemoryPlanStore). The tamper
	// detection happens through TryDelete instead. See orchestrator.go.
	_ = s.inner.Delete(goalID)
}

// TryDelete removes the plan for goalID and returns any tamper error. The
// orchestrator's Resume method calls this (via the deletePlan helper) instead of
// the void Delete so it can surface tamper errors.
func (s *MemoryGuardPlanStore) TryDelete(goalID string) error {
	return s.inner.Delete(goalID)
}

// StoredID returns the memory-guard stored_id for the given goalID (used in tests).
func (s *MemoryGuardPlanStore) StoredID(goalID string) (string, bool) {
	return s.inner.StoredID(goalID)
}

// Compile-time assertion: MemoryGuardPlanStore must satisfy the PlanStore interface.
var _ PlanStore = (*MemoryGuardPlanStore)(nil)

// MemoryGuardLogFunc is a structured-log function the degraded-mode warning uses.
type MemoryGuardLogFunc func(msg string, keysAndValues ...any)

// NewPlanStoreFromEnv reads AGENT_BUILDER_MEMORY_GUARD_BIN. When set, it returns
// a MemoryGuardPlanStore backed by the configured binary. When unset, it calls logFn
// with a structured warning and returns MemoryPlanStore (the in-memory v1 backend).
// This is the live-path constructor called from the CLI / runtime wiring.
func NewPlanStoreFromEnv(logFn MemoryGuardLogFunc) PlanStore {
	binPath := os.Getenv(EnvVarMemoryGuardBin)
	if binPath == "" {
		logFn("memory-guard write-gate DISABLED — running in-memory-only mode",
			"missing_config", EnvVarMemoryGuardBin,
			"component", "memory-guard",
			"degraded", true,
		)
		return NewMemoryPlanStore()
	}
	return NewMemoryGuardPlanStore(binPath, "agent-builder/orchestrator")
}

// WithAuditSink sets the audit.Sink the orchestrator uses to emit security events
// (e.g. tamper-detected on delete-verify failure). When unset, tamper events are
// not emitted to an audit trail (the plan is still halted on tamper — the audit
// sink is optional for the halt, mandatory for the audit event TC-084-05 requires).
func WithAuditSink(sink audit.Sink) Option {
	return func(o *Orchestrator) { o.auditSink = sink }
}

// emitTamperEvent emits an audit.ActionTamper event with TamperDetected=true
// when the memory-guard delete-verify reports a tamper signal (REQ-084-05).
// It is a best-effort call: if the audit sink is nil or returns an error, the
// error is silently dropped — the plan halt (returned to the caller) is the
// load-bearing action; the audit record is secondary evidence.
func (o *Orchestrator) emitTamperEvent(goalID string) {
	if o.auditSink == nil {
		return
	}
	ev := audit.AuditEvent{
		Action: audit.ActionTamper,
		TaskID: goalID,
		Detail: audit.EventDetail{
			TamperDetected: true,
			Reason:         "memory-guard delete-verify reported tamper (residue_detected or !confirmed)",
		},
	}
	_ = o.auditSink.Append(ev)
}
