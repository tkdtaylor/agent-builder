# Behaviors

**Project:** agent-builder
**Last updated:** 2026-06-05

What the system does, observably. Each behavior describes a triggering condition, the system's response, and any externally-visible side effects. This is the "you can verify this from outside the process" view.

Not in this file:
- *How* it does it (that's in source code; the contract is here, the implementation is there)
- *Why* it does it (that's in ADRs)
- *What data it operates on* (that's in [data-model.md](data-model.md))
- *What the entry points are* (that's in [interfaces.md](interfaces.md))

---

## Format

Each behavior is a numbered subsection with three parts:

> **B-NNN: Short imperative title**
>
> - **Trigger:** what causes this behavior to fire
> - **Response:** what the system does
> - **Side effects:** observable effects beyond the immediate response (writes, emitted events, log entries, external calls)
> - **Failure modes:** how it can fail and what the system does when it does
> - *(optional)* **References:** ADRs that drove the behavior, related test specs

Behaviors are numbered `B-001`, `B-002`, … sequentially. Numbers are stable references — never reuse a number, even if a behavior is removed (mark it `B-NNN: REMOVED — see ADR-XXX` and leave the number).

---

## Core behaviors

### B-001: Run the verification gate as an ordered blocking sequence

- **Trigger:** A caller invokes the gate with a target repository worktree path.
- **Response:** The gate runs each registered Step in registration order, records a `StepResult` for each executed Step, and returns a `Verdict`. A passing Verdict contains every configured Step result. A failing Verdict stops at the first failed Step and contains no later Step results.
- **Side effects:** The gate itself writes no persistent state. Individual Steps may spawn subprocesses or read files inside the supplied worktree.
- **Failure modes:** If any Step returns `OK == false`, the Verdict returns `OK == false` and exposes the failing Step's captured output.
- **References:** ADR 002; `docs/tasks/test-specs/002-gate-orchestrator-core-test-spec.md`.

### B-002: Run native Go verification steps against the target worktree

- **Trigger:** A gate is configured with one or more native Go Steps and invoked with a target repository worktree path.
- **Response:** The native Steps shell out in the supplied worktree to `go build ./...`, `go vet ./...`, `go test ./...`, and `gofmt -l .`. Each command is blocking and returns a StepResult.
- **Side effects:** The Steps spawn local `go` or `gofmt` subprocesses with the target worktree as the working directory. `gofmt -l .` lists unformatted files without rewriting them.
- **Failure modes:** A non-zero subprocess exit fails the Step and surfaces combined stdout/stderr. The gofmt Step also fails when `gofmt -l .` exits zero but prints one or more files. A missing `go` or `gofmt` binary on `PATH` is a hard failure that identifies the missing tool.
- **References:** `docs/tasks/test-specs/003-gate-go-checks-test-spec.md`.

### B-004: Run golangci-lint against the target worktree

- **Trigger:** A gate is configured with the golangci-lint Step and invoked with a target repository worktree path.
- **Response:** The Step shells out in the supplied worktree to `golangci-lint run` using the target worktree's lint configuration and returns a StepResult.
- **Side effects:** The Step spawns a local `golangci-lint` subprocess with the target worktree as the working directory. It writes no persistent state itself.
- **Failure modes:** Any non-zero linter exit fails the Step and surfaces combined stdout/stderr. A missing `golangci-lint` binary on `PATH` is a hard failure that identifies the missing tool.
- **References:** `docs/tasks/test-specs/004-gate-golangci-lint-test-spec.md`.

---

## Edge cases and error behaviors

> Behaviors specifically for error conditions, recovery, and edge cases. Keep them here rather than scattered through the core list — most readers want core first, edge cases on demand.

### B-003: Native tool absence fails loudly

- **Trigger:** A native Go Step runs while its required executable is absent from `PATH`.
- **Response:** The Step returns a failed StepResult.
- **Side effects:** No subprocess is started when executable lookup fails.
- **Failure modes:** The StepResult output names the missing tool and includes the lookup failure.

---

## Behavioral invariants

> Cross-cutting properties that hold across many or all behaviors. Examples:
>
> - All write operations are idempotent on retry.
> - No behavior can leave the system in an inconsistent state on partial failure (transactions / rollback / compensating action).
> - User-triggered destructive actions always require confirmation.
>
> Invariants here are stronger than ones in `SPEC.md` "Top-level invariants" — those are about system architecture; these are about observable behavior.

- There is no gate skip or bypass input. All configured Steps are blocking.
- Native Go Steps always run in the caller-supplied worktree, never implicitly in the agent-builder repo.
- The golangci-lint Step always runs in the caller-supplied worktree, never implicitly in the agent-builder repo.
