// Package runtime assembles the concrete Phase 0 run pipeline for the CLI.
package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/executor"
	"github.com/tkdtaylor/agent-builder/internal/executor/ollamatoolset"
	"github.com/tkdtaylor/agent-builder/internal/gate"
	agentloop "github.com/tkdtaylor/agent-builder/internal/loop"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	branchpub "github.com/tkdtaylor/agent-builder/internal/publisher"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/router"
	"github.com/tkdtaylor/agent-builder/internal/sandbox"
	"github.com/tkdtaylor/agent-builder/internal/sandbox/execsandbox"
	"github.com/tkdtaylor/agent-builder/internal/sandbox/podman"
	"github.com/tkdtaylor/agent-builder/internal/secrets"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
	"github.com/tkdtaylor/agent-builder/internal/tasksource"
	"github.com/tkdtaylor/agent-builder/internal/vault"
)

const (
	EnvTaskRoot        = "AGENT_BUILDER_TASK_ROOT"
	EnvWorktree        = "AGENT_BUILDER_WORKTREE"
	EnvClaudeCLI       = "AGENT_BUILDER_CLAUDE_CLI"
	EnvExecBoxLauncher = "AGENT_BUILDER_EXEC_BOX_LAUNCHER"
	EnvExecSandboxBin  = "AGENT_BUILDER_EXEC_SANDBOX_BIN"
	EnvRunRecord       = "AGENT_BUILDER_RUN_RECORD"
	EnvAuditRecord     = "AGENT_BUILDER_AUDIT_RECORD"
	EnvAuditBin        = "AGENT_BUILDER_AUDIT_BIN"
	EnvRunTimeout      = "AGENT_BUILDER_RUN_TIMEOUT"
	EnvMaxAttempts     = "AGENT_BUILDER_MAX_ATTEMPTS"
	EnvPublishRemote   = "AGENT_BUILDER_PUBLISH_REMOTE"
	EnvGitCLI          = "AGENT_BUILDER_GIT_CLI"
	EnvGitHubCLI       = "AGENT_BUILDER_GH_CLI"
	EnvGitToken        = "AGENT_BUILDER_GIT_TOKEN"
	EnvGitHubToken     = "AGENT_BUILDER_GITHUB_TOKEN"
	EnvRecipeName      = "AGENT_BUILDER_RECIPE"

	// Vault wiring (ADR 036, task 066). Vault is opt-in: when EnvVaultBin is
	// unset, vault wiring is skipped and the old env-forwarding behavior holds.
	EnvVaultBin       = "AGENT_BUILDER_VAULT_BIN"
	EnvVaultSocket    = "AGENT_BUILDER_VAULT_SOCKET"
	EnvVaultStorePath = "AGENT_BUILDER_VAULT_STORE_PATH"

	// Policy gate (ADR 038, task 072). The policy gate is opt-in: when
	// EnvPolicyBin is unset, the decide call is skipped entirely and the run
	// proceeds exactly as before (zero regression).
	EnvPolicyBin    = "AGENT_BUILDER_POLICY_BIN"
	EnvPolicySocket = "AGENT_BUILDER_POLICY_SOCKET"
	EnvPolicyRisk   = "AGENT_BUILDER_POLICY_RISK"
	// EnvPolicyAllow is the optional deployment base allow (comma-separated) for
	// the orchestrate path (ADR 055 seam 1, task 122). It INTERSECTS the
	// plan-derived allow set (Plan.AllowedResources): the policy daemon serving a
	// plan's decisions is launched with effectiveAllow(plan, base). The deployment
	// can only NARROW (intersect), never widen, what a plan authorizes. Unset (or
	// whitespace-only) means no narrowing — the full plan-derived set is used.
	EnvPolicyAllow = "AGENT_BUILDER_POLICY_ALLOW"

	// defaultPolicyRisk is the static context.risk value sent in the AuthZEN
	// decide request when AGENT_BUILDER_POLICY_RISK is unset (ADR 038).
	defaultPolicyRisk = "low"

	// Checkpoint signing (ADR 037, task 068). Checkpoint is opt-in: when
	// EnvAuditCheckpointKey is unset, checkpoint creation is skipped.
	EnvAuditCheckpointKey       = "AGENT_BUILDER_AUDIT_CHECKPOINT_KEY"
	EnvAuditCheckpointLogID     = "AGENT_BUILDER_AUDIT_CHECKPOINT_LOG_ID"
	EnvAuditCheckpointOut       = "AGENT_BUILDER_AUDIT_CHECKPOINT_OUT"
	EnvAuditCheckpointPublicKey = "AGENT_BUILDER_AUDIT_CHECKPOINT_PUBLIC_KEY"

	// EnvSandboxRuntime is the removed Phase 0 srt selector. It is retained only
	// to detect and reject a stale value loudly (ADR 021, decision 2).
	EnvSandboxRuntime = "AGENT_BUILDER_SANDBOX_RUNTIME"

	// defaultExecBoxLauncher is the standard Podman execution-box launcher path.
	defaultExecBoxLauncher = "containment/execution-box/run.sh"
)

// Config is the explicit runtime configuration used by agent-builder run.
type Config struct {
	TaskRoot         string
	Worktree         string
	ClaudeCLI        string
	ClaudeToken      string
	ClaudeOAuthToken string
	ExecBoxLauncher  string
	ExecSandboxBin   string
	RunRecordPath    string
	AuditRecordPath  string
	AuditBin         string
	RunTimeout       time.Duration
	MaxAttempts      int
	PublishRemote    string
	GitCLI           string
	GitHubCLI        string
	GitToken         string
	GitHubToken      string
	RecipeName       string

	// DispatchedTask is set by the orchestrate dispatch path (ADR 055 seam 2, task 119).
	// When non-nil, Run uses this single task as the worker's goal instead of
	// calling the recipe's file-based GoalSourceFactory (which would read task files
	// from TaskRoot). The single-task `run` subcommand leaves this nil, preserving
	// its existing file-discovery behaviour unchanged.
	DispatchedTask *supervisor.Task

	// Vault wiring (ADR 036, task 066). VaultBin empty => vault disabled.
	VaultBin       string
	VaultSocket    string
	VaultStorePath string

	// Policy gate (ADR 038, task 072). PolicyBin empty => policy gate disabled.
	PolicyBin    string
	PolicySocket string
	PolicyRisk   string

	// Checkpoint signing (ADR 037, task 068). AuditCheckpointKey empty => disabled.
	AuditCheckpointKey       string // path to Ed25519 PEM signing key
	AuditCheckpointLogID     string // stable log identifier for the checkpoint
	AuditCheckpointOut       string // path for checkpoint JSON output (empty = stdout)
	AuditCheckpointPublicKey string // path to Ed25519 PEM public key (task 069 consume)
}

// seamConfigAdapter wraps Config to implement recipe.SeamConfig (ADR 044, task 077).
// This avoids the Go limitation where a type can't have both a field and method of the same name.
type seamConfigAdapter struct {
	config *Config
}

func (a *seamConfigAdapter) TaskRoot() string      { return a.config.TaskRoot }
func (a *seamConfigAdapter) PublishRemote() string { return a.config.PublishRemote }
func (a *seamConfigAdapter) GitToken() string      { return a.config.GitToken }
func (a *seamConfigAdapter) GitHubToken() string   { return a.config.GitHubToken }
func (a *seamConfigAdapter) GitCLI() string        { return a.config.GitCLI }
func (a *seamConfigAdapter) GitHubCLI() string     { return a.config.GitHubCLI }
func (a *seamConfigAdapter) Worktree() string      { return a.config.Worktree }

// RunFromEnv builds and runs one configured Phase 0 pipeline from environment
// variables. The optional writer receives a short user-visible run summary.
//
// ctx is threaded into Run → Supervisor.Run so a cancellation propagates to the
// in-box loop's run-loop (ADR 054 §5, task 116). The single-task CLI path passes
// context.Background(); the orchestrate path passes the per-goal cancel context.
func RunFromEnv(ctx context.Context, stdout io.Writer) error {
	config, err := ConfigFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	return Run(ctx, config, stdout)
}

// ConfigFromEnv reads the explicit run configuration contract from getenv.
// Token retrieval is delegated to a getenv-backed SecretSource so that the
// token read-sites are abstracted behind the secrets.SecretSource seam (task 065).
// Vault token brokering (task 066) is opt-in at Run time via AGENT_BUILDER_VAULT_BIN;
// it does not change config parsing — the git/GitHub tokens are still read here and
// handed to vault inside Run when vault is enabled.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	return configFromEnvWithSource(getenv, getenvSecretSource(getenv))
}

// configFromEnvWithSource is the internal implementation shared by
// ConfigFromEnv and future vault-aware constructors.
func configFromEnvWithSource(getenv func(string) string, src secrets.SecretSource) (Config, error) {
	// The rented srt selector was removed by ADR 021. A stale non-empty value
	// must fail loudly rather than be silently ignored (decision 2).
	if strings.TrimSpace(getenv(EnvSandboxRuntime)) != "" {
		return Config{}, fmt.Errorf("run config: %s was removed by the Podman containment swap (ADR 021); unset it — containment now runs through %s", EnvSandboxRuntime, defaultExecBoxLauncher)
	}

	authToken, oauthToken := src.ProviderToken()
	gitToken, githubToken := src.PublisherTokens()

	recipeName := strings.TrimSpace(getenv(EnvRecipeName))
	if recipeName == "" {
		recipeName = "coding-agent"
	}

	config := Config{
		TaskRoot:         cleanPath(getenv(EnvTaskRoot)),
		Worktree:         cleanPath(getenv(EnvWorktree)),
		ClaudeCLI:        strings.TrimSpace(getenv(EnvClaudeCLI)),
		ClaudeToken:      authToken,
		ClaudeOAuthToken: oauthToken,
		ExecBoxLauncher:  cleanPath(getenv(EnvExecBoxLauncher)),
		ExecSandboxBin:   cleanPath(getenv(EnvExecSandboxBin)),
		RunRecordPath:    cleanPath(getenv(EnvRunRecord)),
		AuditRecordPath:  cleanPath(getenv(EnvAuditRecord)),
		AuditBin:         strings.TrimSpace(getenv(EnvAuditBin)),
		RunTimeout:       0, // will be set below
		MaxAttempts:      0, // will be set below
		PublishRemote:    strings.TrimSpace(getenv(EnvPublishRemote)),
		GitCLI:           strings.TrimSpace(getenv(EnvGitCLI)),
		GitHubCLI:        strings.TrimSpace(getenv(EnvGitHubCLI)),
		GitToken:         gitToken,
		GitHubToken:      githubToken,
		RecipeName:       recipeName,
		VaultBin:         cleanPath(getenv(EnvVaultBin)),
		VaultSocket:      cleanPath(getenv(EnvVaultSocket)),
		VaultStorePath:   cleanPath(getenv(EnvVaultStorePath)),

		PolicyBin:    cleanPath(getenv(EnvPolicyBin)),
		PolicySocket: cleanPath(getenv(EnvPolicySocket)),
		PolicyRisk:   strings.TrimSpace(getenv(EnvPolicyRisk)),

		AuditCheckpointKey:       cleanPath(getenv(EnvAuditCheckpointKey)),
		AuditCheckpointLogID:     strings.TrimSpace(getenv(EnvAuditCheckpointLogID)),
		AuditCheckpointOut:       cleanPath(getenv(EnvAuditCheckpointOut)),
		AuditCheckpointPublicKey: cleanPath(getenv(EnvAuditCheckpointPublicKey)),
	}
	if config.ClaudeCLI == "" {
		config.ClaudeCLI = "claude"
	}
	if config.ExecBoxLauncher == "" {
		config.ExecBoxLauncher = defaultExecBoxLauncher
	}
	if config.GitCLI == "" {
		config.GitCLI = "git"
	}
	if config.GitHubCLI == "" {
		config.GitHubCLI = "gh"
	}

	if config.TaskRoot == "" {
		return Config{}, missingConfig(EnvTaskRoot)
	}
	if config.Worktree == "" {
		return Config{}, missingConfig(EnvWorktree)
	}

	// Check if all enabled registry entries are local (SecretRef == "").
	// If so, skip the cloud-credential check; pure-local operators do not need ANTHROPIC_API_KEY.
	allLocal := false
	entries, err := registry.LoadFromEnv()
	if err == nil && len(entries) > 0 {
		// Check if all enabled entries have empty SecretRef (local entries).
		allLocal = true
		for _, entry := range entries {
			if entry.SecretRef != "" {
				allLocal = false
				break
			}
		}
	}
	// If registry.LoadFromEnv fails or returns no entries, enforce the cloud-credential check
	// (fail-closed: no evidence of a local-only config).

	// Require at least one credential (OAuth token or API key), unless all entries are local.
	if !allLocal && strings.TrimSpace(config.ClaudeToken) == "" && strings.TrimSpace(config.ClaudeOAuthToken) == "" {
		return Config{}, fmt.Errorf("run config: missing at least one of %s or %s", executor.ClaudeCLIAuthEnv, executor.ClaudeCLIOAuthEnv)
	}
	if config.PublishRemote == "" {
		return Config{}, missingConfig(EnvPublishRemote)
	}

	timeoutRaw := strings.TrimSpace(getenv(EnvRunTimeout))
	if timeoutRaw == "" {
		return Config{}, missingConfig(EnvRunTimeout)
	}
	timeout, err := time.ParseDuration(timeoutRaw)
	if err != nil {
		return Config{}, fmt.Errorf("run config: invalid %s %q: %w", EnvRunTimeout, timeoutRaw, err)
	}
	config.RunTimeout = timeout

	attemptsRaw := strings.TrimSpace(getenv(EnvMaxAttempts))
	if attemptsRaw == "" {
		return Config{}, missingConfig(EnvMaxAttempts)
	}
	attempts, err := strconv.Atoi(attemptsRaw)
	if err != nil {
		return Config{}, fmt.Errorf("run config: invalid %s %q: %w", EnvMaxAttempts, attemptsRaw, err)
	}
	config.MaxAttempts = attempts

	return config, nil
}

// init registers the coding-agent recipe at startup.
func init() {
	recipe.Register("coding-agent", newCodingAgentRecipe)
}

// newCodingAgentRecipe is the factory that constructs the coding-agent Recipe.
// It returns factory functions that will construct the real seams at assembly time (ADR 044, task 077).
func newCodingAgentRecipe() (recipe.Recipe, error) {
	// GoalSourceFactory: constructs a tasksource.Source from config at assembly time.
	goalSourceFactory := func(cfg recipe.SeamConfig) (supervisor.GoalSource, error) {
		return tasksource.New(os.DirFS(cfg.TaskRoot()), tasksource.DefaultRoadmapPath, tasksource.DefaultTaskDirs...), nil
	}

	// ResultSinkFactory: constructs a GitHub publisher wrapped in a ResultSink adapter.
	resultSinkFactory := func(cfg recipe.SeamConfig) (supervisor.ResultSink, error) {
		pub := branchpub.NewGitHubCLI(branchpub.GitHubCLIConfig{
			GitPath:     cfg.GitCLI(),
			GHPath:      cfg.GitHubCLI(),
			Worktree:    cfg.Worktree(),
			Remote:      cfg.PublishRemote(),
			GitToken:    cfg.GitToken(),
			GitHubToken: cfg.GitHubToken(),
		})
		return &publisherAdapter{pub}, nil
	}

	return recipe.New(
		goalSourceFactory,
		recipe.RoutingSpec{MinCapability: 1, SensitivityHint: recipe.SensitivitySensitive},
		newProductionGateFactory,
		resultSinkFactory,
		nil,
	), nil
}

// publisherAdapter wraps internal/publisher.GitHubCLI to satisfy supervisor.ResultSink.
// This adapter is required because the concrete publisher lives in internal/publisher and
// supervisor cannot import it (F-003 invariant).
type publisherAdapter struct {
	pub *branchpub.GitHubCLI
}

func (a *publisherAdapter) Publish(ctx context.Context, req supervisor.PublishRequest) (supervisor.PublishResult, error) {
	// Translate supervisor.PublishRequest to publisher.Request.
	pubReq := branchpub.Request{
		Task:     req.Task,
		Worktree: req.Worktree,
		Branch:   req.Branch,
		Remote:   req.Remote,
	}
	// Call the publisher.
	pubRes, err := a.pub.Publish(ctx, pubReq)
	if err != nil {
		return supervisor.PublishResult{}, err
	}
	// Translate publisher.Result to supervisor.PublishResult.
	return supervisor.PublishResult{
		Branch: pubRes.Branch,
		PRURL:  pubRes.PRURL,
		PRID:   pubRes.PRID,
	}, nil
}

// resultSinkAdapter wraps supervisor.ResultSink to satisfy branchpub.Publisher.
// This is the reverse adapter: converts the seam interface back to the concrete interface
// for use in the retryingInBoxLoop, which predates the seam abstraction and still uses
// the branchpub.Publisher interface directly.
type resultSinkAdapter struct {
	sink supervisor.ResultSink
}

func (a *resultSinkAdapter) Publish(ctx context.Context, req branchpub.Request) (branchpub.Result, error) {
	// Translate branchpub.Request to supervisor.PublishRequest.
	seamReq := supervisor.PublishRequest{
		Task:     req.Task,
		Worktree: req.Worktree,
		Branch:   req.Branch,
		Remote:   req.Remote,
	}
	// Call the sink.
	seamRes, err := a.sink.Publish(ctx, seamReq)
	if err != nil {
		return branchpub.Result{}, err
	}
	// Translate supervisor.PublishResult to branchpub.Result.
	return branchpub.Result{
		Branch: seamRes.Branch,
		PRURL:  seamRes.PRURL,
		PRID:   seamRes.PRID,
	}, nil
}

// newProductionGateFactory wraps newProductionGate to match the GateFactory signature.
func newProductionGateFactory() supervisor.Gate {
	gate, _ := newProductionGate()
	return gate
}

// defaultClaudeEntryID is the synthetic registry entry the runtime injects when
// no AGENT_BUILDER_REGISTRY_* entries are configured. It preserves the original
// task-077 behavior (route the coding-agent recipe to the Claude CLI executor
// built from Config) so existing single-provider deployments are zero-drift: the
// router selects this one entry and the runtime builds the Claude executor from
// Config exactly as the old stub did.
const defaultClaudeEntryID = "claude-default"

// buildCatalog is the seam runtime.Run uses to obtain the executor catalog. The
// production implementation reads AGENT_BUILDER_REGISTRY_* via
// registry.LoadFromEnv and synthesizes the default Claude entry when none are
// configured. Tests override it to inject a fake catalog (empty, or multi-entry)
// without setting process env vars.
var buildCatalog = func(config Config) (*registry.Catalog, error) {
	entries, err := registry.LoadFromEnv()
	if err != nil {
		return nil, fmt.Errorf("run: load executor registry: %w", err)
	}

	catalog := registry.NewCatalog()
	if len(entries) == 0 {
		// No registry entries configured: synthesize the default Claude entry so
		// the coding-agent recipe still routes to Claude (zero-drift with the
		// task-077 stub). The runtime builds the concrete executor from Config —
		// the entry only carries the routing attributes the router selects on.
		catalog.RegisterEntry(defaultClaudeEntry(config))
		return catalog, nil
	}
	for _, e := range entries {
		catalog.RegisterEntry(e)
	}
	return catalog, nil
}

// defaultClaudeEntry constructs the synthetic single-provider Claude entry. It is
// the cheapest possible eligible entry at the base capability tier so the router
// always selects it when it is the only entry. CapabilityTier 1 matches the
// coding-agent recipe's MinCapability floor.
func defaultClaudeEntry(_ Config) registry.RegistryEntry {
	return registry.RegistryEntry{
		ID:             defaultClaudeEntryID,
		Harness:        registry.HarnessClaudeCLI,
		CapabilityTier: 1,
		CostWeight:     1,
		Availability:   registry.Availability{Status: registry.AvailStatusAvailable},
	}
}

// resolveExecutor resolves a recipe's RoutingSpec to a concrete
// supervisor.Executor via the real registry+router (ADR 043). It builds the
// catalog (buildCatalog), constructs a Router over it, and asks the router to
// Select the cheapest eligible entry at sufficient capability. The selected
// entry's harness determines which concrete executor is constructed.
//
// It returns ErrNoEligibleExecutor (wrapped, descriptive) when no entry
// qualifies, so runtime.Run can fail before any sandbox creation.
func resolveExecutor(spec recipe.RoutingSpec, config Config) (supervisor.Executor, registry.RegistryEntry, error) {
	catalog, err := buildCatalog(config)
	if err != nil {
		return nil, registry.RegistryEntry{}, err
	}

	r := router.New(catalog)
	entry, err := r.Select(toRouterSpec(spec))
	if err != nil {
		return nil, registry.RegistryEntry{}, fmt.Errorf("run: resolve executor for routing spec (min_capability=%d): %w", spec.MinCapability, err)
	}

	exec, err := buildExecutorForEntry(entry, config)
	if err != nil {
		return nil, registry.RegistryEntry{}, err
	}
	return exec, entry, nil
}

// toRouterSpec maps the recipe-leaf RoutingSpec to the router's equivalent value
// type. The two types are intentionally decoupled (the recipe leaf must not
// import the router); the assembler is the single site that bridges them.
func toRouterSpec(spec recipe.RoutingSpec) router.RoutingSpec {
	hint := router.SensitivityNone
	if spec.SensitivityHint == recipe.SensitivitySensitive {
		hint = router.SensitivitySensitive
	}
	return router.RoutingSpec{
		MinCapability:   spec.MinCapability,
		SensitivityHint: hint,
	}
}

// buildExecutorForEntry constructs the concrete supervisor.Executor for a
// selected registry entry.
//
// The synthetic default Claude entry is built from Config (CLI path + tokens)
// exactly as the task-077 stub did — this is the zero-drift path for
// single-provider deployments. All other entries are configured via the
// AGENT_BUILDER_REGISTRY_* env contract and are constructed through their
// harness adapter, with credentials brokered by the env-backed SecretSource.
func buildExecutorForEntry(entry registry.RegistryEntry, config Config) (supervisor.Executor, error) {
	if entry.ID == defaultClaudeEntryID {
		return executor.NewClaudeCLI(executor.ClaudeCLIConfig{
			CLIPath:    config.ClaudeCLI,
			Worktree:   config.Worktree,
			AuthToken:  config.ClaudeToken,
			OAuthToken: config.ClaudeOAuthToken,
		}), nil
	}

	src := secrets.NewEnvSecretSource()
	switch entry.Harness {
	case registry.HarnessClaudeCLI:
		return executor.NewClaudeCLIFromEntry(entry, src, config.Worktree), nil
	case registry.HarnessCodexCLI:
		return executor.NewCodexCLI(entry, src, config.Worktree), nil
	case registry.HarnessGeminiCLI:
		return executor.NewGeminiCLI(entry, src, config.Worktree), nil
	case registry.HarnessAntigravityCLI:
		return executor.NewAntigravityCLI(entry, src, config.Worktree), nil
	case registry.HarnessOllamaNative:
		toolset, err := ollamatoolset.NewToolSet(config.Worktree)
		if err != nil {
			return nil, fmt.Errorf("run: create ollama toolset: %w", err)
		}
		ollama, err := executor.NewOllamaNative(executor.OllamaNativeConfig{
			Endpoint:      entry.Endpoint,
			Model:         entry.ModelID,
			MaxIterations: 0, // Use default
			Worktree:      config.Worktree,
		}, toolset)
		if err != nil {
			return nil, fmt.Errorf("run: create ollama executor: %w", err)
		}
		return ollama, nil
	default:
		return nil, fmt.Errorf("run: entry %q names unknown harness driver %q", entry.ID, entry.Harness)
	}
}

// verifyGateExists asserts that the GateFactory is non-nil and produces a real,
// blocking gate. This runtime check complements the compile-time guarantee for
// human-authored recipes and provides defense-in-depth for generated recipes
// (ADR 042, task 078). It rejects any recipe that binds no real blocking gate.
func verifyGateExists(gateFactory recipe.GateFactory) error {
	if gateFactory == nil {
		return fmt.Errorf("runtime: gate assembly error: recipe's GateFactory is nil — a Recipe must have a real, blocking gate")
	}

	g := gateFactory()
	if g == nil {
		return fmt.Errorf("runtime: gate assembly error: GateFactory returned nil — gate must be a non-nil, blocking gate")
	}

	// Check if the gate implements the Blocker marker interface.
	blocker, ok := g.(gate.Blocker)
	if !ok {
		return fmt.Errorf("runtime: gate assembly error: gate does not implement the Blocker marker interface — gate must be a real, blocking gate")
	}

	// Verify the gate reports it is blocking (not a pass-through).
	if !blocker.Blocks() {
		return fmt.Errorf("runtime: gate assembly error: gate.Blocks() returned false — gate must be a real, blocking gate")
	}

	return nil
}

// Run dispatches at most one ready task through the concrete Phase 0 seams.
//
// ctx is the per-worker cancel context (ADR 054 §5, task 116): it is threaded into
// Supervisor.Run so a cancellation cancels the in-box loop via the run-loop's
// case <-ctx.Done(): arm (the same box.Kill/Teardown path the wall-clock timeout
// drives). The single-task CLI path passes context.Background() (no cancellation);
// the orchestrate dispatch path passes the goal's derived cancel context, so a
// `cancel <goalID>` tears down that goal's in-flight workers.
func Run(ctx context.Context, config Config, stdout io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if err := validatePaths(config); err != nil {
		return err
	}

	// Select the recipe and check it exists before creating any sandbox.
	r, err := recipe.SelectRecipe(config.RecipeName)
	if err != nil {
		return fmt.Errorf("run: select recipe: %w", err)
	}

	// Verify the recipe's GateFactory produces a real, blocking gate (ADR 042, task 078).
	// This assembly-time check fires before any sandbox.Create or supervisor construction,
	// rejecting any recipe that binds no real blocking gate.
	if err := verifyGateExists(r.GateFactory); err != nil {
		return err
	}

	// Assemble the four IO seams from the recipe's factories (ADR 044, task 077).
	// This is the thin assembler pattern: all construction flows through the recipe.
	seamConfig := &seamConfigAdapter{config: &config}

	// Goal source: if the orchestrate dispatch path seeded a DispatchedTask (ADR 055
	// seam 2, task 119), bypass the recipe's file-based GoalSourceFactory and yield
	// that one task directly. The single-task `run` subcommand always leaves
	// DispatchedTask nil, so its tasksource file-discovery path is unchanged.
	var goalSource supervisor.GoalSource
	if config.DispatchedTask != nil {
		goalSource = &singleTaskSource{task: *config.DispatchedTask}
	} else {
		goalSource, err = r.GoalSourceFactory(seamConfig)
		if err != nil {
			return fmt.Errorf("run: construct goal source: %w", err)
		}
	}

	resultSink, err := r.ResultSinkFactory(seamConfig)
	if err != nil {
		return fmt.Errorf("run: construct result sink: %w", err)
	}

	task, ok, err := goalSource.Next()
	if err != nil {
		return fmt.Errorf("run: pick task: %w", err)
	}
	if !ok {
		_, _ = fmt.Fprintln(stdout, "run idle: no ready task")
		return nil
	}

	verifier := r.GateFactory()
	policy, err := agentloop.NewRetryPolicy(config.MaxAttempts, agentloop.BootstrapEscalationHook)
	if err != nil {
		return fmt.Errorf("run config: invalid %s: %w", EnvMaxAttempts, err)
	}

	// Resolve the recipe's RoutingSpec to a concrete executor via the real
	// registry+router (ADR 043, task 095). This runs BEFORE any sandbox creation
	// and before the audit sink is constructed, so an unresolvable routing spec
	// (empty registry / all entries exhausted) fails the run without creating a
	// box or emitting any audit event.
	exec, _, err := resolveExecutor(r.RoutingSpec, config)
	if err != nil {
		return err
	}

	// Select the run backend: if AGENT_BUILDER_EXEC_SANDBOX_BIN is set, use execsandbox
	// as the default; otherwise fall back to the Podman launcher.
	var runner sandbox.Runner
	var backendLabel string
	if config.ExecSandboxBin != "" {
		runner = execsandbox.New(config.ExecSandboxBin)
		backendLabel = "exec-sandbox"
	} else {
		runner = podman.NewWithLauncher(config.ExecBoxLauncher)
		backendLabel = "podman"
	}

	// Vault token brokering (ADR 036, task 066). Opt-in: when AGENT_BUILDER_VAULT_BIN
	// is set, start a vault daemon, register the git/GitHub tokens, and pass the
	// resolved handles + socket + injection_mode="proxy" through Request.Wiring.
	// When unset, wiring stays the zero value and the old env-forwarding path holds.
	var wiring sandbox.RunWiring
	if config.VaultBin != "" {
		daemon, src, err := startVault(config)
		if err != nil {
			return err
		}
		defer func() { _ = daemon.Stop() }()
		wiring = sandbox.RunWiring{
			VaultSocket:   daemon.SocketPath,
			SecretRefs:    src.Handles(),
			InjectionMode: "proxy",
		}
	}

	limits := sandbox.Limits{
		WallClockTimeout: config.RunTimeout,
	}

	// Optional typed audit chain (task 041). When AGENT_BUILDER_AUDIT_RECORD is
	// set, the audit-trail binary must resolve and the path must be writable
	// BEFORE dispatch — auditing is never silently skipped when configured.
	// Construction happens here (before the policy gate) so that an audit_emit
	// obligation can emit a policy-decision event even when the gate denies (task 073).
	var auditSink audit.Sink
	if config.AuditRecordPath != "" {
		sink, err := newAuditSink(config)
		if err != nil {
			return err
		}
		auditSink = sink
	}

	// Policy gate (ADR 038, task 072). Opt-in: when AGENT_BUILDER_POLICY_BIN is
	// set, start a policy daemon and call decide BEFORE the box is created.
	// The decide call sits AFTER vault handle resolution (so a
	// vault_injection_floor obligation can raise the already-resolved
	// InjectionMode) and BEFORE sandboxBox.Create (so a deny stops the box from
	// ever starting and obligations apply to the wiring the box will use).
	var tierOverride string
	if config.PolicyBin != "" {
		outcome, err := decideGate(config, task, limits.EgressAllowlist, &wiring)
		if err != nil {
			return err
		}
		// audit_emit obligation (task 073): emit a policy-decision event on the
		// configured sink regardless of the routing outcome. Nil sink = no-op.
		maybeEmitPolicyDecision(auditSink, task.ID, outcome)
		if !outcome.allowed {
			// Deny / require_approval: write needs-human status and return
			// without dispatching. The box never starts.
			statusWriter := tasksource.NewStatusWriter(config.TaskRoot, tasksource.DefaultTaskDirs...)
			if _, werr := statusWriter.WriteStatus(task.ID, tasksource.WritableStatusNeedsHuman); werr != nil {
				return fmt.Errorf("run: %s: write needs-human status for task %s: %w", outcome.reason, task.ID, werr)
			}
			_, _ = fmt.Fprintf(stdout, "run halted: task %s — %s\n", task.ID, outcome.reason)
			return nil
		}
		// allow: a tier_select obligation may have set the tier; the
		// vault_injection_floor obligation may have raised wiring.InjectionMode.
		tierOverride = outcome.tier
	}

	box := sandboxBox{
		runner:   runner,
		worktree: config.Worktree,
		launcher: config.ExecBoxLauncher,
		backend:  backendLabel,
		wiring:   wiring,
		tier:     tierOverride,
		limits:   limits,
	}
	// Adapt the supervisor.ResultSink to the branchpub.Publisher interface for the loop.
	publisher := &resultSinkAdapter{sink: resultSink}

	inBox := retryingInBoxLoop{
		executor:      exec,
		gate:          verifier,
		worktree:      config.Worktree,
		launcher:      config.ExecBoxLauncher,
		statusWriter:  tasksource.NewStatusWriter(config.TaskRoot, tasksource.DefaultTaskDirs...),
		policy:        policy,
		publisher:     publisher,
		publishRemote: config.PublishRemote,
		publishSecrets: []string{
			config.GitToken,
			config.GitHubToken,
		},
	}

	options := []supervisor.Option{
		supervisor.WithTask(task),
		supervisor.WithContainmentBox(box),
		supervisor.WithInBoxLoop(inBox),
		supervisor.WithRunTimeout(config.RunTimeout),
	}
	if config.RunRecordPath != "" {
		options = append(options, supervisor.WithRunRecordPath(config.RunRecordPath))
	}
	if auditSink != nil {
		options = append(options, supervisor.WithSink(auditSink))
	}

	// Optional checkpoint signing (ADR 037, task 068). When
	// AGENT_BUILDER_AUDIT_CHECKPOINT_KEY is set, a CheckpointSigner is
	// constructed and wired into the supervisor. Fail-fast validation runs
	// here (before dispatch), mirroring the newBlockSink pattern.
	cs, err := newCheckpointSigner(config)
	if err != nil {
		return err
	}
	if cs != nil {
		options = append(options, supervisor.WithCheckpointSigner(cs))
	}

	if err := supervisor.New(options...).Run(ctx); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "run completed: task %s\n", task.ID)
	return nil
}

// startVault starts the vault daemon and constructs a VaultSecretSource holding
// the resolved git/GitHub token handles (ADR 036, task 066). It fails loud if the
// daemon cannot start (missing binary, no master key) or a token cannot be
// registered. The caller is responsible for calling daemon.Stop() (deferred).
//
// The git/GitHub token plaintext is read from config (populated from the host
// env) and handed to vault exactly once via Put; afterward only opaque handles
// are retained. The provider token continues to flow via the executor env path.
func startVault(config Config) (*vault.Daemon, *secrets.VaultSecretSource, error) {
	socketPath := config.VaultSocket
	if socketPath == "" {
		socketPath = filepath.Join(os.TempDir(), fmt.Sprintf("agent-builder-vault-%d.sock", os.Getpid()))
	}

	daemon := &vault.Daemon{
		BinPath:    config.VaultBin,
		SocketPath: socketPath,
		StorePath:  config.VaultStorePath,
	}
	if err := daemon.Start(context.Background()); err != nil {
		return nil, nil, fmt.Errorf("run: start vault daemon: %w", err)
	}

	client := vault.NewClient(socketPath)
	src, err := secrets.NewVaultSecretSource(client, secrets.VaultSourceConfig{
		AuthToken:   config.ClaudeToken,
		OAuthToken:  config.ClaudeOAuthToken,
		GitToken:    config.GitToken,
		GitHubToken: config.GitHubToken,
	})
	if err != nil {
		_ = daemon.Stop()
		return nil, nil, fmt.Errorf("run: construct vault secret source: %w", err)
	}
	return daemon, src, nil
}

// gateOutcome is the result of the host-side policy decide gate.
type gateOutcome struct {
	allowed        bool   // true => proceed to box.Create; false => halt (deny/require_approval)
	reason         string // human-facing status reason when !allowed
	tier           string // tier_select obligation value ("" => no override)
	auditEmit      bool   // true => emit ActionPolicyDecision event (audit_emit obligation present)
	policyDecision string // raw policy engine decision string for the audit event
	policyReason   string // policy engine reason string for the audit event
}

// decideGate starts the policy daemon, builds the AuthZEN decide request, calls
// PolicyClient.Decide, and applies obligations (ADR 038, task 072).
//
// Ordering invariant (load-bearing): decideGate is called AFTER vault handle
// resolution (so the vault_injection_floor obligation can raise the
// already-resolved wiring.InjectionMode) and BEFORE sandboxBox.Create (so a deny
// stops the box from ever starting and the tier_select / floor obligations apply
// to the request the box will issue). It receives wiring by pointer and may
// raise wiring.InjectionMode in place — never lower it.
//
// Fail-closed: the client returns DecisionDeny on any transport/parse error
// (see internal/policy.Decide). decideGate routes on response.Decision, never on
// the error, so any failure halts dispatch rather than allowing it. The daemon
// is stopped before return in all paths.
func decideGate(config Config, task supervisor.Task, egressHosts []string, wiring *sandbox.RunWiring) (gateOutcome, error) {
	binPath, err := resolvePolicyBin(config.PolicyBin)
	if err != nil {
		return gateOutcome{}, err
	}

	socketPath := config.PolicySocket
	if socketPath == "" {
		socketPath = filepath.Join(os.TempDir(), fmt.Sprintf("agent-builder-policy-%d.sock", os.Getpid()))
	}

	daemon := &policy.PolicyDaemon{
		BinPath:    binPath,
		SocketPath: socketPath,
		Allow:      egressHosts,
	}
	if err := daemon.Start(context.Background()); err != nil {
		return gateOutcome{}, fmt.Errorf("run: start policy daemon: %w", err)
	}
	defer func() { _ = daemon.Stop() }()

	risk := config.PolicyRisk
	if risk == "" {
		risk = defaultPolicyRisk
	}

	req := policy.DecideRequest{
		Subject:  policy.Subject{Type: "agent", ID: "agent-builder"},
		Action:   policy.Action{Name: "run-task"},
		Resource: policy.Resource{Type: "task", ID: task.ID, Properties: policy.ResourceProperties{EgressHosts: egressHosts}},
		Context:  policy.DecideContext{Risk: risk},
	}

	// Decide is fail-closed: any error yields DecisionDeny. We deliberately route
	// on resp.Decision (not the error) so an error path halts dispatch.
	resp, _ := policy.NewClient(socketPath).Decide(req)

	// Apply obligations regardless of decision shape; on allow they take effect,
	// on deny they are inert (the box never starts).
	tier := policy.TierSelect(resp.Obligations)
	wiring.InjectionMode = policy.RaiseInjectionFloor(wiring.InjectionMode, resp.Obligations)
	auditEmit := policy.AuditEmit(resp.Obligations)
	policyReason := resp.Context.Reason

	switch resp.Decision {
	case policy.DecisionAllow:
		return gateOutcome{
			allowed:        true,
			tier:           tier,
			auditEmit:      auditEmit,
			policyDecision: string(policy.DecisionAllow),
			policyReason:   policyReason,
		}, nil
	case policy.DecisionRequireApproval:
		// require_approval: needs-human path with a status reason distinct from deny.
		// The box does NOT start; runtime.Run returns nil (valid terminal outcome).
		return gateOutcome{
			allowed:        false,
			reason:         "policy: requires human approval",
			auditEmit:      auditEmit,
			policyDecision: string(policy.DecisionRequireApproval),
			policyReason:   policyReason,
		}, nil
	default:
		// deny and any fail-closed deny.
		return gateOutcome{
			allowed:        false,
			reason:         "policy: decision denied",
			auditEmit:      auditEmit,
			policyDecision: string(policy.DecisionDeny),
			policyReason:   policyReason,
		}, nil
	}
}

// resolvePolicyBin resolves the policy-engine binary path: an explicit
// AGENT_BUILDER_POLICY_BIN value (validated executable on $PATH) takes
// precedence, otherwise the bare name "policy-engine" is looked up on $PATH. An
// unresolvable binary is a hard configuration error (fail-loud, mirroring
// resolveAuditBin). Only called when config.PolicyBin is non-empty.
func resolvePolicyBin(configured string) (string, error) {
	if configured != "" {
		resolved, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("run config: %s %q is not an executable policy-engine binary: %w", EnvPolicyBin, configured, err)
		}
		return resolved, nil
	}
	resolved, err := exec.LookPath("policy-engine")
	if err != nil {
		return "", fmt.Errorf("run config: %s is set but no policy-engine binary resolves (set %s or add policy-engine to PATH): %w", EnvPolicyBin, EnvPolicyBin, err)
	}
	return resolved, nil
}

// newAuditSink is the seam runtime.Run uses to construct the audit Sink when
// AGENT_BUILDER_AUDIT_RECORD is set. The production implementation is
// newBlockSink (the os/exec-backed audit-trail block sink). Tests override it to
// inject a FakeSink and assert exactly which audit events the run emits — in
// particular, that the no-eligible-executor path (task 095) emits none.
var newAuditSink = newBlockSink

// newBlockSink resolves the audit-trail binary and verifies the chain logfile
// path is writable, then constructs the production audit.BlockSink. It fails
// fast with a clear configuration error when the binary cannot be resolved or
// the path cannot be written — auditing is never silently skipped once
// AGENT_BUILDER_AUDIT_RECORD is configured. The supervisor depends only on the
// audit.Sink interface; BlockSink reaches the block over os/exec, so no block
// package enters the supervisor import graph (F-003 holds).
func newBlockSink(config Config) (audit.Sink, error) {
	binPath, err := resolveAuditBin(config.AuditBin)
	if err != nil {
		return nil, err
	}
	if err := requireWritable(config.AuditRecordPath); err != nil {
		return nil, err
	}
	return audit.NewBlockSink(binPath, config.AuditRecordPath), nil
}

// newCheckpointSigner constructs an audit.CheckpointSigner when the checkpoint
// signing key env var is set. It fails fast (before dispatch, same as
// newBlockSink) when:
//   - The signing key file does not exist.
//   - The audit-trail binary cannot be resolved.
//   - The output directory is not writable (when AuditCheckpointOut is set).
//
// When AuditCheckpointKey is empty (checkpoint disabled), returns nil, nil.
func newCheckpointSigner(config Config) (*audit.CheckpointSigner, error) {
	if config.AuditCheckpointKey == "" {
		return nil, nil // checkpoint disabled; opt-in
	}
	if err := resolveCheckpointConfig(config); err != nil {
		return nil, err
	}
	binPath, err := resolveAuditBin(config.AuditBin)
	if err != nil {
		return nil, err
	}
	return audit.NewCheckpointSigner(
		binPath,
		config.AuditRecordPath,
		config.AuditCheckpointLogID,
		config.AuditCheckpointKey,
		config.AuditCheckpointOut,
	), nil
}

// resolveCheckpointConfig validates the checkpoint-specific configuration
// fields before dispatch, mirroring the resolveAuditBin/requireWritable pattern.
// It is only called when AuditCheckpointKey is non-empty (checkpoint enabled).
func resolveCheckpointConfig(config Config) error {
	// The signing key file must exist.
	if _, err := os.Stat(config.AuditCheckpointKey); err != nil {
		return fmt.Errorf("run config: %s %q is not accessible: %w", EnvAuditCheckpointKey, config.AuditCheckpointKey, err)
	}
	// When an output path is specified, the parent directory must be writable.
	if config.AuditCheckpointOut != "" {
		if err := requireWritableDir(EnvAuditCheckpointOut, filepath.Dir(config.AuditCheckpointOut)); err != nil {
			return err
		}
	}
	return nil
}

// requireWritableDir confirms the directory at path exists and is writable
// before dispatch. It is used by resolveCheckpointConfig to validate the
// output directory for the checkpoint JSON.
func requireWritableDir(envName, dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("run config: %s parent directory %q is not accessible: %w", envName, dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("run config: %s parent directory %q is not a directory", envName, dir)
	}
	// Probe writability by creating a temp file in the directory.
	f, err := os.CreateTemp(dir, ".agent-builder-probe-*")
	if err != nil {
		return fmt.Errorf("run config: %s parent directory %q is not writable: %w", envName, dir, err)
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return nil
}

// resolveAuditBin resolves the audit-trail binary path: an explicit
// AGENT_BUILDER_AUDIT_BIN value (validated executable) takes precedence,
// otherwise the bare name "audit-trail" is looked up on $PATH. An unresolvable
// binary is a hard configuration error.
func resolveAuditBin(configured string) (string, error) {
	if configured != "" {
		resolved, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("run config: %s %q is not an executable audit-trail binary: %w", EnvAuditBin, configured, err)
		}
		return resolved, nil
	}
	resolved, err := exec.LookPath("audit-trail")
	if err != nil {
		return "", fmt.Errorf("run config: %s is set but no audit-trail binary resolves (set %s or add audit-trail to PATH): %w", EnvAuditRecord, EnvAuditBin, err)
	}
	return resolved, nil
}

// requireWritable confirms the audit chain logfile path can be created/appended
// before dispatch. The block owns the chain format and appends to this path; we
// only verify the host side can write it so a misconfigured path fails loudly
// up front rather than mid-run.
func requireWritable(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644) //nolint:gosec // operator-supplied audit chain path
	if err != nil {
		return fmt.Errorf("run config: %s %q is not writable: %w", EnvAuditRecord, path, err)
	}
	return file.Close()
}

func validatePaths(config Config) error {
	if err := requireDir(EnvTaskRoot, config.TaskRoot); err != nil {
		return err
	}
	if err := requireDir(EnvWorktree, config.Worktree); err != nil {
		return err
	}
	return nil
}

func requireDir(name, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("run config: %s %q is not usable: %w", name, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("run config: %s %q is not a directory", name, path)
	}
	return nil
}

func missingConfig(name string) error {
	return fmt.Errorf("run config: missing %s", name)
}

func cleanPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

// getenvSecretSource wraps a getenv function as a secrets.SecretSource.
// This lets ConfigFromEnv delegate token reads through the SecretSource seam
// while preserving the existing getenv-based test contract (tests pass a fake
// getenv; the SecretSource reads from the same fake getenv, so results match).
func getenvSecretSource(getenv func(string) string) secrets.SecretSource {
	return &envFuncSecretSource{getenv: getenv}
}

// envFuncSecretSource implements secrets.SecretSource backed by an arbitrary
// getenv function rather than os.Getenv. Used internally by ConfigFromEnv.
type envFuncSecretSource struct {
	getenv func(string) string
}

func (s *envFuncSecretSource) ProviderToken() (authToken, oauthToken string) {
	return s.getenv(executor.ClaudeCLIAuthEnv), s.getenv(executor.ClaudeCLIOAuthEnv)
}

func (s *envFuncSecretSource) PublisherTokens() (gitToken, githubToken string) {
	return s.getenv(EnvGitToken), s.getenv(EnvGitHubToken)
}

func (s *envFuncSecretSource) NamedProviderToken(ref string) (string, error) {
	return "", secrets.ErrSecretNotFound
}

type sandboxBox struct {
	runner   sandbox.Runner
	worktree string
	launcher string
	backend  string
	wiring   sandbox.RunWiring
	tier     string // exec-sandbox tier; "" => backend default (set by policy tier_select)
	limits   sandbox.Limits
}

func (b sandboxBox) Create(task supervisor.Task) (supervisor.BoxHandle, error) {
	if b.runner == nil {
		return supervisor.BoxHandle{}, fmt.Errorf("run config: missing sandbox runner")
	}
	// The execution-box image is ENTRYPOINT ["/bin/sh"] (ADR 032), so the command
	// is passed to /bin/sh as its arguments: a bare ["/bin/true"] becomes `sh /bin/true`,
	// which makes the shell read the ELF binary as a script ("ELF: not found", exit 2).
	// Use an sh -c form so the box-liveness probe runs `sh -c true` and exits 0.
	_, exitCode, err := b.runner.Run(sandbox.Request{
		Command:  []string{"-c", "true"},
		Worktree: b.worktree,
		Limits:   b.limits,
		Tier:     b.tier,
		Wiring:   b.wiring,
	})
	if err != nil {
		return supervisor.BoxHandle{}, err
	}
	if exitCode != 0 {
		return supervisor.BoxHandle{}, fmt.Errorf("sandbox: create probe exited %d", exitCode)
	}
	return supervisor.BoxHandle{
		ID:       "sandbox-" + strings.TrimSpace(task.ID),
		Worktree: b.worktree,
		Backend:  b.backend,
	}, nil
}

func (b sandboxBox) Kill(supervisor.BoxHandle) error {
	return nil
}

func (b sandboxBox) Teardown(supervisor.BoxHandle) error {
	return nil
}

type retryingInBoxLoop struct {
	executor       supervisor.Executor
	gate           supervisor.Gate
	worktree       string
	launcher       string
	statusWriter   agentloop.StatusWriter
	policy         agentloop.RetryPolicy
	publisher      branchpub.Publisher
	publishRemote  string
	publishSecrets []string
}

func (l retryingInBoxLoop) RunInside(ctx context.Context, handle supervisor.BoxHandle, task supervisor.Task, streams supervisor.RunStreams) error {
	runID := auditRunID(task, handle)
	writeCommand(streams, "containment=%s launcher=%s", handle.Backend, l.launcher)
	emitAudit(streams, audit.AuditEvent{
		Action: audit.ActionContainment, RunID: runID, TaskID: task.ID,
		Detail: audit.EventDetail{Launcher: l.launcher},
	})
	writeCommand(streams, "pick task %s", task.ID)
	emitAudit(streams, audit.AuditEvent{Action: audit.ActionPick, RunID: runID, TaskID: task.ID})
	writeStdout(streams, "task %s selected\n", task.ID)

	runner, err := agentloop.NewRetryingLoop(singleTaskSource{task: task}, l.executor, l.gate, l.worktree, l.statusWriter, l.policy)
	if err != nil {
		return err
	}
	outcome, err := runner.RunOnce(ctx)
	if err != nil {
		return err
	}
	for _, state := range outcome.LastOutcome.Trace {
		switch state {
		case agentloop.StateAttempt:
			writeCommand(streams, "attempt task %s attempt=%d", task.ID, outcome.Attempts)
			emitAudit(streams, audit.AuditEvent{
				Action: audit.ActionAttempt, RunID: runID, TaskID: task.ID,
				Detail: audit.EventDetail{Attempt: outcome.Attempts},
			})
		case agentloop.StateVerify:
			writeCommand(streams, "verify worktree %s", l.worktree)
			emitAudit(streams, audit.AuditEvent{
				Action: audit.ActionVerify, RunID: runID, TaskID: task.ID,
				Verdict: auditVerdict(outcome.LastOutcome.Verdict),
			})
		}
	}

	switch outcome.Kind {
	case agentloop.RetryOutcomeDone:
		writeStdout(streams, "executor attempt completed: branch=%s\n", outcome.Branch)
		writeStdout(streams, "gate passed: %s\n", summarizeVerdict(outcome.LastOutcome.Verdict))
		writeCommand(streams, "publish branch %s remote=%s", outcome.Branch, l.redact(l.publishRemote))
		emitAudit(streams, audit.AuditEvent{
			Action: audit.ActionPublish, RunID: runID, TaskID: task.ID,
			Detail: audit.EventDetail{Branch: outcome.Branch, Remote: l.redact(l.publishRemote)},
		})
		if l.publisher == nil {
			return fmt.Errorf("run: publish task %s: missing publisher", task.ID)
		}
		result, err := l.publisher.Publish(ctx, branchpub.Request{
			Task:     task,
			Worktree: l.worktree,
			Branch:   outcome.Branch,
			Remote:   l.publishRemote,
		})
		if err != nil {
			message := l.redact(err.Error())
			writeStderr(streams, "publication failed: %s\n", message)
			return fmt.Errorf("run: publish task %s: %s", task.ID, message)
		}
		prArtifact := result.PRURL
		if prArtifact == "" {
			prArtifact = result.PRID
		}
		writeStdout(streams, "publication recorded: branch=%s pr=%s\n", outcome.Branch, l.redact(prArtifact))
		writeCommand(streams, "finish task %s outcome=completed branch=%s pr=%s", task.ID, outcome.Branch, l.redact(prArtifact))
		emitAudit(streams, audit.AuditEvent{
			Action: audit.ActionFinish, RunID: runID, TaskID: task.ID, Outcome: audit.OutcomeCompleted,
			Detail: audit.EventDetail{Branch: outcome.Branch},
		})
		return nil
	case agentloop.RetryOutcomeEscalated:
		writeFailureEvidence(streams, outcome.LastOutcome)
		writeStderr(streams, "task %s escalated after %d attempts\n", task.ID, outcome.Attempts)
		emitAudit(streams, audit.AuditEvent{
			Action: audit.ActionEscalate, RunID: runID, TaskID: task.ID,
			Detail: audit.EventDetail{Attempt: outcome.Attempts},
		})
		emitAudit(streams, audit.AuditEvent{
			Action: audit.ActionFinish, RunID: runID, TaskID: task.ID, Outcome: audit.OutcomeFailed,
		})
		return fmt.Errorf("run: task %s escalated after %d attempts", task.ID, outcome.Attempts)
	case agentloop.RetryOutcomeIdle:
		writeStdout(streams, "no ready task\n")
		return nil
	default:
		return fmt.Errorf("run: unexpected retry outcome %q", outcome.Kind)
	}
}

func (l retryingInBoxLoop) redact(text string) string {
	return branchpub.Redact(text, l.publishSecrets)
}

type singleTaskSource struct {
	task supervisor.Task
}

func (s singleTaskSource) Next() (supervisor.Task, bool, error) {
	return s.task, true, nil
}

func summarizeVerdict(verdict gate.Verdict) string {
	if len(verdict.Results) == 0 {
		if verdict.OK {
			return "no steps"
		}
		return "failed before steps"
	}
	parts := make([]string, 0, len(verdict.Results))
	for _, result := range verdict.Results {
		status := "FAIL"
		if result.OK {
			status = "PASS"
		}
		parts = append(parts, status+" "+result.Name)
	}
	return strings.Join(parts, "; ")
}

func writeFailureEvidence(streams supervisor.RunStreams, outcome agentloop.Outcome) {
	switch outcome.Failure.Reason {
	case agentloop.FailureExecutorError:
		if outcome.Failure.Err != nil {
			writeStderr(streams, "executor error: %v\n", outcome.Failure.Err)
		}
	case agentloop.FailureExecutorIncomplete:
		writeStderr(streams, "executor incomplete: branch=%s\n", outcome.Branch)
	case agentloop.FailureGate:
		writeStderr(streams, "gate failed: %s\n", summarizeVerdict(outcome.Verdict))
		for _, result := range outcome.Verdict.Results {
			if result.OK {
				continue
			}
			output := strings.TrimSpace(result.Output)
			if output == "" {
				output = "no output"
			}
			writeStderr(streams, "gate step %s failed: %s\n", result.Name, output)
			return
		}
	}
}

func writeCommand(streams supervisor.RunStreams, format string, args ...any) {
	if streams.Command == nil {
		return
	}
	_, _ = fmt.Fprintf(streams.Command, format, args...)
}

// maybeEmitPolicyDecision emits an ActionPolicyDecision audit event when the
// audit_emit obligation is present (outcome.auditEmit == true) and sink is non-nil.
// It is a no-op when the obligation is absent or the sink is unconfigured.
// Append errors are intentionally swallowed — the event is a side-effect and does
// not affect routing (same error-swallow convention as emitAudit for in-loop events).
func maybeEmitPolicyDecision(sink audit.Sink, taskID string, outcome gateOutcome) {
	if !outcome.auditEmit || sink == nil {
		return
	}
	_ = sink.Append(audit.AuditEvent{
		Action: audit.ActionPolicyDecision,
		RunID:  taskID,
		TaskID: taskID,
		Detail: audit.EventDetail{
			PolicyDecision: outcome.policyDecision,
			PolicyReason:   outcome.policyReason,
		},
	})
}

// emitAudit projects one typed action event through the optional audit Sink. It
// is a no-op when no Sink is configured (streams.Audit == nil), so a run without
// AGENT_BUILDER_AUDIT_RECORD behaves exactly as before. Only typed action events
// flow here — raw stdout/stderr stay in the RunRecord and never reach the Sink.
//
// Append errors are intentionally swallowed for the in-loop projection: the
// chain is a parallel durable artifact, not the run's control flow, and the
// block-severity integrity gate (VerifyChain) is what surfaces a corrupt chain.
// Misconfiguration (unresolvable binary / unwritable path) is caught up front by
// newBlockSink before dispatch, so a live Append failure here is a block-side
// fault, not a silent skip of a configured audit.
func emitAudit(streams supervisor.RunStreams, ev audit.AuditEvent) {
	if streams.Audit == nil {
		return
	}
	_ = streams.Audit.Append(ev)
}

// auditRunID derives the run correlation id used in audit events, matching the
// RunRecord's run-id shape ("<task>/<box>").
func auditRunID(task supervisor.Task, handle supervisor.BoxHandle) string {
	parts := make([]string, 0, 2)
	if id := strings.TrimSpace(task.ID); id != "" {
		parts = append(parts, id)
	}
	if id := strings.TrimSpace(handle.ID); id != "" {
		parts = append(parts, id)
	}
	if len(parts) == 0 {
		return "run"
	}
	return strings.Join(parts, "/")
}

// auditVerdict maps a gate verdict onto the typed audit verdict.
func auditVerdict(verdict gate.Verdict) audit.AuditVerdict {
	if verdict.OK {
		return audit.VerdictPass
	}
	return audit.VerdictFail
}

func writeStdout(streams supervisor.RunStreams, format string, args ...any) {
	if streams.Stdout == nil {
		return
	}
	_, _ = fmt.Fprintf(streams.Stdout, format, args...)
}

func writeStderr(streams supervisor.RunStreams, format string, args ...any) {
	if streams.Stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(streams.Stderr, format, args...)
}

func newProductionGate() (supervisor.Gate, error) {
	verifier, err := gate.New(
		gate.GoBuildStep{},
		gate.GoVetStep{},
		gate.GoTestStep{},
		gate.GoFmtStep{},
		gate.GolangciLintStep{},
		gate.DepScanStep{},
		gate.CodeScannerStep{},
	)
	if err != nil {
		return nil, fmt.Errorf("construct production gate: %w", err)
	}
	return verifier, nil
}
