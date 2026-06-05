# Interfaces

**Project:** agent-builder
**Last updated:** 2026-06-05

The system's contact surface — everything that calls into the system, everything the system calls out to, and the public boundaries within the system. Each interface is a stable contract: changes here are breaking changes.

Not in this file:
- What the interfaces *do* (that's in [behaviors.md](behaviors.md))
- What data flows through them (that's in [data-model.md](data-model.md))
- How they're configured (that's in [configuration.md](configuration.md))

---

## Inbound interfaces

> What the outside world uses to call into this system.

### CLI

> The command-line surface. List every subcommand, flag, and positional argument. For each, give type, default, and effect.

```
agent-builder <subcommand> [flags] [args]

Subcommands:
  run             dispatch one supervisor loop
  version         print the agent-builder version
  verify <repo>   run the verification gate against a repo
```

| Subcommand / flag | Type | Default | Effect |
|-------------------|------|---------|--------|
| `run` | subcommand | — | Builds the configured Phase 0 runtime pipeline from environment configuration, selects at most one ready task, dispatches it through the supervisor, and returns `0` when the run completes or idles. Returns `1` when configuration, containment, Executor, Gate, or loop execution fails. |
| `version` | subcommand | — | Prints `agent-builder <version>` to stdout and exits `0`. |
| `verify <repo>` | subcommand + path argument | — | Constructs the production verification Gate and runs it against the target repo path. Prints each Gate step result and exits `0` only when every blocking step passes. Exits `1` when any Gate step fails. |
| `-h`, `--help`, `help` | help command | — | Prints top-level usage, subcommands, and exit codes to stdout and exits `0`. |
| subcommand `-h` | help flag | — | Prints usage for the selected subcommand to stdout and exits `0`. |

**Exit codes:**
- `0` — success
- `1` — generic error
- `2` — usage error

There is no `verify` flag that skips, bypasses, or weakens the Gate. The Gate is the definition of done and remains blocking.

`agent-builder run` has no flags. Its required and optional environment configuration is documented in [configuration.md](configuration.md#environment-variables).

### HTTP / RPC API

> If the project exposes an API, document each endpoint. For larger APIs, link to a generated OpenAPI/protobuf file rather than retyping it here.

| Method | Path | Purpose | Request shape | Response shape | Errors |
|--------|------|---------|---------------|----------------|--------|
| | | | | | |

### Wire protocol

> If the project speaks a binary or text protocol (TWS, FIX, custom), document the message catalogue here or link to a separate `protocol.md` if it's large.

---

## Outbound interfaces

> What this system calls out to. Each external dependency is a coupling point — list it explicitly so failure modes and version pinning are visible.

| Dependency | What we call | Library / version | Failure mode |
|------------|-------------|-------------------|--------------|
| Podman | `podman build`, `podman pod create`, `podman create`, `podman inspect`, `podman start`, `podman run --runtime <oci-runtime>`, `podman logs`, `podman pod rm`, and `podman rm` from `containment/execution-box/run.sh` | process `PATH`; rootless Podman for the current non-root user; configured OCI runtime names `runc`, `runsc`, or future `kata`; Gate scanner/linter tools mounted from `EXEC_BOX_GATE_TOOLS` / `--gate-tools` | Missing binary, failed `podman info`, unavailable selected OCI runtime, missing Gate toolchain artifact, failed image build, absent quota/runtime fields, egress sidecar startup failure, or failed in-box probe exits non-zero and names the failing check |
| @anthropic-ai/sandbox-runtime | `srt --settings <generated-json> <command...>` from `internal/sandbox/sandboxruntime` | process `PATH` or `sandboxruntime.Config.CLIPath`; settings generated per request | Missing `srt`, invalid worktree, malformed egress allowlist, subprocess timeout, or settings write failure returns adapter error. Wrapped command non-zero exits return the command exit code with nil adapter error. |
| Claude Code CLI | `claude -p <prompt>` in the configured task worktree | process `PATH` or `executor.ClaudeCLIConfig.CLIPath`; auth supplied through `ANTHROPIC_API_KEY` | Missing binary, blank config, missing token, subprocess non-zero exit, or missing/blank produced branch file fails the executor attempt |
| armor | armor-compatible command configured by `armor.Config.Command` and invoked with JSON stdin/stdout | process `PATH` or caller-supplied command path; fakeable through `armor.Runner` | Missing command, subprocess timeout, non-zero exit, malformed JSON, malformed decision, or armor error output maps to a fail-closed `block` decision |
| Go toolchain | `go build ./...`, `go vet ./...`, `go test ./...` in the target worktree | process `PATH`; Go version supplied by the runtime environment | Missing `go` fails the Step; non-zero exit fails the Step with combined stdout/stderr |
| gofmt | `gofmt -l .` in the target worktree | process `PATH`; Go version supplied by the runtime environment | Missing `gofmt` fails the Step; non-zero exit fails the Step; non-empty output fails the Step as formatting drift |
| golangci-lint | `golangci-lint run` in the target worktree | process `PATH`; version supplied by the runtime environment | Missing `golangci-lint` fails the Step; non-zero exit fails the Step with combined stdout/stderr |
| dep-scan Go scanner | `gods` in the target worktree | process `PATH`; version supplied by the runtime environment | Missing `gods` fails the Step; non-zero exit fails the Step with combined stdout/stderr |
| code-scanner | `code-scanner` in the target worktree | process `PATH`; version supplied by the runtime environment | Missing `code-scanner` fails the Step; non-zero exit fails the Step with combined stdout/stderr |
| git | `git push <remote> <branch>` in the target worktree | process `PATH` or `AGENT_BUILDER_GIT_CLI`; optional token supplied as `GIT_TOKEN` from `AGENT_BUILDER_GIT_TOKEN` | Missing binary, push rejection, auth failure, or non-zero exit fails publication and redacts configured token values from surfaced output |
| GitHub CLI | `gh pr view --head <branch> --json url,number --jq .url`; `gh pr create --head <branch> --fill` in the target worktree | process `PATH` or `AGENT_BUILDER_GH_CLI`; optional token supplied as `GH_TOKEN` and `GITHUB_TOKEN` from `AGENT_BUILDER_GITHUB_TOKEN` | Missing binary, auth failure, malformed repository state, or PR creation failure fails publication and redacts configured token values from surfaced output |

---

## Internal public surface

> Interfaces *within* the project that are stable contracts between modules. Examples: a `Strategy` trait that strategy crates implement, a `Repository` trait that handlers consume, an event bus shape.
>
> If a module's public API isn't listed here, it's an implementation detail — callers should not depend on it. Promotion to this list is a deliberate decision (often via ADR).

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

type ClaudeCLIConfig struct {
	CLIPath          string
	Worktree         string
	AuthToken        string
	IngestionPolicy  ClaudeIngestionPolicy
	IngestionHarness *executorharness.Harness
}

func ParseClaudeIngestionPolicy(raw string) (ClaudeIngestionPolicy, error)
func NewClaudeCLI(config ClaudeCLIConfig) *ClaudeCLI
func NewClaudeCLIFromEnv(worktree string) *ClaudeCLI
func (e *ClaudeCLI) IngestionPolicy() ClaudeIngestionPolicy
func (e *ClaudeCLI) Run(task supervisor.Task) (supervisor.Result, error)
func (e *ClaudeCLI) HandleWebContent(ctx context.Context, event executorharness.WebContentEvent, continuation executorharness.ContentContinuation) executorharness.ContentResult
func (e *ClaudeCLI) HandleToolCall(ctx context.Context, event executorharness.ToolCallEvent, toolExecutor executorharness.ToolExecutor) executorharness.ToolCallResult
```

- **Outbound call:** `claude -p <prompt>` with `cmd.Dir` set to `ClaudeCLIConfig.Worktree`.
- **Branch contract:** the prompt names an executor-owned temp file where the CLI must write the produced branch. The executor trims that file and copies it into `supervisor.Result.Branch`.
- **Web/tool policy:** `IngestionPolicy` defaults to `disabled`. `disabled` fails closed for Claude-facing web/tool events while preserving ordinary subprocess execution. `reviewed` requires `IngestionHarness` and routes web/tool events through it before any continuation or tool executor can run. Unknown policy values and reviewed-without-harness configurations fail before subprocess start.
- **Auth contract:** the only default credential source is `ANTHROPIC_API_KEY`; the executor injects it into subprocess env, replaces host `HOME`/XDG dirs with temp dirs, and redacts the token from subprocess failure output.

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
	TaskRoot       string
	Worktree       string
	ClaudeCLI      string
	ClaudeToken    string
	SandboxRuntime string
	RunRecordPath  string
	RunTimeout     time.Duration
	MaxAttempts    int
	PublishRemote  string
	GitCLI         string
	GitHubCLI      string
	GitToken       string
	GitHubToken    string
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
}

type Limits struct {
	WallClockTimeout time.Duration
	MemoryBytes      int64
	CPUCount         int
	EgressAllowlist  []string
}

type Result struct {
	Stdout   string
	Stderr   string
	Duration time.Duration
}
```

- **Implementors:** in-process `sandbox.FakeRunner` for tests; `sandboxruntime.Runner` for rented `@anthropic-ai/sandbox-runtime`; produced exec-sandbox v0 backend when added.
- **Consumers:** supervisor construction accepts the interface. Dispatch lifecycle code uses the interface when task execution is implemented.
- **Stability:** governed by ADR 020 and updated with any task that changes contained-run inputs, outputs, or error semantics.
- **Required behavior:** `Command` is argv-style and must contain a non-blank executable at index 0. `Worktree` is the target repo worktree path mounted or made available to the backend. `Limits` is a typed struct, not a map, and carries wall-clock, memory, CPU, and egress allowlist values. `Result` captures stdout, stderr, and duration. The integer return is the process exit code. A non-zero exit code is returned with nil error when the backend ran the command; non-nil error means adapter/backend failure or invalid request.

### Concrete backend: `sandboxruntime.Runner`

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

> Plugin slots, hook points, registration mechanisms — anything designed for external code to extend the system without modification. If there are none, say "None — extension is by source modification" so it's an explicit choice.

- Gate checks are extended by registering additional `gate.Step` implementations with `gate.New(steps ...Step)`. Registration rejects nil, blank-name, and duplicate-name steps.
- Task-source input locations are supplied by constructing `tasksource.Source` with a different `fs.FS`, roadmap path, or task directory list.
- Retry escalation behavior is extended by providing a different `loop.EscalationHook`. `loop.BootstrapEscalationHook` returns the current Executor; router-like hooks can return a different Executor for the next attempt.

### Executable artifact: execution-box launcher

```bash
containment/execution-box/run.sh [--worktree PATH] [--workload agent|dev] [--runtime runc|runsc|kata] [--gate-tools PATH] [--probe] [--egress-probe] [--egress-allowlist PATH] [--print-runtime-plan] [--print-egress-plan] [--print-toolchain-plan] [--name NAME] [--image IMAGE] [-- COMMAND...]
```

- `--worktree PATH` mounts the supplied repo worktree at `/work`; default is the current directory.
- `--workload agent|dev` selects the workload tier used for default OCI runtime mapping. `agent` defaults to `runsc`; `dev` defaults to `runc`.
- `--runtime runc|runsc|kata` overrides the workload default and is passed to Podman as `--runtime`.
- `--gate-tools PATH` overrides the host artifact directory containing executable `golangci-lint`, `gods`, and `code-scanner`. The launcher validates the directory before Podman starts and mounts it read-only at `/opt/agent-builder/gate-tools`.
- `--probe` runs the containment probe and prints `TC-001` through `TC-005` PASS/FAIL output plus host-side quota inspection for `TC-003`, host-side runtime inspection for `TC-016`, in-box `TC-016-RUNTIME` output, and Gate tool path/version evidence for `go`, `gofmt`, `golangci-lint`, `gods`, and `code-scanner`. When the runtime is `runsc`, the probe also runs a trivial `go build` and prints `TC-016-GO`.
- `--egress-probe` runs the egress allowlist probe and prints allowlisted success (`TC-003`) and non-allowlisted/direct-IP denial (`TC-004`) lines.
- `--egress-allowlist PATH` overrides the plain-text allowlist file; `EXEC_BOX_EGRESS_ALLOWLIST` provides the default override.
- `--egress-allow-host HOST:PORT`, `--egress-deny-host HOST:PORT`, and `--egress-deny-ip HOST:PORT` override runtime egress probe targets.
- `--print-runtime-plan` validates and prints the resolved workload/runtime/source without requiring Podman.
- `--print-egress-plan` validates and prints the parsed allowlist without requiring Podman.
- `--print-toolchain-plan` validates and prints the Gate toolchain plan without requiring Podman.
- `--name NAME` sets the temporary container-name prefix.
- `--image IMAGE` overrides the local image tag; `EXEC_BOX_IMAGE` provides the default override.
- `COMMAND...` runs inside `/work`; when omitted, the launcher starts `/bin/sh`.
