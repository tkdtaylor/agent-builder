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
| `run` | subcommand | — | Dispatches one supervisor run path. Returns `0` when the supervisor returns nil and `1` when the run fails. |
| `version` | subcommand | — | Prints `agent-builder <version>` to stdout and exits `0`. |
| `verify <repo>` | subcommand + path argument | — | Constructs the production verification Gate and runs it against the target repo path. Prints each Gate step result and exits `0` only when every blocking step passes. Exits `1` when any Gate step fails. |
| `-h`, `--help`, `help` | help command | — | Prints top-level usage, subcommands, and exit codes to stdout and exits `0`. |
| subcommand `-h` | help flag | — | Prints usage for the selected subcommand to stdout and exits `0`. |

**Exit codes:**
- `0` — success
- `1` — generic error
- `2` — usage error

There is no `verify` flag that skips, bypasses, or weakens the Gate. The Gate is the definition of done and remains blocking.

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
| Podman | `podman build`, `podman pod create`, `podman create`, `podman inspect`, `podman start`, `podman run`, `podman logs`, `podman pod rm`, and `podman rm` from `containment/execution-box/run.sh` | process `PATH`; rootless Podman for the current non-root user | Missing binary, failed `podman info`, failed image build, absent quota fields, egress sidecar startup failure, or failed in-box probe exits non-zero and names the failing check |
| Claude Code CLI | `claude -p <prompt>` in the configured task worktree | process `PATH` or `executor.ClaudeCLIConfig.CLIPath`; auth supplied through `ANTHROPIC_API_KEY` | Missing binary, blank config, missing token, subprocess non-zero exit, or missing/blank produced branch file fails the executor attempt |
| Go toolchain | `go build ./...`, `go vet ./...`, `go test ./...` in the target worktree | process `PATH`; Go version supplied by the runtime environment | Missing `go` fails the Step; non-zero exit fails the Step with combined stdout/stderr |
| gofmt | `gofmt -l .` in the target worktree | process `PATH`; Go version supplied by the runtime environment | Missing `gofmt` fails the Step; non-zero exit fails the Step; non-empty output fails the Step as formatting drift |
| golangci-lint | `golangci-lint run` in the target worktree | process `PATH`; version supplied by the runtime environment | Missing `golangci-lint` fails the Step; non-zero exit fails the Step with combined stdout/stderr |
| dep-scan Go scanner | `gods` in the target worktree | process `PATH`; version supplied by the runtime environment | Missing `gods` fails the Step; non-zero exit fails the Step with combined stdout/stderr |
| code-scanner | `code-scanner` in the target worktree | process `PATH`; version supplied by the runtime environment | Missing `code-scanner` fails the Step; non-zero exit fails the Step with combined stdout/stderr |

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
type ClaudeCLIConfig struct {
	CLIPath   string
	Worktree  string
	AuthToken string
}

func NewClaudeCLI(config ClaudeCLIConfig) *ClaudeCLI
func NewClaudeCLIFromEnv(worktree string) *ClaudeCLI
func (e *ClaudeCLI) Run(task supervisor.Task) (supervisor.Result, error)
```

- **Outbound call:** `claude -p <prompt>` with `cmd.Dir` set to `ClaudeCLIConfig.Worktree`.
- **Branch contract:** the prompt names an executor-owned temp file where the CLI must write the produced branch. The executor trims that file and copies it into `supervisor.Result.Branch`.
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

- **Implementors:** in-process `sandbox.FakeRunner` for tests; rented and produced concrete backends live behind this interface when added.
- **Consumers:** supervisor construction accepts the interface. Dispatch lifecycle code uses the interface when task execution is implemented.
- **Stability:** governed by ADR 020 and updated with any task that changes contained-run inputs, outputs, or error semantics.
- **Required behavior:** `Command` is argv-style and must contain a non-blank executable at index 0. `Worktree` is the target repo worktree path mounted or made available to the backend. `Limits` is a typed struct, not a map, and carries wall-clock, memory, CPU, and egress allowlist values. `Result` captures stdout, stderr, and duration. The integer return is the process exit code. A non-zero exit code is returned with nil error when the backend ran the command; non-nil error means adapter/backend failure or invalid request.

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
- **Consumers:** inside-the-box agent loop and executor-facing harness code when web-ingestion or tool-call events are exposed.
- **Stability:** governed by ADR 024 and `docs/tasks/test-specs/024-ingestion-tool-call-boundary-test-spec.md`.
- **Required behavior:** content candidates validate source URI, media type, content bytes, retrieval metadata, provenance, and stable correlation ID before executor context. Tool-call candidates validate tool name, JSON arguments, optional target URI, provenance, and stable correlation ID before execution. The broker invokes the configured guard and releases a candidate only for a valid `allow` decision matching the candidate kind and ID. Guard error, timeout, unavailable guard, malformed result, and explicit `block` or `quarantine` decisions never release the candidate.

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
containment/execution-box/run.sh [--worktree PATH] [--probe] [--egress-probe] [--egress-allowlist PATH] [--print-egress-plan] [--name NAME] [--image IMAGE] [-- COMMAND...]
```

- `--worktree PATH` mounts the supplied repo worktree at `/work`; default is the current directory.
- `--probe` runs the containment probe and prints `TC-001` through `TC-005` PASS/FAIL output plus host-side quota inspection for `TC-003`.
- `--egress-probe` runs the egress allowlist probe and prints allowlisted success (`TC-003`) and non-allowlisted/direct-IP denial (`TC-004`) lines.
- `--egress-allowlist PATH` overrides the plain-text allowlist file; `EXEC_BOX_EGRESS_ALLOWLIST` provides the default override.
- `--egress-allow-host HOST:PORT`, `--egress-deny-host HOST:PORT`, and `--egress-deny-ip HOST:PORT` override runtime egress probe targets.
- `--print-egress-plan` validates and prints the parsed allowlist without requiring Podman.
- `--name NAME` sets the temporary container-name prefix.
- `--image IMAGE` overrides the local image tag; `EXEC_BOX_IMAGE` provides the default override.
- `COMMAND...` runs inside `/work`; when omitted, the launcher starts `/bin/sh`.
