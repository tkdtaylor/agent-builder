// Package orchestrator is the Tier-1 layer above the supervisor/runtime worker
// stack (ADR 042, ADR 046). It accepts a goal (a supervisor.Task carried in by
// the inbound channel), decomposes it into a plan via the Planner seam, gates the
// plan on human approval via policy.Decide, and — only on allow/approval —
// dispatches one worker per sub-goal by reusing the existing runtime per-worker
// assembly (runtime.Run). It aggregates per-sub-goal outcomes into a typed
// PlanResult and reports them over the outbound supervisor.Reporter seam.
//
// # The orchestrator authors no code
//
// It is a consumer of the recipe seam; it coordinates workers, it does not become
// one. It MUST NOT directly import internal/executor (REQ-081-05). Its direct
// imports are internal/recipe, internal/runtime, internal/policy, and
// internal/supervisor (for the Task/Reporter/GoalSource seam types). The
// transitive reach into internal/executor via internal/runtime is the
// ADR-042-blessed dispatch path: the orchestrator dispatches a worker that runs
// the executor inside its box; the orchestrator never references the executor
// itself. A fitness check (make fitness-orchestrator-no-executor) and TC-081-05
// assert this as a DIRECT-import invariant.
//
// # Decision references (ADR 046)
//
//   - §1 Planner seam + StructuredPlanner v1 (no LLM, no executor import).
//   - §2 typed PlanResult, rendered to text only at the Reporter boundary.
//   - §3 in-memory plan state behind PlanStore (task 084 swaps the backend).
//   - §4 approval = pause-and-resume over the envelope-verified channel.
//   - §5 dispatch = reuse runtime.Run, one worker per sub-goal, sequential.
package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// SpawnAction is the policy action name the orchestrator issues for the
// plan-spawn gate (ADR 046 §4). It is distinct from the worker's "run-task"
// action so an operator policy can gate plan spawns independently.
const SpawnAction = "spawn-plan"

// SpawnWorkerAction is the per-sub-goal policy action the orchestrator issues in
// dispatchPlan immediately before dispatching each worker (task 085 / ADR 050 §1).
// It is additive to SpawnAction (spawn-plan): spawn-plan gates the whole plan
// once; spawn-worker gates each dispatch so a per-recipe policy can deny one
// worker without denying the plan. A dispatched worker is thus gated twice — the
// orchestrator's spawn-worker plus the worker's own run-task gate inside
// runtime.Run — which is the intended defense-in-depth.
const SpawnWorkerAction = "spawn-worker"

// DefaultRecipeName is the recipe a sub-goal uses when the goal text names no
// recipe (ADR 046 §1: free-form goal → one worker on the default recipe).
const DefaultRecipeName = "coding-agent"

// SubGoal is one unit of a Plan: a named recipe plus the supervisor.Task payload
// that the dispatched worker will execute.
type SubGoal struct {
	RecipeName string
	Task       supervisor.Task
	// TargetRepo is the repository the dispatched worker will act on. It flows into
	// the spawn-worker policy decision (Resource.Properties.target_repo) and into
	// the self-repo bright-line guard (task 085 / ADR 050). Empty means the worker
	// targets no specific repo (the recipe's own default sink applies).
	TargetRepo string
	// Sink is the result-sink target for the dispatched worker (e.g. the publish
	// destination). It flows into the spawn-worker decision
	// (Resource.Properties.sink) and into the self-repo bright-line guard.
	Sink string
}

// Plan is an ordered list of sub-goals produced by a Planner from one goal.
type Plan struct {
	// Goal is the original goal text (the inbound Task.Spec).
	Goal string
	// GoalID is the original goal's Task.ID, used as the plan-state key.
	GoalID   string
	SubGoals []SubGoal
}

// SubGoalOutcome is the typed result of dispatching one sub-goal (ADR 046 §2).
type SubGoalOutcome struct {
	SubGoal string // the sub-goal spec text
	Recipe  string // the recipe used
	Success bool   // whether the worker dispatch succeeded
	Detail  string // branch/PR on success, failure reason on failure (short)
}

// PlanResult is the aggregated typed outcome of a dispatched plan (ADR 046 §2).
// The orchestrator works in this typed shape; rendering to human-readable text
// happens only at the Reporter boundary (RenderPlanResult).
type PlanResult struct {
	Goal     string
	Outcomes []SubGoalOutcome
}

// Planner decomposes a goal into an ordered plan of sub-goals (ADR 046 §1).
// The v1 concrete is StructuredPlanner (rule-based, no LLM, no executor import).
// An LLMPlanner satisfying the same interface is a named follow-on (ADR 046 §6).
type Planner interface {
	Plan(goal supervisor.Task) (Plan, error)
}

// PlanStore holds plan state for goals awaiting approval (ADR 046 §3). The v1
// backend is in-memory; task 084 swaps a durable/memory-guarded backend behind
// this same interface.
type PlanStore interface {
	Put(plan Plan)
	Get(goalID string) (Plan, bool)
	Delete(goalID string)
}

// TamperAwarePlanStore is an optional extension of PlanStore implemented by
// MemoryGuardPlanStore. It adds error-returning variants of Put and Delete so the
// orchestrator can surface write-gate rejections and delete-verify tamper signals
// without changing the base PlanStore interface.
//
//   - TryPut runs validate_write; returns ErrWriteGateDenied on rejection.
//   - TryDelete runs verify_delete; returns ErrTamperDetected on tamper.
//
// MemoryPlanStore does NOT implement this interface; its Put/Delete are always clean.
type TamperAwarePlanStore interface {
	PlanStore
	TryPut(plan Plan) error
	TryDelete(goalID string) error
}

// DispatchFunc is the dispatch seam (ADR 046 §5). It dispatches one worker for
// one sub-goal and returns nil on success or an error describing the failure.
// The default (defaultDispatch) wires to runtime.Run; tests override it with a
// spy so dispatch is asserted without launching real sandboxes.
type DispatchFunc func(ctx context.Context, sub SubGoal, base runtime.Config) error

// PolicyClient is the narrow decide seam the orchestrator depends on. It is
// satisfied by *policy.Client (the production client over the policy-engine
// socket) and by a fake in tests.
type PolicyClient interface {
	Decide(req policy.DecideRequest) (policy.DecideResponse, error)
}

// Approval is an envelope-verified inbound approval message (ADR 046 §4). The
// channel adapter constructs it AFTER VerifyAndOpen succeeds, carrying the
// verified envelope roles so the orchestrator can assert operator→orchestrator
// (task 098 SEC-001 carry-forward) before acting on it.
type Approval struct {
	From   string // verified envelope From role (must be "operator")
	To     string // verified envelope To role (must be "orchestrator")
	GoalID string // the goal whose plan this approves
	// Approved is the operator's decision: true approves, false rejects.
	Approved bool
}

// Expected envelope roles for an inbound approval (ADR 046 §4, task 098 SEC-001).
const (
	approvalFromRole = "operator"
	approvalToRole   = "orchestrator"
)

// Orchestrator is the Tier-1 coordinator. It is constructed with a Planner, a
// PolicyClient, a Reporter, a PlanStore, and a base runtime.Config used to build
// worker dispatch. The dispatch seam defaults to runtime.Run.
type Orchestrator struct {
	planner    Planner
	policy     PolicyClient
	reporter   supervisor.Reporter
	store      PlanStore
	baseConfig runtime.Config
	dispatch   DispatchFunc
	risk       string
	// auditSink is the optional audit.Sink for security events (e.g. tamper-detected)
	// and the orchestrator's fleet-audit events (goal-intake, plan-decided,
	// spawn-decided, completion). When nil, no audit events are emitted (the plan is
	// still halted on tamper; gating/dispatch are unaffected).
	auditSink audit.Sink
	// containment is the L2-assertable containment posture the orchestrator runs
	// under (task 085 / ADR 050 §3): exec-sandbox profile, rootless, read-only
	// rootfs, resource-limited, default-deny egress — the same profile as a worker.
	containment Containment
}

// Option configures an Orchestrator.
type Option func(*Orchestrator)

// WithDispatchFunc overrides the worker dispatch seam (tests inject a spy). When
// unset, the orchestrator wires defaultDispatch (runtime.Run) on the live path.
func WithDispatchFunc(fn DispatchFunc) Option {
	return func(o *Orchestrator) { o.dispatch = fn }
}

// WithPlanStore overrides the plan-state backend. When unset, an in-memory store
// is used (ADR 046 §3).
func WithPlanStore(s PlanStore) Option {
	return func(o *Orchestrator) { o.store = s }
}

// WithRisk sets the context.risk value sent in the spawn-plan decide request.
func WithRisk(risk string) Option {
	return func(o *Orchestrator) { o.risk = risk }
}

// WithPlanner overrides the Planner seam. The Planner is also a required New()
// argument; this option lets callers (and tests) substitute a planner that emits
// sub-goals carrying TargetRepo/Sink, which the rule-based StructuredPlanner does
// not parse from goal text (task 085 / ADR 050). When unset, the New() planner is
// used unchanged.
func WithPlanner(p Planner) Option {
	return func(o *Orchestrator) {
		if p != nil {
			o.planner = p
		}
	}
}

// New constructs an Orchestrator. planner, pol, and reporter are required.
func New(planner Planner, pol PolicyClient, reporter supervisor.Reporter, base runtime.Config, opts ...Option) *Orchestrator {
	o := &Orchestrator{
		planner:     planner,
		policy:      pol,
		reporter:    reporter,
		store:       NewMemoryPlanStore(),
		baseConfig:  base,
		dispatch:    defaultDispatch,
		risk:        "low",
		containment: defaultContainment(),
	}
	for _, opt := range opts {
		opt(o)
	}
	if o.dispatch == nil {
		o.dispatch = defaultDispatch
	}
	if o.store == nil {
		o.store = NewMemoryPlanStore()
	}
	return o
}

// Handle is the goal-intake entry point. It decomposes the goal into a plan,
// gates the plan on the spawn-plan policy decision, and on allow dispatches the
// plan immediately. On require_approval it pauses: reports the plan, holds it in
// the store, and dispatches nothing — Resume continues once approval returns. On
// deny it reports and stops without dispatching.
func (o *Orchestrator) Handle(ctx context.Context, goal supervisor.Task) (PlanResult, error) {
	// Fleet-audit (task 085 / ADR 050 §4): record goal intake on the shared chain.
	o.emitFleetEvent(audit.AuditEvent{Action: audit.ActionGoalIntake, TaskID: goal.ID, RunID: goal.ID})

	plan, err := o.planner.Plan(goal)
	if err != nil {
		return PlanResult{}, fmt.Errorf("orchestrator: plan goal %q: %w", goal.ID, err)
	}
	if len(plan.SubGoals) == 0 {
		return PlanResult{}, fmt.Errorf("orchestrator: empty plan for goal %q", goal.ID)
	}

	decision, err := o.decideSpawn(plan)
	if err != nil {
		return PlanResult{}, err
	}

	// Fleet-audit: record the plan-level spawn-plan decision on the shared chain.
	o.emitFleetEvent(audit.AuditEvent{
		Action: audit.ActionPlanDecided, TaskID: plan.GoalID, RunID: plan.GoalID,
		Detail: audit.EventDetail{PolicyDecision: string(decision)},
	})

	switch decision {
	case policy.DecisionAllow:
		return o.dispatchPlan(ctx, plan)
	case policy.DecisionRequireApproval:
		// Pause-and-resume (ADR 046 §4): hold the plan, report it, dispatch nothing.
		// When the store implements TamperAwarePlanStore (MemoryGuardPlanStore), use
		// TryPut so write-gate rejections surface as errors (REQ-084-01 / ADR 049 §2).
		if ta, ok := o.store.(TamperAwarePlanStore); ok {
			if err := ta.TryPut(plan); err != nil {
				return PlanResult{}, fmt.Errorf("orchestrator: memory-guard write-gate rejected plan for goal %q: %w", plan.GoalID, err)
			}
		} else {
			o.store.Put(plan)
		}
		if err := o.reporter.Report(ctx, renderApprovalRequest(plan)); err != nil {
			return PlanResult{}, fmt.Errorf("orchestrator: report approval request: %w", err)
		}
		return PlanResult{Goal: plan.Goal}, nil
	default:
		// deny and any fail-closed deny.
		if err := o.reporter.Report(ctx, fmt.Sprintf("Plan denied for goal: %s", plan.Goal)); err != nil {
			return PlanResult{}, fmt.Errorf("orchestrator: report denial: %w", err)
		}
		return PlanResult{Goal: plan.Goal}, nil
	}
}

// Resume continues a paused plan when an explicit approval message returns over
// the inbound channel (ADR 046 §4). The approval is untrusted external input: it
// must already be envelope-verified + armor-guarded by the channel adapter, and
// Resume additionally asserts the verified envelope roles (operator→orchestrator)
// before acting — task 098 SEC-001 carry-forward. A role mismatch, an unknown
// goal, or a rejection dispatches nothing.
func (o *Orchestrator) Resume(ctx context.Context, approval Approval) (PlanResult, error) {
	if approval.From != approvalFromRole || approval.To != approvalToRole {
		return PlanResult{}, fmt.Errorf("orchestrator: approval role mismatch: from=%q to=%q (want from=%q to=%q)",
			approval.From, approval.To, approvalFromRole, approvalToRole)
	}

	plan, ok := o.store.Get(approval.GoalID)
	if !ok {
		return PlanResult{}, fmt.Errorf("orchestrator: no pending plan for goal %q", approval.GoalID)
	}

	// Consume the plan from the store regardless of the decision so a stale
	// approval/rejection cannot be replayed against the same goal ID.
	// When the store is TamperAwarePlanStore (MemoryGuardPlanStore), TryDelete
	// runs verify_delete and returns ErrTamperDetected on a tamper signal —
	// we halt the plan and emit a tamper audit event (REQ-084-05 / ADR 049 §4).
	if ta, ok := o.store.(TamperAwarePlanStore); ok {
		if err := ta.TryDelete(approval.GoalID); err != nil {
			o.emitTamperEvent(approval.GoalID)
			return PlanResult{}, fmt.Errorf("orchestrator: tamper detected on delete-verify for goal %q: %w", approval.GoalID, err)
		}
	} else {
		o.store.Delete(approval.GoalID)
	}

	if !approval.Approved {
		if err := o.reporter.Report(ctx, fmt.Sprintf("Plan rejected for goal: %s", plan.Goal)); err != nil {
			return PlanResult{}, fmt.Errorf("orchestrator: report rejection: %w", err)
		}
		return PlanResult{Goal: plan.Goal}, nil
	}

	return o.dispatchPlan(ctx, plan)
}

// HasPendingPlan reports whether a plan for the given goal ID is held awaiting
// approval (used by callers and TC-081-02).
func (o *Orchestrator) HasPendingPlan(goalID string) bool {
	_, ok := o.store.Get(goalID)
	return ok
}

// decideSpawn issues the spawn-plan policy decision for a plan. It is fail-closed:
// it routes on the response Decision, never on the error, so any transport/parse
// error yields DecisionDeny (no dispatch).
func (o *Orchestrator) decideSpawn(plan Plan) (policy.Decision, error) {
	req := policy.DecideRequest{
		Subject:  policy.Subject{Type: "agent", ID: "orchestrator"},
		Action:   policy.Action{Name: SpawnAction},
		Resource: policy.Resource{Type: "plan", ID: plan.GoalID},
		Context:  policy.DecideContext{Risk: o.risk},
	}
	// Fail-closed: ignore the error and route on resp.Decision (policy.Decide
	// returns DecisionDeny on any error).
	resp, _ := o.policy.Decide(req)
	return resp.Decision, nil
}

// dispatchPlan dispatches every sub-goal sequentially (concurrency is task 086),
// aggregates the per-sub-goal outcomes into a typed PlanResult, and reports the
// rendered summary over the Reporter (ADR 046 §2, §5). Each sub-goal is dispatched
// even if a prior one fails — v1 records all outcomes (no early abort).
func (o *Orchestrator) dispatchPlan(ctx context.Context, plan Plan) (PlanResult, error) {
	result := PlanResult{
		Goal:     plan.Goal,
		Outcomes: make([]SubGoalOutcome, 0, len(plan.SubGoals)),
	}

	for _, sub := range plan.SubGoals {
		outcome := SubGoalOutcome{
			SubGoal: sub.Task.Spec,
			Recipe:  sub.RecipeName,
		}

		// Confirm the recipe exists before the spawn-worker gate (ADR 046 §5). An
		// unknown recipe is a failed outcome, not a dispatch.
		if _, err := recipe.SelectRecipe(sub.RecipeName); err != nil {
			outcome.Success = false
			outcome.Detail = fmt.Sprintf("recipe not found: %s", sub.RecipeName)
			result.Outcomes = append(result.Outcomes, outcome)
			continue
		}

		// Per-sub-goal spawn-worker gate (task 085 / ADR 050 §1). This issues a
		// policy decision for THIS dispatch, additive to the plan-level spawn-plan.
		// The self-repo bright line (REQ-085-05a) is checked inside decideSpawnWorker
		// and overrides the policy decision fail-closed. A non-allow decision skips
		// the dispatch, records a denied outcome, and reports the denial.
		decision, denyReason := o.decideSpawnWorker(sub)
		// For deny events, include the deny reason in the audit event (SEC-004: distinguish
		// policy deny from self-repo deny). For allow, record just the recipe name.
		auditReason := sub.RecipeName
		if decision != policy.DecisionAllow {
			auditReason = denyReason
		}
		// SEC-003: deny events must succeed in audit — if the append fails, that is
		// a hard error that halts the plan (not silent). Use emitFleetEventForDeny
		// for security-relevant denials; other events are best-effort.
		if err := o.emitFleetEventForDeny(audit.AuditEvent{
			Action: audit.ActionSpawnDecided, TaskID: sub.Task.ID, RunID: plan.GoalID,
			Detail: audit.EventDetail{PolicyDecision: string(decision), Reason: auditReason},
		}, decision != policy.DecisionAllow); err != nil {
			return result, fmt.Errorf("orchestrator: audit spawn-decided deny: %w", err)
		}
		if decision != policy.DecisionAllow {
			outcome.Success = false
			outcome.Detail = denyReason
			result.Outcomes = append(result.Outcomes, outcome)
			if err := o.reporter.Report(ctx, fmt.Sprintf("Worker spawn denied: recipe %s — %s", sub.RecipeName, denyReason)); err != nil {
				return result, fmt.Errorf("orchestrator: report worker-spawn denial: %w", err)
			}
			continue
		}

		if err := o.dispatch(ctx, sub, o.baseConfig); err != nil {
			outcome.Success = false
			outcome.Detail = err.Error()
		} else {
			outcome.Success = true
			outcome.Detail = "dispatched"
		}
		result.Outcomes = append(result.Outcomes, outcome)
	}

	// Fleet-audit: record plan completion on the shared chain after aggregation.
	o.emitFleetEvent(audit.AuditEvent{Action: audit.ActionCompletion, TaskID: plan.GoalID, RunID: plan.GoalID})

	if err := o.reporter.Report(ctx, RenderPlanResult(result)); err != nil {
		return result, fmt.Errorf("orchestrator: report plan result: %w", err)
	}
	return result, nil
}

// decideSpawnWorker issues the per-sub-goal spawn-worker policy decision (task 085
// / ADR 050 §1) and applies the self-repo bright line (REQ-085-05a) on top of it.
//
// The self-repo guard runs FIRST and is fail-closed: any sub-goal whose target
// repo or result sink is the agent-builder own-repo is DENIED regardless of what
// the policy engine would say — the orchestrator refuses to dispatch a worker
// against its own repo by construction (ADR 042's non-negotiable bright line).
//
// Otherwise it routes on the policy response Decision (fail-closed: any
// transport/parse error yields DecisionDeny, never allow). It returns the decision
// and, when the decision is not allow, a short human-facing reason recorded as the
// sub-goal's denied-outcome Detail and reported via the Reporter.
func (o *Orchestrator) decideSpawnWorker(sub SubGoal) (policy.Decision, string) {
	// Self-repo bright line — independent of the policy file, fail-closed.
	if targetsOwnRepo(sub) {
		return policy.DecisionDeny, fmt.Sprintf("self-repo bright line: refusing to dispatch a worker targeting %s", OwnRepo)
	}

	req := policy.DecideRequest{
		Subject: policy.Subject{Type: "agent", ID: "orchestrator"},
		Action:  policy.Action{Name: SpawnWorkerAction},
		Resource: policy.Resource{
			Type: "recipe",
			ID:   sub.RecipeName,
			Properties: policy.ResourceProperties{
				TargetRepo: sub.TargetRepo,
				Sink:       sub.Sink,
			},
		},
		Context: policy.DecideContext{Risk: o.risk},
	}
	// Fail-closed: ignore the error and route on resp.Decision (policy.Decide
	// returns DecisionDeny on any error).
	resp, _ := o.policy.Decide(req)
	switch resp.Decision {
	case policy.DecisionAllow:
		return policy.DecisionAllow, ""
	case policy.DecisionRequireApproval:
		return policy.DecisionRequireApproval, "policy: worker spawn requires human approval"
	default:
		return policy.DecisionDeny, "policy: worker spawn denied"
	}
}

// emitFleetEvent appends one orchestrator fleet-audit event to the shared
// audit.Sink chain (task 085 / ADR 050 §4). It is best-effort: a nil sink is a
// no-op and an Append error is dropped — the audit chain is parallel durable
// evidence, not the orchestrator's control flow (same convention as the in-loop
// emitAudit projection and emitTamperEvent).
func (o *Orchestrator) emitFleetEvent(ev audit.AuditEvent) {
	if o.auditSink == nil {
		return
	}
	_ = o.auditSink.Append(ev)
}

// emitFleetEventForDeny appends a fleet-audit event with special handling for
// security-relevant deny events (SEC-003). When isDeny is true, the event must
// succeed in the audit chain or an error is returned (the orchestrator halts the
// plan). When isDeny is false, it behaves as emitFleetEvent (best-effort, errors
// dropped). This asymmetry ensures that denials are never silently lost while
// benign events are best-effort.
func (o *Orchestrator) emitFleetEventForDeny(ev audit.AuditEvent, isDeny bool) error {
	if o.auditSink == nil {
		// If policy gating is active but no sink is configured, require a sink for
		// deny events (fail-closed: if we can't audit the deny, don't proceed).
		if isDeny {
			return fmt.Errorf("orchestrator: policy gating enabled but no audit sink configured; cannot audit deny")
		}
		return nil
	}
	if err := o.auditSink.Append(ev); err != nil {
		// For deny events, a failed append is a hard error.
		if isDeny {
			return fmt.Errorf("orchestrator: failed to audit spawn-decided deny: %w", err)
		}
		// For benign events, drop the error (best-effort).
	}
	return nil
}

// defaultDispatch is the live dispatch seam: it reuses the existing runtime
// per-worker assembly (runtime.Run) for one sub-goal (ADR 046 §5). It sets the
// recipe name on a copy of the base config and invokes runtime.Run, which selects
// the recipe, verifies the gate exists, assembles the four IO seams, resolves the
// executor via the registry/router, and dispatches the contained worker. The
// orchestrator never reimplements supervisor assembly (REQ-081-06) and never
// references internal/executor (REQ-081-05) — the executor is reached only
// transitively, inside the worker runtime invokes.
func defaultDispatch(_ context.Context, sub SubGoal, base runtime.Config) error {
	cfg := base
	cfg.RecipeName = sub.RecipeName
	return runtime.Run(cfg, nil)
}

// RenderPlanResult renders a typed PlanResult to a human-readable plain-text
// summary at the Reporter boundary (ADR 046 §2). The typed result stays in the
// orchestrator core; only this edge function produces presentation text.
func RenderPlanResult(result PlanResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n", result.Goal)
	fmt.Fprintf(&b, "%d sub-goal(s):\n", len(result.Outcomes))
	for _, oc := range result.Outcomes {
		marker := "FAIL"
		if oc.Success {
			marker = "OK"
		}
		fmt.Fprintf(&b, "  [%s] %s -> %s", marker, oc.Recipe, oc.Detail)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderApprovalRequest renders the approval-solicitation message (ADR 046 §4).
// It begins with "Approve?" and names each sub-goal's recipe and spec so the
// operator can review the plan before approving.
func renderApprovalRequest(plan Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Approve? Plan for goal: %s\n", plan.Goal)
	fmt.Fprintf(&b, "%d sub-goal(s):\n", len(plan.SubGoals))
	for _, sub := range plan.SubGoals {
		fmt.Fprintf(&b, "  - %s: %s\n", sub.RecipeName, sub.Task.Spec)
	}
	return strings.TrimRight(b.String(), "\n")
}
