package orchestrator

import (
	"context"
	"sync"
	"time"
)

// GoalState is a goal's lifecycle state in the live status registry (ADR 054 §3).
// Queued/Planning/AwaitingApproval/Dispatching/Done/Failed are the normal lifecycle
// states; Cancelled is the terminal state a `cancel <goalID>` drives (task 116): the
// cancel handler fires the goal's CancelFunc, sets Cancelled, and consumes the plan
// from the PlanStore.
type GoalState int

const (
	// StateQueued — the goal is admitted-pending: registered in the registry but
	// parked behind the goal-admission cap (AGENT_BUILDER_MAX_GOALS) until a slot
	// frees. It has not yet entered Planning.
	StateQueued GoalState = iota
	// StateClarifying — the goal actor is in conversational clarification (ADR 056).
	StateClarifying
	// StatePlanning — the goal actor is decomposing the goal via Planner.Plan and
	// running the spawn-plan gate.
	StatePlanning
	// StateAwaitingApproval — the plan requires human approval; it sits in the
	// PlanStore awaiting Resume (driven by task 115's approval path, reserved here).
	StateAwaitingApproval
	// StateDispatching — the plan was allowed and its sub-goal workers are being
	// dispatched (the per-sub-goal goroutines in dispatchPlan are live).
	StateDispatching
	// StateDone — the goal reached a terminal success/aggregated outcome.
	StateDone
	// StateFailed — the goal terminated with an error (planning error, deny-audit
	// halt, or a dispatch-level failure that errored Handle).
	StateFailed
	// StateCancelled — the goal was cancelled by a `cancel <goalID>` (task 116): its
	// CancelFunc fired (tearing down in-flight workers via the run-loop ctx.Done()
	// arm) and its plan was consumed from the PlanStore.
	StateCancelled
)

// String renders a GoalState as its lowercase lifecycle name for status reports
// and test assertions.
func (s GoalState) String() string {
	switch s {
	case StateQueued:
		return "queued"
	case StateClarifying:
		return "clarifying"
	case StatePlanning:
		return "planning"
	case StateAwaitingApproval:
		return "awaiting-approval"
	case StateDispatching:
		return "dispatching"
	case StateDone:
		return "done"
	case StateFailed:
		return "failed"
	case StateCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// IsTerminal reports whether the state is a terminal lifecycle state (Done,
// Failed, or Cancelled) — used by the goal-admission cap to know when a goal's
// slot has freed and by status renders to distinguish live from finished goals.
func (s GoalState) IsTerminal() bool {
	return s == StateDone || s == StateFailed || s == StateCancelled
}

// SubGoalProgress is the per-sub-goal projection within a GoalStatus: the recipe
// it runs and whether the worker is running/done/failed. Written from inside the
// task-086 dispatch goroutines (the same place outcomes are written).
type SubGoalProgress struct {
	Name   string // the sub-goal task ID
	Recipe string // the recipe the sub-goal dispatches
	State  string // "running" | "done" | "failed"
}

// GoalStatus is one goal's live lifecycle snapshot. It is a PROJECTION for
// observability (ADR 054 §3) — never the source of truth for control flow (the
// PlanStore is). The mailbox / CancelFunc / pending-info fields that tasks
// 113/115/116 add are intentionally NOT present yet; this struct is the seam they
// extend, and the registry's locking discipline is what makes that extension safe.
type GoalStatus struct {
	GoalID    string
	State     GoalState
	SubGoals  []SubGoalProgress
	UpdatedAt time.Time
	// PendingInfo is the per-goal pending-info queue (ADR 054 §4, task 115). An
	// `info` message for this goalID appends its text here synchronously; the queue
	// is read (and drained) ONLY at a checkpoint boundary by the goal actor — never
	// inside a dispatch goroutine. This is the structural guarantee that an info
	// message touches no running worker (queue-don't-interrupt). It is part of the
	// GoalStatus projection for observability; control flow never gates on it.
	PendingInfo []string
}

// StatusRegistry is the goalID-keyed, mutex-guarded live status registry (ADR 054
// §3). It mirrors MemoryPlanStore's locking discipline. It is a projection: a
// registry write NEVER gates control flow, so the orchestrator core treats a nil
// registry as a no-op (the goal still completes). All mutating methods are no-ops
// on a nil *StatusRegistry so call sites need no nil-guard.
//
// nowFn is the clock seam (defaults to time.Now); tests may override it but the
// default keeps UpdatedAt monotonic-enough for an operator snapshot.
type StatusRegistry struct {
	mu    sync.Mutex
	goals map[string]*GoalStatus
	// cancels holds the per-goal CancelFunc (ADR 054 §3/§5, task 116). It is keyed by
	// goalID and is SEPARATE from goals because a CancelFunc is a live control handle,
	// not a clone-able projection value (cloneStatus must never copy it). The control
	// loop derives one context.WithCancel per goal and registers its CancelFunc here
	// (SetCancelFunc); the cancel handler calls Cancel(goalID) to fire only that goal's
	// derived ctx — siblings' contexts are independent, so there is no blast radius.
	cancels map[string]context.CancelFunc
	nowFn   func() time.Time
}

// NewStatusRegistry constructs an empty registry using time.Now as the clock.
func NewStatusRegistry() *StatusRegistry {
	return &StatusRegistry{
		goals:   make(map[string]*GoalStatus),
		cancels: make(map[string]context.CancelFunc),
		nowFn:   time.Now,
	}
}

func (r *StatusRegistry) now() time.Time {
	if r.nowFn != nil {
		return r.nowFn()
	}
	return time.Now()
}

// Register inserts a goal at the given initial state (typically StateQueued at
// admission). It overwrites any prior entry for the same goalID. A nil registry is
// a no-op.
func (r *StatusRegistry) Register(goalID string, state GoalState) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.goals[goalID] = &GoalStatus{
		GoalID:    goalID,
		State:     state,
		UpdatedAt: r.now(),
	}
}

// SetState transitions a goal's lifecycle state. If the goal is not registered it
// is created at this state (so an actor that bypassed Register — e.g. a direct
// Handle call in a unit test — still projects). A nil registry is a no-op.
func (r *StatusRegistry) SetState(goalID string, state GoalState) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.goals[goalID]
	if !ok {
		g = &GoalStatus{GoalID: goalID}
		r.goals[goalID] = g
	}
	g.State = state
	g.UpdatedAt = r.now()
}

// SetCancelFunc registers the per-goal CancelFunc (ADR 054 §5, task 116). The
// control loop derives one context.WithCancel per goal and calls this before
// spawning the goal actor, so a `cancel <goalID>` that races actor startup still
// finds a cancel handle. A nil registry or nil cancel is a no-op. Re-registering a
// goalID overwrites the prior handle.
func (r *StatusRegistry) SetCancelFunc(goalID string, cancel context.CancelFunc) {
	if r == nil || cancel == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancels[goalID] = cancel
}

// Cancel fires the goal's registered CancelFunc and reports whether one was found.
// It cancels ONLY this goal's derived context (siblings are independent — no blast
// radius, ADR 054 §5). The CancelFunc is removed after firing so a second cancel of
// the same goal is a no-op (returns false). A nil registry or unknown goal returns
// false. The state transition to Cancelled and the plan-consume are the caller's
// responsibility (the cancel handler) — this method only fires the context.
func (r *StatusRegistry) Cancel(goalID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	cancel, ok := r.cancels[goalID]
	if ok {
		delete(r.cancels, goalID)
	}
	r.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// SetSubGoal records (or updates) the progress of one sub-goal within a goal.
// Matching is by sub-goal Name; a new name appends, an existing name updates its
// State. A nil registry is a no-op. If the goal is not registered it is created so
// the sub-goal projection is never silently dropped.
func (r *StatusRegistry) SetSubGoal(goalID string, progress SubGoalProgress) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.goals[goalID]
	if !ok {
		g = &GoalStatus{GoalID: goalID}
		r.goals[goalID] = g
	}
	for i := range g.SubGoals {
		if g.SubGoals[i].Name == progress.Name {
			g.SubGoals[i] = progress
			g.UpdatedAt = r.now()
			return
		}
	}
	g.SubGoals = append(g.SubGoals, progress)
	g.UpdatedAt = r.now()
}

// Get returns a deep copy of the goal's status and whether it was found. It
// returns a copy (not the stored pointer) so a status read never races the actor
// mutating the same struct — this is the read path a status query (task 114) and
// the control loop's admission check use. A nil registry returns (zero, false).
func (r *StatusRegistry) Get(goalID string) (GoalStatus, bool) {
	if r == nil {
		return GoalStatus{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.goals[goalID]
	if !ok {
		return GoalStatus{}, false
	}
	return cloneStatus(g), true
}

// Snapshot returns a deep copy of every goal status, for a fleet-status read. A
// nil registry returns an empty slice.
func (r *StatusRegistry) Snapshot() []GoalStatus {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]GoalStatus, 0, len(r.goals))
	for _, g := range r.goals {
		out = append(out, cloneStatus(g))
	}
	return out
}

// LiveNonQueuedCount returns the number of goals in a non-Queued, non-terminal
// state ({Planning, AwaitingApproval, Dispatching}). It is the load-bearing
// observability check behind TC-112-04: under MAX_GOALS=1, this count must never
// exceed 1. A nil registry returns 0.
func (r *StatusRegistry) LiveNonQueuedCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, g := range r.goals {
		if g.State != StateQueued && !g.State.IsTerminal() {
			n++
		}
	}
	return n
}

// EnqueueInfo appends one info text to a goal's pending-info queue (ADR 054 §4,
// task 115). This is the ONLY state an `info` message writes synchronously — it
// touches no running worker. If the goal is not registered it is created so an info
// that races actor startup is never silently dropped. A nil registry is a no-op.
//
// The text is recorded verbatim; deduplication is intentionally NOT applied — two
// identical info messages are two distinct operator intents and both surface at the
// next checkpoint.
func (r *StatusRegistry) EnqueueInfo(goalID, text string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.goals[goalID]
	if !ok {
		g = &GoalStatus{GoalID: goalID}
		r.goals[goalID] = g
	}
	g.PendingInfo = append(g.PendingInfo, text)
	g.UpdatedAt = r.now()
}

// PendingInfo returns a copy of a goal's current pending-info queue WITHOUT
// draining it (the read the approval-solicitation surfacing uses — TC-115-02). A
// nil registry or unknown goal returns nil.
func (r *StatusRegistry) PendingInfo(goalID string) []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.goals[goalID]
	if !ok || len(g.PendingInfo) == 0 {
		return nil
	}
	out := make([]string, len(g.PendingInfo))
	copy(out, g.PendingInfo)
	return out
}

// DrainInfo returns a goal's pending-info queue AND clears it atomically (the read
// the fold path uses at a checkpoint — TC-115-03: after folding, the queue is
// empty so a subsequent checkpoint does not double-apply the same info). A nil
// registry or unknown/empty goal returns nil and leaves the queue untouched.
func (r *StatusRegistry) DrainInfo(goalID string) []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.goals[goalID]
	if !ok || len(g.PendingInfo) == 0 {
		return nil
	}
	out := g.PendingInfo
	g.PendingInfo = nil
	g.UpdatedAt = r.now()
	return out
}

func cloneStatus(g *GoalStatus) GoalStatus {
	cp := *g
	if len(g.SubGoals) > 0 {
		cp.SubGoals = make([]SubGoalProgress, len(g.SubGoals))
		copy(cp.SubGoals, g.SubGoals)
	}
	if len(g.PendingInfo) > 0 {
		cp.PendingInfo = make([]string, len(g.PendingInfo))
		copy(cp.PendingInfo, g.PendingInfo)
	}
	return cp
}
