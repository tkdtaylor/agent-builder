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
//   - §5 dispatch = reuse runtime.Run, one worker per sub-goal. Task 086 makes the
//     dispatch CONCURRENT (ADR 042: multiple workers from the start) — one goroutine
//     per sub-goal, joined by a WaitGroup, outcomes written into a pre-sized slice at
//     the sub-goal index, the shared audit chain and PlanStore serialized. A worker
//     failure does not halt the others (best-effort). See dispatchPlan.
package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/loop"
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

// AllowedResources returns the deduped, deterministically-ordered set of policy
// resource IDs this plan authorizes (ADR 055 seam 1, plan-derived authorization).
// It is the union of the plan's own decision-resource IDs: the goal ID (the
// spawn-plan resource), each sub-goal's recipe name (the spawn-worker resource),
// and each sub-goal's task ID (the worker's run-task resource). The orchestrator
// scopes its spawn decisions to this set so a goal can only authorize the actions
// its own plan declares — least-privilege, constructed from the plan. Ordering is
// deterministic: goal ID first, then recipe names and task IDs in sub-goal order,
// each on first occurrence.
func (p Plan) AllowedResources() []string {
	seen := make(map[string]struct{})
	ordered := make([]string, 0, 1+2*len(p.SubGoals))
	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ordered = append(ordered, id)
	}
	add(p.GoalID)
	for _, sub := range p.SubGoals {
		add(sub.RecipeName)
	}
	for _, sub := range p.SubGoals {
		add(sub.Task.ID)
	}
	return ordered
}

// authorizesResource reports whether id is in the plan's derived allow set.
func (p Plan) authorizesResource(id string) bool {
	for _, r := range p.AllowedResources() {
		if r == id {
			return true
		}
	}
	return false
}

// SubGoalOutcome is the typed result of dispatching one sub-goal (ADR 046 §2).
type SubGoalOutcome struct {
	SubGoal string // the sub-goal spec text
	Recipe  string // the recipe used
	Success bool   // whether the worker dispatch succeeded
	Detail  string // branch/PR on success, failure reason on failure (short)
	// Blocked is non-nil when this sub-goal failed because a NECESSARY action was
	// denied by policy (ADR 055 seam 4, task 121) — a blocked action, distinct from a
	// dispatch error or a gate failure. It carries the denied resource/action + reason
	// so the orchestrate feedback path can route it to bounded reevaluation and then
	// to an independent human escalation. nil for success and for non-policy failures.
	Blocked *loop.BlockedAction
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

// PlanScoper is an OPTIONAL extension of PolicyClient (ADR 055 seam 1, task 122).
// A PolicyClient that owns the policy daemon's lifecycle implements it so the
// orchestrator can hand it the admitted plan BEFORE issuing that plan's decisions.
// The implementation configures the deployment policy engine with the plan-derived
// allow set (Plan.AllowedResources, intersected with an optional deployment base)
// so the independent engine ALLOWS exactly the resources this plan declared — the
// daemon side of plan-derived authorization.
//
// Fail-closed: ConfigureForPlan returns a non-nil error if the engine cannot be
// configured for the plan (e.g. daemon start failure); the orchestrator treats that
// as a planning failure and dispatches nothing. A PolicyClient that does NOT own a
// daemon (the always-deny fallback, test fakes) need not implement this — the
// orchestrator skips the hook and the existing fail-closed decisions stand.
type PlanScoper interface {
	ConfigureForPlan(plan Plan) error
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
	// registry is the live status registry projection (ADR 054 §3, task 112). The
	// goal actor transitions its goalID's state at each lifecycle edge; sub-goal
	// progress is written from inside dispatchPlan's per-sub-goal goroutines. It is
	// a PROJECTION ONLY — a nil registry is a no-op and a registry write NEVER gates
	// control flow (the PlanStore is the source of truth). Set via WithStatusRegistry.
	registry *StatusRegistry
	// workerSem is the fleet-wide worker semaphore (ADR 054 §1, task 112). When
	// non-nil, each per-sub-goal goroutine in dispatchPlan acquires one permit before
	// o.dispatch and releases it (deferred) after, so the TOTAL live workers across
	// all concurrent goals never exceeds the bound. nil disables the cap (the
	// pre-112 unbounded behaviour, used by single-goal unit tests). Set via
	// WithWorkerSemaphore.
	workerSem *Semaphore
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

// WithStatusRegistry sets the live status registry the orchestrator projects
// lifecycle transitions into (ADR 054 §3). When unset the registry is nil and all
// projection writes are no-ops — the goal still completes (the registry never
// gates control flow). The control loop assembles ONE registry shared across all
// goal actors.
func WithStatusRegistry(r *StatusRegistry) Option {
	return func(o *Orchestrator) { o.registry = r }
}

// WithWorkerSemaphore sets the fleet-wide worker semaphore acquired inside
// dispatchPlan's per-sub-goal goroutine (ADR 054 §1). When unset (nil) dispatch is
// unbounded — the pre-112 behaviour single-goal tests rely on. The control loop
// assembles ONE semaphore sized to AGENT_BUILDER_MAX_WORKERS and shares it across
// all goal actors so the bound is fleet-wide, not per-goal.
func WithWorkerSemaphore(s *Semaphore) Option {
	return func(o *Orchestrator) { o.workerSem = s }
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

	// Registry projection (ADR 054 §3): the actor owns its goalID's state. Intake →
	// Planning. A nil registry is a no-op; this never gates control flow.
	o.registry.SetState(goal.ID, StatePlanning)

	plan, err := o.planner.Plan(goal)
	if err != nil {
		o.registry.SetState(goal.ID, StateFailed)
		return PlanResult{}, fmt.Errorf("orchestrator: plan goal %q: %w", goal.ID, err)
	}
	if len(plan.SubGoals) == 0 {
		o.registry.SetState(goal.ID, StateFailed)
		return PlanResult{}, fmt.Errorf("orchestrator: empty plan for goal %q", goal.ID)
	}

	// Plan-derived authorization, daemon side (ADR 055 seam 1, task 122): when the
	// PolicyClient owns the policy daemon's lifecycle (PlanScoper), configure the
	// deployment engine with THIS plan's derived allow set BEFORE issuing any of its
	// decisions, so the independent engine permits exactly the resources the plan
	// declared. Fail-closed: a configuration error fails the goal (dispatch nothing).
	if scoper, ok := o.policy.(PlanScoper); ok {
		if err := scoper.ConfigureForPlan(plan); err != nil {
			o.registry.SetState(goal.ID, StateFailed)
			return PlanResult{}, fmt.Errorf("orchestrator: configure policy for plan %q: %w", plan.GoalID, err)
		}
	}

	decision, err := o.decideSpawn(plan)
	if err != nil {
		o.registry.SetState(goal.ID, StateFailed)
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
				o.registry.SetState(plan.GoalID, StateFailed)
				return PlanResult{}, fmt.Errorf("orchestrator: memory-guard write-gate rejected plan for goal %q: %w", plan.GoalID, err)
			}
		} else {
			o.store.Put(plan)
		}
		// Registry projection: the plan is paused awaiting approval (ADR 054 §3). The
		// Resume / ResumeWithFold path (task 115) continues from here.
		o.registry.SetState(plan.GoalID, StateAwaitingApproval)
		// Surface any info already queued for this goal WITH the solicitation (ADR 054
		// §4) — an info that arrived before the goal reached the pause is folded into
		// the operator's view at the checkpoint. Info that arrives AFTER this pause is
		// surfaced by the actor calling SolicitApproval again.
		info := o.registry.PendingInfo(plan.GoalID)
		if err := o.reporter.Report(ctx, renderApprovalRequestWithInfo(plan, info)); err != nil {
			return PlanResult{}, fmt.Errorf("orchestrator: report approval request: %w", err)
		}
		return PlanResult{Goal: plan.Goal}, nil
	default:
		// deny and any fail-closed deny.
		o.registry.SetState(plan.GoalID, StateFailed)
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

// ResumeWithFold continues a paused plan on approval, FOLDING any queued info for
// the goal into the goal text and RE-PLANNING before dispatch (ADR 054 §4, task
// 115 — the AwaitingApproval-checkpoint fold).
//
// It is the checkpoint-augment variant of Resume: the actor calls it instead of
// Resume when the goal has pending info at the approval checkpoint. The original
// goal is supplied by the caller (the goal actor holds it) so the augmented goal
// text carries the original Spec AND Repo — preserving the self-repo bright line
// across the re-plan.
//
//   - On a role mismatch / unknown goal / rejection it behaves exactly like Resume
//     (dispatch nothing) — and on rejection it still DRAINS the queue so a rejected
//     plan does not leave stale info to re-apply.
//   - On approve: it drains the pending-info queue, augments the goal Spec with the
//     drained info, re-runs planner.Plan on the augmented goal, REPLACES the stored
//     plan with the re-planned one, and dispatches the re-planned plan. The queue is
//     drained (empty) after this so a subsequent checkpoint does not double-apply.
//
// When the queue is empty this is equivalent to Resume (re-plan on the unchanged
// goal text), so the actor may always route an approved goal through ResumeWithFold.
func (o *Orchestrator) ResumeWithFold(ctx context.Context, approval Approval, goal supervisor.Task) (PlanResult, error) {
	if approval.From != approvalFromRole || approval.To != approvalToRole {
		return PlanResult{}, fmt.Errorf("orchestrator: approval role mismatch: from=%q to=%q (want from=%q to=%q)",
			approval.From, approval.To, approvalFromRole, approvalToRole)
	}

	stored, ok := o.store.Get(approval.GoalID)
	if !ok {
		return PlanResult{}, fmt.Errorf("orchestrator: no pending plan for goal %q", approval.GoalID)
	}

	// Consume the stored plan regardless of decision (a stale approval cannot be
	// replayed against the same goal ID) — same tamper-aware delete discipline as
	// Resume.
	if ta, ok := o.store.(TamperAwarePlanStore); ok {
		if err := ta.TryDelete(approval.GoalID); err != nil {
			o.emitTamperEvent(approval.GoalID)
			return PlanResult{}, fmt.Errorf("orchestrator: tamper detected on delete-verify for goal %q: %w", approval.GoalID, err)
		}
	} else {
		o.store.Delete(approval.GoalID)
	}

	// Drain the pending-info queue at this checkpoint. Draining on BOTH the approve
	// and reject paths prevents stale info from re-applying to a later goal/plan.
	info := o.registry.DrainInfo(approval.GoalID)

	if !approval.Approved {
		if err := o.reporter.Report(ctx, fmt.Sprintf("Plan rejected for goal: %s", stored.Goal)); err != nil {
			return PlanResult{}, fmt.Errorf("orchestrator: report rejection: %w", err)
		}
		return PlanResult{Goal: stored.Goal}, nil
	}

	// Re-plan boundary (a checkpoint): fold the drained info into the goal text and
	// re-run the planner. The augmented goal keeps the original ID and Repo so the
	// re-planned sub-goals carry Task.Repo into the self-repo bright line unchanged.
	augmented := goal
	augmented.Spec = FoldGoalText(goal.Spec, info)

	// Registry projection: back to Planning for the re-plan, then dispatchPlan moves
	// it to Dispatching. A nil registry is a no-op.
	o.registry.SetState(approval.GoalID, StatePlanning)

	plan, err := o.planner.Plan(augmented)
	if err != nil {
		o.registry.SetState(approval.GoalID, StateFailed)
		return PlanResult{}, fmt.Errorf("orchestrator: re-plan goal %q: %w", approval.GoalID, err)
	}
	if len(plan.SubGoals) == 0 {
		o.registry.SetState(approval.GoalID, StateFailed)
		return PlanResult{}, fmt.Errorf("orchestrator: empty re-plan for goal %q", approval.GoalID)
	}

	// Replace the stored plan with the re-planned one BEFORE dispatch. The store was
	// already cleared above; Put writes the new plan so HasPendingPlan reflects the
	// re-planned state if anything inspects it mid-dispatch.
	o.store.Put(plan)

	return o.dispatchPlan(ctx, plan)
}

// FoldGoalText augments a goal's text with queued info (ADR 054 §4, task 115). The
// original text is preserved verbatim and each info line is appended under an
// "Additional information:" header so the re-planned goal carries BOTH the original
// intent and the operator's new info. Empty info returns the original text
// unchanged (a no-op fold), so an approved goal with no pending info re-plans on the
// same text.
func FoldGoalText(original string, info []string) string {
	if len(info) == 0 {
		return original
	}
	var b strings.Builder
	b.WriteString(original)
	b.WriteString("\n\nAdditional information:")
	for _, line := range info {
		b.WriteString("\n- ")
		b.WriteString(line)
	}
	return b.String()
}

// SolicitApproval re-renders and re-sends the approval solicitation for a goal
// currently held in the PlanStore, INCLUDING any info queued in the registry's
// pending-info queue (ADR 054 §4, task 115 — TC-115-02). The goal actor calls this
// when an `info` message arrives while the goal is AwaitingApproval, so the
// operator sees the amended context before approving. It reads the queue WITHOUT
// draining it (the drain happens at the ResumeWithFold approve checkpoint), so the
// info is still available to fold on approve. It is a no-op error if no plan is
// pending for the goal.
func (o *Orchestrator) SolicitApproval(ctx context.Context, goalID string) error {
	plan, ok := o.store.Get(goalID)
	if !ok {
		return fmt.Errorf("orchestrator: no pending plan for goal %q", goalID)
	}
	info := o.registry.PendingInfo(goalID)
	return o.reporter.Report(ctx, renderApprovalRequestWithInfo(plan, info))
}

// HasPendingPlan reports whether a plan for the given goal ID is held awaiting
// approval (used by callers and TC-081-02).
func (o *Orchestrator) HasPendingPlan(goalID string) bool {
	_, ok := o.store.Get(goalID)
	return ok
}

// ConsumePlanOnCancel removes a goal's plan from the PlanStore on cancellation
// (ADR 054 §5 / §6 race (d), task 116). It uses the SAME delete path Resume uses
// (TamperAwarePlanStore.TryDelete when available, else PlanStore.Delete), so a
// cancel racing a Resume-approve consumes the plan exactly once — whichever wins the
// delete dispatches/tears down; the loser finds no plan and does NOT double-dispatch.
// It returns whether a plan was present (false when the goal had already dispatched
// or no plan was held) and any tamper error from the delete-verify gate (surfaced by
// the caller; a tamper signal also emits a tamper audit event, mirroring Resume).
func (o *Orchestrator) ConsumePlanOnCancel(goalID string) (had bool, err error) {
	if _, ok := o.store.Get(goalID); !ok {
		return false, nil
	}
	if ta, ok := o.store.(TamperAwarePlanStore); ok {
		if delErr := ta.TryDelete(goalID); delErr != nil {
			o.emitTamperEvent(goalID)
			return true, fmt.Errorf("orchestrator: tamper detected on cancel delete-verify for goal %q: %w", goalID, delErr)
		}
		return true, nil
	}
	o.store.Delete(goalID)
	return true, nil
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

// dispatchPlan dispatches every approved sub-goal CONCURRENTLY (task 086 / ADR 042:
// multiple workers from the start), aggregates the per-sub-goal outcomes into a
// typed PlanResult, and reports the rendered summary over the Reporter (ADR 046 §2,
// §5). Concurrency model:
//
//   - One goroutine per sub-goal, joined by a sync.WaitGroup. All goroutines start
//     before any completes (REQ-086-01) — there is no per-sub-goal sequencing.
//   - Outcomes are written into a PRE-SIZED slice at the sub-goal index (never via
//     append from a goroutine), so the aggregate is race-free AND deterministically
//     ordered = sub-goal order (TC-085-02 / TC-086-03 assert ordered outcomes).
//   - A worker's dispatch error is captured into ITS OWN outcome slot; goroutines
//     never return early and never cancel siblings → best-effort completion
//     (REQ-086-02). One worker failing does not halt the others.
//   - The shared audit.Sink and PlanStore are the only cross-goroutine mutable state;
//     both serialize their writes (audit sinks are mutex-guarded — task 086 — and the
//     hash chain is appended serially; MemoryPlanStore is mutex-guarded). The
//     envelope.ReplayCache shared across workers is likewise mutex-guarded (083
//     SEC-001 carry-forward: ONE long-lived cache per direction, not per worker).
//
// The SEC-003 invariant (task 085) is preserved: a failed audit-append for a
// security-relevant DENY event is a hard error that halts the plan. Such errors are
// collected from the goroutines and, if any occurred, returned after the join so the
// plan does not silently proceed on an un-audited deny.
func (o *Orchestrator) dispatchPlan(ctx context.Context, plan Plan) (PlanResult, error) {
	n := len(plan.SubGoals)
	result := PlanResult{
		Goal:     plan.Goal,
		Outcomes: make([]SubGoalOutcome, n),
	}
	// Registry projection (ADR 054 §3): the plan was allowed → Dispatching. A nil
	// registry is a no-op; this never gates control flow.
	o.registry.SetState(plan.GoalID, StateDispatching)
	// denyAuditErrs[i] holds a non-nil error only if sub-goal i's deny-event audit
	// append failed (SEC-003). Each goroutine writes its own index → no shared-write
	// race; we scan it after the join.
	denyAuditErrs := make([]error, n)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := range plan.SubGoals {
		go func(i int, sub SubGoal) {
			defer wg.Done()
			outcome, auditErr := o.dispatchOne(ctx, plan, sub)
			result.Outcomes[i] = outcome
			denyAuditErrs[i] = auditErr
		}(i, plan.SubGoals[i])
	}
	wg.Wait()

	// SEC-003: if any worker's deny-event audit append failed, halt the plan with the
	// first such error (the deny was not durably recorded). Outcomes are still
	// returned so the caller sees what ran before the halt.
	for _, err := range denyAuditErrs {
		if err != nil {
			o.registry.SetState(plan.GoalID, StateFailed)
			return result, fmt.Errorf("orchestrator: audit spawn-decided deny: %w", err)
		}
	}

	// Fleet-audit: record plan completion on the shared chain after aggregation. This
	// runs after the join, so completion is the LAST orchestrator event on the chain
	// (TC-085-03 / TC-086-05 assert completion is last).
	o.emitFleetEvent(audit.AuditEvent{Action: audit.ActionCompletion, TaskID: plan.GoalID, RunID: plan.GoalID})

	// Registry projection: terminal Done — UNLESS this dispatch was cancelled (ADR 054
	// §5, task 116). When the per-goal ctx was cancelled, the workers were torn down via
	// the run-loop ctx.Done() arm and the cancel handler owns the terminal state
	// (Cancelled); overwriting it with Done here would erase the cancellation from the
	// operator's view. ctx.Err() != nil is the cancel signal — leave the terminal state
	// to the cancel handler. (Per-sub-goal failures on a non-cancelled run are
	// best-effort and recorded in SubGoals[i]; an aggregate that reached this point is a
	// completed dispatch, not a goal-level failure — only a hard halt above is Failed.)
	if ctx.Err() == nil {
		o.registry.SetState(plan.GoalID, StateDone)
	}

	if err := o.reporter.Report(ctx, RenderPlanResult(result)); err != nil {
		return result, fmt.Errorf("orchestrator: report plan result: %w", err)
	}
	return result, nil
}

// dispatchOne runs the full per-sub-goal pipeline for one worker: recipe existence
// check → spawn-worker policy/self-repo gate + audit → dispatch. It returns the
// typed outcome and, separately, a non-nil error ONLY when a security-relevant
// deny-event audit append failed (SEC-003), which the caller treats as a plan-halt
// signal. All other failures (unknown recipe, policy deny, dispatch error) are
// recorded in the returned outcome — they do not halt sibling workers (REQ-086-02).
//
// dispatchOne is safe to run concurrently from N goroutines: it reads only immutable
// inputs (plan, sub, o.baseConfig, o.policy) and writes only to the shared audit.Sink
// and Reporter, both of which serialize their own writes.
func (o *Orchestrator) dispatchOne(ctx context.Context, plan Plan, sub SubGoal) (SubGoalOutcome, error) {
	outcome := SubGoalOutcome{
		SubGoal: sub.Task.Spec,
		Recipe:  sub.RecipeName,
	}

	// Confirm the recipe exists before the spawn-worker gate (ADR 046 §5). An
	// unknown recipe is a failed outcome, not a dispatch.
	if _, err := recipe.SelectRecipe(sub.RecipeName); err != nil {
		outcome.Success = false
		outcome.Detail = fmt.Sprintf("recipe not found: %s", sub.RecipeName)
		return outcome, nil
	}

	// Per-sub-goal spawn-worker gate (task 085 / ADR 050 §1). This issues a policy
	// decision for THIS dispatch, additive to the plan-level spawn-plan. The
	// self-repo bright line (REQ-085-05a) is checked inside decideSpawnWorker and
	// overrides the policy decision fail-closed. A non-allow decision skips the
	// dispatch, records a denied outcome, and reports the denial.
	decision, denyReason := o.decideSpawnWorker(plan, sub)
	// For deny events, include the deny reason in the audit event (SEC-004: distinguish
	// policy deny from self-repo deny). For allow, record just the recipe name.
	auditReason := sub.RecipeName
	if decision != policy.DecisionAllow {
		auditReason = denyReason
	}
	// SEC-003: deny events must succeed in audit — if the append fails, that is a hard
	// error that halts the plan (not silent). The error is returned to dispatchPlan,
	// which halts after the join.
	if err := o.emitFleetEventForDeny(audit.AuditEvent{
		Action: audit.ActionSpawnDecided, TaskID: sub.Task.ID, RunID: plan.GoalID,
		Detail: audit.EventDetail{PolicyDecision: string(decision), Reason: auditReason},
	}, decision != policy.DecisionAllow); err != nil {
		return outcome, err
	}
	if decision != policy.DecisionAllow {
		outcome.Success = false
		outcome.Detail = denyReason
		// Classify the denial of a NECESSARY action as a typed blocked action (ADR 055
		// seam 4, REQ-121-01): a distinct failure kind carrying the denied
		// resource/action + reason, so the orchestrate feedback path routes it to
		// bounded reevaluation + independent human escalation, never to a self-grant.
		// A blank-detail deny still records the denial (Detail is set above); the
		// blocked action is attached only when the classifier accepts the reason.
		blocked := classifyBlockedSpawn(sub, denyReason)
		if !blocked.IsZero() {
			b := blocked
			outcome.Blocked = &b
		}
		if err := o.reporter.Report(ctx, fmt.Sprintf("Worker spawn denied: recipe %s — %s", sub.RecipeName, denyReason)); err != nil {
			// A reporter error on a denial is not a security-relevant halt; record it
			// in the outcome detail and continue (best-effort reporting). Returning it
			// here would halt the whole plan for a transport hiccup.
			outcome.Detail = fmt.Sprintf("%s (report failed: %v)", denyReason, err)
		}
		return outcome, nil
	}

	// Fleet-wide worker semaphore (ADR 054 §1, task 112): acquire ONE permit before
	// the worker dispatch and release it (deferred) after, so total live workers
	// across ALL concurrent goals never exceeds AGENT_BUILDER_MAX_WORKERS. The
	// acquire/release wraps ONLY o.dispatch — the recipe check and gate above are
	// cheap and not the bound. Release is deferred inside this block so the permit
	// is returned on every path (success, dispatch error, or panic), never leaked
	// (REQ-112-07). A nil semaphore disables the cap (pre-112 unbounded dispatch).
	//
	// Registry projection: the sub-goal moves running → done/failed (ADR 054 §3),
	// written here from inside the task-086 dispatch goroutine — the same place the
	// outcome is produced.
	o.registry.SetSubGoal(plan.GoalID, SubGoalProgress{Name: sub.Task.ID, Recipe: sub.RecipeName, State: "running"})

	dispatchErr := o.dispatchWithSemaphore(ctx, sub)
	if dispatchErr != nil {
		outcome.Success = false
		outcome.Detail = dispatchErr.Error()
		o.registry.SetSubGoal(plan.GoalID, SubGoalProgress{Name: sub.Task.ID, Recipe: sub.RecipeName, State: "failed"})
	} else {
		outcome.Success = true
		outcome.Detail = "dispatched"
		o.registry.SetSubGoal(plan.GoalID, SubGoalProgress{Name: sub.Task.ID, Recipe: sub.RecipeName, State: "done"})
	}
	return outcome, nil
}

// dispatchWithSemaphore acquires one fleet-wide worker permit (when a semaphore is
// configured), runs o.dispatch, and releases the permit on EVERY return path via
// defer (REQ-112-07: permits balanced, no leak even on the dispatch-error path).
// When no semaphore is configured it dispatches directly (pre-112 unbounded). The
// acquire is the only blocking step: a saturated fleet parks this goroutine here
// (its goal stays Dispatching) until a permit frees, which is exactly the
// total-live-workers cap. An acquire failure (ctx cancelled) is returned as the
// dispatch error so the sub-goal is recorded failed rather than silently skipped.
func (o *Orchestrator) dispatchWithSemaphore(ctx context.Context, sub SubGoal) error {
	if o.workerSem != nil {
		if err := o.workerSem.Acquire(ctx); err != nil {
			return fmt.Errorf("orchestrator: acquire worker permit for %q: %w", sub.Task.ID, err)
		}
		defer o.workerSem.Release()
	}
	return o.dispatch(ctx, sub, o.baseConfig)
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
func (o *Orchestrator) decideSpawnWorker(plan Plan, sub SubGoal) (policy.Decision, string) {
	// Self-repo bright line — independent of the policy file, fail-closed.
	if targetsOwnRepo(sub) {
		return policy.DecisionDeny, fmt.Sprintf("self-repo bright line: refusing to dispatch a worker targeting %s", OwnRepo)
	}

	// Plan-derived authorization gate (ADR 055 seam 1): a worker may only spawn for
	// a recipe and task this plan actually declared. A sub-goal whose recipe or task
	// is not in the plan's derived allow set is denied WITHOUT consulting the policy
	// engine — the plan can only authorize the actions it constructed. In normal
	// operation every dispatched sub-goal comes from plan.SubGoals, so this passes;
	// it is the least-privilege backstop against a foreign/injected sub-goal.
	if !plan.authorizesResource(sub.RecipeName) {
		return policy.DecisionDeny, fmt.Sprintf("plan-derived authorization: recipe %q is not in the plan's allowed set", sub.RecipeName)
	}
	if !plan.authorizesResource(sub.Task.ID) {
		return policy.DecisionDeny, fmt.Sprintf("plan-derived authorization: task %q is not in the plan's allowed set", sub.Task.ID)
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
func defaultDispatch(ctx context.Context, sub SubGoal, base runtime.Config) error {
	cfg := base
	cfg.RecipeName = sub.RecipeName
	// Thread the per-goal cancel context (ADR 054 §5, task 116): a `cancel <goalID>`
	// cancels this ctx, which propagates through runtime.Run → Supervisor.Run to the
	// run-loop's case <-ctx.Done(): arm, tearing down the in-flight worker box. The
	// ctx is no longer dropped here (the pre-116 behaviour).
	return runtime.Run(ctx, cfg, nil)
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

// renderApprovalRequestWithInfo renders the approval solicitation (ADR 046 §4) and
// appends any queued pending-info under a "Queued info (will be folded on approve):"
// section (ADR 054 §4, task 115 — TC-115-02). It begins with "Approve?" and names
// each sub-goal's recipe and spec so the operator can review the plan before
// approving. Empty info renders exactly the base ADR-046 solicitation, so the
// pre-115 single-plan path is unchanged.
func renderApprovalRequestWithInfo(plan Plan, info []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Approve? Plan for goal: %s\n", plan.Goal)
	fmt.Fprintf(&b, "%d sub-goal(s):\n", len(plan.SubGoals))
	for _, sub := range plan.SubGoals {
		fmt.Fprintf(&b, "  - %s: %s\n", sub.RecipeName, sub.Task.Spec)
	}
	if len(info) > 0 {
		b.WriteString("Queued info (will be folded on approve):\n")
		for _, line := range info {
			fmt.Fprintf(&b, "  * %s\n", line)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
