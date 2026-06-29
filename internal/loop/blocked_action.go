package loop

import (
	"errors"
	"fmt"

	"github.com/tkdtaylor/agent-builder/internal/tasksource"
)

// FailureBlockedAction classifies a fail outcome whose cause is a policy denial of
// an action the agent NEEDED to make progress (ADR 055 seam 4, task 121). It is
// deliberately distinct from FailureGate (the verification gate failed) and from
// FailureExecutorError (the executor process errored): a blocked action is not a
// bug in the work or a crash — it is the authorization model refusing a necessary
// action, which must route to bounded reevaluation (replan) and then to an
// independent human escalation, never to a self-grant.
const FailureBlockedAction FailureReason = "blocked-action"

// BlockedAction is the typed feedback carried by a FailureBlockedAction failure
// (ADR 055 seam 4). It names exactly what the policy denied and why, so the
// orchestrator can (a) replan within what is permitted, or (b) escalate to a human
// with enough context to decide whether to grant independently. It carries no
// allow set and no "widen" affordance: a BlockedAction describes the denial; it can
// never itself authorize the denied resource.
type BlockedAction struct {
	// Resource is the policy resource ID that was denied (e.g. the recipe name for a
	// spawn-worker deny, or the task ID for a run-task deny).
	Resource string
	// Action is the policy action that was denied (e.g. "spawn-worker", "run-task").
	Action string
	// Reason is the human-readable deny reason returned by the gate.
	Reason string
}

// IsZero reports whether the BlockedAction carries no denial detail. A
// FailureBlockedAction with an empty Resource AND empty Reason is malformed — the
// classifier and the reevaluation policy reject it rather than escalate an empty
// "something was denied" signal.
func (b BlockedAction) IsZero() bool {
	return b.Resource == "" && b.Action == "" && b.Reason == ""
}

// String renders the blocked action for human-facing escalation/report text.
func (b BlockedAction) String() string {
	return fmt.Sprintf("action %q on resource %q denied: %s", b.Action, b.Resource, b.Reason)
}

// ErrEmptyBlockedAction means a blocked-action failure was constructed or routed
// without naming the denied resource/action — a programming error, surfaced loudly
// rather than escalated as an empty signal.
var ErrEmptyBlockedAction = errors.New("loop: blocked-action carries no resource/action/reason")

// ClassifyBlockedAction builds the typed FailureBlockedAction failure from the
// denial detail produced at the policy gate (ADR 055 seam 4, REQ-121-01). It is the
// producer-side classifier the orchestrate feedback path calls when a NECESSARY
// action is denied, so the denial surfaces as a distinct failure kind (NOT
// FailureGate / FailureExecutorError) carrying the resource/action + reason. A
// blank resource AND action AND reason is rejected (ErrEmptyBlockedAction): an empty
// "something was denied" signal must never masquerade as a typed blocked action.
func ClassifyBlockedAction(resource, action, reason string) (Failure, error) {
	blocked := BlockedAction{Resource: resource, Action: action, Reason: reason}
	if blocked.IsZero() {
		return Failure{}, ErrEmptyBlockedAction
	}
	return Failure{
		Reason:  FailureBlockedAction,
		Blocked: &blocked,
	}, nil
}

// Reevaluation is the result of one replan triggered by a blocked action (ADR 055
// seam 4, REQ-121-02/03). The replanner re-derives the plan and returns the FRESH
// plan-derived allow set (task 118 Plan.AllowedResources). This is the structural
// guarantee against self-granting: the reevaluation policy applies EXACTLY this
// allow set on the next attempt — there is no field and no code path that lets the
// previous allow set be unioned with the denied resource. Authorization on a retry
// is always a freshly re-derived plan's set, never previous ∪ denied.
type Reevaluation struct {
	// AllowedResources is the freshly re-derived plan's authorized resource set
	// (Plan.AllowedResources of the NEW plan). The reevaluation policy treats this as
	// the complete, replacement allow set for the next attempt.
	AllowedResources []string
	// StillBlocked reports whether the re-derived plan STILL requires the denied
	// action — i.e. the replan could not route around the denial. When true after the
	// reevaluation bound is exhausted, the policy escalates to a human. When false,
	// the denial was successfully replanned around and no escalation occurs.
	StillBlocked bool
}

// ReplanFunc re-derives a plan in response to a blocked action and returns the new
// plan's allow set (ADR 055 seam 4). It takes the BlockedAction as INPUT describing
// what was denied — never as an instruction to grant it. The orchestrate wiring
// supplies a ReplanFunc that re-runs the planner and returns the new plan's
// Plan.AllowedResources; the denied resource appears in the result ONLY if the new
// plan independently re-derived it (in which case StillBlocked stays true and the
// path leads to human escalation, not a self-grant).
type ReplanFunc func(BlockedAction) (Reevaluation, error)

// ReevaluationPolicy bounds blocked-action reevaluation before human escalation
// (ADR 055 seam 4, REQ-121-02). It mirrors RetryPolicy's bounded-attempts +
// needs-human shape: at most MaxReevaluations replans, then escalate. Replan re-runs
// the planner (re-deriving the plan AND its allow set); only after the bound is
// exhausted does the policy route to an independent human grant.
type ReevaluationPolicy struct {
	// MaxReevaluations is the bound on replan attempts before escalation. It must be
	// non-negative. 0 escalates immediately (no replan), mirroring RetryPolicy's
	// MaxAttempts==0 immediate-escalation semantics.
	MaxReevaluations int
	// Replan re-derives the plan and its allow set for the next attempt.
	Replan ReplanFunc
}

var (
	// ErrNegativeMaxReevaluations means the reevaluation policy was configured below zero.
	ErrNegativeMaxReevaluations = errors.New("loop: negative max reevaluations")
	// ErrNilReplanFunc means the reevaluation policy has no replan seam.
	ErrNilReplanFunc = errors.New("loop: nil replan func")
)

// NewReevaluationPolicy validates and returns a reevaluation policy.
func NewReevaluationPolicy(maxReevaluations int, replan ReplanFunc) (ReevaluationPolicy, error) {
	policy := ReevaluationPolicy{MaxReevaluations: maxReevaluations, Replan: replan}
	if policy.MaxReevaluations < 0 {
		return ReevaluationPolicy{}, ErrNegativeMaxReevaluations
	}
	if policy.Replan == nil {
		return ReevaluationPolicy{}, ErrNilReplanFunc
	}
	return policy, nil
}

// ReevaluationOutcomeKind identifies the result of a bounded reevaluation cycle.
type ReevaluationOutcomeKind string

const (
	// ReevaluationResolved means a replan routed around the blocked action (the
	// re-derived plan no longer needs the denied action). No escalation occurs.
	ReevaluationResolved ReevaluationOutcomeKind = "resolved"
	// ReevaluationEscalated means the bound was exhausted with the action still
	// blocked: the policy escalated to a human (needs-human) carrying the denial.
	ReevaluationEscalated ReevaluationOutcomeKind = "escalated"
)

// Escalation is the payload routed to a human when bounded reevaluation cannot
// route around a blocked action (ADR 055 seam 4, REQ-121-02). It names the denied
// action and reason so a human (NOT the agent) can decide whether to widen
// authorization independently. It carries no allow set and grants nothing.
type Escalation struct {
	// Status is the human-attention marker written for the task — always
	// needs-human for a blocked-action escalation.
	Status tasksource.WritableStatus
	// Blocked is the denied action + reason surfaced to the human.
	Blocked BlockedAction
	// Reevaluations is how many replans were attempted before escalating.
	Reevaluations int
}

// Reason renders the human-facing escalation reason text (REQ-121-02 TC-003: the
// escalation must carry the denied action + reason).
func (e Escalation) Reason() string {
	return fmt.Sprintf(
		"blocked action requires an independent human grant after %d reevaluation(s): %s",
		e.Reevaluations, e.Blocked.String(),
	)
}

// ReevaluationOutcome is the observable result of reevaluating one blocked action.
type ReevaluationOutcome struct {
	Kind ReevaluationOutcomeKind
	// Reevaluations is the number of replans performed (0..MaxReevaluations).
	Reevaluations int
	// AllowedResources is the allow set APPLIED for the most recent attempt — always
	// the freshly re-derived plan's set returned by the replanner, NEVER the previous
	// set unioned with the denied resource. On escalation it is the last re-derived
	// set (the plan that still needed the denied action).
	AllowedResources []string
	// Escalation is populated only when Kind == ReevaluationEscalated.
	Escalation Escalation
	// StatusWrite is the result of the needs-human write (only on escalation).
	StatusWrite tasksource.StatusWriteResult
}

// ReevaluateBlocked routes a blocked action through bounded reevaluation and, if the
// action remains blocked after the bound, to an independent human escalation (ADR
// 055 seam 4, REQ-121-02/03).
//
// The never-self-grant invariant (REQ-121-03) is STRUCTURAL: each attempt's allow
// set is whatever the ReplanFunc returns (a freshly re-derived Plan.AllowedResources)
// and nothing else. ReevaluateBlocked never reads the denied resource into an allow
// set, never unions the previous set with it, and exposes no parameter to do so. A
// replan that routes around the denial (StillBlocked == false) resolves without
// escalation; a replan whose re-derived plan still needs the denied action
// (StillBlocked == true) is attempted again until the bound, then escalates.
//
//   - taskID is the task whose status is written needs-human on escalation.
//   - blocked must name a denied resource/action/reason (ErrEmptyBlockedAction otherwise).
//   - statusWriter must be non-nil (ErrNilStatusWriter otherwise).
func (p ReevaluationPolicy) ReevaluateBlocked(taskID string, blocked BlockedAction, statusWriter StatusWriter) (ReevaluationOutcome, error) {
	if statusWriter == nil {
		return ReevaluationOutcome{}, ErrNilStatusWriter
	}
	if blocked.IsZero() {
		return ReevaluationOutcome{}, ErrEmptyBlockedAction
	}
	if p.Replan == nil {
		return ReevaluationOutcome{}, ErrNilReplanFunc
	}

	var lastAllowed []string
	for reeval := 1; reeval <= p.MaxReevaluations; reeval++ {
		reevaluation, err := p.Replan(blocked)
		if err != nil {
			return ReevaluationOutcome{}, fmt.Errorf("loop: replan after blocked action (reevaluation %d): %w", reeval, err)
		}
		// STRUCTURAL never-self-grant: the applied allow set is EXACTLY the freshly
		// re-derived plan's set. The denied resource is not added here under any
		// condition — only the replanner's own re-derivation can include it.
		lastAllowed = reevaluation.AllowedResources
		if !reevaluation.StillBlocked {
			return ReevaluationOutcome{
				Kind:             ReevaluationResolved,
				Reevaluations:    reeval,
				AllowedResources: lastAllowed,
			}, nil
		}
	}

	// Bound exhausted (or MaxReevaluations == 0): escalate to an independent human
	// grant. The agent never widens its own authorization — a human decides.
	escalation := Escalation{
		Status:        tasksource.WritableStatusNeedsHuman,
		Blocked:       blocked,
		Reevaluations: p.MaxReevaluations,
	}
	result, err := statusWriter.WriteStatus(taskID, tasksource.WritableStatusNeedsHuman)
	if err != nil {
		return ReevaluationOutcome{}, fmt.Errorf("loop: mark blocked task %s needs-human: %w", taskID, err)
	}
	return ReevaluationOutcome{
		Kind:             ReevaluationEscalated,
		Reevaluations:    p.MaxReevaluations,
		AllowedResources: lastAllowed,
		Escalation:       escalation,
		StatusWrite:      result,
	}, nil
}
