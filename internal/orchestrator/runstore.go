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
