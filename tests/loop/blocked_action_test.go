package loop_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	agentloop "github.com/tkdtaylor/agent-builder/internal/loop"
	"github.com/tkdtaylor/agent-builder/internal/tasksource"
)

// Task 121: blocked-action feedback + bounded reevaluation (ADR 055 seam 4).
//
// TC-001: a denied NECESSARY action surfaces as a typed BlockedAction failure
//         (FailureBlockedAction, NOT FailureGate / FailureExecutorError), with the
//         resource/action + reason populated.
// TC-002: bounded reevaluation — exactly N replans precede escalation (no infinite
//         loop, no immediate give-up).
// TC-003: escalation carries the denied action + reason to a human (needs-human +
//         reason text present).
// TC-004: the agent never self-grants — the allow set applied on a retry is exactly
//         the freshly re-derived Plan.AllowedResources, never previous ∪ denied.

// blockedTaskID is the task whose status is written needs-human on escalation.
const blockedTaskID = "121"

func sampleBlocked() agentloop.BlockedAction {
	return agentloop.BlockedAction{
		Resource: "coding-agent",
		Action:   "spawn-worker",
		Reason:   "policy: worker spawn denied",
	}
}

// ---------------------------------------------------------------------------
// TC-001: a denied necessary action surfaces as a typed BlockedAction failure
// ---------------------------------------------------------------------------

func TestBlockedActionClassifiedAsDistinctFailureKind(t *testing.T) {
	failure, err := agentloop.ClassifyBlockedAction("coding-agent", "spawn-worker", "policy: worker spawn denied")
	if err != nil {
		t.Fatalf("TC-001 ClassifyBlockedAction error = %v, want nil", err)
	}

	// Distinct kind — NOT gate / executor.
	if failure.Reason != agentloop.FailureBlockedAction {
		t.Fatalf("TC-001 Reason = %q, want %q", failure.Reason, agentloop.FailureBlockedAction)
	}
	if failure.Reason == agentloop.FailureGate {
		t.Fatalf("TC-001 blocked action must NOT be classified as FailureGate")
	}
	if failure.Reason == agentloop.FailureExecutorError {
		t.Fatalf("TC-001 blocked action must NOT be classified as FailureExecutorError")
	}
	if failure.Reason == agentloop.FailureExecutorIncomplete {
		t.Fatalf("TC-001 blocked action must NOT be classified as FailureExecutorIncomplete")
	}

	// Resource/action + reason populated.
	if failure.Blocked == nil {
		t.Fatalf("TC-001 Failure.Blocked = nil, want populated")
	}
	if failure.Blocked.Resource != "coding-agent" {
		t.Fatalf("TC-001 Blocked.Resource = %q, want coding-agent", failure.Blocked.Resource)
	}
	if failure.Blocked.Action != "spawn-worker" {
		t.Fatalf("TC-001 Blocked.Action = %q, want spawn-worker", failure.Blocked.Action)
	}
	if failure.Blocked.Reason != "policy: worker spawn denied" {
		t.Fatalf("TC-001 Blocked.Reason = %q, want the deny reason", failure.Blocked.Reason)
	}
}

func TestBlockedActionEmptyDenialIsRejected(t *testing.T) {
	if _, err := agentloop.ClassifyBlockedAction("", "", ""); !errors.Is(err, agentloop.ErrEmptyBlockedAction) {
		t.Fatalf("TC-001 ClassifyBlockedAction(empty) error = %v, want %v", err, agentloop.ErrEmptyBlockedAction)
	}
}

func TestBlockedActionFormatsAsDistinctFeedback(t *testing.T) {
	failure, err := agentloop.ClassifyBlockedAction("coding-agent", "spawn-worker", "policy: worker spawn denied")
	if err != nil {
		t.Fatalf("TC-001 ClassifyBlockedAction error = %v", err)
	}
	formatted := agentloop.FormatFailure(agentloop.Outcome{Kind: agentloop.OutcomeFail, Failure: failure})
	if !strings.Contains(formatted, "denied by policy") {
		t.Fatalf("TC-001 FormatFailure = %q, want it to name the policy denial", formatted)
	}
	if !strings.Contains(formatted, "coding-agent") || !strings.Contains(formatted, "spawn-worker") {
		t.Fatalf("TC-001 FormatFailure = %q, want it to name resource+action", formatted)
	}
}

// ---------------------------------------------------------------------------
// TC-002: bounded reevaluation before escalation (exactly N replans)
// ---------------------------------------------------------------------------

func TestReevaluationPerformsExactlyNReplansBeforeEscalation(t *testing.T) {
	for _, n := range []int{1, 3} {
		t.Run("N="+itoa(n), func(t *testing.T) {
			writer := newRecordingStatusWriter()
			replans := 0
			// A replanner that ALWAYS returns a plan still needing the denied resource
			// (StillBlocked: true) — the goal genuinely requires the denied action.
			replan := func(b agentloop.BlockedAction) (agentloop.Reevaluation, error) {
				replans++
				return agentloop.Reevaluation{
					AllowedResources: []string{"goal-1", "coding-agent", "goal-1-0"},
					StillBlocked:     true,
				}, nil
			}
			policy, err := agentloop.NewReevaluationPolicy(n, replan)
			if err != nil {
				t.Fatalf("TC-002 NewReevaluationPolicy error = %v", err)
			}

			outcome, err := policy.ReevaluateBlocked(blockedTaskID, sampleBlocked(), writer)
			if err != nil {
				t.Fatalf("TC-002 ReevaluateBlocked error = %v", err)
			}

			// Exactly N replans precede escalation — no infinite loop, no immediate give-up.
			if replans != n {
				t.Fatalf("TC-002 replans = %d, want exactly N=%d", replans, n)
			}
			if outcome.Kind != agentloop.ReevaluationEscalated {
				t.Fatalf("TC-002 Kind = %q, want %q", outcome.Kind, agentloop.ReevaluationEscalated)
			}
			if outcome.Reevaluations != n {
				t.Fatalf("TC-002 Reevaluations = %d, want %d", outcome.Reevaluations, n)
			}
			assertSingleNeedsHumanWrite(t, "TC-002", writer, blockedTaskID)
		})
	}
}

func TestReevaluationResolvesWhenReplanRoutesAround(t *testing.T) {
	writer := newRecordingStatusWriter()
	replans := 0
	// The FIRST replan routes around the denial (StillBlocked: false) — no escalation.
	replan := func(b agentloop.BlockedAction) (agentloop.Reevaluation, error) {
		replans++
		return agentloop.Reevaluation{
			AllowedResources: []string{"goal-1", "docs-fix", "goal-1-0"},
			StillBlocked:     false,
		}, nil
	}
	policy, err := agentloop.NewReevaluationPolicy(3, replan)
	if err != nil {
		t.Fatalf("TC-002 NewReevaluationPolicy error = %v", err)
	}

	outcome, err := policy.ReevaluateBlocked(blockedTaskID, sampleBlocked(), writer)
	if err != nil {
		t.Fatalf("TC-002 ReevaluateBlocked error = %v", err)
	}

	if outcome.Kind != agentloop.ReevaluationResolved {
		t.Fatalf("TC-002 Kind = %q, want %q (replan routed around)", outcome.Kind, agentloop.ReevaluationResolved)
	}
	if replans != 1 {
		t.Fatalf("TC-002 replans = %d, want 1 (resolved on first replan)", replans)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("TC-002 status writes = %d, want 0 (no escalation on resolve)", len(writer.writes))
	}
}

func TestReevaluationZeroBoundEscalatesImmediately(t *testing.T) {
	writer := newRecordingStatusWriter()
	replans := 0
	replan := func(b agentloop.BlockedAction) (agentloop.Reevaluation, error) {
		replans++
		return agentloop.Reevaluation{StillBlocked: true}, nil
	}
	policy, err := agentloop.NewReevaluationPolicy(0, replan)
	if err != nil {
		t.Fatalf("TC-002 NewReevaluationPolicy(0) error = %v", err)
	}

	outcome, err := policy.ReevaluateBlocked(blockedTaskID, sampleBlocked(), writer)
	if err != nil {
		t.Fatalf("TC-002 ReevaluateBlocked error = %v", err)
	}
	if replans != 0 {
		t.Fatalf("TC-002 replans = %d, want 0 (immediate escalation, no replan)", replans)
	}
	if outcome.Kind != agentloop.ReevaluationEscalated {
		t.Fatalf("TC-002 Kind = %q, want %q", outcome.Kind, agentloop.ReevaluationEscalated)
	}
	assertSingleNeedsHumanWrite(t, "TC-002", writer, blockedTaskID)
}

func TestReevaluationNegativeBoundIsExplicitError(t *testing.T) {
	replan := func(b agentloop.BlockedAction) (agentloop.Reevaluation, error) {
		return agentloop.Reevaluation{}, nil
	}
	if _, err := agentloop.NewReevaluationPolicy(-1, replan); !errors.Is(err, agentloop.ErrNegativeMaxReevaluations) {
		t.Fatalf("TC-002 NewReevaluationPolicy(-1) error = %v, want %v", err, agentloop.ErrNegativeMaxReevaluations)
	}
}

// ---------------------------------------------------------------------------
// TC-003: escalation carries the denied action + reason to a human
// ---------------------------------------------------------------------------

func TestEscalationCarriesDeniedActionAndReason(t *testing.T) {
	writer := newRecordingStatusWriter()
	replan := func(b agentloop.BlockedAction) (agentloop.Reevaluation, error) {
		return agentloop.Reevaluation{AllowedResources: []string{"goal-1"}, StillBlocked: true}, nil
	}
	policy, err := agentloop.NewReevaluationPolicy(2, replan)
	if err != nil {
		t.Fatalf("TC-003 NewReevaluationPolicy error = %v", err)
	}

	blocked := sampleBlocked()
	outcome, err := policy.ReevaluateBlocked(blockedTaskID, blocked, writer)
	if err != nil {
		t.Fatalf("TC-003 ReevaluateBlocked error = %v", err)
	}

	if outcome.Kind != agentloop.ReevaluationEscalated {
		t.Fatalf("TC-003 Kind = %q, want %q", outcome.Kind, agentloop.ReevaluationEscalated)
	}
	// needs-human status written.
	if outcome.Escalation.Status != tasksource.WritableStatusNeedsHuman {
		t.Fatalf("TC-003 Escalation.Status = %q, want %q", outcome.Escalation.Status, tasksource.WritableStatusNeedsHuman)
	}
	if outcome.StatusWrite.Path == "" {
		t.Fatalf("TC-003 StatusWrite.Path is empty, want the needs-human task path")
	}
	// The escalation payload names the denied action AND reason.
	if outcome.Escalation.Blocked != blocked {
		t.Fatalf("TC-003 Escalation.Blocked = %+v, want %+v", outcome.Escalation.Blocked, blocked)
	}
	reason := outcome.Escalation.Reason()
	if !strings.Contains(reason, blocked.Resource) {
		t.Fatalf("TC-003 escalation reason %q does not name the denied resource %q", reason, blocked.Resource)
	}
	if !strings.Contains(reason, blocked.Action) {
		t.Fatalf("TC-003 escalation reason %q does not name the denied action %q", reason, blocked.Action)
	}
	if !strings.Contains(reason, blocked.Reason) {
		t.Fatalf("TC-003 escalation reason %q does not carry the deny reason %q", reason, blocked.Reason)
	}
	assertSingleNeedsHumanWrite(t, "TC-003", writer, blockedTaskID)
}

// ---------------------------------------------------------------------------
// TC-004: the agent never self-grants
// ---------------------------------------------------------------------------

// TestReevaluationNeverUnionsDeniedResourceIntoAllowSet asserts the structural
// never-self-grant invariant: the allow set APPLIED on each retry is EXACTLY the
// freshly re-derived Plan.AllowedResources returned by the replanner — never the
// previous set ∪ the denied resource. The replanner returns an allow set that does
// NOT contain the denied resource; the policy must surface that exact set and must
// not add the denied resource back.
func TestReevaluationNeverUnionsDeniedResourceIntoAllowSet(t *testing.T) {
	writer := newRecordingStatusWriter()
	blocked := sampleBlocked() // denied resource = "coding-agent"

	// The re-derived plan routes AROUND the denial: its allow set excludes the denied
	// resource entirely (a different recipe). This is the only legitimate way a denial
	// gets resolved — by a new plan that does not need the denied action.
	rederived := []string{"goal-1", "docs-fix", "goal-1-0"}
	replan := func(b agentloop.BlockedAction) (agentloop.Reevaluation, error) {
		// Return a COPY so the test's golden slice cannot be mutated by the policy.
		set := append([]string(nil), rederived...)
		// Whether the re-derived plan still needs the denied resource is read from the
		// plan's OWN derived set — here it does not.
		stillBlocked := contains(set, b.Resource)
		return agentloop.Reevaluation{AllowedResources: set, StillBlocked: stillBlocked}, nil
	}
	policy, err := agentloop.NewReevaluationPolicy(2, replan)
	if err != nil {
		t.Fatalf("TC-004 NewReevaluationPolicy error = %v", err)
	}

	outcome, err := policy.ReevaluateBlocked(blockedTaskID, blocked, writer)
	if err != nil {
		t.Fatalf("TC-004 ReevaluateBlocked error = %v", err)
	}
	// The applied allow set must equal the re-derived set EXACTLY.
	if !reflect.DeepEqual(outcome.AllowedResources, rederived) {
		t.Fatalf("TC-004 applied allow set = %v, want exactly the re-derived set %v", outcome.AllowedResources, rederived)
	}
	// The denied resource must NOT have been unioned back into the applied set.
	if contains(outcome.AllowedResources, blocked.Resource) {
		t.Fatalf("TC-004 SELF-GRANT DETECTED: denied resource %q appears in the applied allow set %v",
			blocked.Resource, outcome.AllowedResources)
	}
	// Because the re-derived plan does not need the denied resource, the cycle resolves
	// — it does NOT escalate, and it certainly does not grant.
	if outcome.Kind != agentloop.ReevaluationResolved {
		t.Fatalf("TC-004 Kind = %q, want %q (replan routed around without granting)", outcome.Kind, agentloop.ReevaluationResolved)
	}
}

// TestReevaluationEscalatesRatherThanGrantsWhenStillNeeded asserts the OTHER half of
// the never-self-grant invariant: when the re-derived plan STILL needs the denied
// action, the only outcome is human escalation — the policy never widens
// authorization itself. The applied allow set on escalation is the last re-derived
// plan's set; the resolution is needs-human, not a grant of the denied resource.
func TestReevaluationEscalatesRatherThanGrantsWhenStillNeeded(t *testing.T) {
	writer := newRecordingStatusWriter()
	blocked := sampleBlocked() // denied resource = "coding-agent"

	// Every replan re-derives a plan that STILL needs the denied resource. There is no
	// path in which the policy itself grants it; the only escape is a human.
	rederived := []string{"goal-1", "coding-agent", "goal-1-0"}
	replan := func(b agentloop.BlockedAction) (agentloop.Reevaluation, error) {
		set := append([]string(nil), rederived...)
		return agentloop.Reevaluation{AllowedResources: set, StillBlocked: contains(set, b.Resource)}, nil
	}
	policy, err := agentloop.NewReevaluationPolicy(2, replan)
	if err != nil {
		t.Fatalf("TC-004 NewReevaluationPolicy error = %v", err)
	}

	outcome, err := policy.ReevaluateBlocked(blockedTaskID, blocked, writer)
	if err != nil {
		t.Fatalf("TC-004 ReevaluateBlocked error = %v", err)
	}

	// The applied allow set is the re-derived plan's set — NOT previous ∪ denied. Here
	// the denied resource IS present, but ONLY because the new plan independently
	// re-derived it (a legitimate plan need), and that path leads to escalation, never
	// to the policy granting it on its own authority.
	if !reflect.DeepEqual(outcome.AllowedResources, rederived) {
		t.Fatalf("TC-004 applied allow set = %v, want exactly the re-derived set %v", outcome.AllowedResources, rederived)
	}
	if outcome.Kind != agentloop.ReevaluationEscalated {
		t.Fatalf("TC-004 Kind = %q, want %q (still-needed action escalates, never self-grants)", outcome.Kind, agentloop.ReevaluationEscalated)
	}
	// The escape is a human grant (needs-human), not the policy widening authorization.
	if outcome.Escalation.Status != tasksource.WritableStatusNeedsHuman {
		t.Fatalf("TC-004 escalation status = %q, want %q (independent human grant)", outcome.Escalation.Status, tasksource.WritableStatusNeedsHuman)
	}
	assertSingleNeedsHumanWrite(t, "TC-004", writer, blockedTaskID)
}

func contains(set []string, id string) bool {
	for _, s := range set {
		if s == id {
			return true
		}
	}
	return false
}
