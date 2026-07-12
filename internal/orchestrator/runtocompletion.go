package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/tkdtaylor/agent-builder/internal/runstore"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// ErrGoalAttemptsExhausted is the sentinel RunToCompletion returns (wrapped) when
// a goal still has a terminal sub-goal failure after maxAttempts goal-level
// re-plans. Callers match it with errors.Is.
var ErrGoalAttemptsExhausted = errors.New("orchestrator: goal attempts exhausted")

// HasTerminalFailure reports whether the plan result carries at least one failed
// sub-goal outcome (the signal RunToCompletion folds and re-plans on). A
// fully-successful or empty (paused) result has none.
func (r PlanResult) HasTerminalFailure() bool {
	for _, o := range r.Outcomes {
		if !o.Success {
			return true
		}
	}
	return false
}

// FailureDetails returns the per-sub-goal failure detail text for every failed
// outcome, to be folded into the goal for the next re-plan. Each Detail is already
// the sub-goal's own failure text (set by dispatchOne from the dispatch/gate
// failure), so this reuses that convention rather than inventing a new format.
func (r PlanResult) FailureDetails() []string {
	var details []string
	for _, o := range r.Outcomes {
		if o.Success {
			continue
		}
		d := o.Detail
		if d == "" {
			d = "sub-goal failed"
		}
		details = append(details, d)
	}
	return details
}

// RunToCompletion runs a bounded, goal-level re-plan loop (ADR 065, task 169). It
// calls Handle; on a terminal sub-goal failure it folds the failure detail into
// the goal text via FoldGoalText and re-plans, up to maxAttempts GOAL-LEVEL
// attempts (distinct from, one layer above, each sub-goal's runtime.Run retry
// budget). On exhaustion it escalates once over the Reporter and returns the last
// PlanResult plus a wrapped ErrGoalAttemptsExhausted. When a RunStore is configured
// the attempt counter is persisted per attempt so the budget survives a crash
// mid-loop; without a RunStore the loop still functions (in-memory counting, no
// cross-process durability).
func (o *Orchestrator) RunToCompletion(ctx context.Context, goal supervisor.Task, maxAttempts int) (PlanResult, error) {
	attempt := o.loadAttemptCount(goal.ID)
	currentGoal := goal
	var last PlanResult
	for attempt < maxAttempts {
		attempt++
		// Persist the attempt BEFORE dispatch so a crash mid-attempt does not let a
		// restart re-run past the configured budget.
		o.saveAttemptCount(goal.ID, attempt)

		result, err := o.Handle(ctx, currentGoal)
		last = result
		if err != nil {
			// A hard error (not a plan-level sub-goal failure) is not retried here.
			return result, err
		}
		if !result.HasTerminalFailure() {
			return result, nil
		}
		// Fold the failure detail into the ORIGINAL goal text (not the already-folded
		// text) so successive re-plans do not accrete stale detail from prior rounds.
		currentGoal.Spec = FoldGoalText(goal.Spec, result.FailureDetails())
	}

	o.escalateExhausted(ctx, goal.ID, attempt)
	return last, fmt.Errorf("%w: goal %q after %d attempts", ErrGoalAttemptsExhausted, goal.ID, attempt)
}

// loadAttemptCount reads the persisted goal-level attempt counter, or 0 when no
// RunStore is configured or no prior record exists.
func (o *Orchestrator) loadAttemptCount(goalID string) int {
	if o.runStore == nil {
		return 0
	}
	o.runStoreMu.Lock()
	defer o.runStoreMu.Unlock()
	rec, ok, err := o.runStore.Load(goalID)
	if err != nil || !ok {
		return 0
	}
	return rec.Attempt
}

// saveAttemptCount persists the goal-level attempt counter, preserving the rest of
// the record. Best-effort; a nil RunStore is a no-op (in-memory counting only).
func (o *Orchestrator) saveAttemptCount(goalID string, attempt int) {
	if o.runStore == nil {
		return
	}
	o.runStoreMu.Lock()
	defer o.runStoreMu.Unlock()
	rec, ok, err := o.runStore.Load(goalID)
	if err != nil || !ok {
		rec = runstore.Record{GoalID: goalID, Status: runstore.StatusRunning}
	}
	rec.Attempt = attempt
	_ = o.runStore.Save(rec)
}

// escalateExhausted reports the goal's budget exhaustion over the Reporter exactly
// once, naming the goal ID, the attempt count, and the word "exhausted".
func (o *Orchestrator) escalateExhausted(ctx context.Context, goalID string, attempt int) {
	_ = o.reporter.Report(ctx, fmt.Sprintf(
		"Goal %q exhausted after %d attempts, escalating to human", goalID, attempt))
}
