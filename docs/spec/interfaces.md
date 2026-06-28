# Interfaces

**Project:** agent-builder
**Last updated:** 2026-06-28 (task 084 — memory-guard adoption: memoryguard adapter + TamperAwarePlanStore + WithAuditSink)

The system's contact surface — everything that calls into the system, everything the system calls out to, and the public boundaries within the system. Each interface is a stable contract: changes here are breaking changes.

Not in this file:
- What the interfaces *do* (that's in [behaviors.md](behaviors.md))
- What data flows through them (that's in [data-model.md](data-model.md))
- How they're configured (that's in [configuration.md](configuration.md))

---

## Inbound interfaces

The CLI is agent-builder's only inbound surface. It exposes no network API and serves
no wire protocol.

### CLI

```
agent-builder <subcommand> [flags] [args]

Subcommands:
  run                   dispatch one supervisor loop
  version               print the agent-builder version
  verify <repo>         run the verification gate against a repo
  verify-checkpoint     verify a signed checkpoint against an Ed25519 public key
```

| Subcommand / flag | Type | Default | Effect |
|-------------------|------|---------|--------|
| `run` | subcommand | — | Builds the configured Phase 0 runtime pipeline from environment configuration, selects at most one ready task, dispatches it through the supervisor, and returns `0` when the run completes or idles. Returns `1` when configuration, containment, Executor, Gate, or loop execution fails. |
| `version` | subcommand | — | Prints `agent-builder <version>` to stdout and exits `0`. |
| `verify <repo>` | subcommand + path argument | — | Constructs the production verification Gate and runs it against the target repo path. Prints each Gate step result and exits `0` only when every blocking step passes. Exits `1` when any Gate step fails. |
| `verify-checkpoint` | subcommand | — | Verifies a signed checkpoint JSON file against an Ed25519 public key by delegating to `audit-trail checkpoint verify`. Exits `0` when valid, `1` when invalid, `2` on usage error or binary resolution failure. Writes JSON `{"valid":bool,"message":string}` to stdout. |
| `verify-checkpoint --checkpoint <path>` | flag | — | **Required.** Path to the `SignedCheckpoint` JSON file produced by `audit-trail checkpoint create`. |
| `verify-checkpoint --public-key <path>` | flag | — | **Required.** Path to the PEM-encoded Ed25519 public key file used to verify the checkpoint signature. |
| `verify-checkpoint --logfile <path>` | flag | `""` | Optional. Path to the chain logfile for live cross-check against the checkpoint. When omitted, only the Ed25519 signature over the checkpoint payload is verified (pure offline mode). |
| `-h`, `--help`, `help` | help command | — | Prints top-level usage, subcommands, and exit codes to stdout and exits `0`. |
| subcommand `-h` | help flag | — | Prints usage for the selected subcommand to stdout and exits `0`. |

**Exit codes:**
- `0` — success
- `1` — generic error (verification failed)
- `2` — usage error (missing required flags, binary resolution failure)

There is no `verify` flag that skips, bypasses, or weakens the Gate. The Gate is the definition of done and remains blocking.

`agent-builder run` has no flags. Its required and optional environment configuration is documented in [configuration.md](configuration.md#environment-variables).

**Binary resolution for `verify-checkpoint`:** The subcommand resolves the `audit-trail` binary from `AGENT_BUILDER_AUDIT_BIN` (takes precedence; stat-checked at invocation time) falling back to `audit-trail` on `$PATH`. Binary resolution failure exits `2` with a usage error naming the failure. This is the same resolution pattern as `internal/runtime` uses for `audit-trail emit` and `audit-trail verify`.

### HTTP / RPC API

None. agent-builder exposes no HTTP or RPC endpoints.

### Wire protocol

None served. agent-builder is a *client* of the vault, policy-engine, and audit-trail
block socket/CLI protocols — those are documented under [Outbound interfaces](#outbound-interfaces) and the blocks' own contracts, not served by agent-builder.

---

## Outbound interfaces

What agent-builder calls out to. Each external dependency is a coupling point — listed
explicitly so failure modes and version pinning are visible.

| Dependency | What we call | Library / version | Failure mode |
|------------|-------------|-------------------|--------------|
| Podman | `podman build`, `podman pod create`, `podman create`, `podman inspect`, `podman start`, `podman run --runtime <oci-runtime>`, `podman logs`, `podman pod rm`, and `podman rm` from `containment/execution-box/run.sh`; `podman info` for graph-driver and backing-FS detection (storage quota enforceability) | process `PATH`; rootless Podman for the current non-root user; configured OCI runtime names `runc`, `runsc`, or future `kata`; Gate scanner/linter tools mounted from `EXEC_BOX_GATE_TOOLS` / `--gate-tools` | Missing binary, failed `podman info`, unavailable selected OCI runtime, missing Gate toolchain artifact, failed image build, failed `podman create` / `podman run` (exits non-zero with a named error — never exit 0 on a non-started box), absent load-bearing CPU/memory/PID/SHM fields in TC-003 inspect, egress sidecar startup failure, or failed in-box probe exits non-zero and names the failing check. The per-container disk quota (`--storage-opt size=...`) degrades gracefully on non-XFS hosts (flag omitted, `WARNING` emitted, box still launches — see ADR 027). |
| Claude Code CLI | `claude -p <prompt>` in the configured task worktree | process `PATH` or `executor.ClaudeCLIConfig.CLIPath`; auth supplied through `ANTHROPIC_API_KEY` | Missing binary, blank config, missing token, subprocess non-zero exit, or missing/blank produced branch file fails the executor attempt |
| Codex CLI | `codex --model <model-id> --approval-policy never-require <prompt>` in the configured task worktree | process `PATH`; auth supplied through `OPENAI_API_KEY` resolved from `entry.SecretRef` via `secrets.SecretSource.NamedProviderToken` | Missing binary, `ErrSecretNotFound` on secret resolution, subprocess non-zero exit, or missing `BRANCH: <name>` marker in stdout fails the executor attempt |
| Gemini CLI | `gemini --model <model-id> <prompt>` in the configured task worktree | process `PATH`; auth supplied through `GEMINI_API_KEY` resolved from `entry.SecretRef` via `secrets.SecretSource.NamedProviderToken` | Missing binary, `ErrSecretNotFound` on secret resolution, subprocess non-zero exit, or missing `BRANCH: <name>` marker in stdout fails the executor attempt |
| armor | armor-compatible command configured by `armor.Config.Command` and invoked with JSON stdin/stdout | process `PATH` or caller-supplied command path; fakeable through `armor.Runner` | Missing command, subprocess timeout, non-zero exit, malformed JSON, malformed decision, or armor error output maps to a fail-closed `block` decision |
| Go toolchain | `go build ./...`, `go vet ./...`, `go test ./...` in the target worktree | process `PATH`; Go version supplied by the runtime environment | Missing `go` fails the Step; non-zero exit fails the Step with combined stdout/stderr |
| gofmt | `gofmt -l .` in the target worktree | process `PATH`; Go version supplied by the runtime environment | Missing `gofmt` fails the Step; non-zero exit fails the Step; non-empty output fails the Step as formatting drift |
| golangci-lint | `golangci-lint run` in the target worktree | process `PATH`; version supplied by the runtime environment | Missing `golangci-lint` fails the Step; non-zero exit fails the Step with combined stdout/stderr |
| dep-scan Go scanner | `dep-scan check --registry go --lockfile go.sum --lockfile-type go` in the target worktree when `go.sum` is present; passes without invocation when `go.sum` is absent (no third-party deps) | process `PATH`; version supplied by the runtime environment | Missing `go.sum` passes the Step; with `go.sum` present, missing `dep-scan` fails the Step with "missing tool" message; non-zero exit fails the Step with combined stdout/stderr |
| code-scanner | `code-scanner` in the target worktree | process `PATH`; version supplied by the runtime environment | Missing `code-scanner` fails the Step; non-zero exit fails the Step with combined stdout/stderr |
| git | `git push <remote> <branch>` in the target worktree | process `PATH` or `AGENT_BUILDER_GIT_CLI`; optional token supplied as `GIT_TOKEN` from `AGENT_BUILDER_GIT_TOKEN` | Missing binary, push rejection, auth failure, or non-zero exit fails publication and redacts configured token values from surfaced output |
| GitHub CLI | `gh pr view --head <branch> --json url,number --jq .url`; `gh pr create --head <branch> --fill` in the target worktree | process `PATH` or `AGENT_BUILDER_GH_CLI`; optional token supplied as `GH_TOKEN` and `GITHUB_TOKEN` from `AGENT_BUILDER_GITHUB_TOKEN` | Missing binary, auth failure, malformed repository state, or PR creation failure fails publication and redacts configured token values from surfaced output |

---

## Internal public surface

Interfaces *within* agent-builder that are stable contracts between modules. A package's
public API that isn't listed here is an implementation detail — callers should not depend
on it, and promotion to this list is a deliberate decision (often via ADR).

### Interface: `gate.Step`

```go
type Step interface {
	Name() string
	Run(repoPath string) StepResult
}
```

- **Implementors:** `gate.GoBuildStep`, `gate.GoVetStep`, `gate.GoTestStep`, `gate.GoFmtStep`, `gate.GolangciLintStep`, `gate.DepScanStep`, `gate.CodeScannerStep`, and future concrete checks.
- **Consumers:** `gate.Gate`.
- **Stability:** governed by ADR 002 and updated with any task that changes gate behavior.
- **Required behavior:** each Step is blocking. It receives the repo worktree path, returns captured output in its StepResult, and reports pass/fail through `OK`.

### Interface: supervisor gate seam

```go
type Gate interface {
	Verify(repoPath string) gate.Verdict
}
```

- **Implementors:** `*gate.Gate` and test fakes.
- **Consumers:** supervisor/agent-loop code that needs the machine-checkable definition of done.
- **Stability:** governed by ADR 002.
- **Required behavior:** `Verify` has no skip or bypass parameter. It returns OK only when every configured blocking step passes.

### Interface: `supervisor.Executor`

```go
type Executor interface {
	Run(t Task) (Result, error)
}
```

- **Implementors:** `*executor.ClaudeCLI` and test fakes.
- **Consumers:** `loop.Loop` and retry/escalation policy code.
- **Stability:** governed by `docs/tasks/test-specs/022-claude-cli-executor-test-spec.md` and updated with any task that changes executor inputs, branch output, or auth handling.
- **Required behavior:** `Run` attempts exactly one task in the configured worktree and returns the produced branch in `Result.Branch`. `Result.OK` is true only when the subprocess exits successfully and reports a non-blank branch. Executor errors fail the attempt before Gate verification; callers decide retry/escalation.

### Concrete executor: `executor.ClaudeCLI`

```go
type ClaudeIngestionPolicy string

const (
	ClaudeIngestionDisabled ClaudeIngestionPolicy = "disabled"
	ClaudeIngestionReviewed ClaudeIngestionPolicy = "reviewed"
)

const (
	ClaudeCLIAuthEnv      = "ANTHROPIC_API_KEY"
	ClaudeCLIOAuthEnv     = "CLAUDE_CODE_OAUTH_TOKEN"
	ClaudeCLIBaseURLEnv   = "ANTHROPIC_BASE_URL"   // set for local/translation-proxy entries
	ClaudeCLIAuthTokenEnv = "ANTHROPIC_AUTH_TOKEN" // gateway bearer token; placeholder injected for local entries
)

type ClaudeCLIConfig struct {
	CLIPath          string
	Worktree         string
	AuthToken        string
	OAuthToken       string
	BaseURL          string // translation-proxy URL for local entries; empty for cloud entries
	IngestionPolicy  ClaudeIngestionPolicy
	IngestionHarness *executorharness.Harness
}

func ParseClaudeIngestionPolicy(raw string) (ClaudeIngestionPolicy, error)
func NewClaudeCLI(config ClaudeCLIConfig) *ClaudeCLI
func NewClaudeCLIFromEnv(worktree string) *ClaudeCLI
func NewClaudeCLIFromEntry(entry registry.RegistryEntry, secretSource secrets.SecretSource, worktree string) *ClaudeCLI
func (e *ClaudeCLI) IngestionPolicy() ClaudeIngestionPolicy
func (e *ClaudeCLI) Run(task supervisor.Task) (supervisor.Result, error)
func (e *ClaudeCLI) HandleWebContent(ctx context.Context, event executorharness.WebContentEvent, continuation executorharness.ContentContinuation) executorharness.ContentResult
func (e *ClaudeCLI) HandleToolCall(ctx context.Context, event executorharness.ToolCallEvent, toolExecutor executorharness.ToolExecutor) executorharness.ToolCallResult
```

- **Outbound call:** `claude -p <prompt>` with `cmd.Dir` set to `ClaudeCLIConfig.Worktree`.
- **Branch contract:** the prompt names an executor-owned temp file where the CLI must write the produced branch. The executor trims that file and copies it into `supervisor.Result.Branch`.
- **Web/tool policy:** `IngestionPolicy` defaults to `disabled`. `disabled` fails closed for Claude-facing web/tool events while preserving ordinary subprocess execution. `reviewed` requires `IngestionHarness` and routes web/tool events through it before any continuation or tool executor can run. Unknown policy values and reviewed-without-harness configurations fail before subprocess start.
- **Auth contract (cloud mode):** when `BaseURL` is empty (cloud entry), the executor accepts either `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` (OAuth token preferred when both are set). Exactly one credential is injected into subprocess env; the other is stripped. Host `HOME`/XDG dirs are replaced with temp dirs. Both credential values are redacted from subprocess failure output. The executor fails before subprocess start when both are absent.
- **Auth contract (local/translation-proxy mode):** when `BaseURL` is non-empty (local entry, `entry.SecretRef == ""`), `ANTHROPIC_BASE_URL` is set to `BaseURL` in the subprocess env. `ANTHROPIC_AUTH_TOKEN` is injected with the fixed placeholder value `executor.LocalProxyAuthPlaceholder` (value: `"local-proxy-no-auth"`, not the operator's real key) to satisfy the Claude Code CLI's auth check. `ANTHROPIC_AUTH_TOKEN` (not `ANTHROPIC_API_KEY`) is required because the current Claude Code CLI validates `ANTHROPIC_API_KEY` as a real Anthropic credential and rejects a placeholder with `Not logged in`, whereas `ANTHROPIC_AUTH_TOKEN` is the gateway bearer-token var passed straight through to `ANTHROPIC_BASE_URL`. Neither `ANTHROPIC_API_KEY` nor `CLAUDE_CODE_OAUTH_TOKEN` is injected for local entries. The translation proxy at `BaseURL` converts Anthropic API requests to the local inference server and ignores the token value. Missing cloud credentials in the host environment are not an error when all registry entries are local.
- **`NewClaudeCLIFromEntry` contract:** constructs a `ClaudeCLI` from a `registry.RegistryEntry`. When `entry.SecretRef == ""` (local entry), sets `BaseURL = entry.Endpoint` and omits cloud auth. When `entry.SecretRef != ""` (cloud entry), resolves credentials via `secretSource.ProviderToken()`. Additive constructor — existing `NewClaudeCLI` and `NewClaudeCLIFromEnv` call sites are unchanged.

### Concrete executor: `executor.CodexCLI`

```go
const CodexAPIKeyEnv = "OPENAI_API_KEY"

func NewCodexCLI(entry registry.RegistryEntry, secretSource secrets.SecretSource, worktree string) *CodexCLI
func (c *CodexCLI) Run(task supervisor.Task) (supervisor.Result, error)
```

- **Outbound call:** `codex --model <model-id> --approval-policy never-require <prompt>` with `cmd.Dir` set to the configured worktree.
- **Branch contract:** the prompt instructs the CLI to write `BRANCH: <branch-name>` as the last output line. The executor extracts the branch name by scanning stdout in reverse for the `BRANCH:` prefix.
- **Auth contract:** the API key is resolved at dispatch time via `secretSource.NamedProviderToken(entry.SecretRef)`. The resolved key is injected as `OPENAI_API_KEY` into the subprocess env; `ErrSecretNotFound` fails the run before subprocess start. The key is redacted from subprocess failure output. It is never stored on the struct beyond the `Run` call.
- **Supervisor isolation:** `internal/executor` imports `internal/supervisor` (for types); `internal/supervisor` never imports `internal/executor` (F-003 invariant, enforced by `make fitness-supervisor-isolation`).

### Concrete executor: `executor.GeminiCLI`

```go
const GeminiAPIKeyEnv = "GEMINI_API_KEY"

func NewGeminiCLI(entry registry.RegistryEntry, secretSource secrets.SecretSource, worktree string) *GeminiCLI
func (g *GeminiCLI) Run(task supervisor.Task) (supervisor.Result, error)
```

- **Outbound call:** `gemini --model <model-id> <prompt>` with `cmd.Dir` set to the configured worktree.
- **Branch contract:** the prompt instructs the CLI to write `BRANCH: <branch-name>` as the last output line. The executor extracts the branch name by scanning stdout in reverse for the `BRANCH:` prefix.
- **Auth contract:** the API key is resolved at dispatch time via `secretSource.NamedProviderToken(entry.SecretRef)`. The resolved key is injected as `GEMINI_API_KEY` into the subprocess env; `ErrSecretNotFound` fails the run before subprocess start. The key is redacted from subprocess failure output. It is never stored on the struct beyond the `Run` call.
- **Supervisor isolation:** `internal/executor` imports `internal/supervisor` (for types); `internal/supervisor` never imports `internal/executor` (F-003 invariant, enforced by `make fitness-supervisor-isolation`).

### Executor Registry Interface

```go
type HarnessDriver string

const (
    HarnessClaudeCLI    HarnessDriver = "claude-cli"
    HarnessCodexCLI     HarnessDriver = "codex-cli"
    HarnessGeminiCLI    HarnessDriver = "gemini-cli"
    HarnessOllamaNative HarnessDriver = "ollama-native"
)

func (h HarnessDriver) String() string

// TranslationProxySeam names the local-entry endpoint convention:
// local inference server → translation proxy (LiteLLM/claude-code-router) → Claude CLI via ANTHROPIC_BASE_URL.
const TranslationProxySeam = "litellm/claude-code-router"

type QuotaBudget struct {
    Limit  int
    Window time.Duration
}

type AvailStatus string

const (
    AvailStatusAvailable AvailStatus = "available"
    AvailStatusExhausted AvailStatus = "exhausted"
)

type Availability struct {
    Status  AvailStatus
    ResetAt time.Time
}

type RegistryEntry struct {
    ID             string
    Harness        HarnessDriver
    CapabilityTier int
    CostWeight     int
    ModelID        string
    Endpoint       string
    SecretRef      string  // empty for local entries (no cloud auth)
    Budget         QuotaBudget
    Usage          int
    Availability   Availability
}

func (e RegistryEntry) IsUnlimited() bool

type Catalog struct { /* unexported */ }

func NewCatalog() *Catalog
func (c *Catalog) RegisterEntry(e RegistryEntry)
func (c *Catalog) LookupEntry(id string) (RegistryEntry, bool)
func (c *Catalog) UpdateEntry(e RegistryEntry)
func (c *Catalog) ListEntries() []RegistryEntry

func LoadFromEnv() ([]RegistryEntry, error)
```

- **Implementors:** `*registry.Catalog` (in-process catalog); `registry.LoadFromEnv` (env-var loader).
- **Consumers:** router (task 092), harness adapters (tasks 089–091), dispatcher/runtime wiring.
- **Stability:** governed by ADR 043 and updated with any task that changes executor registration, entry structure, or env-var config surface.
- **Required behavior:**
  - `HarnessDriver` discriminates which harness runs the loop; four values are supported (`claude-cli`, `codex-cli`, `gemini-cli`, `ollama-native`).
  - `String()` returns human-readable harness names.
  - `TranslationProxySeam` is a named constant identifying the local-entry pattern: local model → translation proxy (LiteLLM / claude-code-router) → Claude CLI redirected via `ANTHROPIC_BASE_URL`. Consumers may reference this constant for documentation or routing logic.
  - `QuotaBudget` with `Limit == 0` means unlimited (no cap). Non-zero limit caps dispatches over the rolling window.
  - `IsUnlimited()` returns `true` when `Budget.Limit == 0`. Local entries always have `Limit == 0` and are therefore always unlimited; the router must never mark an unlimited entry as exhausted.
  - `Availability` tracks whether an entry can be selected (`available`) or must be skipped until `ResetAt` (`exhausted`). Mutable state owned by the router.
  - `RegistryEntry` is a value type: all fields are directly accessible. `SecretRef` is empty for local entries (translation-proxy mode; no cloud auth needed).
  - `NewCatalog()` creates an empty, thread-safe catalog.
  - `RegisterEntry` adds an entry; panics if an entry with the same ID already exists.
  - `LookupEntry(id)` retrieves an entry by ID; returns `(RegistryEntry{}, false)` if not found. Empty ID returns `(_, false)`.
  - `UpdateEntry(e)` replaces an existing entry in place, preserving its position in the stable order; a no-op when `e.ID` is not present. This is the seam the router uses to mutate an entry's router-owned `Availability`/`Usage` state (ADR 043).
  - `ListEntries()` returns all entries in stable, deterministic (insertion) order.
  - `LoadFromEnv()` parses well-known env-var prefixes (`AGENT_BUILDER_REGISTRY_<ID>_*`) and returns enabled entries. For local entries (`local-qwen`, `local`), `SECRET_REF` is optional (empty is valid). For cloud entries, missing required fields or non-integer tier/cost/budget values return a descriptive error.

### Interface: model router (`internal/router`)

The capability/cost-first model router (ADR 043). It lives on the executor side of
the supervisor injection boundary — a sibling of `internal/executor` that imports
`internal/registry` and `internal/executor`. `internal/supervisor` imports neither
the router nor the registry (F-003; enforced by `make fitness-supervisor-isolation`).

Router state (Usage + Availability) is held in memory and optionally persisted to a
plain-text (JSON) file via `SaveState`/`LoadState`. The router accepts an injected
`Clock` seam for deterministic time-based tests.

```go
// Clock is the time-source seam. The production clock calls time.Now(); tests
// inject FakeClock so reset-window logic is testable without time.Sleep.
type Clock interface {
    Now() time.Time
}

// FakeClock is the test implementation: starts at an explicit time and advances
// only when Advance is called.
type FakeClock struct { /* unexported */ }
func NewFakeClock(t time.Time) *FakeClock
func (c *FakeClock) Now() time.Time
func (c *FakeClock) Advance(d time.Duration)

const DefaultCooldown = 5 * time.Minute

type Sensitivity int

const (
    SensitivityNone Sensitivity = iota
    SensitivitySensitive
)

type RoutingSpec struct {
    MinCapability   int
    SensitivityHint Sensitivity
}

type Router struct { /* unexported */ }

// New constructs a Router with the real wall clock and DefaultCooldown.
func New(catalog *registry.Catalog) *Router

// NewWithClock constructs a Router with an explicit clock and cooldown.
// Use this in tests with FakeClock.
func NewWithClock(catalog *registry.Catalog, clock Clock, cooldown time.Duration) *Router

func (r *Router) Select(spec RoutingSpec) (registry.RegistryEntry, error)
func (r *Router) OnGateFailure(entryID string)
func (r *Router) ResetEscalation()
func (r *Router) OnQuotaExhausted(entryID string, resetAt time.Time)

// RecordDispatch increments Usage for the entry. When Usage >= Budget.Limit,
// marks the entry AvailStatusExhausted with ResetAt = now + Budget.Window.
// No-op for local entries (Budget.Limit == 0) and unknown IDs.
func (r *Router) RecordDispatch(entryID string)

// OnRateLimit marks the entry exhausted (reactive 429 path). ResetAt is derived
// from retryAfterHeader (plain integer seconds, e.g. "60") when present and
// parseable, else from the configured cooldown (DefaultCooldown by default).
// No-op for local entries (Budget.Limit == 0) and unknown IDs.
func (r *Router) OnRateLimit(entryID string, retryAfterHeader string)

// SaveState persists Usage + Availability for all entries to a plain-text (JSON)
// file at path. Creates or overwrites the file.
func (r *Router) SaveState(path string) error

// LoadState restores Usage + Availability from a plain-text (JSON) file at path.
// A corrupted or malformed file returns a descriptive error (not a silent zero value).
// IDs in the file but absent from the catalog are silently skipped.
func (r *Router) LoadState(path string) error

func (r *Router) ResolveExecutor(spec RoutingSpec, secretSource secrets.SecretSource, worktree string) (supervisor.Executor, registry.RegistryEntry, error)

var ErrNoEligibleExecutor error
var ErrUnknownHarness error
```

- **Implementors:** `*router.Router`.
- **Consumers:** runtime/dispatcher wiring (task 095) resolves a recipe's routing spec to a concrete executor through this router.
- **Stability:** governed by ADR 043; updated with any task that changes selection, escalation, or the two-axis fallback model.
- **Required behavior:**
  - **Eligibility (hard filters):** an entry is eligible for a dispatch when `CapabilityTier >= spec.MinCapability` AND `Availability.Status == AvailStatusAvailable` AND it has not been escalated past via `OnGateFailure` in the current dispatch. `Sensitivity` is never a hard filter.
  - **Auto-recovery:** at the start of each `Select`, entries with `Availability.Status == AvailStatusExhausted` and `now > Availability.ResetAt` are automatically flipped to `AvailStatusAvailable` with `Usage` reset to `0`. No manual intervention is required.
  - **Selection:** `Select` returns the cheapest eligible entry (lowest `CostWeight`). Ties break by the soft sensitivity weight (a `SensitivitySensitive` hint sorts a local entry — `SecretRef == ""` and `IsUnlimited()` — before a non-local one), then by stable entry ID. The hint never excludes an otherwise-eligible entry. Returns `ErrNoEligibleExecutor` when no entry qualifies.
  - **Gate-failure escalation (quality axis):** `OnGateFailure(entryID)` records the entry as tried-and-failed for the current dispatch, so the next `Select` returns the next-stronger eligible entry. Once every eligible entry has gate-failed, `Select` returns `ErrNoEligibleExecutor`. Does not touch the entry's availability. `ResetEscalation()` clears this set for a fresh dispatch (availability state is unaffected).
  - **Quota-exhaustion fallback (availability axis):** `OnQuotaExhausted(entryID, resetAt)` marks the entry `AvailStatusExhausted` with the supplied `ResetAt`, removing it from the eligible set so the next `Select` routes sideways to the next cheapest still-available eligible entry at sufficient capability. It does NOT climb the quality ladder. The two axes are independent.
  - **Proactive budget check:** `RecordDispatch(entryID)` increments the entry's `Usage`. When `Usage >= Budget.Limit`, the entry is proactively marked `AvailStatusExhausted` with `ResetAt = now + Budget.Window`, preventing a wasted dispatch that would only earn a 429. The entry auto-recovers when the clock passes `ResetAt`.
  - **Reactive rate-limit handling:** `OnRateLimit(entryID, retryAfterHeader)` marks the entry `AvailStatusExhausted`. `ResetAt` is derived from the header value (plain integer seconds, e.g. `"60"`) when present and parseable; otherwise the configured cooldown is used.
  - **Quota-free backstop:** `RecordDispatch`, `OnRateLimit`, and `OnQuotaExhausted` are all silent no-ops for an entry with `Budget.Limit == 0` (every local entry — `IsUnlimited()` true) and for an unknown entry ID. A local entry is never marked exhausted.
  - **State persistence:** `SaveState(path)` writes current `Usage` and `Availability` for all entries as a plain-text (JSON) file. `LoadState(path)` restores that state; a corrupted or malformed file returns a descriptive error, never a silent zero value.
  - **Clock seam:** `New` uses the real wall clock (`time.Now()`). `NewWithClock` takes an explicit `Clock` so tests can inject `FakeClock` and advance time programmatically without `time.Sleep`.
  - **`ResolveExecutor`:** selects an entry via `Select`, then constructs the concrete `supervisor.Executor` for the entry's harness (`HarnessClaudeCLI` → `executor.NewClaudeCLIFromEntry`, `HarnessCodexCLI` → `executor.NewCodexCLI`, `HarnessGeminiCLI` → `executor.NewGeminiCLI`, `HarnessOllamaNative` → `executor.NewOllamaNative`), returning it alongside the selected entry so the caller can feed the entry ID back into the fallback hooks. Returns `ErrNoEligibleExecutor` when selection fails, or `ErrUnknownHarness` for an unrecognized harness driver. This is the executor-side boundary: the caller receives a `supervisor.Executor` seam, not a router.

### Interface: `supervisor.Reporter`

```go
// In internal/supervisor — symmetric outbound counterpart to GoalSource.
// Reporter is the seam for sending a message back to the human over the channel
// (approval solicitation, result summary). text is rendered at the channel edge.
type Reporter interface {
    Report(ctx context.Context, text string) error
}
```

- **Implementors:** `*telegram.ReplyAdapter` (Telegram outbound concrete); `*telegram.FakeReporter` (in-memory test double for orchestrator L2 tests).
- **Consumers:** `*orchestrator.Orchestrator` (task 081) for REQ-081-02 (solicit approval over `renderApprovalRequest`) and REQ-081-04 (report the rendered `PlanResult` summary).
- **Package:** `internal/supervisor` — symmetric outbound counterpart to `supervisor.GoalSource` (the inbound seam). The pure-stdlib signature (`context.Context`, `string`, `error`) ensures that adding this interface adds no import to `internal/supervisor`, preserving F-003 / F-007 (enforced by `make fitness-supervisor-isolation` and `make fitness-envelope-isolation`).
- **Stability:** governed by ADR 046 §2 and task 098.
- **Required behavior:** `Report` sends `text` over the channel via an encrypted+signed envelope (ADR 045). The concrete implementation must never log or expose the bot token, private key material, or the plaintext on the wire. The fake captures reported strings in order via `Reported()` for test assertions.

### Concrete outbound adapter: `telegram.ReplyAdapter`

```go
type ReplyConfig struct {
    BotToken   string
    BaseURL    string
    ChatID     string
    HTTPClient *http.Client
    OrchEdPriv ed25519.PrivateKey // orchestrator's Ed25519 priv — signs the envelope
    OrchXPriv  [32]byte           // orchestrator's X25519 priv — sender key for Seal
    OpXPub     [32]byte           // operator's X25519 pub — recipient key for Seal
    Logger     *slog.Logger
}

func NewReplyAdapter(cfg ReplyConfig) *ReplyAdapter
func (r *ReplyAdapter) Report(ctx context.Context, text string) error
```

- **Outbound call:** `POST <BaseURL>/bot<BotToken>/sendMessage` with JSON body `{"chat_id":"<ChatID>","text":"<sealed-envelope-JSON>"}`.
- **Envelope shape:** the sealed+signed `envelope.Envelope` appears as the `text` field of the `sendMessage` body. The plaintext never appears on the wire.
- **Key roles (outbound, orchestrator → operator — the mirror of inbound):**
  | Role | Inbound (operator → orchestrator) | Outbound reply (orchestrator → operator) |
  |------|-----------------------------------|------------------------------------------|
  | Ed25519 signer | operator signs | **orchestrator signs** (`OrchEdPriv`) |
  | Ed25519 verifier | adapter trusts operator pub | operator trusts **orchestrator pub** |
  | X25519 sender | operator priv | **orchestrator priv** (`OrchXPriv`) |
  | X25519 recipient | orchestrator pub | **operator pub** (`OpXPub`) |
- **Round-trip invariant (TC-098-03):** the emitted envelope is accepted by `envelope.VerifyAndOpen(env, orchEdPub, cache, opXPriv, orchXPub)` and recovers the exact reported text byte-for-byte.
- **Security invariants:** bot token and private key bytes (hex/base64) never appear in logs; no plaintext on the wire; `Report` uses `context.Context` to honour caller cancellation.

### In-memory fake: `telegram.FakeReporter`

```go
func NewFakeReporter() *FakeReporter
func (f *FakeReporter) Report(ctx context.Context, text string) error
func (f *FakeReporter) Reported() []string
```

- **Purpose:** test double for task 081's L2 tests — assert "approval solicited" / "summary reported" without a live bot, network, or crypto setup.
- **Behavior:** `Report` appends `text` to an internal slice; `Reported()` returns a copy in insertion order. Concurrent-safe.

### Tier-1 orchestrator (`internal/orchestrator`)

The orchestrator is the Tier-1 coordination layer above the `supervisor`/`runtime`
worker stack (ADR 042, ADR 046). It accepts a goal, decomposes it into a plan, gates
the plan on human approval, and — on allow/approval — dispatches one worker per
sub-goal by reusing `runtime.Run`. It aggregates outcomes into a typed `PlanResult`
and reports them over `supervisor.Reporter`. **It authors no code and never directly
imports `internal/executor`** (REQ-081-05; enforced by `make
fitness-orchestrator-no-executor` and TC-081-05's direct-import assertion).

```go
// Planner decomposes a goal into an ordered plan (ADR 046 §1).
type Planner interface {
    Plan(goal supervisor.Task) (Plan, error)
}

type Plan struct {
    Goal     string    // original goal text (inbound Task.Spec)
    GoalID   string    // original goal Task.ID — the plan-state key
    SubGoals []SubGoal // ordered
}

type SubGoal struct {
    RecipeName string          // recipe selected for this sub-goal
    Task       supervisor.Task // payload the dispatched worker runs
    TargetRepo string          // repo the worker acts on — flows into the spawn-worker decision + self-repo guard (task 085)
    Sink       string          // result-sink target — flows into the spawn-worker decision + self-repo guard (task 085)
}

// Typed aggregate (ADR 046 §2) — rendered to text only at the Reporter edge.
type PlanResult struct {
    Goal     string
    Outcomes []SubGoalOutcome
}
type SubGoalOutcome struct {
    SubGoal string // sub-goal spec text
    Recipe  string // recipe used
    Success bool
    Detail  string // "dispatched" / failure reason / "recipe not found: …"
}

// In-memory plan state behind a swappable store (ADR 046 §3; task 084 backend).
type PlanStore interface {
    Put(plan Plan)
    Get(goalID string) (Plan, bool)
    Delete(goalID string)
}

// Dispatch seam (ADR 046 §5). Default wires to runtime.Run; tests inject a spy.
type DispatchFunc func(ctx context.Context, sub SubGoal, base runtime.Config) error

// Narrow policy decide seam (satisfied by *policy.PolicyClient and test fakes).
type PolicyClient interface {
    Decide(req policy.DecideRequest) (policy.DecideResponse, error)
}

// Envelope-verified inbound approval (ADR 046 §4; task 098 SEC-001 carry-forward).
type Approval struct {
    From, To string // verified envelope roles — MUST be operator → orchestrator
    GoalID   string
    Approved bool
}

func New(planner Planner, pol PolicyClient, reporter supervisor.Reporter,
    base runtime.Config, opts ...Option) *Orchestrator
func (o *Orchestrator) Handle(ctx context.Context, goal supervisor.Task) (PlanResult, error)
func (o *Orchestrator) Resume(ctx context.Context, approval Approval) (PlanResult, error)
func (o *Orchestrator) HasPendingPlan(goalID string) bool
func (o *Orchestrator) Containment() Containment // task 085 — L2 containment posture

// Self-containment posture (task 085 / ADR 050 §3): the L2-assertable evidence the
// orchestrator runs under the SAME exec-sandbox profile as its workers.
const (
    ContainmentProfileExecSandbox = "exec-sandbox"
    EgressDefaultDeny             = "default-deny"
    OwnRepo                       = "github.com/tkdtaylor/agent-builder"
)
type Containment struct {
    Profile         string // "exec-sandbox"
    Rootless        bool   // true
    ReadOnlyRootfs  bool   // true
    ResourceLimited bool   // true
    EgressPolicy    string // "default-deny"
}
```

- **v1 Planner concrete — `StructuredPlanner`** (ADR 046 §1 Option A, rule-based, no
  LLM, no `internal/executor` import). Structured-plan text format: each non-blank,
  non-`#` line is one sub-goal `"<recipe>: <spec>"`; a line with no recognized recipe
  prefix uses the default recipe (`coding-agent`) with the whole line as the spec; a
  goal with no parseable sub-goal line collapses to a single sub-goal on the default
  recipe. The `LLMPlanner` is a named follow-on (ADR 046 §6) behind the same seam.
- **Approval gate (ADR 046 §4):** `Handle` issues `policy.Decide` with action
  `"spawn-plan"` (distinct from the worker `"run-task"`). `allow` → dispatch
  immediately; `deny` → report + stop; `require_approval` → **pause**: report the
  rendered plan (`"Approve? …"`), hold it in `PlanStore`, dispatch nothing. `Resume`
  continues a held plan when an approval returns; it asserts the verified envelope
  roles (`From == "operator"`, `To == "orchestrator"`) before acting and consumes the
  held plan so a stale approval cannot be replayed.
- **Dispatch (ADR 046 §5 / task 086 / ADR 042):** **concurrent**, one worker per
  sub-goal. `dispatchPlan` fans out one goroutine per approved sub-goal — **all start
  before any completes** — and joins them with a `sync.WaitGroup`. Per sub-goal the
  goroutine runs the full pipeline: `recipe.SelectRecipe` (an unknown recipe is a
  failed outcome, not a dispatch) → the `spawn-worker` gate + self-repo deny + audit →
  `DispatchFunc`. The default `DispatchFunc` sets `RecipeName` on a copy of the base
  `runtime.Config` and calls `runtime.Run` — the orchestrator never reimplements
  supervisor assembly (REQ-081-06). Concurrency invariants: (1) outcomes are written
  into a **pre-sized** `[]SubGoalOutcome` at the sub-goal index, so the aggregate is
  race-free **and** `PlanResult.Outcomes` order is deterministic = sub-goal order even
  though dispatch order is not; (2) a worker failure is recorded in **its own** outcome
  slot and never halts or cancels siblings — **best-effort** completion (REQ-086-02);
  (3) the shared `audit.Sink` (mutex-guarded hash chain) and `PlanStore` (mutex-guarded)
  are the only cross-goroutine mutable state, both serialize their writes, and
  `go test -race -count=1 ./internal/orchestrator/...` is clean (REQ-086-04). The
  SEC-003 deny-audit hard-error is preserved: a failed deny-event append is collected
  from the goroutines and halts the plan after the join. The shared
  `envelope.ReplayCache` per direction (083 SEC-001 carry-forward) is itself
  mutex-guarded; the live transport must pass **one** long-lived cache across all
  concurrent workers, never a fresh per-worker cache.
- **Per-worker spawn-worker gate (task 085 / ADR 050 §1):** before dispatching each
  sub-goal, `dispatchPlan` issues a second policy decision with action
  `"spawn-worker"`, `Resource{Type:"recipe", ID:recipeName,
  Properties:{target_repo, sink}}`. This is **additive** to `spawn-plan`: spawn-plan
  gates the whole plan once; spawn-worker gates each dispatch so a per-recipe policy can
  deny one worker without denying the plan (a dispatched worker is thus gated twice —
  here plus its own `run-task` inside `runtime.Run`, defense-in-depth). `allow` →
  dispatch; `deny`/`require_approval`/any error → that worker is **not** spawned, a
  failed (denied) outcome is recorded, and the denial is reported via `Reporter`.
  Fail-closed (routes on `resp.Decision`, never the error).
- **Self-repo bright line (task 085 / ADR 050 §2, ADR 042):** belt-and-suspenders.
  *Runtime:* `decideSpawnWorker` denies any sub-goal whose `TargetRepo` or `Sink` equals
  `OwnRepo` (`github.com/tkdtaylor/agent-builder`) **before** consulting the policy
  engine — fail-closed, independent of the policy file. *Static:* fitness check **F-013**
  (`make fitness-no-self-repo-sink`) asserts no registered recipe declares the own-repo
  as a result sink. Either alone is a single point of failure; both make the line
  unreachable by construction and at runtime.
- **Self-containment posture (task 085 / ADR 050 §3):** `Containment()` returns the
  L2-assertable posture — `exec-sandbox` profile, rootless, read-only rootfs,
  resource-limited, `default-deny` egress — the same profile a worker box runs under.
  Live Podman+runsc / nftables enforcement is **L6 operator-run** (not claimed in CI).
- **Fleet audit (task 085 / ADR 050 §4):** when constructed `WithAuditSink`, the
  orchestrator appends its own events — `goal-intake`, `plan-decided`, per-worker
  `spawn-decided`, `completion` — to the SAME `audit.Sink` chain its workers write to,
  so the chain is tamper-evident across both tiers. `audit-trail verify` → `valid=true`
  (L5 with the real binary; L2 FakeSink ordering/coverage otherwise).
- **Rendering:** `RenderPlanResult(PlanResult) string` produces the plain-text summary
  at the Reporter boundary; the typed `PlanResult` stays in the core (ADR 046 §2).
- **Stability:** governed by ADR 046 (extends ADR 042). v1 plan state is in-memory
  (`MemoryPlanStore`); memory-guarded backend (`MemoryGuardPlanStore`) is wired by
  task 084 when `AGENT_BUILDER_MEMORY_GUARD_BIN` is set.
- **memory-guard backend (task 084):** `MemoryGuardPlanStore` implements `PlanStore`
  (and `TamperAwarePlanStore`) and gates writes through `validate_write` + deletes
  through `verify_delete`. When the store implements `TamperAwarePlanStore`, `Handle`
  calls `TryPut` (write-gate rejection surfaces as an error) and `Resume` calls
  `TryDelete` (tamper → plan halted + `audit.ActionTamper` event emitted). The
  `TamperAwarePlanStore` extension keeps the base `PlanStore` interface unchanged
  (void `Put`/`Delete`) so `MemoryPlanStore` (the degraded-mode default) needs no change.

```go
// TamperAwarePlanStore is an optional extension of PlanStore implemented by
// MemoryGuardPlanStore. TryPut surfaces write-gate rejections; TryDelete surfaces
// delete-verify tamper signals. Asserted via type assertion in orchestrator.Handle
// and orchestrator.Resume; MemoryPlanStore does NOT implement this interface.
type TamperAwarePlanStore interface {
    PlanStore
    TryPut(plan Plan) error
    TryDelete(goalID string) error
}
```

---

### `internal/memoryguard` adapter

Binary IPC adapter leaf for the memory-guard block (ADR 049). The block is `package
main` (not importable as a Go library); this package speaks its JSON IPC contract via
per-op subprocess calls (one subprocess per `validate_write` / `verify_delete`).

```go
const EnvVarMemoryGuardBin = "AGENT_BUILDER_MEMORY_GUARD_BIN"

var ErrWriteGateDenied error // allow=false from validate_write
var ErrTamperDetected  error // confirmed=false OR residue_detected=true from verify_delete

type ExecRunner interface {
    Run(binPath string, reqJSON []byte) ([]byte, error)
}

type Client struct { /* unexported */ }
func NewClient(binPath string) *Client
func NewClientWithRunner(binPath string, runner ExecRunner) *Client
func (c *Client) ValidateWrite(entry, identity string) (storedID string, err error)
func (c *Client) VerifyDelete(storedID string) error

type MemoryGuardStore[P any] struct { /* unexported */ }
func NewMemoryGuardStore[P any](client *Client, identity string) *MemoryGuardStore[P]
func (s *MemoryGuardStore[P]) Put(key string, plan P) error
func (s *MemoryGuardStore[P]) Get(key string) (P, bool)
func (s *MemoryGuardStore[P]) Delete(key string) error // returns ErrTamperDetected on tamper
func (s *MemoryGuardStore[P]) StoredID(key string) (string, bool)
```

**In `internal/orchestrator`** (not in `internal/memoryguard` — leaf isolation):

```go
// MemoryGuardPlanStore wraps MemoryGuardStore[Plan] and implements PlanStore +
// TamperAwarePlanStore. Lives in internal/orchestrator so the leaf never imports it.
type MemoryGuardPlanStore struct { /* unexported */ }
func NewMemoryGuardPlanStore(binPath, identity string) *MemoryGuardPlanStore
func NewMemoryGuardPlanStoreWithRunner(binPath, identity string, runner memoryguard.ExecRunner) *MemoryGuardPlanStore
func (s *MemoryGuardPlanStore) TryPut(plan Plan) error
func (s *MemoryGuardPlanStore) TryDelete(goalID string) error
func (s *MemoryGuardPlanStore) StoredID(goalID string) (string, bool)

// NewPlanStoreFromEnv reads AGENT_BUILDER_MEMORY_GUARD_BIN. Set → MemoryGuardPlanStore;
// unset → MemoryPlanStore + structured warning via logFn.
type MemoryGuardLogFunc func(msg string, keysAndValues ...any)
func NewPlanStoreFromEnv(logFn MemoryGuardLogFunc) PlanStore

// WithAuditSink wires an audit.Sink for tamper events (ActionTamper,
// Detail.TamperDetected=true). Optional; plan still halts on tamper when nil.
func WithAuditSink(sink audit.Sink) Option
```

- **IPC contract (memory-guard JSON, ADR 049):**
  - `validate_write`: `{"op":"validate_write","entry":"<json>","identity":"<actor>"}` → `{"allow":bool,"stored_id":"…","flags":[…]}`
  - `verify_delete`: `{"op":"verify_delete","id":"<stored_id>"}` → `{"confirmed":bool,"residue_detected":bool,"residue_summary":"…","deletion_hash":"…"}`
- **Leaf isolation (F-012):** `internal/memoryguard` imports only stdlib. Enforced by `make fitness-memoryguard-isolation`.
- **Degraded mode (REQ-084-04):** when `AGENT_BUILDER_MEMORY_GUARD_BIN` is unset, `NewPlanStoreFromEnv` returns `MemoryPlanStore` and calls `logFn` with a structured warning naming `AGENT_BUILDER_MEMORY_GUARD_BIN` and `"memory-guard"`. No IPC, no subprocess. Existing e2e tests pass unchanged.
- **Tamper halt (REQ-084-05):** when `VerifyDelete` returns `ErrTamperDetected`, `Resume` returns an error (plan halted, no dispatch) and emits `audit.AuditEvent{Action: ActionTamper, Detail: EventDetail{TamperDetected: true}}` through the wired `audit.Sink`.

---

### Orchestrator↔worker transport (`internal/channel/worker`)

The transport adapter carries work-items and results across the orchestrator↔worker
trust boundary (ADR 042) wrapped in `internal/envelope.Envelope` objects — Ed25519-signed,
X25519+AEAD-sealed, replay-checked. Per ADR 048 the v1 wire is **in-process** (matching
task 081's sequential dispatch), but the envelope is the load-bearing security layer
regardless of the wire (tamper-evidence, provenance, replay resistance, and a ready seam
for a future out-of-process worker). The package is a leaf (F-011): its only direct
`internal/` imports are `internal/envelope`, `internal/supervisor`, and `internal/audit`.

```go
// Sender wraps a payload in a signed+sealed envelope.
type Sender struct{ /* unexported */ }
func NewWorkItemSender(SenderConfig) *Sender // orchestrator → worker (From=orchestrator, To=worker)
func NewResultSender(SenderConfig) *Sender   // worker → orchestrator (From=worker, To=orchestrator)
func (s *Sender) DispatchWorkItem(supervisor.Task) (envelope.Envelope, error)
func (s *Sender) DispatchResult(supervisor.Result) (envelope.Envelope, error)

// Receiver verifies+opens an inbound envelope, then asserts the From/To roles.
type Receiver struct{ /* unexported */ }
func NewWorkItemReceiver(ReceiverConfig) *Receiver // worker side; asserts orchestrator→worker
func NewResultReceiver(ReceiverConfig) *Receiver   // orchestrator side; asserts worker→orchestrator
func (r *Receiver) ReceiveWorkItem(envelope.Envelope) (supervisor.Task, error)
func (r *Receiver) ReceiveResult(envelope.Envelope) (supervisor.Result, error)

// Startup key-material loader (REQ-083-05).
const EnvWorkerSigningKey = "AGENT_BUILDER_WORKER_SIGNING_KEY"
var ErrMissingSigningKey error // NAMED sentinel
func LoadSigningKey() (ed25519.PrivateKey, error)
func NewWorkItemSenderFromEnv(orchXPriv, workerXPub [32]byte) (*Sender, error)

var ErrRoleMismatch error // verified envelope From/To ≠ expected direction (task 098 SEC-001)
```

- **Key roles (mirror the Telegram adapter):** work-items — orchestrator signs (its
  Ed25519 priv) + seals (its X25519 priv → worker X25519 pub); results — worker signs +
  seals (→ orchestrator X25519 pub). A fresh `crypto/rand` nonce per message (from
  `envelope.Seal`), never reused.
- **Inbound ordering + role assertion:** receivers run `envelope.VerifyAndOpen` (verify →
  replay check → open) then assert `env.From`/`env.To` match the expected direction
  before returning the payload (task 098 SEC-001 carry-forward — key separation is not
  relied on alone). On any rejection the receiver returns the zero `Task`/`Result` plus a
  classified error and emits an `audit.ActionChannelReject` event whose `Detail.Reason`
  carries the classification (`bad_signature`, `unknown_key`, `replay_detected`,
  `role_mismatch`); plaintext and key material never appear in the event or in logs.
- **Startup fail-closed (REQ-083-05):** `LoadSigningKey` / `NewWorkItemSenderFromEnv`
  fail at construction (not at first receipt) when `AGENT_BUILDER_WORKER_SIGNING_KEY` is
  unset or names an absent/malformed key file; the error satisfies
  `errors.Is(err, ErrMissingSigningKey)` and names the variable.
- **Integration depth (ADR 048 §1):** v1 wires the transport's startup key-material check
  and the envelope wrap/unwrap seam. Task 086 makes `Orchestrator.dispatchPlan`
  **N-concurrent** (one goroutine per sub-goal) and confirms the shared
  `envelope.ReplayCache` is goroutine-safe under `-race` (083 SEC-001 carry-forward: one
  long-lived shared cache per direction across all workers). Wiring the transport's
  signed envelopes onto the **live** dispatch path (the orchestrator constructing
  `Sender`/`Receiver` around each `runtime.Run` call, with the shared cache) remains a
  follow-on: the `DispatchFunc` seam keeps the live sandbox launch behind a stub so
  concurrency + race-safety are L2-testable in-process, and the envelope sign/verify +
  replay are exercised in `internal/channel/worker` (including a concurrent shared-cache
  test). An out-of-process / cross-host concrete (e.g. agent-mesh A2A, ADR 048 §5) can
  implement the same seam later without changing the orchestrator.
- **Stability:** governed by ADR 048 (extends ADR 042/045). Leaf isolation enforced by
  `make fitness-worker-transport-isolation` (F-011).

---

### Interface: supervisor dispatch lifecycle seams

```go
type ContainmentBox interface {
	Create(Task) (BoxHandle, error)
	Kill(BoxHandle) error
	Teardown(BoxHandle) error
}

type InBoxLoop interface {
	RunInside(BoxHandle, Task, RunStreams) error
}

func WithTask(task Task) Option
func WithContainmentBox(box ContainmentBox) Option
func WithInBoxLoop(loop InBoxLoop) Option
func WithLogger(logger *slog.Logger) Option
func WithRunRecordPath(path string) Option
func WithRunTimeout(timeout time.Duration) Option
func (s *Supervisor) Run() error

type RunStreams struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Command io.Writer
}
```

- **Implementors:** fake boxes and fake in-box loops in tests; concrete containment and loop wiring when runtime backends land.
- **Consumers:** `internal/supervisor.Supervisor`.
- **Stability:** governed by `docs/tasks/test-specs/017-supervisor-dispatch-test-spec.md`, `docs/tasks/test-specs/018-wall-clock-kill-test-spec.md`, and `docs/tasks/test-specs/019-run-log-collection-test-spec.md`.
- **Required behavior:** `Run` dispatches exactly one configured task per call. It creates a box before starting the in-box loop, passes the created `BoxHandle`, task, and host-side stream writers to the loop, and tears the box down exactly once after the loop returns, panics, or exceeds a configured timeout. Missing task, box, or loop dependencies fail before creation. Loop errors and recovered panics are returned after teardown. When `WithRunTimeout` receives a positive duration and the in-box loop exceeds it, the supervisor calls `Kill` on the created box, records a timed-out run outcome, then tears down the box. `Kill` implementations must terminate the active contained run so `RunInside` returns; kill errors are joined into the returned error and do not skip teardown. Non-positive timeouts leave the timeout disabled. When `WithRunRecordPath` is configured, stdout/stderr/command writes are persisted as RunRecord NDJSON during the run, the terminal outcome is written, and the file is closed before teardown. Retry and escalation behavior remain outside this seam.

### Concrete wiring: default CLI run runtime

```go
type Config struct {
	TaskRoot         string
	Worktree         string
	ClaudeCLI        string
	ClaudeToken      string
	ClaudeOAuthToken string
	RunRecordPath    string
	RunTimeout       time.Duration
	MaxAttempts      int
	PublishRemote    string
	GitCLI           string
	GitHubCLI        string
	GitToken         string
	GitHubToken      string
}

func ConfigFromEnv(getenv func(string) string) (Config, error)
func Run(config Config, stdout io.Writer) error
func RunFromEnv(stdout io.Writer) error
```

- **Consumers:** `internal/cli` uses `RunFromEnv` as the default implementation of `agent-builder run`.
- **Collaborators:** `tasksource.Source`, `executor.ClaudeCLI`, production `gate.Gate`, `sandboxruntime.Runner`, supervisor dispatch seams, `loop.RetryingLoop`, and `publisher.GitHubCLI`.
- **Required behavior:** required configuration is validated before task selection mutates status or the Executor can start. The runtime selects at most one task, gives that task to the supervisor, publishes only after Executor success plus Gate pass plus non-empty branch capture, and records pick/attempt/verify/publish/finish evidence through the supervisor RunRecord streams when configured.

### Interface: branch publisher

```go
type Publisher interface {
	Publish(context.Context, Request) (Result, error)
}

type Request struct {
	Task     supervisor.Task
	Worktree string
	Branch   string
	Remote   string
}

type Result struct {
	Branch string
	PRURL  string
	PRID   string
}
```

- **Implementors:** `*publisher.GitHubCLI`; tests provide fake git/gh commands or fake publisher seams.
- **Consumers:** default run wiring after a retry outcome succeeds.
- **Stability:** governed by `docs/tasks/test-specs/034-branch-pr-publication-test-spec.md`.
- **Required behavior:** publication is attempted only for a non-blank branch and non-blank remote after the Gate has passed. Publisher failures return errors that make the run non-successful. Configured git/GitHub token values are redacted from externally-visible errors.

### Concrete publisher: `publisher.GitHubCLI`

```go
type GitHubCLIConfig struct {
	GitPath     string
	GHPath      string
	Worktree    string
	Remote      string
	GitToken    string
	GitHubToken string
}

func NewGitHubCLI(config GitHubCLIConfig) *GitHubCLI
func (p *GitHubCLI) Publish(context.Context, publisher.Request) (publisher.Result, error)
```

- **Outbound calls:** `git push <remote> <branch>` runs first. `gh pr view --head <branch> --json url,number --jq .url` reuses an existing PR when available. `gh pr create --head <branch> --fill` creates the PR artifact when no existing PR is found.
- **Auth contract:** `GitToken`, when supplied, is passed as `GIT_TOKEN`. `GitHubToken`, when supplied, is passed as `GH_TOKEN` and `GITHUB_TOKEN`. The publisher does not read arbitrary host-home credential files itself, and it redacts configured token values from command output embedded in errors.

### Interface: exec-sandbox `run()` adapter seam

```go
type Runner interface {
	Run(Request) (Result, int, error)
}

type Request struct {
	Command  []string
	Worktree string
	Limits   Limits
	Tier     string    // "bubblewrap" (default) or "gvisor" (ADR 035)
	Wiring   RunWiring // vault/proxy token-brokering wiring (ADR 036); zero value = empty wiring
}

type RunWiring struct {
	VaultSocket   string   // Unix socket path of the running vault daemon
	SecretRefs    []string // opaque vault handles to inject (never plaintext values)
	InjectionMode string   // "proxy" when vault wiring is active; "" otherwise
}

type Limits struct {
	WallClockTimeout time.Duration
	MemoryBytes      int64
	CPUCount         int
	PidsLimit        int
	EgressAllowlist  []string
}

type Result struct {
	Stdout   string
	Stderr   string
	Duration time.Duration
}
```

- **Implementors:** `podman.Runner` for the rootless Podman execution-box (ADR 021) and `execsandbox.Runner` for the exec-sandbox block (ADR 035) are production backends wired into the default `agent-builder run` pipeline (runtime selects one based on binary availability); in-process `sandbox.FakeRunner` for tests; `sandboxruntime.Runner` for the retired rented `@anthropic-ai/sandbox-runtime` adapter remains in the tree for reference but is no longer wired into the run pipeline.
- **Consumers:** supervisor construction accepts the interface. Dispatch lifecycle code uses the interface when task execution is implemented.
- **Stability:** governed by ADR 020 and updated with any task that changes contained-run inputs, outputs, or error semantics.
- **Required behavior:** `Command` is argv-style and must contain a non-blank executable at index 0. `Worktree` is the target repo worktree path mounted or made available to the backend (non-empty, must exist, must be a directory). `Limits` is a typed struct, not a map, and carries wall-clock timeout, memory limit, CPU count, process ID limit, and egress allowlist values. `Result` captures stdout, stderr, and duration. The integer return is the process exit code. A non-zero exit code is returned with nil error when the backend ran the command; non-nil error means adapter/backend failure or invalid request.
- **Vault wiring contract (ADR 036, task 066):** `Request.Wiring` carries the vault token-brokering wiring from the trusted host into the box. The **zero-value `RunWiring`** produces empty `wiring.vault_socket`, empty `wiring.injection_mode`, and an empty `run.secret_refs` array — the ADR 035 deferred default, with no behavior change. When populated by `runtime` (only when `AGENT_BUILDER_VAULT_BIN` is set), `Wiring.VaultSocket` is the live vault daemon socket, `Wiring.SecretRefs` are **opaque vault handles** (never plaintext token values), and `Wiring.InjectionMode` is `"proxy"`. The `execsandbox.Runner` maps these onto `wiring.vault_socket`, `run.secret_refs`, and `wiring.injection_mode` in the block's JSON RunRequest; exec-sandbox calls `vault.inject` per handle at spawn time and the egress proxy injects the credential. The raw git/GitHub token values are therefore never present in the RunRequest JSON.

### Concrete backend: `sandboxruntime.Runner`

> **Retained out-of-graph, not in the run pipeline (ADR 021).** This adapter for the rented `@anthropic-ai/sandbox-runtime` (`srt`) backend was removed from the default `agent-builder run` wiring by ADR 021; `internal/runtime` no longer imports it and the `fitness-no-srt` check enforces its absence from the run graph. The interface below is documented for reference only — the live backends are `podman.Runner` and `execsandbox.Runner`.

```go
type Config struct {
	CLIPath string
}

func New(Config) *Runner
func (r *Runner) Run(sandbox.Request) (sandbox.Result, int, error)
```

- **Outbound call:** `srt --settings <temp-settings-json> <Command...>` with `cmd.Dir` set to the validated worktree.
- **Settings contract:** `Limits.EgressAllowlist` is converted from exact `host:port` entries to sandbox-runtime `network.allowedDomains` hostnames; an empty allowlist writes an empty `allowedDomains` list. Filesystem settings allow reads/writes for the worktree, allow writes to temp, deny writes to `.env`, and deny reads from common credential directories.
- **Failure contract:** invalid command, missing/non-directory worktree, malformed allowlist entries, missing `srt`, settings-file failure, and wall-clock timeout return non-nil adapter errors. A wrapped command that exits non-zero returns that exit code with nil adapter error.

### Concrete backend: `podman.Runner`

```go
func New() *Runner
func NewWithLauncher(path string) *Runner
func (r *Runner) Run(sandbox.Request) (sandbox.Result, int, error)
```

- **Outbound call:** `containment/execution-box/run.sh --worktree <path> [--egress-allowlist <tmpfile>] [--] <Command...>` with resource limits passed via environment variables `EXEC_BOX_CPUS`, `EXEC_BOX_MEMORY`, and `EXEC_BOX_PIDS_LIMIT`.
- **Egress allowlist contract:** each allowlist entry is written to a temporary file in `host:port # comment` format; non-commented entries cause the launcher to reject the allowlist. Zero values for memory, CPU, or process ID leave the corresponding env var unset (launcher uses its defaults).
- **Failure contract:** invalid command, blank/missing worktree, and wall-clock timeout return non-nil adapter errors. A launcher that exits non-zero returns that exit code with nil adapter error.

### Concrete backend: `execsandbox.Runner`

```go
func New(binPath string) *Runner
func (r *Runner) Run(sandbox.Request) (sandbox.Result, int, error)
```

- **Outbound call:** `<binary> run` with a JSON RunRequest struct on stdin containing `run.payload` (shell script), `run.profile` (limits and capabilities), `run.tier`, `run.workdir` (absolute host worktree path or "" for no mount), `run.env` (map of environment variables provisioned into the payload), `run.secret_refs` (opaque vault handles; empty array unless vault is enabled), and `wiring` (vault socket, audit socket, origin_map for egress routing, injection_mode). When vault is configured (`AGENT_BUILDER_VAULT_BIN` set), `wiring.vault_socket`, `run.secret_refs`, and `wiring.injection_mode="proxy"` are non-empty and carry the git/GitHub token brokering (ADR 036); otherwise they remain empty.
- **Command form contract:** `Request.Command` is processed by the adapter to extract shell-script bodies: the ADR 032 probe form `["-c", "<script>"]` and the `["/bin/sh", "-c", "<script>"]` form both extract `<script>` directly as the payload; all other forms are shell-quoted. This reflects the block's `/usr/bin/sh /payload.sh` execution model (ADR 032).
- **Toolchain forwarding contract:** when the Go toolchain is discoverable (via `go env GOROOT` or `AGENT_BUILDER_EXEC_SANDBOX_GOROOT`), the adapter adds a `FileRead` capability entry with the toolchain root path. When `AGENT_BUILDER_GATE_TOOLS` is set or the bundled gate-tools directory exists, it is also added to `FileRead.paths`. The adapter populates `run.env["PATH"]` with these directories plus the standard system PATH (`/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`), enabling in-box resolution of Go toolchain and gate tools (`go`, `gofmt`, `golangci-lint`, `dep-scan`, `code-scanner`).
- **Egress routing contract:** `Request.Limits.EgressAllowlist` entries are forwarded both to a `NetConnect` capability in `run.profile.capabilities` (for the block's egress gate) and to `wiring.origin_map`, which maps hostname → `[host, port]` pairs to allow the block's egress proxy to route allowlisted requests.
- **Worktree mapping contract:** `Request.Worktree` is validated to be an absolute existing directory, then forwarded to `run.workdir` on the block's JSON contract. The block mounts it read-write at `/work` inside the sandbox with the payload's initial working directory set to `/work` (so absolute paths and relative paths starting from repo root both work). An empty `Request.Worktree` is an error (matching Podman backend semantics).
- **Sandbox status contract:** the block returns `sandbox_status` containing `sandbox_id`, `tier`, `duration_ms`, `status` (clean/timeout/degraded/error), and `limits` with optional `degraded` field listing degraded resources. These are surfaced on `Result.SandboxID`, `Result.Tier`, `Result.Status`, and `Result.Degraded` respectively.
- **Failure contract:** invalid command, missing/non-directory worktree, non-executable binary, missing `AGENT_BUILDER_EXEC_SANDBOX_BIN`, malformed JSON, and block errors return non-nil adapter errors. A block that exits non-zero or returns an error field in its JSON output is a loud error. A wrapped command that exits non-zero is returned as exit code with nil adapter error.

### Interface: `tasksource.Source`

```go
func New(fsys fs.FS, roadmapPath string, taskDirs ...string) *Source

func (s *Source) Candidates() ([]Candidate, error)
func (s *Source) Next() (supervisor.Task, bool, error)
```

- **Implementors:** `*tasksource.Source`.
- **Consumers:** future supervisor/agent-loop task picking code.
- **Stability:** governed by `docs/tasks/test-specs/010-roadmap-task-source-test-spec.md`.
- **Required behavior:** the source reads through `fs.FS`, parses task files into deterministic candidate order, returns the first ready task whose dependencies are completed, and exposes no write-side operation.

### Interface: `tasksource.StatusWriter`

```go
func NewStatusWriter(root string, taskDirs ...string) *StatusWriter

func (w *StatusWriter) WriteStatus(taskID string, status WritableStatus) (StatusWriteResult, error)
```

- **Implementors:** `*tasksource.StatusWriter`.
- **Consumers:** retrying loop status governance code.
- **Stability:** governed by `docs/tasks/test-specs/011-task-status-writer-test-spec.md`.
- **Required behavior:** the writer exposes only a task ID plus constrained status marker mutation method. It accepts `WritableStatusDone`, `WritableStatusBlocked`, and `WritableStatusNeedsHuman`; it rejects every other status value before writing. It rewrites exactly one `**Status:**` line in the matched task file and has no API for arbitrary content replacement.

### Interface: `loop.TaskSource`

```go
type TaskSource interface {
	Next() (supervisor.Task, bool, error)
}
```

- **Implementors:** `*tasksource.Source` and test fakes.
- **Consumers:** `loop.Loop`.
- **Stability:** governed by ADR 012 and `docs/tasks/test-specs/012-agent-loop-test-spec.md`.
- **Required behavior:** returns one ready task plus `ok == true`, no task plus `ok == false`, or an error before any executor attempt begins.

### Interface: `loop.Loop`

```go
func New(source TaskSource, executor supervisor.Executor, verifier supervisor.Gate, worktreePath string) (*Loop, error)

func (l *Loop) RunOnce() (Outcome, error)
```

- **Implementors:** `*loop.Loop`.
- **Consumers:** callers that need one inside-the-box loop cycle and escalation-policy consumers.
- **Stability:** governed by ADR 012 and `docs/tasks/test-specs/012-agent-loop-test-spec.md`.
- **Required behavior:** `RunOnce` records explicit state transitions, invokes the Executor only after a task is picked, invokes the Gate only after a successful executor attempt, returns `done` with the Executor branch only when the Gate passes, and returns `fail` without retry or escalation policy decisions when the Executor or Gate fails.

### Interface: `ingestion` boundary

```go
type Guard interface {
	DecideContent(context.Context, ContentCandidate) (Decision, error)
	DecideToolCall(context.Context, ToolCallCandidate) (Decision, error)
}

func NewContentCandidate(ContentInput) (ContentCandidate, error)
func NewToolCallCandidate(ToolCallInput) (ToolCallCandidate, error)

func NewBroker(guard Guard, timeout time.Duration) Broker

func (b Broker) ReviewContent(context.Context, ContentCandidate) ContentReview
func (b Broker) ReviewToolCall(context.Context, ToolCallCandidate) ToolCallReview

func (r ContentReview) Release() (ContentCandidate, bool)
func (r ToolCallReview) Release() (ToolCallCandidate, bool)
```

- **Implementors:** `internal/ingestion.Broker`; fake guards in tests; the task 025 armor adapter implements `Guard`.
- **Concrete adapter:** `armor.Guard` implements this interface by invoking an external armor-compatible runner.
- **Consumers:** inside-the-box agent loop and executor-facing harness code when web-ingestion or tool-call events are exposed.
- **Stability:** governed by ADR 024 and `docs/tasks/test-specs/024-ingestion-tool-call-boundary-test-spec.md`.
- **Required behavior:** content candidates validate source URI, media type, content bytes, retrieval metadata, provenance, and stable correlation ID before executor context. Tool-call candidates validate tool name, JSON arguments, optional target URI, provenance, and stable correlation ID before execution. The broker invokes the configured guard and releases a candidate only for a valid `allow` decision matching the candidate kind and ID. Guard error, timeout, unavailable guard, malformed result, and explicit `block` or `quarantine` decisions never release the candidate.

### Interface: executor ingestion harness

```go
type TraceRecorder interface {
	RecordTrace(TraceEvent)
}

type ContentContinuation func(context.Context, ContentRelease) error
type ToolExecutor func(context.Context, ToolCallRelease) error

func New(executorharness.Config) executorharness.Harness
func NewArmorGuarded(executorharness.ArmorConfig) executorharness.Harness

func (h Harness) HandleWebContent(context.Context, WebContentEvent, ContentContinuation) ContentResult
func (h Harness) HandleToolCall(context.Context, ToolCallEvent, ToolExecutor) ToolCallResult

func (r ContentRelease) Candidate() (ingestion.ContentCandidate, error)
func (r ContentRelease) Content() ([]byte, error)
func (r ToolCallRelease) Candidate() (ingestion.ToolCallCandidate, error)
func (r ToolCallRelease) Arguments() (json.RawMessage, error)
```

- **Implementors:** `internal/executorharness.Harness`; tests provide fake guards, trace recorders, continuations, and tool executors.
- **Consumers:** inside-the-box executor-facing wiring that receives web content or tool-call requests before executor context/tool execution.
- **Stability:** governed by ADR 024, `docs/tasks/test-specs/027-executor-ingestion-tool-harness-test-spec.md`, and `docs/tasks/test-specs/026-armor-ingestion-wiring-test-spec.md`.
- **Required behavior:** each web-content event is converted to an `ingestion.ContentCandidate` before continuation, and each tool-call event is converted to an `ingestion.ToolCallCandidate` before execution. The harness calls the broker before any continuation/executor callback. Only matching `allow` decisions produce valid opaque release values. Invalid event inputs, fail-closed broker outcomes, nil callbacks, and externally constructed release values do not reach executor use. `NewArmorGuarded` wires the harness to `armor.NewGuard` and `ingestion.NewBroker` so armor `block`, `quarantine`, allow-with-findings, unavailable, or timeout results do not reach executor use.

### Interface: armor guard adapter

```go
type Runner interface {
	Run(context.Context, Request) (Response, error)
}

type Config struct {
	Runner  Runner
	Command []string
	Timeout time.Duration
}

func NewGuard(Config) Guard

func (g Guard) DecideContent(context.Context, ingestion.ContentCandidate) (ingestion.Decision, error)
func (g Guard) DecideToolCall(context.Context, ingestion.ToolCallCandidate) (ingestion.Decision, error)
```

- **Implementors:** `armor.Guard`; `armor.ProcessRunner`; test fakes implementing `armor.Runner`.
- **Consumers:** ingestion broker configuration and future executor-facing wiring.
- **Stability:** governed by ADR 024 and `docs/tasks/test-specs/025-armor-guard-adapter-test-spec.md`.
- **Required behavior:** the adapter sends candidate data through an external invocation seam as JSON-compatible `armor.Request`, consumes `armor.Response`, and returns `ingestion.Decision`. `allow`/`clean` responses without findings map to `allow`. `flag`/`block` findings map to `block`; `quarantine` maps to `quarantine`; finding categories, severities, warnings, and response metadata remain visible in decision metadata. Missing command, runner error, context timeout, non-zero process exit, malformed JSON, malformed decision strings, and explicit armor error responses map to fail-closed `block` decisions without returning adapter errors.

### Interface: `loop.RetryingLoop`

```go
type StatusWriter interface {
	WriteStatus(taskID string, status tasksource.WritableStatus) (tasksource.StatusWriteResult, error)
}

type EscalationHook func(EscalationRequest) (supervisor.Executor, error)

type EscalationRequest struct {
	Task            supervisor.Task
	Attempt         int
	Outcome         loop.Outcome
	CurrentExecutor supervisor.Executor
}

type RetryPolicy struct {
	MaxAttempts int
	Escalate    EscalationHook
}

func NewRetryPolicy(maxAttempts int, hook EscalationHook) (RetryPolicy, error)

func BootstrapEscalationHook(EscalationRequest) (supervisor.Executor, error)

func NewRetryingLoop(source TaskSource, executor supervisor.Executor, verifier supervisor.Gate, worktreePath string, statusWriter StatusWriter, policy RetryPolicy) (*RetryingLoop, error)

func (l *RetryingLoop) RunOnce() (RetryOutcome, error)
```

- **Implementors:** `*loop.RetryingLoop`; test fakes implement `loop.StatusWriter`, `supervisor.Executor`, and `supervisor.Gate`.
- **Consumers:** future inside-the-box runtime wiring and supervisor dispatch lifecycle.
- **Stability:** governed by ADR 013 and `docs/tasks/test-specs/013-escalation-retry-policy-test-spec.md`.
- **Required behavior:** `MaxAttempts` is non-negative. `MaxAttempts == 0` escalates immediately without Executor or Gate attempts. Positive values bound Executor attempts exactly. Failed non-terminal attempts invoke `EscalationHook`, and the returned Executor is used for the next attempt. Exhausted failures write `needs-human` through `StatusWriter`; success before exhaustion returns done without a status write.

---

## Extension points

- Gate checks are extended by registering additional `gate.Step` implementations with `gate.New(steps ...Step)`. Registration rejects nil, blank-name, and duplicate-name steps.
- Task-source input locations are supplied by constructing `tasksource.Source` with a different `fs.FS`, roadmap path, or task directory list.
- Retry escalation behavior is extended by providing a different `loop.EscalationHook`. `loop.BootstrapEscalationHook` returns the current Executor; router-like hooks can return a different Executor for the next attempt.

### Executable artifact: L6 host preflight doctor

```bash
scripts/l6-preflight.sh
make l6-preflight
```

- Operator-invoked diagnostic. Checks all prerequisites from `docs/plans/phase0-l6-verification-checklist.md` and emits a structured readiness report.
- **Not a gate prerequisite.** `make l6-preflight` is in `.PHONY` but is not listed as a prerequisite of `make check` or `make fitness`.
- **Output format:** one row per prerequisite: `PASS <name>`, `FAIL <name> — <hint>`, or `MISSING <name> — <hint>`. Final line is `READY` (exit 0) or `NOT READY` (exit 1).
- **Prerequisites checked (in order):** tool presence via `command -v` for `podman`, `runsc`, `bwrap`, `srt`, `claude`, `gh`; rootless Podman via `podman info`; git remote configured via `git remote -v`; `make check` green; `make fitness` green.
- **srt snap-confine detection:** if `srt --version` exits non-zero with the string `snap-confine has elevated permissions and is not confined`, the `srt` row reports `FAIL` with a snap-specific hint (install srt outside snap). Any other non-zero exit produces a generic `FAIL`.
- **rootless-Podman detection:** if `podman info` exits non-zero or prints a value other than `true`, the `podman-rootless` row reports `FAIL` with a rootless configuration hint.
- **Testability seam (REQ-043-05):** setting `L6_PREFLIGHT_PATH` to a directory of stub binaries replaces PATH entirely for the script's duration. This allows TC-043-01 through TC-043-04 to run with only stub binaries and no live host tooling.

### Executable artifact: L6 probe harness and evidence collector

```bash
scripts/l6-probe.sh [--dry-run] [--help]
make l6-probe
```

- Operator-invoked evidence collector. Runs (or in `--dry-run` simulates) all 10 Phase 0 L6 probe steps in the closing order from `docs/plans/phase0-l6-verification-checklist.md`: 014 → 015 → 016 → 021 → 030 (ledger) → 022 → 028 → 033 → 034 → 032.
- **Not a gate prerequisite.** `make l6-probe` is in `.PHONY` but is not listed as a prerequisite of `make check` or `make fitness`.
- **Preflight gate:** calls `scripts/l6-preflight.sh` before running any real probes and exits non-zero with an informative message (referencing `make l6-preflight`) if preflight is NOT READY. `--dry-run` bypasses the preflight gate.
- **Gating rules (per-probe prerequisites):**
  - 014, 015, 016, 033: require `podman` on PATH and rootless Podman; 016 also requires `runsc`.
  - 021: requires `srt`. The snap-confine blocker is a SKIP reason, not FAIL.
  - 022, 028: require authenticated `claude` CLI.
  - 034: requires `gh` (authenticated) and a configured git remote.
  - 032: requires all of the above (capstone).
  - 030 (ledger update): requires 014, 015, 016, and 021 to have all completed without SKIP or FAIL; if any are SKIP, 030 is also SKIP.
- **SKIP semantics:** a missing prerequisite marks the probe as `SKIP` with a recorded reason; execution continues; exit 0. A SKIP is not a FAIL.
- **Evidence file:** written after each run (real or `--dry-run`) to a fixed path: `docs/plans/l6-evidence.txt` (override with `L6_EVIDENCE_FILE`). Contains 10 rows (one per closing-order step), pipe-delimited: `TASK-<id> | <command> | <output-line> | <status> [| SKIP-REASON: <reason>]`. Paste-ready for the `Verified by` column of `docs/tasks/test-specs/coverage-tracker.md`.
- **Output format (stdout):** one line per probe: `[<id>] <STATUS>   <detail>`.
- **Testability seam (REQ-044-05):** setting `L6_PROBE_PATH` to a directory of stub binaries replaces PATH entirely for the script's duration. `L6_EVIDENCE_FILE` overrides the evidence file path. `--dry-run` exercises all ordering, gating, and evidence-file logic without invoking real probe commands. TC-044-01 through TC-044-04 run with only stub binaries and no live host tooling.

### Executable artifact: execution-box launcher

```bash
containment/execution-box/run.sh [--worktree PATH] [--workload agent|dev] [--runtime runc|runsc|kata] [--gate-tools PATH] [--probe] [--egress-probe] [--egress-allowlist PATH] [--print-runtime-plan] [--print-egress-plan] [--print-toolchain-plan] [--name NAME] [--image IMAGE] [-- COMMAND...]
```

- `--worktree PATH` mounts the supplied repo worktree at `/work`; default is the current directory.
- `--workload agent|dev` selects the workload tier used for default OCI runtime mapping. `agent` defaults to `runsc`; `dev` defaults to `runc`.
- `--runtime runc|runsc|kata` overrides the workload default and is passed to Podman as `--runtime`. On the rootless egress (pod) path, the workload runtime resolves to `runc` regardless of the agent-tier `runsc` default or an explicit `--runtime` value (because gVisor's gofer cannot join a rootless pod userns — ADR 030). Non-networked paths (`--probe`, plain workload runs) keep the selected runtime unchanged. If an operator explicitly passes `--runtime runsc` together with an egress-path run (`--egress-probe` or a workload in an egress pod), the launcher fails loudly with an ADR-030 error message and does not launch any workload.
- `--gate-tools PATH` overrides the host artifact directory containing executable `golangci-lint`, `dep-scan`, and `code-scanner`. The launcher validates the directory before Podman starts and mounts it read-only at `/opt/agent-builder/gate-tools`.
- `--probe` runs the containment probe and prints `TC-001` through `TC-005` PASS/FAIL output plus host-side resource inspection for `TC-003` (load-bearing CPU/memory/PID/SHM limits verified via `podman inspect`; storage-quota state reported from the launcher's enforceability detection — `podman` 5.x does not portably expose `StorageOpt` via inspect on non-XFS hosts, so it is not inspected — see ADR 027), host-side runtime inspection for `TC-016`, in-box `TC-016-RUNTIME` output, and Gate tool path/version evidence for `go`, `gofmt`, `golangci-lint`, `dep-scan`, and `code-scanner`. When the runtime is `runsc`, the probe also runs a trivial `go build` and prints `TC-016-GO`. When the storage quota was applied, `TC-003 PASS` carries `storage quota applied (size=…)`; on non-XFS hosts it carries `storage quota not enforced on this host`. **In-box cap-visibility contract (TC-003, runtime-aware — ADR 028):** under `runc` (and any runtime that is not `runsc`) all three caps (cpu, memory, pids) must be visible in-box (`cpu.max`/`memory.max`/`pids.max` or their cgroup-v1 fallbacks); `TC-003 FAIL` is printed if any is absent. Under `runsc` (gVisor), the in-box check requires only the memory cap to be visible in-box; cpu and pids are verified by the launcher's authoritative host-side `podman inspect` (which already fails the probe if NanoCpus=0 or PidsLimit=-1). The `runsc` PASS line names cpu/pids as host-side-authoritative (`TC-003 PASS: memory cap visible in-box; cpu/pids caps verified host-side under runsc …`). The relaxed path is an explicit `runsc` allowlist — any other runtime (including `kata`, `unknown`, or future runtimes) takes the strict all-three-in-box path. **Probe-path launch-guard contract:** a podman launch failure on `podman start --attach` (exit 125) is reported as "container did not start"; an in-box probe non-zero exit (any exit ≠ 125) is reported as "probe failed: in-box probe exited non-zero" and is never mislabeled as a launch failure (ADR 028).
- `--egress-probe` runs the egress allowlist probe and prints allowlisted success (`TC-003`) and non-allowlisted/direct-IP denial (`TC-004`) lines. The sidecar applies an idempotent nftables ruleset and has write access to a transient per-run egress-state readiness directory for rootless pod operations (ADR 029). The workload runs under `runc` on the rootless egress path (gVisor unavailable per ADR 030). The allowlist entries are declared as `--add-host` on `podman pod create` (not on the workload member) so the pod resolves allowlisted hosts while the workload runs `--dns none`. If an operator passes `--runtime runsc` explicitly, the launcher fails loudly with an ADR-030 message before any workload launches.
- `--egress-allowlist PATH` overrides the plain-text allowlist file; `EXEC_BOX_EGRESS_ALLOWLIST` provides the default override.
- `--egress-allow-host HOST:PORT`, `--egress-deny-host HOST:PORT`, and `--egress-deny-ip HOST:PORT` override runtime egress probe targets.
- `--print-runtime-plan` validates and prints the resolved workload/runtime/source without requiring Podman.
- `--print-egress-plan` validates and prints the parsed allowlist without requiring Podman.
- `--print-toolchain-plan` validates and prints the Gate toolchain plan without requiring Podman.
- `--name NAME` sets the temporary container-name prefix.
- `--image IMAGE` overrides the local image tag; `EXEC_BOX_IMAGE` provides the default override.
- `COMMAND...` runs inside `/work`; when omitted, the launcher starts `/bin/sh`.
