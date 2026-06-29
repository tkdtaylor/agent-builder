package orchestrator

import (
	"github.com/tkdtaylor/agent-builder/internal/loop"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// classifyBlockedSpawn converts a non-allow spawn-worker decision for a NECESSARY
// sub-goal into a typed loop.BlockedAction (ADR 055 seam 4, REQ-121-01). It is the
// orchestrator-side producer of the blocked-action feedback: a policy denial of an
// action the plan needed is named by its denied resource (the recipe), action
// (spawn-worker), and the deny reason — a signal DISTINCT from a gate failure or an
// executor error. The returned BlockedAction grants nothing; it only describes the
// denial so the reevaluation path can replan or escalate.
func classifyBlockedSpawn(sub SubGoal, reason string) loop.BlockedAction {
	return loop.BlockedAction{
		Resource: sub.RecipeName,
		Action:   SpawnWorkerAction,
		Reason:   reason,
	}
}

// ReevaluateBlockedSpawn routes a blocked sub-goal spawn through bounded
// reevaluation and then to an independent human escalation (ADR 055 seam 4,
// REQ-121-02/03). It is the orchestrator-side consumer of the blocked-action
// feedback produced by classifyBlockedSpawn.
//
// The bound and escalation semantics live in internal/loop (ReevaluationPolicy),
// mirroring the existing RetryPolicy gate-failure bound + needs-human escalation.
// This method supplies the orchestrator-specific ReplanFunc: each replan re-runs the
// planner on the ORIGINAL goal and returns the FRESH plan's allow set
// (Plan.AllowedResources, task 118). The re-derived plan is also inspected to decide
// whether it STILL needs the denied resource — if it does after the bound, the
// policy escalates to a human carrying the denied action + reason.
//
// The never-self-grant invariant (REQ-121-03) is structural here too: the ReplanFunc
// returns ONLY re-derived Plan.AllowedResources sets; it never unions the previous
// allow set with the denied resource. Authorization on a retry is always a freshly
// re-derived plan's set. The denied resource can re-appear ONLY if the new plan
// independently re-derived it — in which case StillBlocked stays true and the path
// leads to human escalation, not a self-grant.
//
//   - goal is the original goal text (re-planned on each reevaluation).
//   - blocked is the typed denial produced at the spawn-worker gate.
//   - policy bounds reevaluation; statusWriter receives the needs-human write on escalation.
func (o *Orchestrator) ReevaluateBlockedSpawn(
	goal supervisor.Task,
	blocked loop.BlockedAction,
	maxReevaluations int,
	statusWriter loop.StatusWriter,
) (loop.ReevaluationOutcome, error) {
	replan := func(b loop.BlockedAction) (loop.Reevaluation, error) {
		// Re-derive the plan from the ORIGINAL goal. The re-planned plan, not the
		// previous allow set, is the sole source of the next attempt's authorization.
		plan, err := o.planner.Plan(goal)
		if err != nil {
			return loop.Reevaluation{}, err
		}
		allowed := plan.AllowedResources()
		// StillBlocked iff the re-derived plan ITSELF still authorizes (and therefore
		// still needs) the denied resource. This is read from the fresh plan's own
		// derived set — the denied resource is never injected here.
		return loop.Reevaluation{
			AllowedResources: allowed,
			StillBlocked:     plan.authorizesResource(b.Resource),
		}, nil
	}

	pol, err := loop.NewReevaluationPolicy(maxReevaluations, replan)
	if err != nil {
		return loop.ReevaluationOutcome{}, err
	}
	return pol.ReevaluateBlocked(goal.ID, blocked, statusWriter)
}
