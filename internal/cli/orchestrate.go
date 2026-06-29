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
	"strings"

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
	// source is the goal-intake seam the loop reads from.
	source supervisor.GoalSource
	// stdout receives the user-visible per-goal summary.
	stdout io.Writer
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

	if err := runGoalIntakeLoop(context.Background(), oc); err != nil {
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
		dispatch = newTransportDispatch(signingKey, workItemCache, resultCache, auditSink, logger)
	}

	// 9. Reporter — wired to the outbound channel seam (task 098); falls back to a
	//    stderr log reporter when no outbound channel is configured.
	reporter := newLogReporter(config.Stdout)

	orch := orchestrator.New(
		planner, pol, reporter, baseConfig,
		orchestrator.WithPlanStore(store),
		orchestrator.WithAuditSink(auditSink),
		orchestrator.WithDispatchFunc(dispatch),
	)

	source := ov.source
	if source == nil {
		source = newEnvGoalSource(os.Getenv, config.Stdin)
	}

	return orchestrateConfig{orch: orch, source: source, stdout: config.Stdout}, cleanup, nil
}

// runGoalIntakeLoop reads goals one at a time from the GoalSource and drives
// Handle for each. A goal is processed until the source signals no more goals
// (ok=false) or returns an error.
func runGoalIntakeLoop(ctx context.Context, oc orchestrateConfig) error {
	for {
		goal, ok, err := oc.source.Next()
		if err != nil {
			return fmt.Errorf("orchestrate: read goal: %w", err)
		}
		if !ok {
			return nil
		}
		result, err := oc.orch.Handle(ctx, goal)
		if err != nil {
			return fmt.Errorf("orchestrate: handle goal %q: %w", goal.ID, err)
		}
		writef(oc.stdout, "%s\n", orchestrator.RenderPlanResult(result))
	}
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
