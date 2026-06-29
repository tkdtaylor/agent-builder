package cli

// Status handler body for ADR 054 §3 (task 114).
//
// statusQueryHandler is the BODY of the status-query path that routeStatus
// dispatches to. It reads the goalID-keyed StatusRegistry and answers
// IMMEDIATELY over the shared Reporter — no Handle/Resume call, no goal-actor
// interaction.
//
// Two render shapes (ADR 054 §3):
//
//   - Fleet status (empty goalID): one entry per registered goal, each with its
//     GoalID and GoalState string. Suitable for an operator scan.
//   - Per-goal status (non-empty goalID): the goal's GoalState plus per-sub-goal
//     progress (name/recipe=state). Tolerates an empty SubGoals slice without
//     panicking (the goal may not have entered Dispatching yet).
//
// Unknown goalID: the caller (task 113's routeStatus) already routes unknown
// goals as "no such goal" before reaching this handler on the per-goal path.
// This handler is only reached for a KNOWN goalID; it is nonetheless safe if
// passed an unregistered goalID (returns an empty-looking reply).

import (
	"context"
	"fmt"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// newStatusHandler constructs the live statusHandler func from the shared
// registry and reporter. It is called inside assembleOrchestrate to wire the
// status path end-to-end without exposing the registry or reporter to the
// router directly. The returned func captures ctx from outside so the same
// background context the control loop uses reaches the Reporter.
//
// The handler MUST NOT call o.orch.Handle or o.orch.Resume — those are
// goal-processing paths. It reads only the mutex-guarded registry snapshot
// and writes to the Reporter. This is what makes status answerable while a
// goal is mid-dispatch (ADR 054 §3, REQ-114-01).
func newStatusHandler(ctx context.Context, reg *orchestrator.StatusRegistry, rep supervisor.Reporter) func(goalID string) {
	return func(goalID string) {
		var text string
		if goalID == "" {
			text = renderFleetStatus(reg)
		} else {
			text = renderGoalStatus(reg, goalID)
		}
		// Reporter error is swallowed (same convention as routeCommand's report):
		// a failed report must not halt the control plane.
		_ = rep.Report(ctx, text)
	}
}

// renderFleetStatus renders a fleet-wide status summary: one line per
// registered goal with its GoalID and GoalState string. The order is
// deterministic within a test (Snapshot order is insertion order in the
// underlying map, which Go does not guarantee, so the caller must not rely on
// ordering — the spec only requires all three entries are present, not their
// order).
func renderFleetStatus(reg *orchestrator.StatusRegistry) string {
	statuses := reg.Snapshot()
	if len(statuses) == 0 {
		return "status: no goals registered"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "fleet status (%d goal(s)):\n", len(statuses))
	for _, gs := range statuses {
		fmt.Fprintf(&b, "  %s: %s\n", gs.GoalID, gs.State)
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderGoalStatus renders one goal's status: its GoalState plus per-sub-goal
// progress. It tolerates an empty SubGoals slice (the goal may not have
// entered Dispatching yet) — the state line is still emitted, no sub-goal
// lines follow. This is the defensive no-panic contract from the spec
// (REQ-114-03).
func renderGoalStatus(reg *orchestrator.StatusRegistry, goalID string) string {
	gs, ok := reg.Get(goalID)
	if !ok {
		// Should not normally be reached (task 113's router handles unknown
		// goalID before calling this handler), but return a safe fallback.
		return fmt.Sprintf("status %s: not found", goalID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "status %s: %s", goalID, gs.State)
	for _, sub := range gs.SubGoals {
		fmt.Fprintf(&b, "\n  %s/%s=%s", sub.Name, sub.Recipe, sub.State)
	}
	return b.String()
}
