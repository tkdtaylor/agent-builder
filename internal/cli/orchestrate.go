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
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// EnvPlanner selects the Planner the orchestrate subcommand assembles. The only
// live-path value is "structured" (the default StructuredPlanner). "llm" is
// reserved for task 100 and currently returns a clear "not yet available" error —
// this task does not build the LLM planner (099 / 100 scope split).
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

// ErrPlannerNotAvailable is returned when AGENT_BUILDER_PLANNER selects a planner
// whose concrete is not yet wired (currently "llm", pending task 100).
var ErrPlannerNotAvailable = errors.New("orchestrate: selected planner is not yet available")

// orchestrateConfig carries the assembled orchestrator wiring. Its fields are
// populated by assembleOrchestrator from environment configuration on the live
// path; tests construct it directly with overrides (a shared ReplayCache, a stub
// PolicyClient, a spy DispatchFunc, a FakeSink, a stub GoalSource) to assert the
// security invariants without a full integration harness.
type orchestrateConfig struct {
	// orch is the fully-assembled Tier-1 coordinator.
	orch *orchestrator.Orchestrator
	// source is the goal-intake seam the control loop reads from. The control loop
	// is the ONLY reader of this seam (no concurrent Next() races — ADR 054 §1).
	source supervisor.GoalSource
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
	source       supervisor.GoalSource
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
		writef(config.Stderr, "error: %v\n", err)
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

	// 4. Planner (default StructuredPlanner; "llm" reserved for task 100).
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
	//    stderr log reporter when no outbound channel is configured.
	reporter := newLogReporter(config.Stdout)

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

	orch := orchestrator.New(
		planner, pol, reporter, baseConfig,
		orchestrator.WithPlanStore(store),
		orchestrator.WithAuditSink(auditSink),
		orchestrator.WithDispatchFunc(dispatch),
		orchestrator.WithStatusRegistry(registry),
		orchestrator.WithWorkerSemaphore(workerSem),
	)

	source := ov.source
	if source == nil {
		source = newEnvGoalSource(os.Getenv, config.Stdin)
	}

	return orchestrateConfig{
		orch:     orch,
		source:   source,
		stdout:   config.Stdout,
		registry: registry,
		maxGoals: maxGoals,
	}, cleanup, nil
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

	var wg sync.WaitGroup
	var srcErr error

	for {
		goal, ok, err := oc.source.Next()
		if err != nil {
			srcErr = fmt.Errorf("orchestrate: read goal: %w", err)
			break
		}
		if !ok {
			break
		}

		// Register the goal as Queued BEFORE spawning its actor, so a status read
		// (or the admission check) never sees a goal that has been accepted but not
		// yet projected (register-then-start ordering, ADR 054 §271b).
		oc.registry.Register(goal.ID, orchestrator.StateQueued)

		wg.Add(1)
		go func(goal supervisor.Task) {
			defer wg.Done()
			// Acquire a goal-admission slot. While the fleet is at maxGoals live
			// goals this blocks — the goal stays Queued in the registry (Handle has
			// not run, so no Planning transition). When a slot frees, the actor
			// proceeds and Handle transitions Queued → Planning → … itself.
			select {
			case admit <- struct{}{}:
			case <-ctx.Done():
				oc.registry.SetState(goal.ID, orchestrator.StateFailed)
				return
			}
			defer func() { <-admit }()

			// The actor owns the goal's lifecycle via Handle. A goal-level error never
			// halts the fleet (best-effort across goals — ADR 054 §1): the registry
			// already recorded StateFailed inside Handle, and the orchestrator's
			// Reporter (the single serialized stdout owner — orchestrate_seams.go
			// logReporter) emits the per-goal summary / denial. The control loop does
			// NOT write stdout itself: M actors run concurrently and a plain io.Writer
			// is not safe for concurrent writes; routing all user-visible output
			// through the one mutex-guarded Reporter is what keeps stdout race-free.
			if _, err := oc.orch.Handle(ctx, goal); err != nil {
				_ = err // recorded in the registry as StateFailed; surfaced via status (task 114)
			}
		}(goal)
	}

	wg.Wait()
	return srcErr
}

// plannerFromEnv selects the Planner per AGENT_BUILDER_PLANNER. "structured"
// (default) returns the StructuredPlanner. "llm" is reserved for task 100 and
// returns ErrPlannerNotAvailable.
func plannerFromEnv() (orchestrator.Planner, error) {
	choice := strings.TrimSpace(os.Getenv(EnvPlanner))
	switch choice {
	case "", plannerStructured:
		return orchestrator.NewStructuredPlanner(), nil
	case plannerLLM:
		return nil, fmt.Errorf("%s=%q: %w (pending task 100 — use %q)", EnvPlanner, plannerLLM, ErrPlannerNotAvailable, plannerStructured)
	default:
		return nil, fmt.Errorf("%s=%q is not a known planner (want %q or %q)", EnvPlanner, choice, plannerStructured, plannerLLM)
	}
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
  AGENT_BUILDER_PLANNER              "structured" (default) | "llm" (pending task 100)
  AGENT_BUILDER_MEMORY_GUARD_BIN     memory-guard binary (else in-memory PlanStore + warning)
  AGENT_BUILDER_POLICY_BIN           policy-engine binary (else all spawns denied)
  AGENT_BUILDER_AUDIT_RECORD         audit chain logfile (shared orchestrator+worker sink)
  AGENT_BUILDER_AUDIT_BIN            audit-trail binary
`)
}
