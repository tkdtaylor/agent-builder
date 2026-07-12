package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/memoryguard"
)

// EnvVarPlanStoreDir configures the on-disk directory backing the durable,
// read-gated plan store (task 173). When unset, a default under the OS temp dir is
// used; operators wanting cross-reboot durability should set it to a persistent path.
const EnvVarPlanStoreDir = "AGENT_BUILDER_PLAN_STORE_DIR"

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
	inner *memoryguard.DurableStore[Plan]
}

// NewMemoryGuardPlanStore constructs a MemoryGuardPlanStore backed by the given
// memory-guard binary path and a durable on-disk directory (task 173). identity is
// the actor label sent to memory-guard (e.g. "agent-builder/orchestrator"). It
// returns an error when the durable store cannot rebuild from a malformed on-disk
// state (fail loud, not silent degradation).
func NewMemoryGuardPlanStore(binPath, identity, dir string) (*MemoryGuardPlanStore, error) {
	client := memoryguard.NewClient(binPath)
	inner, err := memoryguard.NewDurableStore[Plan](client, identity, dir)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: durable plan store: %w", err)
	}
	return &MemoryGuardPlanStore{inner: inner}, nil
}

// NewMemoryGuardPlanStoreWithRunner constructs a MemoryGuardPlanStore with an
// injectable ExecRunner (for tests: substitutes a spy/stub for the real binary).
func NewMemoryGuardPlanStoreWithRunner(binPath, identity, dir string, runner memoryguard.ExecRunner) (*MemoryGuardPlanStore, error) {
	client := memoryguard.NewClientWithRunner(binPath, runner)
	inner, err := memoryguard.NewDurableStore[Plan](client, identity, dir)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: durable plan store: %w", err)
	}
	return &MemoryGuardPlanStore{inner: inner}, nil
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

// Get returns the plan held for goalID and whether one was found. The read is now
// gated through the memory-guard read-gate (task 173): a read-gate denial or any
// transport error returns (Plan{}, false), matching PlanStore.Get's void "not found"
// contract, so a denied read NEVER leaks the plan.
func (s *MemoryGuardPlanStore) Get(goalID string) (Plan, bool) {
	plan, ok, err := s.inner.Get(goalID)
	if err != nil || !ok {
		return Plan{}, false
	}
	return plan, true
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
func NewPlanStoreFromEnv(logFn MemoryGuardLogFunc) (PlanStore, error) {
	binPath := os.Getenv(EnvVarMemoryGuardBin)
	if binPath == "" {
		logFn("memory-guard write-gate DISABLED — running in-memory-only mode",
			"missing_config", EnvVarMemoryGuardBin,
			"component", "memory-guard",
			"degraded", true,
		)
		return NewMemoryPlanStore(), nil
	}
	dir := os.Getenv(EnvVarPlanStoreDir)
	if dir == "" {
		// Default under the OS temp dir. Operators wanting cross-reboot durability
		// set AGENT_BUILDER_PLAN_STORE_DIR to a persistent path.
		dir = filepath.Join(os.TempDir(), "agent-builder-planstore")
	}
	return NewMemoryGuardPlanStore(binPath, "agent-builder/orchestrator", dir)
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
