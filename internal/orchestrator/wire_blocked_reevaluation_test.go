package orchestrator

import (
	"context"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/loop"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// Task 123: Wire dispatchPlan → ReevaluateBlockedSpawn on live deny path
// (ADR 055 seam 4). These tests close the producer→consumer trace: a blocked
// outcome from dispatchOne drives reevaluation in dispatchPlan, folding the result
// into the returned PlanResult.

// TC-001: a blocked outcome drives ReevaluateBlockedSpawn on the live path
func TestDispatchPlanCallsReevaluateOnBlockedOutcome(t *testing.T) {
	// Plan with one sub-goal that will be denied by the denyingPolicy.
	plan := Plan{
		Goal:   "goal text",
		GoalID: "goal-1",
		SubGoals: []SubGoal{
			{RecipeName: "coding-agent", Task: supervisor.Task{ID: "goal-1-0", Spec: "do work"}},
		},
	}

	writer := &memWriter{}
	o := New(
		fixedPlanner{plan: plan},
		denyingPolicy{},
		nopReporter{},
		runtime.Config{MaxAttempts: 1},
		WithDispatchFunc(dispatchNoop),
		WithAuditSink(audit.NewFakeSink()),
		WithStatusWriter(writer),
		WithReevaluationBound(1),
	)

	result, err := o.dispatchPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("TC-001 dispatchPlan error = %v", err)
	}
	if len(result.Outcomes) != 1 {
		t.Fatalf("TC-001 outcomes length = %d, want 1", len(result.Outcomes))
	}

	outcome := result.Outcomes[0]
	// The blocked outcome should be present.
	if outcome.Blocked == nil {
		t.Fatalf("TC-001 outcome.Blocked = nil, want a typed BlockedAction on the live deny path")
	}
	if outcome.Blocked.Resource != "coding-agent" {
		t.Fatalf("TC-001 Blocked.Resource = %q, want coding-agent", outcome.Blocked.Resource)
	}

	// ReevaluationOutcome should be set (escalated because fixedPlanner still needs it).
	if outcome.ReevaluationOutcome.Kind != loop.ReevaluationEscalated {
		t.Fatalf("TC-001 ReevaluationOutcome.Kind = %q, want %q", outcome.ReevaluationOutcome.Kind, loop.ReevaluationEscalated)
	}

	// The critical assertion: memWriter.writes should contain "goal-1:needs-human" —
	// proving ReevaluateBlockedSpawn was invoked and the dead-code gap is closed.
	if len(writer.writes) != 1 || writer.writes[0] != "goal-1:needs-human" {
		t.Fatalf("TC-001 memWriter.writes = %v, want [goal-1:needs-human]", writer.writes)
	}
}

// TC-002: nil status writer means reevaluation is skipped, not panicked
func TestDispatchPlanNilStatusWriterSkipsReevaluation(t *testing.T) {
	plan := Plan{
		Goal:   "goal text",
		GoalID: "goal-1",
		SubGoals: []SubGoal{
			{RecipeName: "coding-agent", Task: supervisor.Task{ID: "goal-1-0", Spec: "do work"}},
		},
	}

	// No WithStatusWriter — statusWriter is nil.
	o := New(
		fixedPlanner{plan: plan},
		denyingPolicy{},
		nopReporter{},
		runtime.Config{MaxAttempts: 1},
		WithDispatchFunc(dispatchNoop),
		WithAuditSink(audit.NewFakeSink()),
		WithReevaluationBound(1),
	)

	result, err := o.dispatchPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("TC-002 dispatchPlan error = %v", err)
	}

	outcome := result.Outcomes[0]
	// The blocked outcome should still be present.
	if outcome.Blocked == nil {
		t.Fatalf("TC-002 outcome.Blocked = nil, want it to survive nil writer")
	}

	// ReevaluationOutcome should be zero (reevaluation was skipped).
	if outcome.ReevaluationOutcome.Kind != "" {
		t.Fatalf("TC-002 ReevaluationOutcome.Kind = %q, want empty (no reevaluation)", outcome.ReevaluationOutcome.Kind)
	}

	// dispatchPlan should not panic or error with a nil writer.
	if err != nil {
		t.Fatalf("TC-002 dispatchPlan should not error on nil writer; got %v", err)
	}
}

// TC-003: escalated reevaluation outcome folded into PlanResult
func TestDispatchPlanFoldsEscalatedOutcomeIntoPlanResult(t *testing.T) {
	plan := Plan{
		Goal:   "goal text",
		GoalID: "goal-1",
		SubGoals: []SubGoal{
			{RecipeName: "coding-agent", Task: supervisor.Task{ID: "goal-1-0", Spec: "do work"}},
		},
	}

	writer := &memWriter{}
	o := New(
		fixedPlanner{plan: plan}, // Re-derived plan STILL needs coding-agent → escalation.
		denyingPolicy{},
		nopReporter{},
		runtime.Config{MaxAttempts: 1},
		WithDispatchFunc(dispatchNoop),
		WithAuditSink(audit.NewFakeSink()),
		WithStatusWriter(writer),
		WithReevaluationBound(1),
	)

	result, err := o.dispatchPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("TC-003 dispatchPlan error = %v", err)
	}

	outcome := result.Outcomes[0]
	if outcome.ReevaluationOutcome.Kind != loop.ReevaluationEscalated {
		t.Fatalf("TC-003 Kind = %q, want %q", outcome.ReevaluationOutcome.Kind, loop.ReevaluationEscalated)
	}

	// Escalation should carry the blocked action details.
	if outcome.ReevaluationOutcome.Escalation.Blocked.Resource != "coding-agent" {
		t.Fatalf("TC-003 Escalation.Blocked.Resource = %q, want coding-agent", outcome.ReevaluationOutcome.Escalation.Blocked.Resource)
	}

	// Reason text should be present.
	reason := outcome.ReevaluationOutcome.Escalation.Reason()
	if reason == "" {
		t.Fatalf("TC-003 Escalation.Reason() empty, want non-empty deny reason")
	}

	// memWriter should have the needs-human write.
	if len(writer.writes) != 1 || writer.writes[0] != "goal-1:needs-human" {
		t.Fatalf("TC-003 memWriter.writes = %v, want [goal-1:needs-human]", writer.writes)
	}
}

// TC-004: resolved reevaluation outcome folded, no escalation write
func TestDispatchPlanFoldsResolvedOutcomeIntoPlanResult(t *testing.T) {
	// Re-derived plan does NOT need coding-agent (rerouter routes around).
	resolvedPlan := Plan{
		Goal:   "goal text",
		GoalID: "goal-1",
		SubGoals: []SubGoal{
			{RecipeName: "docs-fix", Task: supervisor.Task{ID: "goal-1-0", Spec: "do work"}},
		},
	}

	writer := &memWriter{}
	o := New(
		rerouter{plan: resolvedPlan},
		denyingPolicy{},
		nopReporter{},
		runtime.Config{MaxAttempts: 1},
		WithDispatchFunc(dispatchNoop),
		WithAuditSink(audit.NewFakeSink()),
		WithStatusWriter(writer),
		WithReevaluationBound(1),
	)

	// Dispatch the original plan (still needs coding-agent).
	originalPlan := Plan{
		Goal:   "goal text",
		GoalID: "goal-1",
		SubGoals: []SubGoal{
			{RecipeName: "coding-agent", Task: supervisor.Task{ID: "goal-1-0", Spec: "do work"}},
		},
	}

	result, err := o.dispatchPlan(context.Background(), originalPlan)
	if err != nil {
		t.Fatalf("TC-004 dispatchPlan error = %v", err)
	}

	outcome := result.Outcomes[0]
	if outcome.ReevaluationOutcome.Kind != loop.ReevaluationResolved {
		t.Fatalf("TC-004 Kind = %q, want %q", outcome.ReevaluationOutcome.Kind, loop.ReevaluationResolved)
	}

	// No escalation write on resolved outcome.
	if len(writer.writes) != 0 {
		t.Fatalf("TC-004 memWriter.writes = %v, want empty (no escalation write on resolved)", writer.writes)
	}

	// Never-self-grant: coding-agent should NOT be in AllowedResources.
	if outcome.ReevaluationOutcome.AllowedResources != nil {
		for _, r := range outcome.ReevaluationOutcome.AllowedResources {
			if r == "coding-agent" {
				t.Fatalf("TC-004 AllowedResources contains coding-agent, violates never-self-grant invariant")
			}
		}
	}
}

// TC-005: WithStatusWriter functional option and CLI wiring
func TestWithStatusWriterSetsField(t *testing.T) {
	plan := Plan{
		Goal:   "test",
		GoalID: "test-1",
		SubGoals: []SubGoal{
			{RecipeName: "coding-agent", Task: supervisor.Task{ID: "test-1-0", Spec: "test"}},
		},
	}

	writer := &memWriter{}
	o := New(
		fixedPlanner{plan: plan},
		denyingPolicy{},
		nopReporter{},
		runtime.Config{MaxAttempts: 1},
		WithDispatchFunc(dispatchNoop),
		WithAuditSink(audit.NewFakeSink()),
		WithStatusWriter(writer),
	)

	// Verify the field is set (white-box check since this is package-level test).
	if o.statusWriter != writer {
		t.Fatalf("TC-005 WithStatusWriter did not set o.statusWriter")
	}
}

// TC-005-CLI: assembleOrchestrate constructs a non-nil status writer from baseConfig.TaskRoot.
// This is tested in internal/cli tests (orchestrate_test.go).
