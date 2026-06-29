package cli

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// goalActor owns one goal's lifecycle for the duration of its in-flight life (ADR
// 054 §3/§4, task 115). It runs the orchestrator's Handle for the goal AND drains
// the goal's command mailbox at checkpoint boundaries, applying apply-info-at-
// checkpoint semantics to each MsgInfo:
//
//   - Queue, don't interrupt (ADR 054 §4): an info appends to the registry's
//     per-goal pending-info queue synchronously. This is the ONLY synchronous state
//     an info writes; it touches no running worker.
//   - Fold at the next checkpoint:
//   - while the goal is AwaitingApproval, the actor re-solicits approval so the
//     queued info is surfaced with the solicitation (the fold itself happens on
//     ResumeWithFold-approve, which re-plans the augmented goal — driven by the
//     operator/approval channel, L6).
//   - once the goal has dispatched (no upcoming natural checkpoint), the info
//     SPAWNS an amendment sub-goal `G/amend-N` carrying the info as its goal text,
//     gated through the NORMAL Handle → spawn-plan/spawn-worker + self-repo path.
//
// The actor NEVER cancels, restarts, or mutates a running worker in response to an
// info: the dispatch goroutines live inside Orchestrator.dispatchPlan and the actor
// only ever reads the pending-info queue and spawns sibling actors. This is the
// structural guarantee behind REQ-115-05.
type goalActor struct {
	oc        orchestrateConfig
	goal      supervisor.Task
	mailbox   chan supervisor.Message
	mailboxes *commandMailboxes
	admit     chan struct{}
	wg        *sync.WaitGroup
	// shutdown is the control-loop's drain-only stop signal (closed when the source is
	// exhausted). It stops an AwaitingApproval actor's command-drain loop; it does NOT
	// interrupt an in-flight dispatch (the held worker finishes as-is — task 116 owns
	// teardown).
	shutdown <-chan struct{}

	// amendSeq is the monotonic amendment counter for THIS goal. Each MsgInfo on an
	// already-dispatched goal increments it to derive a collision-free `G/amend-N`
	// ID (TC-115-04/06: amend-1, amend-2, …). It is touched only by the single
	// command-drain goroutine, so a plain int is race-free; atomic is used for
	// defensiveness and to make the read in a test trivially safe.
	amendSeq atomic.Int64
}

// runGoalActor is the goal-actor entry the control loop spawns per new-goal. It
// acquires the goal-admission slot, then runs Handle while concurrently draining
// the mailbox so an info/cancel that arrives during the goal's life is processed at
// a checkpoint without blocking — or mutating — the in-flight dispatch.
//
// Ordering (ADR 054 §6 race surface (b)): the mailbox and registry entry already
// exist (routeNewGoal created them before spawning this actor), so an info that
// arrives at actor startup is delivered, not lost.
func (oc orchestrateConfig) runGoalActor(ctx context.Context, goal supervisor.Task, mailboxes *commandMailboxes, admit chan struct{}, wg *sync.WaitGroup, shutdown <-chan struct{}) {
	wg.Add(1)
	go func() {
		defer wg.Done()

		// Acquire a goal-admission slot. While the fleet is at maxGoals live goals this
		// blocks — the goal stays Queued in the registry (Handle has not run). When a
		// slot frees, the actor proceeds.
		// NOTE: shutdown is intentionally NOT a case here. An actor that has been spawned
		// (including an amendment spawned after the source was exhausted) is in-flight
		// work that must run to completion — the shutdown signal only stops a lingering
		// AwaitingApproval drain loop (drainPostHandle), it never abandons pending work.
		select {
		case admit <- struct{}{}:
		case <-ctx.Done():
			oc.registry.SetState(goal.ID, orchestrator.StateFailed)
			return
		}
		defer func() { <-admit }()

		mailbox, _ := mailboxes.Lookup(goal.ID)
		a := &goalActor{
			oc:        oc,
			goal:      goal,
			mailbox:   mailbox,
			mailboxes: mailboxes,
			admit:     admit,
			wg:        wg,
			shutdown:  shutdown,
		}
		a.run(ctx)
	}()
}

// run drives Handle while a sibling goroutine drains the command mailbox at
// checkpoint boundaries (apply-info-at-checkpoint). Lifecycle of the drain goroutine
// depends on where Handle leaves the goal:
//
//   - Handle returns having DISPATCHED or FAILED the goal (the allow / deny path):
//     the goal is terminal, there is no further checkpoint, so the drain stops after
//     a final non-blocking sweep of anything buffered.
//   - Handle returns with the goal AwaitingApproval (the require_approval pause): the
//     goal is still live with an upcoming checkpoint (the approve/reject), so the
//     drain KEEPS RUNNING — folding each arriving info into the queue + re-soliciting
//     — until the control loop cancels the context (source exhausted) or a cancel
//     lands. This is what makes info-during-AwaitingApproval work on the live path.
func (a *goalActor) run(ctx context.Context) {
	handleDone := make(chan struct{})

	var drainWG sync.WaitGroup
	if a.mailbox != nil {
		drainWG.Add(1)
		go func() {
			defer drainWG.Done()
			a.drainCommands(ctx, handleDone)
		}()
	}

	// The actor owns the goal's lifecycle via Handle. A goal-level error never halts
	// the fleet (best-effort across goals — ADR 054 §1): the registry already recorded
	// StateFailed inside Handle, and the orchestrator's Reporter emits the per-goal
	// summary / denial. The control loop does NOT write stdout itself.
	if _, err := a.oc.orch.Handle(ctx, a.goal); err != nil {
		_ = err // recorded in the registry as StateFailed; surfaced via status (task 114)
	}

	close(handleDone)
	drainWG.Wait()
}

// drainCommands reads the goal's mailbox and processes each command at a checkpoint.
// While Handle is still running (before handleDone) it blocks on the mailbox. Once
// Handle returns, it inspects the goal's lifecycle: if the goal is AwaitingApproval
// (a pending plan held in the store) it keeps draining until the context is
// cancelled or a cancel lands; otherwise it makes a final non-blocking sweep and
// returns (the goal is terminal — no further checkpoint).
func (a *goalActor) drainCommands(ctx context.Context, handleDone <-chan struct{}) {
	// While Handle is still running the actor keeps draining regardless of shutdown:
	// a goal actively planning/dispatching has in-flight work, and an info arriving
	// now must still be queued/amended (shutdown only stops a POST-handle lingering
	// AwaitingApproval drain — see drainPostHandle).
	for {
		select {
		case msg := <-a.mailbox:
			a.handleCommand(ctx, msg)
		case <-handleDone:
			a.drainPostHandle(ctx)
			return
		case <-ctx.Done():
			return
		}
	}
}

// drainPostHandle runs after Handle has returned. If the goal is AwaitingApproval it
// keeps the drain loop alive (folding info, re-soliciting) until the context is
// cancelled — so an info that arrives while the goal is paused at the approval
// checkpoint is still applied. Otherwise (the goal dispatched or failed — no further
// checkpoint) it makes a single non-blocking sweep so an info buffered just before
// Handle returned is still folded as an amendment, then returns.
func (a *goalActor) drainPostHandle(ctx context.Context) {
	if a.oc.orch.HasPendingPlan(a.goal.ID) {
		// AwaitingApproval: keep draining until shutdown / cancellation. The
		// fold-on-approve itself is driven by an approval message (L6/operator); here
		// the actor surfaces each arriving info with the solicitation and queues it for
		// the eventual fold. On shutdown / cancellation, a final non-blocking sweep
		// applies anything still buffered before exit so an info delivered just before
		// shutdown is not dropped.
		for {
			select {
			case msg := <-a.mailbox:
				a.handleCommand(ctx, msg)
			case <-ctx.Done():
				a.sweep(ctx)
				return
			case <-a.shutdown:
				a.sweep(ctx)
				return
			}
		}
	}
	// Terminal goal: one non-blocking sweep of anything still buffered.
	a.sweep(ctx)
}

// sweep applies every command currently buffered in the mailbox, non-blocking. It is
// the final pass that guarantees an info delivered just before the actor stops
// (terminal goal, shutdown, or context cancellation) is still folded rather than
// dropped.
func (a *goalActor) sweep(ctx context.Context) {
	for {
		select {
		case msg := <-a.mailbox:
			a.handleCommand(ctx, msg)
		default:
			return
		}
	}
}

// handleCommand dispatches one mailbox command. This task (115) acts on MsgInfo
// (apply-info-at-checkpoint). MsgCancel teardown is task 116: the actor consumes a
// cancel off its mailbox here but takes no teardown action yet (the case is the
// seam 116 fills in). An unknown kind is ignored (the router already rejected
// malformed input upstream).
func (a *goalActor) handleCommand(ctx context.Context, msg supervisor.Message) {
	switch msg.Kind {
	case supervisor.MsgInfo:
		a.applyInfo(ctx, msg.Text)
	case supervisor.MsgCancel:
		// Cancel teardown is task 116; the actor consumes the message but does not yet
		// tear down in-flight workers. Until 116 lands this is intentionally a no-op.
	}
}

// applyInfo implements apply-info-at-checkpoint for one info text (ADR 054 §4):
//
//  1. Queue, don't interrupt — append to the registry's pending-info queue. This is
//     the only synchronous write and it touches no running worker.
//  2. Fold at the next checkpoint, chosen by the goal's CURRENT lifecycle state:
//     - AwaitingApproval → re-solicit approval (surface the queued info with the
//     solicitation); the fold re-plan happens on ResumeWithFold-approve.
//     - anything else (Planning/Dispatching/terminal) → no upcoming natural
//     checkpoint for this info, so SPAWN an amendment sub-goal `G/amend-N` through
//     the normal gated Handle path. The running workers are untouched.
func (a *goalActor) applyInfo(ctx context.Context, text string) {
	// 1. Queue-don't-interrupt: the single synchronous write.
	a.oc.registry.EnqueueInfo(a.goal.ID, text)

	// 2. Choose the checkpoint by the goal's CURRENT lifecycle state.
	st, ok := a.oc.registry.Get(a.goal.ID)
	state := orchestrator.StateQueued
	if ok {
		state = st.State
	}

	switch state {
	case orchestrator.StateAwaitingApproval:
		// Paused at the approval checkpoint: surface the queued info WITH the approval
		// solicitation (TC-115-02). The drain/fold happens on approve via
		// ResumeWithFold; we only re-solicit here (the queue is left intact for the fold).
		if err := a.oc.orch.SolicitApproval(ctx, a.goal.ID); err != nil {
			a.oc.report(ctx, fmt.Sprintf("info for goal %q: re-solicit failed: %v", a.goal.ID, err))
		}
	case orchestrator.StateQueued, orchestrator.StatePlanning:
		// Heading toward the approval checkpoint (admitted/planning, not yet paused or
		// dispatched). The info is queued; it will be surfaced + folded when the goal
		// reaches its AwaitingApproval checkpoint. Nothing more to do synchronously —
		// queue-don't-interrupt holds, and there is an upcoming natural checkpoint.
	default:
		// Dispatching or terminal (Done/Failed/Cancelled): the goal is PAST its approval
		// gate, so there is no upcoming natural checkpoint for this info. Drain the queue
		// and spawn an amendment sub-goal through the normal gated path. The running
		// workers are never touched (amendment is an independent sibling actor).
		a.spawnAmendment(ctx, a.oc.registry.DrainInfo(a.goal.ID))
	}
}

// spawnAmendment spawns one amendment goal actor per drained info line, each with a
// monotonic, collision-free `G/amend-N` ID (TC-115-04/06). The amendment carries the
// info as its goal text and the PARENT goal's Repo, so it flows through the SAME
// Handle → spawn-plan/spawn-worker + self-repo bright-line path as any goal — an
// info-spawned amendment cannot bypass a gate (REQ-115-06). The parent's running
// workers are never touched: the amendment is an independent sibling actor.
func (a *goalActor) spawnAmendment(ctx context.Context, infos []string) {
	for _, text := range infos {
		n := a.amendSeq.Add(1)
		amendID := fmt.Sprintf("%s/amend-%d", a.goal.ID, n)
		amend := supervisor.Task{ID: amendID, Repo: a.goal.Repo, Spec: text}

		// register-then-start: mailbox + registry entry before the actor goroutine, so
		// the amendment is addressable (a future info on the amendment itself routes).
		a.mailboxes.Create(amendID)
		a.oc.registry.Register(amendID, orchestrator.StateQueued)

		// The amendment runs as its own goal actor under the SAME admission slot model.
		// It is gated end-to-end by Handle (spawn-plan + per-sub-goal spawn-worker +
		// self-repo bright line) — no bypass via the amendment route. It inherits the
		// same drain-shutdown signal so a lingering amendment actor also stops cleanly.
		a.oc.runGoalActor(ctx, amend, a.mailboxes, a.admit, a.wg, a.shutdown)
	}
}
