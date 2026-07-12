package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/runstore"
)

// This file holds the durable run-journal (ADR 065, task 168) integration: the
// write side (persistPlanRecord, recordAttempt, finalizeGoalStatus) and the
// crash-recovery read side (RehydrateInFlight, ResumeFromRecord). Every write is
// opt-in behind an explicit `o.runStore != nil` guard, so an Orchestrator built
// without WithRunStore behaves byte-for-byte as before this task.

// persistPlanRecord durably records an admitted plan as a StatusRunning Record
// before dispatch, so a crash after admission can rehydrate and resume it. The
// plan is marshaled to JSON so runstore never needs the orchestrator's plan type
// (leaf discipline). Best-effort: a marshal or Save failure is reported, not
// fatal, matching the audit/tamper side-effect convention. Nil runStore is a no-op.
func (o *Orchestrator) persistPlanRecord(ctx context.Context, plan Plan) {
	if o.runStore == nil {
		return
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		_ = o.reporter.Report(ctx, fmt.Sprintf("run-journal: marshal plan %q: %v", plan.GoalID, err))
		return
	}
	o.runStoreMu.Lock()
	defer o.runStoreMu.Unlock()
	// Preserve the goal-level re-plan attempt counter (task 169) across the fresh
	// plan a re-plan iteration admits; a new plan resets Attempts (sub-goal state)
	// but the goal-level Attempt budget must carry forward.
	var attempt int
	if prev, ok, _ := o.runStore.Load(plan.GoalID); ok {
		attempt = prev.Attempt
	}
	if err := o.runStore.Save(runstore.Record{
		GoalID:  plan.GoalID,
		Goal:    plan.Goal,
		Plan:    planJSON,
		Status:  runstore.StatusRunning,
		Attempt: attempt,
	}); err != nil {
		_ = o.reporter.Report(ctx, fmt.Sprintf("run-journal: persist plan %q: %v", plan.GoalID, err))
	}
}

// recordAttempt records or updates the AttemptState for one sub-goal on the goal's
// Record. The before/after dispatch calls for a given (goalID, taskID) update the
// SAME logical attempt entry (matched by TaskID at Attempt 1) rather than appending
// a second one. The whole read-modify-write is guarded by runStoreMu so concurrent
// sub-goals of the same goal never lose an update. Nil runStore is a no-op.
func (o *Orchestrator) recordAttempt(goalID, taskID string, status runstore.Status, detail string) {
	if o.runStore == nil {
		return
	}
	o.runStoreMu.Lock()
	defer o.runStoreMu.Unlock()

	rec, ok, err := o.runStore.Load(goalID)
	if err != nil {
		return
	}
	if !ok {
		// No plan Record was persisted (e.g. resume against a store that never held
		// this goal); start from a minimal running record so attempts are still kept.
		rec = runstore.Record{GoalID: goalID, Status: runstore.StatusRunning}
	}

	updated := false
	for i := range rec.Attempts {
		if rec.Attempts[i].TaskID == taskID && rec.Attempts[i].Attempt == 1 {
			rec.Attempts[i].Status = status
			rec.Attempts[i].Detail = detail
			rec.Attempts[i].UpdatedAt = time.Now().UTC()
			updated = true
			break
		}
	}
	if !updated {
		rec.Attempts = append(rec.Attempts, runstore.AttemptState{
			TaskID:    taskID,
			Attempt:   1,
			Status:    status,
			Detail:    detail,
			UpdatedAt: time.Now().UTC(),
		})
	}
	_ = o.runStore.Save(rec)
}

// finalizeGoalStatus marks the goal terminal (StatusCompleted) once every sub-goal
// in the plan has a completed attempt, so a fully-succeeded goal drops out of
// ListInFlight. A goal with any non-completed sub-goal stays StatusRunning
// (in-flight, resumable). Nil runStore is a no-op.
func (o *Orchestrator) finalizeGoalStatus(plan Plan) {
	if o.runStore == nil {
		return
	}
	o.runStoreMu.Lock()
	defer o.runStoreMu.Unlock()

	rec, ok, err := o.runStore.Load(plan.GoalID)
	if err != nil || !ok {
		return
	}
	completed := make(map[string]bool, len(rec.Attempts))
	for _, a := range rec.Attempts {
		if a.Status == runstore.StatusCompleted {
			completed[a.TaskID] = true
		}
	}
	// Never downgrade a pause: a plan awaiting approval stays awaiting, even if the
	// sub-goals that DID run all completed (task 170).
	if rec.Status == runstore.StatusAwaitingApproval {
		return
	}
	allDone := len(plan.SubGoals) > 0
	for _, s := range plan.SubGoals {
		if !completed[s.Task.ID] {
			allDone = false
			break
		}
	}
	if allDone && rec.Status != runstore.StatusCompleted {
		rec.Status = runstore.StatusCompleted
		_ = o.runStore.Save(rec)
	}
}

// recordPendingApproval appends a PendingApproval for a sub-goal that hit the
// per-sub-goal run-task require_approval gate and marks the goal's Record
// StatusAwaitingApproval (ADR 065, task 170). Guarded by runStoreMu so concurrent
// sub-goals of the same plan never lose an update. Nil runStore is a no-op.
func (o *Orchestrator) recordPendingApproval(goalID, taskID, reason string) {
	if o.runStore == nil {
		return
	}
	o.runStoreMu.Lock()
	defer o.runStoreMu.Unlock()
	rec, ok, err := o.runStore.Load(goalID)
	if err != nil {
		return
	}
	if !ok {
		rec = runstore.Record{GoalID: goalID}
	}
	rec.Pending = append(rec.Pending, runstore.PendingApproval{
		TaskID:      taskID,
		Reason:      reason,
		RequestedAt: time.Now().UTC(),
	})
	rec.Status = runstore.StatusAwaitingApproval
	_ = o.runStore.Save(rec)
}

// planAwaitingApproval reports whether the plan's Record has been marked
// StatusAwaitingApproval by an earlier sub-goal's require_approval pause (task 170).
// Nil runStore or no record is false.
func (o *Orchestrator) planAwaitingApproval(goalID string) bool {
	if o.runStore == nil {
		return false
	}
	o.runStoreMu.Lock()
	defer o.runStoreMu.Unlock()
	rec, ok, err := o.runStore.Load(goalID)
	if err != nil || !ok {
		return false
	}
	return rec.Status == runstore.StatusAwaitingApproval
}

// ResumeApproval resolves a paused sub-goal (task 171): it removes the matching
// PendingApproval and, on approved, re-dispatches JUST that sub-goal (reusing
// dispatchOne); on !approved, marks that sub-goal's attempt needs-human without
// dispatching. When no pending approval remains, the plan is finalized to a terminal
// status and reported.
func (o *Orchestrator) ResumeApproval(ctx context.Context, goalID, taskID string, approved bool) error {
	if o.runStore == nil {
		return fmt.Errorf("orchestrator: ResumeApproval: no run store configured")
	}

	// 1. Remove the pending approval for taskID.
	o.runStoreMu.Lock()
	rec, ok, err := o.runStore.Load(goalID)
	if err != nil {
		o.runStoreMu.Unlock()
		return fmt.Errorf("orchestrator: ResumeApproval: load goal %q: %w", goalID, err)
	}
	if !ok {
		o.runStoreMu.Unlock()
		return fmt.Errorf("orchestrator: ResumeApproval: no record for goal %q", goalID)
	}
	found := false
	kept := make([]runstore.PendingApproval, 0, len(rec.Pending))
	for _, p := range rec.Pending {
		if p.TaskID == taskID {
			found = true
			continue
		}
		kept = append(kept, p)
	}
	if !found {
		o.runStoreMu.Unlock()
		return fmt.Errorf("orchestrator: ResumeApproval: no pending approval for goal %q task %q", goalID, taskID)
	}
	rec.Pending = kept
	// On approve, clear the awaiting-approval status BEFORE re-dispatch so the
	// task-170 pause-halt does not block this resumed sub-goal (it stops NEW
	// sub-goals; a resume is an explicit continuation). finalizeAfterApproval sets
	// the final status once the re-dispatch completes.
	if approved {
		rec.Status = runstore.StatusRunning
	}
	planJSON := rec.Plan
	_ = o.runStore.Save(rec)
	o.runStoreMu.Unlock()

	// 2. Resume (re-dispatch) or abort (mark needs-human).
	if approved {
		var plan Plan
		if uerr := json.Unmarshal(planJSON, &plan); uerr != nil {
			return fmt.Errorf("orchestrator: ResumeApproval: unmarshal plan for goal %q: %w", goalID, uerr)
		}
		var target *SubGoal
		for i := range plan.SubGoals {
			if plan.SubGoals[i].Task.ID == taskID {
				target = &plan.SubGoals[i]
				break
			}
		}
		if target == nil {
			return fmt.Errorf("orchestrator: ResumeApproval: task %q not in plan for goal %q", taskID, goalID)
		}
		if _, derr := o.dispatchOne(ctx, plan, *target); derr != nil {
			return fmt.Errorf("orchestrator: ResumeApproval: re-dispatch task %q: %w", taskID, derr)
		}
	} else {
		o.recordAttempt(goalID, taskID, runstore.StatusNeedsHuman, "denied by operator")
	}

	// 3. Finalize the plan when no pending approval remains.
	o.finalizeAfterApproval(ctx, goalID)
	return nil
}

// finalizeAfterApproval sets a terminal status and reports once when a goal has no
// remaining pending approvals (task 171). A denied/needs-human sub-goal makes the
// plan terminal-Failed; all-completed makes it Completed; anything else stays
// StatusRunning (some sub-goal is neither done nor pending).
func (o *Orchestrator) finalizeAfterApproval(ctx context.Context, goalID string) {
	if o.runStore == nil {
		return
	}
	o.runStoreMu.Lock()
	rec, ok, err := o.runStore.Load(goalID)
	if err != nil || !ok {
		o.runStoreMu.Unlock()
		return
	}
	if len(rec.Pending) > 0 {
		// Still awaiting other approvals.
		rec.Status = runstore.StatusAwaitingApproval
		_ = o.runStore.Save(rec)
		o.runStoreMu.Unlock()
		return
	}
	anyUnresolved := false
	allCompleted := len(rec.Attempts) > 0
	for _, a := range rec.Attempts {
		switch a.Status {
		case runstore.StatusNeedsHuman, runstore.StatusFailed:
			anyUnresolved = true
		case runstore.StatusCompleted:
			// ok
		default:
			allCompleted = false
		}
		if a.Status != runstore.StatusCompleted {
			allCompleted = false
		}
	}
	var final runstore.Status
	switch {
	case anyUnresolved:
		final = runstore.StatusFailed
	case allCompleted:
		final = runstore.StatusCompleted
	default:
		final = runstore.StatusRunning
	}
	rec.Status = final
	_ = o.runStore.Save(rec)
	o.runStoreMu.Unlock()

	if final == runstore.StatusCompleted || final == runstore.StatusFailed {
		_ = o.reporter.Report(ctx, fmt.Sprintf("Goal %q resolved after approval: %s", goalID, final))
	}
}

// SweepApprovalTimeouts scans in-flight records for pending approvals older than
// timeout and escalates each over the Reporter exactly once (task 171). The
// per-pending Escalated flag makes a later sweep idempotent. now is injected so the
// sweep is testable with a fake clock. Nil runStore is a no-op.
func (o *Orchestrator) SweepApprovalTimeouts(ctx context.Context, now time.Time, timeout time.Duration) {
	if o.runStore == nil {
		return
	}
	records, err := o.runStore.ListInFlight()
	if err != nil {
		return
	}
	for _, rec := range records {
		changed := false
		for i := range rec.Pending {
			p := &rec.Pending[i]
			if p.Escalated {
				continue
			}
			elapsed := now.Sub(p.RequestedAt)
			if elapsed <= timeout {
				continue
			}
			_ = o.reporter.Report(ctx, fmt.Sprintf(
				"Approval timeout: goal %q task %q has awaited approval for %s, escalating to human",
				rec.GoalID, p.TaskID, elapsed.Round(time.Second)))
			p.Escalated = true
			changed = true
		}
		if changed {
			o.runStoreMu.Lock()
			_ = o.runStore.Save(rec)
			o.runStoreMu.Unlock()
		}
	}
}

// RehydrateInFlight returns every non-terminal run record in the store, so a fresh
// process (task 174's daemon) can discover the goals interrupted by a crash. It is
// a thin, documented wrapper over Store.ListInFlight naming the operation at the
// orchestrator layer.
func RehydrateInFlight(store runstore.Store) ([]runstore.Record, error) {
	return store.ListInFlight()
}

// ResumeFromRecord re-drives dispatch for a rehydrated record, re-dispatching ONLY
// sub-goals whose TaskID has no completed attempt in rec.Attempts. A sub-goal with
// a running (crashed mid-dispatch) or absent attempt IS re-dispatched: running-and-
// crashed is indistinguishable from never-started at this scope, and the idempotency
// rule is specifically "never DOUBLE-dispatch a COMPLETED attempt" (re-running an
// interrupted one is correct and safe). The plan is reconstructed from rec.Plan and
// the SAME dispatch path the normal flow uses is invoked on the filtered remainder.
func (o *Orchestrator) ResumeFromRecord(ctx context.Context, rec runstore.Record) (PlanResult, error) {
	var plan Plan
	if err := json.Unmarshal(rec.Plan, &plan); err != nil {
		return PlanResult{}, fmt.Errorf("orchestrator: resume goal %q: unmarshal plan: %w", rec.GoalID, err)
	}

	completed := make(map[string]bool, len(rec.Attempts))
	for _, a := range rec.Attempts {
		if a.Status == runstore.StatusCompleted {
			completed[a.TaskID] = true
		}
	}

	remaining := make([]SubGoal, 0, len(plan.SubGoals))
	for _, s := range plan.SubGoals {
		if !completed[s.Task.ID] {
			remaining = append(remaining, s)
		}
	}

	// Nothing left to do: the record is already fully completed. Finalize its status
	// (idempotent) and report the plan as done without dispatching anything.
	if len(remaining) == 0 {
		o.finalizeGoalStatus(plan)
		return PlanResult{Goal: plan.Goal}, nil
	}

	return o.dispatchSubGoals(ctx, plan, remaining)
}
