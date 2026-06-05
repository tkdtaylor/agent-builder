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
  <subcommand>    <one-line description>

Global flags:
  --flag <type>   <effect> (default: <value>)
```

| Subcommand / flag | Type | Default | Effect |
|-------------------|------|---------|--------|
| | | | |

**Exit codes:**
- `0` — success
- `1` — generic error
- `2` — usage error
- *(add more as defined)*

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
- **Consumers:** future supervisor/agent-loop status governance code.
- **Stability:** governed by `docs/tasks/test-specs/011-task-status-writer-test-spec.md`.
- **Required behavior:** the writer exposes only a task ID plus constrained status marker mutation method. It accepts `WritableStatusDone`, `WritableStatusBlocked`, and `WritableStatusNeedsHuman`; it rejects every other status value before writing. It rewrites exactly one `**Status:**` line in the matched task file and has no API for arbitrary content replacement.

---

## Extension points

> Plugin slots, hook points, registration mechanisms — anything designed for external code to extend the system without modification. If there are none, say "None — extension is by source modification" so it's an explicit choice.

- Gate checks are extended by registering additional `gate.Step` implementations with `gate.New(steps ...Step)`. Registration rejects nil, blank-name, and duplicate-name steps.
- Task-source input locations are supplied by constructing `tasksource.Source` with a different `fs.FS`, roadmap path, or task directory list.
