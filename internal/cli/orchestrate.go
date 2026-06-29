package cli

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/worker"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/executor"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator/planner"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/router"
	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// EnvPlanner selects the Planner the orchestrate subcommand assembles. Live
// values: "structured" (default, rule-based StructuredPlanner) and "llm"
// (LLM-backed LLMPlanner, routes a decomposition prompt through the router/
// registry path — ollama-native entries only; cloud harnesses fail closed via
// ErrSingleShotUnsupported). Any other value is a fail-fast configuration error
// (ExitUsage). Added in task 099; llm branch wired live in task 110 (ADR 053).
const EnvPlanner = "AGENT_BUILDER_PLANNER"

const (
	plannerStructured = "structured"
	plannerLLM        = "llm"
)

// EnvMaxWorkers / EnvMaxGoals are the two async-control-plane concurrency bounds
// (ADR 054 §1, task 112). MAX_WORKERS is the load-bearing fleet-wide cap on total
// live sub-goal workers (sandbox/box pressure); MAX_GOALS is the looser
// goal-admission cap on how many goal actors run at once (back-pressure on
// planning state).
const (
	EnvMaxWorkers = "AGENT_BUILDER_MAX_WORKERS"
	EnvMaxGoals   = "AGENT_BUILDER_MAX_GOALS"
)

// Conservative defaults (ADR 054 §1): a small worker cap bounds sandbox pressure;
// a looser goal cap bounds planning-state growth. Both are overridable by env.
const (
	defaultMaxWorkers = 4
	defaultMaxGoals   = 8
)

// errUsageConfig is the sentinel that marks assembly errors caused by a bad
// AGENT_BUILDER_PLANNER value (or any other env-config mistake detected at
// assembly time that the CLI should report as ExitUsage rather than ExitGeneric).
// runOrchestrate checks for it with errors.Is to route to the usage exit path.
var errUsageConfig = errors.New("orchestrate: configuration error (check env vars)")

// defaultCLIClaudeEntryID is the synthetic fallback catalog entry the CLI builds
// when no AGENT_BUILDER_REGISTRY_* entries are enabled — the same shape as
// internal/runtime's defaultClaudeEntry (option (b), ADR 053 §3: the CLI builds
// its own catalog rather than calling the unexported runtime.buildCatalog). Both
// synthesize a CapabilityTier=1, CostWeight=1, HarnessClaudeCLI entry so the
// router always has a routable entry at the base capability floor. The CLI's
// synthetic entry carries no Endpoint/ModelID — it is only used to satisfy the
// router's selection logic; a CompleterForEntry call against it fails closed with
// ErrSingleShotUnsupported (cloud harness), which is the expected behavior.
const defaultCLIClaudeEntryID = "claude-default"

// orchestrateConfig carries the assembled orchestrator wiring. Its fields are
// populated by assembleOrchestrator from environment configuration on the live
// path; tests construct it directly with overrides (a shared ReplayCache, a stub
// PolicyClient, a spy DispatchFunc, a FakeSink, a stub GoalSource) to assert the
// security invariants without a full integration harness.
type orchestrateConfig struct {
	// orch is the fully-assembled Tier-1 coordinator.
	orch *orchestrator.Orchestrator
	// source is the inbound message seam the control loop reads from (ADR 054 §2).
	// The control loop is the ONLY reader of this seam (no concurrent Next() races —
	// ADR 054 §1). It carries typed messages (new-goal/status/info/cancel), not just
	// goals; the router dispatches each by kind.
	source supervisor.MessageSource
	// reporter is the outbound seam the router uses to answer status queries and
	// graceful "no such goal" reports for an unknown goalID (ADR 054 §2). It is the
	// SAME mutex-guarded Reporter the orchestrator core holds (single serialized
	// stdout owner under concurrency).
	reporter supervisor.Reporter
	// statusHandler is the seam the router dispatches MsgStatus to. The handler BODY
	// (registry render + immediate Reporter answer) is task 114; this task routes the
	// kind to it. A nil handler falls back to a minimal Reporter acknowledgement so
	// the status path is observable end-to-end before 114 lands.
	statusHandler func(goalID string)
	// stdout is the user-visible output destination. The per-goal summary is emitted
	// through the orchestrator's Reporter (built from this same writer in
	// assembleOrchestrate — the single mutex-guarded stdout owner under concurrency,
	// ADR 054 §1), NOT written directly by the control loop. Retained on the config
	// as the seam's record of the output destination.
	stdout io.Writer
	// registry is the shared live status registry (ADR 054 §3). The control loop
	// registers each new goal (Queued) before spawning its actor; the actor and the
	// dispatch goroutines project lifecycle transitions into it. Shared across all
	// goal actors via the orchestrator's WithStatusRegistry.
	registry *orchestrator.StatusRegistry
	// maxGoals is the goal-admission cap (AGENT_BUILDER_MAX_GOALS): the maximum
	// number of goal actors that may be in a non-Queued, non-terminal state at once.
	// Excess goals park with Queued status until a slot frees. Enforced at the
	// control loop, not the orchestrator core.
	maxGoals int
	// mailboxes is the per-goal command-mailbox map (ADR 054 §3). When nil,
	// runControlLoop creates a fresh one; a router test injects its own so it can
	// read the delivered MsgInfo/MsgCancel and assert addressing (TC-113-04/06).
	mailboxes *commandMailboxes
}

// assembleOverrides lets tests inject the security-relevant collaborators that the
// live path constructs from env. Every nil field falls back to the env-constructed
// default. The two ReplayCache fields are the load-bearing 083 SEC-001 hooks: the
// live path creates ONE cache per direction at startup (see assembleOrchestrator);
// a test injects an explicit cache and asserts that the SAME instance is reused
// across dispatches (TC-099-03).
type assembleOverrides struct {
	policyClient orchestrator.PolicyClient
	dispatch     orchestrator.DispatchFunc
	auditSink    audit.Sink
	planStore    orchestrator.PlanStore
	planner      orchestrator.Planner
	// source is a goal-only GoalSource override (task-112 tests inject this). When
	// set and messageSource is nil, the control loop adapts it into a MessageSource
	// that yields each goal as a MsgNewGoal — preserving the goal-only test path.
	source supervisor.GoalSource
	// messageSource is a typed MessageSource override (task-113 router tests inject
	// this directly to script new-goal/status/info/cancel sequences). It takes
	// precedence over source.
	messageSource supervisor.MessageSource
	// reporter overrides the outbound Reporter (router tests inject a spy to assert
	// the graceful "no such goal" report). Nil falls back to the env-built reporter.
	reporter supervisor.Reporter
	// statusHandler overrides the MsgStatus dispatch target (router tests inject a
	// recording handler to assert the status path is reached with empty=fleet). Nil
	// falls back to a minimal Reporter acknowledgement (the handler body is task 114).
	statusHandler func(goalID string)
	// workItemCache, when non-nil, is used as the shared work-item-direction
	// ReplayCache instead of the one assembleOrchestrator creates at startup.
	workItemCache *envelope.ReplayCache
	// resultCache, when non-nil, is used as the shared result-direction ReplayCache.
	resultCache *envelope.ReplayCache
	// signingKey, when non-nil, substitutes for worker.LoadSigningKey (so a test can
	// exercise the assembled path without a real key file). When nil, the live
	// SEC-003 startup check runs.
	signingKey ed25519.PrivateKey
	// maxWorkers / maxGoals, when > 0, override the env-parsed concurrency bounds so
	// a concurrency test can pin MAX_WORKERS=2 / MAX_GOALS=1 without touching process
	// env. 0 means "use the env-parsed value" (which itself falls back to the default).
	maxWorkers int
	maxGoals   int
	// registry, when non-nil, is used as the shared status registry instead of the
	// one assembleOrchestrate creates. A test injects a registry to assert lifecycle
	// transitions, or a no-op projection (TC-112-05) to prove the registry never
	// gates control flow.
	registry *orchestrator.StatusRegistry
	// workerSem, when non-nil, is used as the shared worker semaphore instead of the
	// one assembleOrchestrate sizes from maxWorkers — lets a test hold the SAME
	// semaphore instance to assert "no permit leak after drain" (TC-112-07).
	workerSem *orchestrator.Semaphore
	// onPlanner, when non-nil, is invoked with the exact Planner value that
	// assembleOrchestrate hands to orchestrator.New — the producer→consumer link a
	// test needs to assert that the env-selected planner (e.g. *llmplanner.LLMPlanner
	// under AGENT_BUILDER_PLANNER=llm) actually survives the control-plane assembly
	// and reaches the orchestrator (TC-110-05). Production passes assembleOverrides{}
	// so this is nil on the live path.
	onPlanner func(orchestrator.Planner)
}

// runOrchestrate is the CLI dispatch for the orchestrate subcommand. It parses
// flags, assembles the orchestrator stack from the environment, and drives the
// goal-intake → Handle/Resume loop. The SEC-003 worker-signing-key check fires
// inside assembleOrchestrate before any goal is read.
func runOrchestrate(config Config, args []string) int {
	flags := newFlagSet("orchestrate", config.Stderr)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			orchestrateUsage(config.Stdout)
			return ExitOK
		}
		return usage(config.Stderr, err)
	}
	if flags.NArg() != 0 {
		return usage(config.Stderr, fmt.Errorf("orchestrate accepts no positional arguments"))
	}

	oc, cleanup, err := assembleOrchestrate(config, assembleOverrides{})
	if err != nil {
		// SEC-003 fail-closed: a missing worker signing key (or any assembly error)
		// exits non-zero BEFORE the goal-intake loop is ever entered.
		//
		// A usage-config error (e.g. unknown AGENT_BUILDER_PLANNER value) gets
		// ExitUsage so the caller knows it is a configuration mistake, not a runtime
		// failure. All other assembly errors get ExitGeneric.
		writef(config.Stderr, "error: %v\n", err)
		if errors.Is(err, errUsageConfig) {
			return ExitUsage
		}
		return ExitGeneric
	}
	defer cleanup()

	if err := runControlLoop(context.Background(), oc); err != nil {
		writef(config.Stderr, "error: %v\n", err)
		return ExitGeneric
	}
	return ExitOK
}

// assembleOrchestrate builds the orchestrate wiring from environment configuration,
// applying any test overrides. It returns the assembled config, a cleanup func
// (stops the policy daemon when one was started), and an error.
//
// SECURITY-CRITICAL ORDERING (083 SEC-003): worker.LoadSigningKey runs FIRST,
// before the PlanStore, the orchestrator, or the goal source are touched — a
// missing/unreadable key returns immediately with an error satisfying
// errors.Is(err, worker.ErrMissingSigningKey), so the subcommand never reaches the
// goal-intake loop without verified key material.
//
// SECURITY-CRITICAL CACHE LIFETIME (083 SEC-001): exactly ONE *envelope.ReplayCache
// is created per direction here, at assembly time, and threaded into the dispatch
// seam. A fresh cache per dispatch would defeat replay rejection — the dispatch
// closure captures these two caches by reference and never reconstructs them.
func assembleOrchestrate(config Config, ov assembleOverrides) (orchestrateConfig, func(), error) {
	noop := func() {}

	// 1. SEC-003 fail-closed startup key check — BEFORE anything else.
	if ov.signingKey == nil {
		if _, err := worker.LoadSigningKey(); err != nil {
			return orchestrateConfig{}, noop, err
		}
	}

	logger := slog.New(slog.NewTextHandler(config.Stderr, nil))

	// 2. PlanStore (ADR 049 §3 / REQ-084-04): memory-guard-backed when configured,
	//    else in-memory + structured warning.
	store := ov.planStore
	if store == nil {
		store = orchestrator.NewPlanStoreFromEnv(func(msg string, kv ...any) {
			logger.Warn(msg, kv...)
		})
	}

	// 3. Worker transport replay caches — ONE per direction, created ONCE here.
	workItemCache := ov.workItemCache
	if workItemCache == nil {
		workItemCache = envelope.NewReplayCache(0)
	}
	resultCache := ov.resultCache
	if resultCache == nil {
		resultCache = envelope.NewReplayCache(0)
	}

	// 4. Planner: default StructuredPlanner; AGENT_BUILDER_PLANNER=llm selects the
	//    LLMPlanner (ollama-native single-shot; cloud harnesses fail closed).
	planner := ov.planner
	if planner == nil {
		p, err := plannerFromEnv()
		if err != nil {
			return orchestrateConfig{}, noop, err
		}
		planner = p
	}

	// 5. AuditSink — one sink shared between the orchestrator and the workers it
	//    dispatches (ADR 050 §4). Constructed from the same env vars as the run path.
	auditSink := ov.auditSink
	cleanup := noop
	if auditSink == nil {
		sink, err := auditSinkFromEnv()
		if err != nil {
			return orchestrateConfig{}, noop, err
		}
		auditSink = sink
	}

	// 6. PolicyClient + daemon lifecycle (ADR 050 §1, fail-closed).
	pol := ov.policyClient
	if pol == nil {
		client, stop, err := policyClientFromEnv(logger)
		if err != nil {
			return orchestrateConfig{}, noop, err
		}
		pol = client
		cleanup = stop
	}

	// 7. Base runtime.Config from env (reuses the run path's contract).
	baseConfig, err := runtimewiring.ConfigFromEnv(os.Getenv)
	if err != nil {
		cleanup()
		return orchestrateConfig{}, noop, fmt.Errorf("orchestrate: build base config: %w", err)
	}

	// 8. Dispatch seam. Both shared ReplayCaches (one per direction) are threaded in
	//    here so the assembled live dispatch path replay-checks every work-item AND
	//    every returned result against the ONE long-lived cache for that direction
	//    (083 SEC-001). A nil override means the env-backed dispatch that round-trips
	//    the work-item + result through the worker transport before declaring success.
	dispatch := ov.dispatch
	if dispatch == nil {
		signingKey := ov.signingKey
		if signingKey == nil {
			// Already validated above; load once for the transport.
			key, err := worker.LoadSigningKey()
			if err != nil {
				cleanup()
				return orchestrateConfig{}, noop, err
			}
			signingKey = key
		}
		d, keyErr := newTransportDispatch(signingKey, workItemCache, resultCache, auditSink, logger)
		if keyErr != nil {
			cleanup()
			return orchestrateConfig{}, noop, fmt.Errorf("orchestrate: %w", keyErr)
		}
		dispatch = d
	}

	// 9. Reporter — wired to the outbound channel seam (task 098); falls back to a
	//    stderr log reporter when no outbound channel is configured. The SAME reporter
	//    instance is handed to the orchestrator core AND held by the control-loop
	//    router (status answers + graceful "no such goal" reports — ADR 054 §2), so all
	//    user-visible output flows through the one mutex-guarded stdout owner.
	reporter := ov.reporter
	if reporter == nil {
		reporter = newLogReporter(config.Stdout)
	}

	// 10. Async control-plane concurrency bounds (ADR 054 §1, task 112). The worker
	//     semaphore is shared across ALL goal actors so the bound is fleet-wide
	//     (total live workers), enforced inside dispatchPlan's per-sub-goal goroutine.
	//     The status registry is one shared projection. Test overrides (maxWorkers/
	//     maxGoals/registry/workerSem) take precedence over the env-parsed values.
	maxWorkers := ov.maxWorkers
	if maxWorkers <= 0 {
		maxWorkers = envInt(os.Getenv, EnvMaxWorkers, defaultMaxWorkers)
	}
	maxGoals := ov.maxGoals
	if maxGoals <= 0 {
		maxGoals = envInt(os.Getenv, EnvMaxGoals, defaultMaxGoals)
	}
	registry := ov.registry
	if registry == nil {
		registry = orchestrator.NewStatusRegistry()
	}
	workerSem := ov.workerSem
	if workerSem == nil {
		workerSem = orchestrator.NewSemaphore(maxWorkers)
	}

	// Test seam (TC-110-05): observe the exact planner the assembly feeds into
	// orchestrator.New, proving the env-selected planner survives the control loop.
	if ov.onPlanner != nil {
		ov.onPlanner(planner)
	}

	orch := orchestrator.New(
		planner, pol, reporter, baseConfig,
		orchestrator.WithPlanStore(store),
		orchestrator.WithAuditSink(auditSink),
		orchestrator.WithDispatchFunc(dispatch),
		orchestrator.WithStatusRegistry(registry),
		orchestrator.WithWorkerSemaphore(workerSem),
	)

	// Inbound message seam (ADR 054 §2). Precedence: an explicit typed MessageSource
	// override → a goal-only GoalSource override adapted to messages → the live
	// env/stdin line-oriented MessageSource.
	source := ov.messageSource
	switch {
	case source != nil:
		// use the injected typed source
	case ov.source != nil:
		source = goalSourceAsMessages(ov.source)
	default:
		source = newEnvMessageSource(os.Getenv, config.Stdin)
	}

	return orchestrateConfig{
		orch:          orch,
		source:        source,
		stdout:        config.Stdout,
		registry:      registry,
		maxGoals:      maxGoals,
		reporter:      reporter,
		statusHandler: ov.statusHandler,
	}, cleanup, nil
}

// goalSourceAsMessages adapts a goal-only GoalSource into a MessageSource that
// yields each goal as a MsgNewGoal. It preserves the task-112 goal-only test path
// (and any future GoalSource-only producer) under the generalized control loop
// without disturbing the GoalSource contract.
func goalSourceAsMessages(gs supervisor.GoalSource) supervisor.MessageSource {
	return goalSourceAdapter{gs: gs}
}

type goalSourceAdapter struct{ gs supervisor.GoalSource }

func (a goalSourceAdapter) Next() (supervisor.Message, bool, error) {
	task, ok, err := a.gs.Next()
	if err != nil || !ok {
		return supervisor.Message{}, ok, err
	}
	return supervisor.Message{Kind: supervisor.MsgNewGoal, GoalID: task.ID, Goal: task}, true, nil
}

// envInt parses a non-negative integer env var, returning def when unset, empty,
// or unparseable, and clamping to a minimum of 1 (a zero/negative concurrency
// bound would deadlock dispatch / admit no goals). It is intentionally lenient —
// a malformed bound falls back to the conservative default rather than failing
// the whole subcommand, since the bounds are tuning knobs, not security gates.
func envInt(getenv func(string) string, key string, def int) int {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return def
	}
	return n
}

// runControlLoop is the non-blocking control loop (ADR 054 §1, task 112). It
// replaces the serial runGoalIntakeLoop's `for { Next(); Handle() }`: a single
// goroutine (this one) owns the GoalSource and is its ONLY reader (no concurrent
// Next() races). For each new goal it registers a Queued entry in the status
// registry, then spawns a GOAL-ACTOR goroutine that owns that goal's lifecycle by
// calling Orchestrator.Handle. Reading the next goal and processing a goal are
// thus decoupled — a goal stalled in Dispatching does NOT block intake of the next
// (REQ-112-01), and M goals run concurrently (REQ-112-02).
//
// Two bounds compose (ADR 054 §1): the per-actor goal-admission cap
// (AGENT_BUILDER_MAX_GOALS), enforced here by a buffered admission channel that
// each actor must acquire before transitioning out of Queued — excess goals park
// with Queued status until a slot frees (REQ-112-04); and the fleet-wide worker
// semaphore (AGENT_BUILDER_MAX_WORKERS), enforced INSIDE dispatchPlan's per-sub-goal
// goroutine in the orchestrator core (REQ-112-03), not here.
//
// The loop reads until the source is exhausted (ok=false) or errors, then waits
// for every spawned actor to join before returning — so the subcommand exits only
// after all in-flight goals drain (REQ-112-07 permit balance is asserted after this
// returns).
func runControlLoop(ctx context.Context, oc orchestrateConfig) error {
	// admission is the goal-admission cap: cap(admit)=maxGoals tokens. An actor
	// sends a token (blocking when full) before leaving Queued and receives it back
	// at terminal — so at most maxGoals actors are ever non-Queued/non-terminal.
	maxGoals := oc.maxGoals
	if maxGoals < 1 {
		maxGoals = 1
	}
	admit := make(chan struct{}, maxGoals)

	// mailboxes is the per-goal command-mailbox map (ADR 054 §3 / §6 race surface
	// (b)). A new-goal creates its mailbox BEFORE the actor is registered/started;
	// info/cancel are routed to it; an unknown goalID never auto-creates one. A test
	// may inject its own map (oc.mailboxes) to observe deliveries.
	mailboxes := oc.mailboxes
	if mailboxes == nil {
		mailboxes = newCommandMailboxes()
	}

	var wg sync.WaitGroup
	var srcErr error

	for {
		msg, ok, err := oc.source.Next()
		if err != nil {
			// A malformed control line surfaces here. It is NOT fatal to the control
			// plane — report it gracefully and continue reading the next message
			// (fail-loud-but-graceful, ADR 054 §2). Only a hard source error breaks.
			if isParseError(err) {
				oc.report(ctx, fmt.Sprintf("ignored malformed input: %v", err))
				continue
			}
			srcErr = fmt.Errorf("orchestrate: read message: %w", err)
			break
		}
		if !ok {
			break
		}

		switch msg.Kind {
		case supervisor.MsgNewGoal:
			oc.routeNewGoal(ctx, msg.Goal, mailboxes, admit, &wg)
		case supervisor.MsgStatus:
			oc.routeStatus(ctx, msg.GoalID)
		case supervisor.MsgInfo, supervisor.MsgCancel:
			oc.routeCommand(ctx, msg, mailboxes)
		default:
			oc.report(ctx, fmt.Sprintf("ignored message of unknown kind: %v", msg.Kind))
		}
	}

	wg.Wait()
	return srcErr
}

// routeNewGoal handles a MsgNewGoal: create the goal's command mailbox, register it
// Queued, then spawn the goal actor (register-then-start ordering — ADR 054 §6 race
// surface (b): the mailbox and registry entry exist before the actor starts, so an
// info/cancel arriving at actor startup is delivered, not lost).
func (oc orchestrateConfig) routeNewGoal(ctx context.Context, goal supervisor.Task, mailboxes *commandMailboxes, admit chan struct{}, wg *sync.WaitGroup) {
	// Mailbox BEFORE registration/start: a cancel/info for this goal that arrives
	// while the actor is still booting must find a mailbox.
	mailboxes.Create(goal.ID)

	// Register the goal as Queued BEFORE spawning its actor, so a status read (or the
	// admission check) never sees a goal that has been accepted but not yet projected.
	oc.registry.Register(goal.ID, orchestrator.StateQueued)

	wg.Add(1)
	go func(goal supervisor.Task) {
		defer wg.Done()
		// Acquire a goal-admission slot. While the fleet is at maxGoals live goals this
		// blocks — the goal stays Queued in the registry (Handle has not run, so no
		// Planning transition). When a slot frees, the actor proceeds and Handle
		// transitions Queued → Planning → … itself.
		select {
		case admit <- struct{}{}:
		case <-ctx.Done():
			oc.registry.SetState(goal.ID, orchestrator.StateFailed)
			return
		}
		defer func() { <-admit }()

		// The actor owns the goal's lifecycle via Handle. A goal-level error never
		// halts the fleet (best-effort across goals — ADR 054 §1): the registry already
		// recorded StateFailed inside Handle, and the orchestrator's Reporter (the
		// single serialized stdout owner) emits the per-goal summary / denial. The
		// control loop does NOT write stdout itself.
		if _, err := oc.orch.Handle(ctx, goal); err != nil {
			_ = err // recorded in the registry as StateFailed; surfaced via status (task 114)
		}
	}(goal)
}

// routeStatus dispatches a MsgStatus to the status handler (task 114's body). An
// empty GoalID is a fleet query. With no handler injected, a minimal Reporter
// acknowledgement keeps the status path observable end-to-end before 114 lands.
func (oc orchestrateConfig) routeStatus(ctx context.Context, goalID string) {
	if oc.statusHandler != nil {
		oc.statusHandler(goalID)
		return
	}
	if goalID == "" {
		oc.report(ctx, "status: fleet (handler pending task 114)")
		return
	}
	oc.report(ctx, fmt.Sprintf("status: goal %q (handler pending task 114)", goalID))
}

// routeCommand dispatches a MsgInfo/MsgCancel to the addressed goal's command
// mailbox. An unknown goalID (no mailbox) yields a graceful "no such goal" report —
// never a panic, and no mailbox is created for the unknown goal (ADR 054 §2).
func (oc orchestrateConfig) routeCommand(ctx context.Context, msg supervisor.Message, mailboxes *commandMailboxes) {
	if mailboxes.deliver(msg) {
		return
	}
	oc.report(ctx, fmt.Sprintf("no such goal: %q (%s ignored)", msg.GoalID, msg.Kind))
}

// report sends a control-loop message over the shared Reporter, swallowing the
// Reporter error (a failed report must not halt the control plane). A nil reporter
// is a no-op.
func (oc orchestrateConfig) report(ctx context.Context, text string) {
	if oc.reporter == nil {
		return
	}
	_ = oc.reporter.Report(ctx, text)
}

// plannerFromEnv selects the Planner per AGENT_BUILDER_PLANNER (ADR 053 §3,
// task 110). "structured" (default) returns the rule-based StructuredPlanner.
// "llm" assembles the LLM-backed LLMPlanner by building a router catalog from
// the environment (option (b) — the CLI builds its own catalog; internal/runtime
// is unchanged), wrapping *router.Router.Select as the ExecutorResolver, and
// closing over executor.CompleterForEntry as the Invoker. An unknown value
// returns a fail-fast error that drives ExitUsage.
func plannerFromEnv() (orchestrator.Planner, error) {
	choice := strings.TrimSpace(os.Getenv(EnvPlanner))
	switch choice {
	case "", plannerStructured:
		return orchestrator.NewStructuredPlanner(), nil
	case plannerLLM:
		return buildLLMPlanner()
	default:
		// Wrap errUsageConfig so runOrchestrate returns ExitUsage instead of ExitGeneric.
		return nil, fmt.Errorf("%w: %s=%q is not a known planner (want %q or %q)", errUsageConfig, EnvPlanner, choice, plannerStructured, plannerLLM)
	}
}

// buildLLMPlanner assembles the LLM-backed planner: catalog → router →
// ExecutorResolver adapter + Invoker closure → planner.NewPlannerFromEnv.
//
// Catalog-build option (b) (ADR 053 §3): the CLI builds its own *registry.Catalog
// from registry.LoadFromEnv() with the same synthetic-default fallback as
// internal/runtime.buildCatalog (CapabilityTier=1, CostWeight=1,
// HarnessClaudeCLI). This keeps internal/runtime unchanged and avoids exporting
// its unexported buildCatalog. Both sites must NOT diverge in the default-entry
// shape; see defaultCLIClaudeEntryID comment above.
//
// ExecutorResolver adapter: Resolve(ctx, spec) drops the ctx and calls
// r.Select(spec). router.Select takes NO ctx — the router's entry selection is
// not context-cancellable today. Dropping ctx is a documented, deliberate
// limitation; a future cancellable Select can add ctx without changing the
// adapter's external contract.
//
// Invoker closure: closes over executor.CompleterForEntry in internal/cli (where
// internal/executor is already a transitive import via internal/runtime), so
// internal/orchestrator/planner never sees the executor import — F-014 stays green.
func buildLLMPlanner() (orchestrator.Planner, error) {
	// 1. Build catalog from env, with synthetic-default fallback.
	entries, err := registry.LoadFromEnv()
	if err != nil {
		return nil, fmt.Errorf("plannerFromEnv: load registry: %w", err)
	}
	cat := registry.NewCatalog()
	if len(entries) == 0 {
		// Synthetic default: same shape as internal/runtime.defaultClaudeEntry
		// (CapabilityTier 1, CostWeight 1, HarnessClaudeCLI). A CompleterForEntry
		// call against this entry fails closed with ErrSingleShotUnsupported (cloud
		// harness) — the cloud-only fail-closed path documented in ADR 053 §2.
		cat.RegisterEntry(registry.RegistryEntry{
			ID:             defaultCLIClaudeEntryID,
			Harness:        registry.HarnessClaudeCLI,
			CapabilityTier: 1,
			CostWeight:     1,
			Availability:   registry.Availability{Status: registry.AvailStatusAvailable},
		})
	} else {
		for _, e := range entries {
			cat.RegisterEntry(e)
		}
	}

	// 2. Router wraps the catalog; resolver adapter wraps router.Select.
	r := router.New(cat)
	// routerResolver is the ExecutorResolver adapter over *router.Router.Select.
	// It drops the planner's ctx: router.Select takes no context and is not
	// context-cancellable today — this is a deliberate, documented limitation.
	// See ADR 053 §3 and the dropped-ctx note in the test spec (TC-110-03).
	routerResolver := &routerResolverAdapter{r: r}

	// 3. Invoker closure captures executor.CompleterForEntry. Constructed here in
	// internal/cli (where internal/executor is already imported transitively via
	// internal/runtime), so internal/orchestrator/planner never imports executor
	// directly — F-010/F-014 stay green (ADR 053 §"Why F-010 and F-014 stay green").
	invoke := planner.Invoker(func(ctx context.Context, entry registry.RegistryEntry, prompt string) (string, error) {
		c, err := executor.CompleterForEntry(entry)
		if err != nil {
			return "", err
		}
		return c.Complete(ctx, entry, prompt)
	})

	return planner.NewPlannerFromEnv(routerResolver, invoke)
}

// routerResolverAdapter implements planner.ExecutorResolver by wrapping
// *router.Router.Select. Resolve drops the ctx argument because router.Select
// takes no context — selection is not context-cancellable today. This is a
// deliberate, documented limitation (ADR 053 §3 / TC-110-03): a cancelled ctx
// does not change the result; the router still returns the selected entry (or
// ErrNoEligibleExecutor). A future cancellable Select can add ctx here without
// changing the planner.ExecutorResolver contract.
type routerResolverAdapter struct {
	r *router.Router
}

func (a *routerResolverAdapter) Resolve(_ context.Context, spec router.RoutingSpec) (registry.RegistryEntry, error) {
	// ctx intentionally dropped — router.Select is not context-cancellable today.
	return a.r.Select(spec)
}

// auditSinkFromEnv constructs the shared audit.Sink from AGENT_BUILDER_AUDIT_RECORD
// + AGENT_BUILDER_AUDIT_BIN, the same env contract the run path uses. When
// AGENT_BUILDER_AUDIT_RECORD is unset, the sink is nil (no audit chain configured) —
// the orchestrator tolerates a nil sink for benign events but fails closed on deny
// events (emitFleetEventForDeny).
func auditSinkFromEnv() (audit.Sink, error) {
	recordPath := strings.TrimSpace(os.Getenv(runtimewiring.EnvAuditRecord))
	if recordPath == "" {
		return nil, nil
	}
	binPath, err := resolveAuditBin()
	if err != nil {
		return nil, fmt.Errorf("orchestrate: resolve audit binary: %w", err)
	}
	return audit.NewBlockSink(binPath, recordPath), nil
}

// policyClientFromEnv constructs the PolicyClient and (when a daemon is started)
// returns a stop func. When AGENT_BUILDER_POLICY_BIN is unset, a fail-closed
// always-deny client is returned: the orchestrate path requires an explicit policy
// engine to permit a spawn, mirroring the spawn-decided deny-on-error invariant.
func policyClientFromEnv(logger *slog.Logger) (orchestrator.PolicyClient, func(), error) {
	binPath := strings.TrimSpace(os.Getenv(runtimewiring.EnvPolicyBin))
	if binPath == "" {
		logger.Warn("policy engine not configured — orchestrate denies all spawns (fail-closed)",
			"missing_config", runtimewiring.EnvPolicyBin)
		return denyAllPolicy{}, func() {}, nil
	}

	socketPath := strings.TrimSpace(os.Getenv(runtimewiring.EnvPolicySocket))
	if socketPath == "" {
		socketPath = filepathJoinTemp(fmt.Sprintf("agent-builder-orchestrate-policy-%d.sock", os.Getpid()))
	}

	daemon := &policy.PolicyDaemon{BinPath: binPath, SocketPath: socketPath}
	if err := daemon.Start(context.Background()); err != nil {
		return nil, func() {}, fmt.Errorf("orchestrate: start policy daemon: %w", err)
	}
	stop := func() { _ = daemon.Stop() }
	return policy.NewClient(socketPath), stop, nil
}

// denyAllPolicy is the fail-closed PolicyClient used when no policy engine is
// configured: every decision is deny. Decide never errors (the orchestrator routes
// on resp.Decision), so this is the safe default — no spawn proceeds without an
// explicit policy allow.
type denyAllPolicy struct{}

func (denyAllPolicy) Decide(policy.DecideRequest) (policy.DecideResponse, error) {
	return policy.DecideResponse{Decision: policy.DecisionDeny}, nil
}

func orchestrateUsage(w io.Writer) {
	write(w, `Usage: agent-builder orchestrate

Drives the Tier-1 orchestrator: reads goals from the configured inbound source,
decomposes each into a plan, gates it on the spawn-plan / spawn-worker policy
decisions, and dispatches one worker per approved sub-goal.

Required environment:
  AGENT_BUILDER_WORKER_SIGNING_KEY   orchestrator Ed25519 signing key (fail-closed at startup)

Optional environment:
  AGENT_BUILDER_PLANNER              "structured" (default) | "llm" (ollama-native; cloud harnesses fail closed)
  AGENT_BUILDER_MEMORY_GUARD_BIN     memory-guard binary (else in-memory PlanStore + warning)
  AGENT_BUILDER_POLICY_BIN           policy-engine binary (else all spawns denied)
  AGENT_BUILDER_AUDIT_RECORD         audit chain logfile (shared orchestrator+worker sink)
  AGENT_BUILDER_AUDIT_BIN            audit-trail binary
`)
}
