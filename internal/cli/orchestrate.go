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

	"encoding/hex"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram/authz"
	"github.com/tkdtaylor/agent-builder/internal/channel/worker"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/executor"
	"github.com/tkdtaylor/agent-builder/internal/ingestion"
	"github.com/tkdtaylor/agent-builder/internal/loop"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	llmplanner "github.com/tkdtaylor/agent-builder/internal/orchestrator/planner"
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

const EnvClarifier = "AGENT_BUILDER_CLARIFIER"

const (
	clarifierHeuristic = "heuristic"
	clarifierLLM       = "llm"
)

// EnvInbound selects the inbound channel for the orchestrate subcommand (ADR 054 §2,
// task 117). Unset or "env" selects the default env/stdin line-oriented MessageSource
// (the local-test default). "telegram" selects the Telegram bot channel adapter
// (telegram.Adapter), which derives kind/GoalID at the adapter edge from verified
// plaintext + reply-to threading. Any other value is a fail-fast configuration error
// (ExitUsage). Telegram requires the full set of AGENT_BUILDER_TELEGRAM_* env vars;
// missing required vars fail-fast at assembly time before the goal-intake loop runs.
const EnvInbound = "AGENT_BUILDER_INBOUND"
const EnvIntake = "AGENT_BUILDER_INTAKE"
const EnvRequireApproval = "AGENT_BUILDER_REQUIRE_APPROVAL"

const (
	inboundEnv      = "env"
	inboundTelegram = "telegram"
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

// Telegram inbound channel env vars (task 117, ADR 054 §2). All are required when
// AGENT_BUILDER_INBOUND=telegram; missing any is a fail-fast assembly error.
//
//   - AGENT_BUILDER_TELEGRAM_BOT_TOKEN   — Telegram Bot API token (secret; never logged)
//   - AGENT_BUILDER_TELEGRAM_BASE_URL    — Bot API base URL (default: https://api.telegram.org)
//   - AGENT_BUILDER_TELEGRAM_SIGNING_KEY — operator Ed25519 public key (hex-encoded 32 bytes)
//   - AGENT_BUILDER_TELEGRAM_X25519_PUB  — operator X25519 public key (hex-encoded 32 bytes)
//   - AGENT_BUILDER_TELEGRAM_ORCH_PRIV   — orchestrator X25519 private key (hex-encoded 32 bytes; secret; never logged)
//   - AGENT_BUILDER_TELEGRAM_CHAT_ID     — Telegram chat ID the ReplyAdapter sends to
//
// Inbound (envelope-verify+armor+kind-derivation) and outbound (ReplyAdapter sealing)
// use the same key material already present in the adapter. This is the FIRST live
// inbound wiring of the Telegram adapter; when no armor binary is configured the
// content guard defaults to a fail-open allowAllContentGuard (the load-bearing trust
// gate on this path is the envelope Ed25519-verify + X25519-decrypt + replay-cache
// pipeline, which is always enforced — armor is an additional injection filter over
// already-authenticated operator plaintext). Operators SHOULD wire a real armor guard
// here before production; see the operator follow-up note on this seam.
const (
	EnvTelegramBotToken   = "AGENT_BUILDER_TELEGRAM_BOT_TOKEN"
	EnvTelegramBaseURL    = "AGENT_BUILDER_TELEGRAM_BASE_URL"
	EnvTelegramSigningKey = "AGENT_BUILDER_TELEGRAM_SIGNING_KEY"
	EnvTelegramX25519Pub  = "AGENT_BUILDER_TELEGRAM_X25519_PUB"
	EnvTelegramOrchPriv   = "AGENT_BUILDER_TELEGRAM_ORCH_PRIV"
	EnvTelegramChatID     = "AGENT_BUILDER_TELEGRAM_CHAT_ID"

	// EnvTelegramOrchEdPriv is the orchestrator's Ed25519 private key (hex-encoded 64
	// bytes) used by ReplyAdapter to sign outbound envelopes. Required for outbound replies.
	EnvTelegramOrchEdPriv = "AGENT_BUILDER_TELEGRAM_ORCH_ED_PRIV"
	// EnvTelegramOpX25519Pub is the operator's X25519 public key (hex-encoded 32 bytes)
	// used by ReplyAdapter to seal outbound envelopes for the operator. Required for
	// outbound replies.
	EnvTelegramOpX25519Pub = "AGENT_BUILDER_TELEGRAM_OP_X25519_PUB"

	// EnvTelegramAuthMode selects the inbound sender-ID auth mode (ADR 063). Unset (or
	// "envelope") reproduces today's adapter exactly. Recognized: envelope, allowlist,
	// pairing, open, disabled. An unrecognized value is a fail-fast ExitUsage error.
	EnvTelegramAuthMode = "AGENT_BUILDER_TELEGRAM_AUTH_MODE"
	// EnvTelegramApprovedStore is the direct path to the persisted 0600 JSON
	// approved-sender store (ADR 063 Decision 4). Required (fail-fast if blank) for any
	// mode that consults sender-ID approval (allowlist; pairing in task 152); ignored by
	// envelope/disabled/open.
	EnvTelegramApprovedStore = "AGENT_BUILDER_TELEGRAM_APPROVED_STORE"
	// EnvTelegramApprovedIDs is the static, comma-separated list of approved numeric
	// sender IDs seeded into the store at startup in allowlist mode (ADR 063 Decision 4).
	// Seeding is additive (union), never a destructive overwrite of an existing store.
	EnvTelegramApprovedIDs = "AGENT_BUILDER_TELEGRAM_APPROVED_IDS"
	// EnvTelegramOwnerID is the numeric Telegram sender ID of the owner who can approve/deny
	// pending senders in pairing mode (ADR 063 Decision 3). REQUIRED (fail-fast ExitUsage)
	// when AUTH_MODE=pairing — an owner-less pairing mode has no one who can ever approve a
	// sender. It is normalized to the same canonical numeric form as approved-store entries.
	// Ignored by every other mode.
	EnvTelegramOwnerID = "AGENT_BUILDER_TELEGRAM_OWNER_ID"
)

// defaultTelegramBaseURL is the Telegram Bot API base URL used when
// AGENT_BUILDER_TELEGRAM_BASE_URL is unset.
const defaultTelegramBaseURL = "https://api.telegram.org"

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
	// statusWriter, when non-nil, is used as the task status writer for blocked-action
	// reevaluation (task 123, ADR 055 seam 4). When nil, assembleOrchestrate constructs
	// one from baseConfig.TaskRoot. Tests may inject a spy to assert escalation writes.
	statusWriter loop.StatusWriter
	// onPlanner, when non-nil, is invoked with the exact Planner value that
	// assembleOrchestrate hands to orchestrator.New — the producer→consumer link a
	// test needs to assert that the env-selected planner (e.g. *llmplanner.LLMPlanner
	// under AGENT_BUILDER_PLANNER=llm) actually survives the control-plane assembly
	// and reaches the orchestrator (TC-110-05). Production passes assembleOverrides{}
	// so this is nil on the live path.
	onPlanner func(orchestrator.Planner)
	// onStatusWriter, when non-nil, is invoked with the exact StatusWriter value that
	// assembleOrchestrate constructs and hands to orchestrator.New — the producer→consumer
	// link a test needs to assert that the status writer (e.g. *tasksource.StatusWriter
	// constructed from baseConfig.TaskRoot) actually survives the control-plane assembly
	// and reaches the orchestrator (TC-123 / task 123, ADR 055 seam 4). Production passes
	// assembleOverrides{} so this is nil on the live path.
	onStatusWriter func(loop.StatusWriter)
	clarifier      orchestrator.Clarifier
	getenv         func(string) string
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

	// 8. Reporter — assembled BEFORE the dispatch seam (step 9) so the transport
	//    dispatch closure can report the worker's real outcome to the operator (ADR 055
	//    seam 3, task 120). Falls back to a log reporter when no outbound channel is
	//    configured. The SAME reporter instance is handed to the orchestrator core AND
	//    held by the control-loop router (status answers + graceful "no such goal"
	//    reports — ADR 054 §2), so all user-visible output flows through the one
	//    mutex-guarded stdout owner.
	//
	//    NOTE: when AGENT_BUILDER_INBOUND=telegram is selected (step 11), the reporter
	//    is replaced with the Telegram ReplyAdapter for the orchestrator's plan-summary
	//    and status paths. The dispatch closure retains this pre-Telegram reporter for
	//    per-sub-goal progress reports; a future task may wire the Telegram adapter into
	//    the dispatch closure if per-sub-goal Telegram notifications are desired.
	reporter := ov.reporter
	if reporter == nil {
		reporter = newLogReporter(config.Stdout)
	}

	// 9. Dispatch seam. Both shared ReplayCaches (one per direction) are threaded in
	//    here so the assembled live dispatch path replay-checks every work-item AND
	//    every returned result against the ONE long-lived cache for that direction
	//    (083 SEC-001). A nil override means the env-backed dispatch that round-trips
	//    the work-item + result through the worker transport before declaring success.
	//    The reporter (step 8) is threaded in so the closure can report the worker's
	//    real outcome (ADR 055 seam 3, task 120).
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
		d, keyErr := newTransportDispatch(signingKey, workItemCache, resultCache, auditSink, logger, reporter)
		if keyErr != nil {
			cleanup()
			return orchestrateConfig{}, noop, fmt.Errorf("orchestrate: %w", keyErr)
		}
		dispatch = d
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

	// 10b. Status writer for blocked-action reevaluation escalation (ADR 055 seam 4,
	//      task 123, updated in task 130). The orchestrate path constructs a
	//      reporterStatusWriter routing escalation text to the reporter (REQ-130-03).
	statusWriter := ov.statusWriter
	if statusWriter == nil {
		statusWriter = &reporterStatusWriter{reporter: reporter}
	}

	// Test seam (TC-123 / task 123): observe the exact StatusWriter the assembly feeds into
	// orchestrator.New, proving the constructed status writer survives the control loop.
	if ov.onStatusWriter != nil {
		ov.onStatusWriter(statusWriter)
	}

	// 10c. getenv selection
	getenv := ov.getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	// 10d. Clarifier and GoalAnalyzer LLM seams (shared) (REQ-131-03, REQ-142-03)
	// Build seams upfront if either clarifier or analyzer needs them.
	clarifierChoice := strings.TrimSpace(getenv(EnvClarifier))
	analyzerChoice := strings.ToLower(strings.TrimSpace(getenv(EnvGoalAnalysis)))
	needsSeams := (clarifierChoice == clarifierLLM) || (analyzerChoice == "llm")

	var llmResolver llmplanner.ExecutorResolver
	var llmInvoke llmplanner.Invoker
	if needsSeams {
		res, inv, err := buildLLMSeams()
		if err != nil {
			cleanup()
			return orchestrateConfig{}, noop, err
		}
		llmResolver = res
		llmInvoke = inv
	}

	// 10e. Clarifier selection (REQ-131-03)
	clarifier := ov.clarifier
	if clarifier == nil {
		clar, err := clarifierFromEnv(getenv, llmResolver, llmInvoke)
		if err != nil {
			cleanup()
			return orchestrateConfig{}, noop, err
		}
		clarifier = clar
	}

	// 10f. requireApproval selection: default true, false on lenient false values
	requireApproval := true
	rawRequireApproval := strings.ToLower(strings.TrimSpace(getenv(EnvRequireApproval)))
	if rawRequireApproval == "false" || rawRequireApproval == "0" || rawRequireApproval == "no" {
		requireApproval = false
	}

	orch := orchestrator.New(
		planner, pol, reporter, baseConfig,
		orchestrator.WithPlanStore(store),
		orchestrator.WithAuditSink(auditSink),
		orchestrator.WithDispatchFunc(dispatch),
		orchestrator.WithStatusRegistry(registry),
		orchestrator.WithWorkerSemaphore(workerSem),
		orchestrator.WithStatusWriter(statusWriter),
		orchestrator.WithClarifier(clarifier),
		orchestrator.WithGetEnv(getenv),
		orchestrator.WithRequireApproval(requireApproval),
		// ADR 060: when AGENT_BUILDER_GOAL_ANALYSIS is enabled, classify the goal at
		// intake and answer general questions over the channel. Nil analyzer (default)
		// = every goal is coding (pre-060 behavior). The answerer routes to a brain by
		// complexity via the single-shot Completer.
		orchestrator.WithGoalAnalyzer(goalAnalyzerFromEnv(getenv, llmResolver, llmInvoke)),
		orchestrator.WithAnswerer(cliAnswerer{}),
	)

	// Inbound message seam (ADR 054 §2). Precedence: an explicit typed MessageSource
	// override → a goal-only GoalSource override adapted to messages → the live
	// channel selected by AGENT_BUILDER_INBOUND (telegram or env/stdin default).
	source := ov.messageSource
	switch {
	case source != nil:
		// use the injected typed source
	case ov.source != nil:
		source = goalSourceAsMessages(ov.source)
	default:
		inboundSrc, inboundReporter, err := inboundFromEnv(os.Getenv, config.Stdin, auditSink, logger, config.Stderr)
		if err != nil {
			cleanup()
			return orchestrateConfig{}, noop, fmt.Errorf("orchestrate: inbound channel: %w", err)
		}
		source = inboundSrc
		// When Telegram is selected, override the reporter with the Telegram ReplyAdapter
		// so acks/status/results flow back over the encrypted channel (ADR 054 §2).
		if inboundReporter != nil && ov.reporter == nil {
			reporter = inboundReporter
		}
	}

	// Status handler (task 114): wire the live registry-read + Reporter-answer
	// handler when no test override is provided. The handler captures
	// context.Background() — a status read is a non-blocking registry snapshot
	// that does not need the control-loop's cancellation signal (ADR 054 §3:
	// status is immediate; it never waits on a goal actor).
	statusHandler := ov.statusHandler
	if statusHandler == nil {
		statusHandler = newStatusHandler(context.Background(), registry, reporter)
	}

	return orchestrateConfig{
		orch:          orch,
		source:        source,
		stdout:        config.Stdout,
		registry:      registry,
		maxGoals:      maxGoals,
		reporter:      reporter,
		statusHandler: statusHandler,
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

	// shutdown signals goal actors to stop draining their command mailbox once the
	// source is exhausted. It is a DRAIN-ONLY signal, distinct from the dispatch
	// context: an actor LINGERING at an AwaitingApproval checkpoint (kept alive to
	// drain info — apply-info-at-checkpoint, task 115) stops on this close, while an
	// in-flight DISPATCH is NOT cancelled by it (the held worker finishes as-is —
	// cancellation/teardown is task 116). No approval/cancel channel is wired in this
	// task; live approval delivery is L6/operator.
	shutdown := make(chan struct{})

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
			oc.routeNewGoal(ctx, msg.Goal, mailboxes, admit, &wg, shutdown)
		case supervisor.MsgStatus:
			oc.routeStatus(ctx, msg.GoalID)
		case supervisor.MsgInfo, supervisor.MsgCancel, supervisor.MsgConfirm:
			oc.routeCommand(ctx, msg, mailboxes)
		default:
			oc.report(ctx, fmt.Sprintf("ignored message of unknown kind: %v", msg.Kind))
		}
	}

	// Source exhausted (or hard error): signal lingering AwaitingApproval actors to
	// stop draining, then join. An in-flight dispatch is NOT interrupted — it finishes
	// as-is (task 116 owns teardown).
	close(shutdown)
	wg.Wait()
	return srcErr
}

// routeNewGoal handles a MsgNewGoal: create the goal's command mailbox, register it
// Queued, then spawn the goal actor (register-then-start ordering — ADR 054 §6 race
// surface (b): the mailbox and registry entry exist before the actor starts, so an
// info/cancel arriving at actor startup is delivered, not lost).
func (oc orchestrateConfig) routeNewGoal(ctx context.Context, goal supervisor.Task, mailboxes *commandMailboxes, admit chan struct{}, wg *sync.WaitGroup, shutdown <-chan struct{}) {
	// Mailbox BEFORE registration/start: a cancel/info for this goal that arrives
	// while the actor is still booting must find a mailbox.
	mailboxes.Create(goal.ID)

	// Register the goal as Queued BEFORE spawning its actor, so a status read (or the
	// admission check) never sees a goal that has been accepted but not yet projected.
	oc.registry.Register(goal.ID, orchestrator.StateQueued)

	// Per-goal cancel context (ADR 054 §5, task 116): derive ONE context.WithCancel
	// per goal from the control-loop ctx and register its CancelFunc BEFORE spawning
	// the actor (register-then-start), so a `cancel <goalID>` that races actor startup
	// still finds a cancel handle. The derived ctx threads through the actor → Handle
	// → dispatchPlan → runtime.Run → Supervisor.Run to the run-loop's ctx.Done() arm,
	// so cancelling it tears down ONLY this goal's in-flight workers — siblings derive
	// independent contexts from the same parent (no blast radius).
	goalCtx, cancel := context.WithCancel(ctx)
	oc.registry.SetCancelFunc(goal.ID, cancel)

	// The goal actor (task 115) owns the goal's lifecycle: it acquires the admission
	// slot, runs Handle, and concurrently drains the command mailbox at checkpoint
	// boundaries (apply-info-at-checkpoint). It does its own wg.Add(1)/Done. It runs
	// under goalCtx so a cancel propagates to its in-flight dispatch.
	oc.runGoalActor(goalCtx, goal, mailboxes, admit, wg, shutdown)
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

// routeCommand dispatches a MsgInfo/MsgCancel/MsgConfirm to the addressed goal's command
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

// clarifierFromEnv selects the Clarifier (default: HeuristicClarifier) (REQ-131-03).
func clarifierFromEnv(getenv func(string) string, resolver llmplanner.ExecutorResolver, invoke llmplanner.Invoker) (orchestrator.Clarifier, error) {
	choice := strings.TrimSpace(getenv(EnvClarifier))
	switch choice {
	case "", clarifierHeuristic:
		return orchestrator.NewHeuristicClarifier(), nil
	case clarifierLLM:
		if resolver == nil || invoke == nil {
			return nil, fmt.Errorf("clarifierFromEnv: LLM seams not configured")
		}
		return llmplanner.NewLLMClarifier(resolver, invoke), nil
	default:
		// Wrap errUsageConfig so runOrchestrate returns ExitUsage instead of ExitGeneric.
		return nil, fmt.Errorf("%w: %s=%q is not a known clarifier (want %q or %q)", errUsageConfig, EnvClarifier, choice, clarifierHeuristic, clarifierLLM)
	}
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

// buildLLMSeams constructs the shared resolver and invoker for LLM-backed planning
// and clarification.
func buildLLMSeams() (llmplanner.ExecutorResolver, llmplanner.Invoker, error) {
	// 1. Build catalog from env, with synthetic-default fallback.
	entries, err := registry.LoadFromEnv()
	if err != nil {
		return nil, nil, fmt.Errorf("load registry: %w", err)
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
	invoke := llmplanner.Invoker(func(ctx context.Context, entry registry.RegistryEntry, prompt string) (string, error) {
		c, err := executor.CompleterForEntry(entry)
		if err != nil {
			return "", err
		}
		return c.Complete(ctx, entry, prompt)
	})

	return routerResolver, invoke, nil
}

// buildLLMPlanner assembles the LLM-backed planner: catalog → router →
// ExecutorResolver adapter + Invoker closure → planner.NewPlannerFromEnv.
func buildLLMPlanner() (orchestrator.Planner, error) {
	resolver, invoke, err := buildLLMSeams()
	if err != nil {
		return nil, fmt.Errorf("plannerFromEnv: %w", err)
	}

	return llmplanner.NewPlannerFromEnv(resolver, invoke)
}

// routerResolverAdapter implements llmplanner.ExecutorResolver by wrapping
// *router.Router.Select. Resolve drops the ctx argument because router.Select
// takes no context — selection is not context-cancellable today. This is a
// deliberate, documented limitation (ADR 053 §3 / TC-110-03): a cancelled ctx
// does not change the result; the router still returns the selected entry (or
// ErrNoEligibleExecutor). A future cancellable Select can add ctx here without
// changing the llmplanner.ExecutorResolver contract.
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
//
// When a policy binary IS configured, a *planScopedPolicy is returned. It does NOT
// start the daemon at assembly time (no plan exists yet): the daemon is launched —
// with the plan-derived allow set (ADR 055 seam 1, task 122) — the first time the
// orchestrator hands it an admitted plan via ConfigureForPlan. Until then every
// Decide is fail-closed deny (no daemon, no allow). The optional deployment base
// allow (AGENT_BUILDER_POLICY_ALLOW) intersects the plan-derived set so deployment
// can only narrow, never widen, what a plan authorizes.
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

	psp := &planScopedPolicy{
		binPath:    binPath,
		socketPath: socketPath,
		base:       parseAllowBase(os.Getenv(runtimewiring.EnvPolicyAllow)),
		newDaemon:  startPolicyDaemon,
	}
	return psp, psp.stop, nil
}

// parseAllowBase parses the comma-separated AGENT_BUILDER_POLICY_ALLOW deployment
// base into a trimmed, empty-dropped slice. A whitespace-only or empty value yields
// nil (no base → no narrowing).
func parseAllowBase(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// effectiveAllow computes the policy-daemon allow set for a plan: the plan-derived
// resources (Plan.AllowedResources) when the deployment base is empty/whitespace-only,
// else the INTERSECTION of the plan-derived set with the base. The deployment base
// can only NARROW — it never adds a resource the plan did not declare, and the plan
// can never widen beyond the base. The result preserves the plan-derived ordering
// (deterministic: goal ID first, then recipe names and task IDs in sub-goal order).
//
// Fail-closed corollary: a base disjoint from the plan yields an empty set, so the
// daemon is launched with an empty --allow and denies every one of this plan's spawns.
func effectiveAllow(plan orchestrator.Plan, base []string) []string {
	derived := plan.AllowedResources()
	if len(base) == 0 {
		return derived
	}
	inBase := make(map[string]struct{}, len(base))
	for _, b := range base {
		inBase[b] = struct{}{}
	}
	out := make([]string, 0, len(derived))
	for _, r := range derived {
		if _, ok := inBase[r]; ok {
			out = append(out, r)
		}
	}
	return out
}

// daemonStarter launches a policy daemon configured with allow and returns a handle
// exposing Stop. It is a seam so tests can record the --allow argv without execing a
// real policy-engine binary. The production implementation is startPolicyDaemon.
type daemonStarter func(binPath, socketPath string, allow []string) (policyDaemon, error)

// policyDaemon is the narrow lifecycle handle planScopedPolicy needs (Stop only;
// Start is performed by the daemonStarter). *policy.PolicyDaemon satisfies it.
type policyDaemon interface {
	Stop() error
}

// startPolicyDaemon is the production daemonStarter: it execs the policy-engine
// daemon with --allow set to the plan-derived (intersected) resource set and blocks
// until it is reachable, mirroring the L6-proven run path (internal/runtime/run.go).
func startPolicyDaemon(binPath, socketPath string, allow []string) (policyDaemon, error) {
	daemon := &policy.PolicyDaemon{BinPath: binPath, SocketPath: socketPath, Allow: allow}
	if err := daemon.Start(context.Background()); err != nil {
		return nil, err
	}
	return daemon, nil
}

// planScopedPolicy is the orchestrate-path PolicyClient that owns the policy daemon
// lifecycle and feeds it the PLAN-DERIVED allow set (ADR 055 seam 1, task 122). It
// implements orchestrator.PlanScoper: the orchestrator calls ConfigureForPlan with
// the admitted plan before issuing that plan's decisions, and planScopedPolicy
// (re)starts the daemon with effectiveAllow(plan, base) so the independent engine
// ALLOWS exactly the resources the plan declared.
//
// Until ConfigureForPlan runs there is no daemon and no underlying client, so every
// Decide is fail-closed deny — the orchestrate path never permits a spawn without a
// plan-scoped engine behind it.
type planScopedPolicy struct {
	binPath    string
	socketPath string
	base       []string
	newDaemon  daemonStarter

	mu      sync.Mutex
	daemon  policyDaemon
	client  *policy.PolicyClient
	lastSet string // joined effective-allow set of the daemon currently serving
}

// ConfigureForPlan (re)launches the policy daemon with this plan's effective allow
// set. When a daemon is already serving the identical set it is reused; otherwise the
// running daemon is stopped and a new one launched with the new --allow. An empty
// effective set is valid and intended: the daemon is launched with an empty --allow
// so it denies every spawn (fail-closed, ADR 055 / TC-004).
func (p *planScopedPolicy) ConfigureForPlan(plan orchestrator.Plan) error {
	allow := effectiveAllow(plan, p.base)
	key := strings.Join(allow, "\x00")

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.daemon != nil && p.client != nil && key == p.lastSet {
		return nil // already serving this exact set
	}
	if p.daemon != nil {
		_ = p.daemon.Stop()
		p.daemon = nil
		p.client = nil
	}

	daemon, err := p.newDaemon(p.binPath, p.socketPath, allow)
	if err != nil {
		return fmt.Errorf("orchestrate: start policy daemon: %w", err)
	}
	p.daemon = daemon
	p.client = policy.NewClient(p.socketPath)
	p.lastSet = key
	return nil
}

// Decide routes to the daemon-backed client when a plan has been configured. Before
// ConfigureForPlan (no daemon yet) it is fail-closed deny — no spawn proceeds without
// a plan-scoped engine.
func (p *planScopedPolicy) Decide(req policy.DecideRequest) (policy.DecideResponse, error) {
	p.mu.Lock()
	client := p.client
	p.mu.Unlock()
	if client == nil {
		return policy.DecideResponse{Decision: policy.DecisionDeny}, nil
	}
	return client.Decide(req)
}

// stop tears down the running daemon (if any). Safe to call when no daemon ran.
func (p *planScopedPolicy) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.daemon != nil {
		_ = p.daemon.Stop()
		p.daemon = nil
		p.client = nil
	}
}

// denyAllPolicy is the fail-closed PolicyClient used when no policy engine is
// configured: every decision is deny. Decide never errors (the orchestrator routes
// on resp.Decision), so this is the safe default — no spawn proceeds without an
// explicit policy allow.
type denyAllPolicy struct{}

func (denyAllPolicy) Decide(policy.DecideRequest) (policy.DecideResponse, error) {
	return policy.DecideResponse{Decision: policy.DecisionDeny}, nil
}

// inboundFromEnv constructs the inbound MessageSource and optional outbound Reporter
// from environment configuration. The returned reporter is non-nil only when
// AGENT_BUILDER_INBOUND=telegram — callers replace the default log reporter with it
// so acks/status/results flow over the encrypted Telegram channel (ADR 054 §2).
//
// When AGENT_BUILDER_INBOUND is unset or "env", the env/stdin line-oriented source
// is returned with a nil reporter (the caller keeps its log-reporter default).
//
// When AGENT_BUILDER_INBOUND=telegram, a telegram.Adapter (MessageSource) and
// telegram.ReplyAdapter (Reporter) are built from the AGENT_BUILDER_TELEGRAM_*
// env vars. Missing required vars are a fail-fast assembly error.
//
// warnOut receives the mandatory footgun WARNING (ADR 063 Decision 1 / task 153) when
// the resolved Telegram auth mode is "open" — see assembleTelegramInbound. Production
// passes config.Stderr; a nil warnOut is tolerated (the warning is simply discarded,
// never a panic) so existing callers that do not care about the warning still compile.
func inboundFromEnv(getenv func(string) string, stdin io.Reader, sink audit.Sink, logger *slog.Logger, warnOut io.Writer) (supervisor.MessageSource, supervisor.Reporter, error) {
	choice := strings.TrimSpace(getenv(EnvInbound))
	switch choice {
	case "", inboundEnv:
		return newEnvMessageSource(getenv, stdin), nil, nil

	case inboundTelegram:
		src, rep, err := assembleTelegramInbound(getenv, sink, logger, warnOut)
		if err != nil {
			return nil, nil, err
		}
		return src, rep, nil

	default:
		return nil, nil, fmt.Errorf("%w: %s=%q is not a known inbound channel (want %q or %q)",
			errUsageConfig, EnvInbound, choice, inboundEnv, inboundTelegram)
	}
}

// assembleTelegramAuthMode reads AGENT_BUILDER_TELEGRAM_AUTH_MODE and, for modes that
// consult sender-ID approval, constructs + seeds the persisted approved-sender store
// from AGENT_BUILDER_TELEGRAM_APPROVED_STORE / _APPROVED_IDS (ADR 063 Decisions 1/4/5).
//
// Fail-fast (errUsageConfig ⇒ ExitUsage), never a nil-adapter panic at first Next():
//   - an unrecognized AUTH_MODE value is an error (ParseMode);
//   - a blank APPROVED_STORE path in a store-consulting mode (allowlist/pairing) is an error;
//   - a malformed existing store file, an unwritable store path, or a non-numeric static
//     approved ID is an error.
//
// Returns the resolved mode and the store (nil for envelope/disabled/open, which never
// read the store). Seeding is additive (union): it loads any existing store, adds the
// static IDs, and persists — it never removes IDs already on disk, so task 152's pairing
// mode can grow the same store in-chat without a later allowlist restart wiping it.
func assembleTelegramAuthMode(getenv func(string) string) (authz.Mode, *authz.Store, error) {
	mode, err := authz.ParseMode(getenv(EnvTelegramAuthMode))
	if err != nil {
		return "", nil, fmt.Errorf("%w: %s: %v", errUsageConfig, EnvTelegramAuthMode, err)
	}

	if !mode.ConsultsStore() {
		// envelope / disabled / open never read the approved store.
		return mode, nil, nil
	}

	storePath := strings.TrimSpace(getenv(EnvTelegramApprovedStore))
	if storePath == "" {
		return "", nil, fmt.Errorf("%w: %s is required when %s=%s", errUsageConfig, EnvTelegramApprovedStore, EnvTelegramAuthMode, mode)
	}

	store := authz.NewStore(storePath)
	// Load any existing approvals first — a missing file is graceful absence, a malformed
	// file is a fail-fast error so a corrupted store is noticed, not silently emptied.
	if err := store.Load(); err != nil {
		return "", nil, fmt.Errorf("%w: %s: %v", errUsageConfig, EnvTelegramApprovedStore, err)
	}

	// Seed the static approved IDs (allowlist). Additive/union: existing IDs are kept.
	rawIDs := strings.TrimSpace(getenv(EnvTelegramApprovedIDs))
	if rawIDs != "" {
		for _, part := range strings.Split(rawIDs, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if err := store.Add(part); err != nil {
				return "", nil, fmt.Errorf("%w: %s: %v", errUsageConfig, EnvTelegramApprovedIDs, err)
			}
		}
	}

	// Persist once at startup (creates the 0600 file if absent). This both writes the
	// seeded IDs and validates the path is writable (an unwritable path is fail-fast here,
	// not a first-Next() surprise).
	if err := store.Persist(); err != nil {
		return "", nil, fmt.Errorf("%w: %s: %v", errUsageConfig, EnvTelegramApprovedStore, err)
	}

	return mode, store, nil
}

// assembleTelegramOwnerID resolves the pairing-mode owner sender ID (ADR 063 Decision 3).
// It is REQUIRED (fail-fast ExitUsage) only when mode == pairing: an owner-less pairing
// mode has no one who can ever approve a sender, which is a footgun the config layer
// catches rather than silently shipping an un-onboardable channel.
//
//   - non-pairing modes: the owner ID is irrelevant → returns (0, nil), value ignored.
//   - pairing, unset/blank OWNER_ID: fail-fast error.
//   - pairing, non-numeric OWNER_ID: fail-fast error (normalized to the same canonical
//     numeric form as approved-store entries — task 151's Normalize rule).
//   - pairing, valid numeric OWNER_ID: returns the normalized int64.
func assembleTelegramOwnerID(getenv func(string) string, mode authz.Mode) (int64, error) {
	if mode != authz.ModePairing {
		return 0, nil
	}
	raw := strings.TrimSpace(getenv(EnvTelegramOwnerID))
	if raw == "" {
		return 0, fmt.Errorf("%w: %s is required when %s=%s", errUsageConfig, EnvTelegramOwnerID, EnvTelegramAuthMode, mode)
	}
	ownerID, err := authz.Normalize(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %v", errUsageConfig, EnvTelegramOwnerID, err)
	}
	return ownerID, nil
}

// openModeWarning is the mandatory startup WARNING (ADR 063 Decision 1 / task 153,
// REQ-153-03) emitted to stderr exactly once, unconditionally, whenever the resolved
// Telegram auth mode is "open". It names the concrete risk in the exact framing ADR
// 063's mode-matrix Notes column uses, so a substring check for the risk phrase
// ("any account that finds the bot can command it") is stable across refactors.
const openModeWarning = "WARNING: AGENT_BUILDER_TELEGRAM_AUTH_MODE=open — any account that finds the bot can command it (no sender-ID gate, no allowlist, no pairing approval). Plaintext only; armor/size-caps/audit are still enforced, but there is no gate on WHO can send commands.\n"

// assembleTelegramInbound constructs the telegram.Adapter (inbound MessageSource)
// and telegram.ReplyAdapter (outbound Reporter) from AGENT_BUILDER_TELEGRAM_* env vars.
// All required vars must be set; absence is a fail-fast assembly error (never a
// nil-adapter panic at first Next() call).
//
// Key material is decoded from hex but never logged (consistent with the existing
// telegram adapter and worker transport).
//
// warnOut receives the mandatory footgun WARNING (REQ-153-03) iff the resolved mode is
// "open" — emitted exactly once, unconditionally (never gated behind a verbosity flag),
// regardless of what else is configured (e.g. an unrelated APPROVED_STORE value present
// alongside "open" does not suppress or duplicate it). A nil warnOut is tolerated (the
// warning is simply discarded), so a caller that does not care about it never panics.
func assembleTelegramInbound(getenv func(string) string, sink audit.Sink, logger *slog.Logger, warnOut io.Writer) (*telegram.Adapter, *telegram.ReplyAdapter, error) {
	// Resolve the inbound auth mode (ADR 063) and, for store-consulting modes, build+seed
	// the persisted approved-sender store. Fail-fast on an unknown mode / blank-or-bad
	// store path before decoding any crypto key material.
	authMode, authStore, err := assembleTelegramAuthMode(getenv)
	if err != nil {
		return nil, nil, err
	}

	// REQ-153-03: the mandatory footgun warning fires iff (and only iff) the resolved
	// mode is "open" — exactly once per assembly, unconditional. No warning for any of
	// the other four modes.
	if authMode == authz.ModeOpen && warnOut != nil {
		_, _ = io.WriteString(warnOut, openModeWarning)
	}

	// Pairing mode requires a configured owner sender ID (ADR 063 Decision 3). Fail-fast
	// here so an owner-less pairing config never ships an un-onboardable channel.
	ownerID, err := assembleTelegramOwnerID(getenv, authMode)
	if err != nil {
		return nil, nil, err
	}

	botToken := strings.TrimSpace(getenv(EnvTelegramBotToken))
	if botToken == "" {
		return nil, nil, fmt.Errorf("orchestrate: %s is required when %s=telegram", EnvTelegramBotToken, EnvInbound)
	}

	baseURL := strings.TrimSpace(getenv(EnvTelegramBaseURL))
	if baseURL == "" {
		baseURL = defaultTelegramBaseURL
	}

	chatID := strings.TrimSpace(getenv(EnvTelegramChatID))
	if chatID == "" {
		return nil, nil, fmt.Errorf("orchestrate: %s is required when %s=telegram", EnvTelegramChatID, EnvInbound)
	}

	// Operator Ed25519 signing key (32 bytes public key, hex-encoded).
	signingKeyHex := strings.TrimSpace(getenv(EnvTelegramSigningKey))
	if signingKeyHex == "" {
		return nil, nil, fmt.Errorf("orchestrate: %s is required when %s=telegram", EnvTelegramSigningKey, EnvInbound)
	}
	signingKeyBytes, err := hex.DecodeString(signingKeyHex)
	if err != nil || len(signingKeyBytes) != ed25519.PublicKeySize {
		return nil, nil, fmt.Errorf("orchestrate: %s must be a %d-byte hex-encoded Ed25519 public key", EnvTelegramSigningKey, ed25519.PublicKeySize)
	}
	trustedSigningKey := ed25519.PublicKey(signingKeyBytes)

	// Operator X25519 public key (32 bytes, hex-encoded) — inbound sender.
	x25519PubHex := strings.TrimSpace(getenv(EnvTelegramX25519Pub))
	if x25519PubHex == "" {
		return nil, nil, fmt.Errorf("orchestrate: %s is required when %s=telegram", EnvTelegramX25519Pub, EnvInbound)
	}
	x25519PubBytes, err := hex.DecodeString(x25519PubHex)
	if err != nil || len(x25519PubBytes) != 32 {
		return nil, nil, fmt.Errorf("orchestrate: %s must be a 32-byte hex-encoded X25519 public key", EnvTelegramX25519Pub)
	}
	var trustedX25519Pub [32]byte
	copy(trustedX25519Pub[:], x25519PubBytes)

	// Orchestrator X25519 private key (32 bytes, hex-encoded) — inbound recipient.
	orchPrivHex := strings.TrimSpace(getenv(EnvTelegramOrchPriv))
	if orchPrivHex == "" {
		return nil, nil, fmt.Errorf("orchestrate: %s is required when %s=telegram", EnvTelegramOrchPriv, EnvInbound)
	}
	orchPrivBytes, err := hex.DecodeString(orchPrivHex)
	if err != nil || len(orchPrivBytes) != 32 {
		return nil, nil, fmt.Errorf("orchestrate: %s must be a 32-byte hex-encoded X25519 private key", EnvTelegramOrchPriv)
	}
	var orchXPriv [32]byte
	copy(orchXPriv[:], orchPrivBytes)

	// Orchestrator Ed25519 private key (64 bytes, hex-encoded) — outbound signer.
	orchEdPrivHex := strings.TrimSpace(getenv(EnvTelegramOrchEdPriv))
	if orchEdPrivHex == "" {
		return nil, nil, fmt.Errorf("orchestrate: %s is required when %s=telegram", EnvTelegramOrchEdPriv, EnvInbound)
	}
	orchEdPrivBytes, err := hex.DecodeString(orchEdPrivHex)
	if err != nil || len(orchEdPrivBytes) != ed25519.PrivateKeySize {
		return nil, nil, fmt.Errorf("orchestrate: %s must be a %d-byte hex-encoded Ed25519 private key", EnvTelegramOrchEdPriv, ed25519.PrivateKeySize)
	}
	orchEdPriv := ed25519.PrivateKey(orchEdPrivBytes)

	// Operator X25519 public key for outbound sealing (32 bytes, hex-encoded).
	opX25519PubHex := strings.TrimSpace(getenv(EnvTelegramOpX25519Pub))
	if opX25519PubHex == "" {
		return nil, nil, fmt.Errorf("orchestrate: %s is required when %s=telegram", EnvTelegramOpX25519Pub, EnvInbound)
	}
	opX25519PubBytes, err := hex.DecodeString(opX25519PubHex)
	if err != nil || len(opX25519PubBytes) != 32 {
		return nil, nil, fmt.Errorf("orchestrate: %s must be a 32-byte hex-encoded X25519 public key", EnvTelegramOpX25519Pub)
	}
	var opXPub [32]byte
	copy(opXPub[:], opX25519PubBytes)

	// Armor content guard: default to fail-open allow-all when no armor binary is
	// configured. This is a NEW fail-open default introduced with the first live
	// Telegram inbound wiring (there was no prior no-armor fallback on the run path).
	// The load-bearing trust gate on this path is the envelope-verify pipeline
	// (Ed25519 verify + X25519 decrypt + replay-cache), which is always enforced;
	// armor is an additional injection filter over already-authenticated operator
	// plaintext, so fail-open here degrades that extra filter, not authentication.
	// NOTE: operators SHOULD wire a real armor guard (see executorharness's
	// NewArmorGuarded binding) before production use of AGENT_BUILDER_INBOUND=telegram.
	var guard telegram.ContentGuard = allowAllContentGuard{}

	// Pairing mode (ADR 063 Decision 3) needs a PLAINTEXT outbound notifier for the
	// "pending" reply to an unknown sender and the approve/deny notification to the owner
	// — distinct from the envelope-sealing ReplyAdapter (an unknown sender holds no
	// envelope key). Wired only in pairing mode; nil (and the owner chat blank) otherwise.
	var notifier telegram.PairingNotifier
	ownerChatID := ""
	if authMode == authz.ModePairing {
		notifier = telegram.NewPlaintextNotifier(telegram.PlaintextNotifierConfig{
			BotToken: botToken,
			BaseURL:  baseURL,
			Logger:   logger,
		})
		// In a 1:1 owner DM, the owner's chat ID equals the owner sender ID.
		ownerChatID = strconv.FormatInt(ownerID, 10)
	}

	adapter := telegram.NewAdapter(telegram.Config{
		BotToken:          botToken,
		BaseURL:           baseURL,
		TrustedSigningKey: trustedSigningKey,
		TrustedX25519Pub:  trustedX25519Pub,
		OrchestratorPriv:  orchXPriv,
		ContentGuard:      guard,
		AuditSink:         sink,
		Logger:            logger,
		AuthMode:          authMode,
		AuthStore:         authStore,
		OwnerID:           ownerID,
		OwnerChatID:       ownerChatID,
		Notifier:          notifier,
	})

	replyAdapter := telegram.NewReplyAdapter(telegram.ReplyConfig{
		BotToken:   botToken,
		BaseURL:    baseURL,
		ChatID:     chatID,
		OrchEdPriv: orchEdPriv,
		OrchXPriv:  orchXPriv,
		OpXPub:     opXPub,
		Logger:     logger,
	})

	return adapter, replyAdapter, nil
}

// allowAllContentGuard is the fail-open content guard used when AGENT_BUILDER_INBOUND=telegram
// but no armor binary is configured. It always ALLOWS content through — the
// envelope-verify pipeline (Ed25519+X25519+AEAD) is the load-bearing gate; armor is
// an optional additional filter. Operators SHOULD configure a real armor guard for
// production use. This guard satisfies telegram.ContentGuard without importing armor.
type allowAllContentGuard struct{}

func (allowAllContentGuard) DecideContent(_ context.Context, candidate ingestion.ContentCandidate) (ingestion.Decision, error) {
	return ingestion.Decision{
		CandidateID: candidate.ID,
		Kind:        ingestion.CandidateKindContent,
		Outcome:     ingestion.DecisionAllow,
	}, nil
}

func orchestrateUsage(w io.Writer) {
	write(w, `Usage: agent-builder orchestrate

Drives the Tier-1 orchestrator: reads goals from the configured inbound source,
decomposes each into a plan, gates it on the spawn-plan / spawn-worker policy
decisions, and dispatches one worker per approved sub-goal.

Required environment:
  AGENT_BUILDER_WORKER_SIGNING_KEY   orchestrator Ed25519 signing key (fail-closed at startup)

Optional environment:
  AGENT_BUILDER_INBOUND              "" or "env" (default) | "telegram" (Telegram bot channel)
  AGENT_BUILDER_PLANNER              "structured" (default) | "llm" (ollama-native; cloud harnesses fail closed)
  AGENT_BUILDER_MEMORY_GUARD_BIN     memory-guard binary (else in-memory PlanStore + warning)
  AGENT_BUILDER_POLICY_BIN           policy-engine binary (else all spawns denied)
  AGENT_BUILDER_AUDIT_RECORD         audit chain logfile (shared orchestrator+worker sink)
  AGENT_BUILDER_AUDIT_BIN            audit-trail binary

When AGENT_BUILDER_INBOUND=telegram, these are also required:
  AGENT_BUILDER_TELEGRAM_BOT_TOKEN   Telegram Bot API token
  AGENT_BUILDER_TELEGRAM_BASE_URL    Bot API base URL (default: https://api.telegram.org)
  AGENT_BUILDER_TELEGRAM_SIGNING_KEY operator Ed25519 public key (hex 32 bytes)
  AGENT_BUILDER_TELEGRAM_X25519_PUB  operator X25519 public key (hex 32 bytes)
  AGENT_BUILDER_TELEGRAM_ORCH_PRIV   orchestrator X25519 private key (hex 32 bytes)
  AGENT_BUILDER_TELEGRAM_ORCH_ED_PRIV orchestrator Ed25519 private key (hex 64 bytes)
  AGENT_BUILDER_TELEGRAM_OP_X25519_PUB operator X25519 public key for outbound (hex 32 bytes)
  AGENT_BUILDER_TELEGRAM_CHAT_ID     Telegram chat ID for outbound replies

Optional inbound auth mode (ADR 063; default "envelope" = today's behavior):
  AGENT_BUILDER_TELEGRAM_AUTH_MODE      "envelope" (default) | "allowlist" | "pairing" | "open" | "disabled"
  AGENT_BUILDER_TELEGRAM_APPROVED_STORE path to 0600 JSON approved-sender store (required for allowlist/pairing)
  AGENT_BUILDER_TELEGRAM_APPROVED_IDS   comma-separated numeric sender IDs seeded into the store (allowlist)
  AGENT_BUILDER_TELEGRAM_OWNER_ID       numeric owner sender ID who approves/denies pending senders (required for pairing)
`)
}
