# Test Spec 174: `agent-builder daemon`, a long-lived control-plane process

**Linked task:** [`docs/tasks/backlog/174-daemon-mode.md`](../backlog/174-daemon-mode.md)
**Written:** 2026-07-11
**Status:** ready for implementation

## Context

`docs/plans/roadmap.md`'s Forward arc item 5, "Heartbeat / daemon (always-on), a
persistent self-hosted daemon so the agent runs continuously, not only when
invoked", and ADR 065's Decision section both name this task. Today
`cmd/agent-builder/main.go` has no signal handling at all
(`grep -rn "signal.Notify\|os/signal" internal/ cmd/` returns nothing), and
`internal/cli/cli.go`'s `Main` dispatch table (`cli.go:75-96`) has no `daemon`
case; the closest existing behavior is `orchestrate`, which runs
`runControlLoop` until its context is cancelled by the caller, then exits, an
operator has to keep a terminal/process supervisor open for it.

This task adds `agent-builder daemon`: reuses `assembleOrchestrate`/`runControlLoop`
verbatim (no new control-loop logic, this task is process lifecycle only),
adds graceful shutdown via `signal.NotifyContext` (stdlib, SIGINT/SIGTERM), a
single-instance lock file (stdlib `os.OpenFile` with `O_CREATE|O_EXCL`, a
PID-file convention, no third-party flock dependency), and calls task 168's
`RehydrateInFlight`/`ResumeFromRecord` at startup when a RunStore is configured.

**Module boundary:** `internal/cli` (new `daemon` subcommand + lock file logic)
and `cmd/agent-builder` (none, `Main` is already generic). One module,
`internal/cli`, plus the trivial dispatch-table addition.

---

## Requirements coverage

| Req ID     | Description | Test cases |
|------------|--------------|------------|
| REQ-174-01 | `internal/cli.Main` recognizes `"daemon"` in its dispatch table, calling a new `runDaemon(config Config, args []string) int`. | TC-174-01 |
| REQ-174-02 | `runDaemon` acquires a single-instance lock file (`O_CREATE\|O_EXCL`, path from `AGENT_BUILDER_DAEMON_LOCK` or a documented default) before doing anything else; a second concurrent invocation against the same lock path fails fast with a clear, non-zero-exit error naming the lock path, WITHOUT starting a second control loop. | TC-174-02, TC-174-03 |
| REQ-174-03 | The lock file is removed on graceful shutdown (SIGINT/SIGTERM via `signal.NotifyContext`) and on any startup-assembly error path (never left behind by a process that never actually started the control loop). | TC-174-04, TC-174-05 |
| REQ-174-04 | On SIGINT/SIGTERM, `runDaemon` cancels the SAME context `runControlLoop` runs under (reusing `orchestrate`'s existing `ctx, cancel := context.WithCancel(...)` + `runControlLoop(ctx, oc)` pattern, `internal/cli/orchestrate.go:337-357`) and returns `ExitOK` once the control loop's cleanup completes, not before. | TC-174-06 |
| REQ-174-05 | When `AGENT_BUILDER_RUN_STORE_DIR` (or equivalent, matching task 167/168's config surface) is configured, `runDaemon` calls `orchestrator.RehydrateInFlight`/`ResumeFromRecord` for each in-flight record BEFORE entering the steady-state `runControlLoop`. | TC-174-07 |
| REQ-174-06 | `runDaemon` reuses `assembleOrchestrate` unmodified (no daemon-specific assembly branch); every existing `orchestrate` env var/behavior applies identically under `daemon`. | TC-174-08 |
| REQ-174-07 | Pre-existing `orchestrate`/`run`/`ask`/`verify` subcommands and `internal/cli` suites are unaffected. | TC-174-09 |

---

## Pre-implementation checklist

- [x] Task 167/168 merged (`RehydrateInFlight`/`ResumeFromRecord` exist)
- [x] Task 099 merged (`orchestrate` subcommand, `assembleOrchestrate`,
  `runControlLoop` exist and are reused unmodified)
- [ ] `make check` green on `main` before branching

---

## Test cases

### TC-174-01, `Main` dispatches `"daemon"`

- **Requirement:** REQ-174-01
- **Level:** L2 (unit test, extends `internal/cli/cli_test.go`'s existing
  dispatch-table pattern)
- **Test file:** `internal/cli/cli_test.go` (extend) or `daemon_test.go` (new)

**Step:** `Main(Config{Args: []string{"daemon", "-h"}, ...})` (help flag, so the
test does not actually enter a long-lived loop).

**Expected output:** exits `ExitOK`, prints daemon usage text (mirrors
`orchestrateUsage`'s existing help-flag handling shape).

---

### TC-174-02, lock acquisition prevents a second instance

- **Requirement:** REQ-174-02
- **Level:** L2 (a fast-exiting test harness: inject a `ctx` that is already
  cancelled, or a fake `runControlLoop` seam that returns immediately, so the
  test does not block on a real long-lived loop)
- **Test file:** `internal/cli/daemon_test.go` (new)

**Step:** Acquire the lock directly (`os.OpenFile(lockPath,
os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)`), simulating a first instance already
running. Call `runDaemon` (with a config pointing at the SAME lock path).

**Expected output:** `runDaemon` returns a non-zero exit code; stderr names the
lock path and states another instance appears to be running; the pre-existing
lock file is untouched (still present, not overwritten); the fake
`runControlLoop` seam (if the test wires one) is never invoked.

---

### TC-174-03, lock acquisition succeeds when no lock exists

- **Requirement:** REQ-174-02
- **Level:** L2

**Step:** No pre-existing lock file. Call `runDaemon` with a fake
`runControlLoop` seam that returns immediately (`nil`).

**Expected output:** the lock file is created during the call (assert via
`os.Stat` from a second goroutine racing the call, or, more simply, assert the
lock file exists immediately after `runDaemon` acquires it but before it
returns, via an injectable "lock acquired" test hook); `runDaemon` returns
`ExitOK`.

---

### TC-174-04, lock file removed on graceful shutdown

- **Requirement:** REQ-174-03
- **Level:** L2

**Step:** Call `runDaemon` with a fake `runControlLoop` seam that blocks until
its `ctx` is cancelled, then returns `nil`. From a second goroutine, cancel the
context the test injects (simulating the signal handler firing) after a short
delay.

**Expected output:** `runDaemon` returns `ExitOK`; the lock file no longer
exists (`os.Stat` returns `os.ErrNotExist`) after `runDaemon` returns.

---

### TC-174-05, lock file removed on a startup-assembly error

- **Requirement:** REQ-174-03
- **Level:** L2

**Step:** Configure `runDaemon` so `assembleOrchestrate` fails (e.g. a missing
worker signing key, mirroring `orchestrate`'s existing SEC-003 fail-fast test
fixture).

**Expected output:** `runDaemon` returns a non-zero exit code; the lock file
does not exist afterward (acquired, then released on the error path, never left
behind by a process that never reached the steady-state loop).

---

### TC-174-06, SIGINT/SIGTERM cancels the shared context

- **Requirement:** REQ-174-04
- **Level:** L5 (real `agent-builder daemon` process, signalled via
  `os/exec` + `cmd.Process.Signal(syscall.SIGTERM)`, mirroring
  `tests/e2e`'s existing real-subprocess harness conventions)
- **Test file:** `tests/e2e/daemon_e2e_test.go` (new)

**Step:** Start `agent-builder daemon` as a real subprocess (minimal valid
config, a fake/no-op channel so it does not need real Telegram credentials,
matching `orchestrate`'s existing e2e fixture conventions if one exists, or a
disabled/CLI-only channel mode). Send `SIGTERM`. Wait (bounded) for the process
to exit.

**Expected output:** the process exits `0` within a bounded time (e.g. 5s); its
stderr/stdout contains a graceful-shutdown message; the configured lock file no
longer exists after exit.

---

### TC-174-07, RunStore-configured startup rehydrates before the steady-state loop

- **Requirement:** REQ-174-05
- **Level:** L2

**Step:** Pre-seed a `runstore.FileStore` (task 167) with one in-flight
`Record`. Configure `runDaemon` with the matching RunStore directory and a fake
`runControlLoop` seam. Wire a spy in place of
`orchestrator.RehydrateInFlight`/`ResumeFromRecord` (or, more directly, assert
via the RunStore's own state after `runDaemon`'s startup phase, before the fake
control loop is entered).

**Expected output:** `RehydrateInFlight` is called exactly once, with the
seeded record among its results, BEFORE the fake `runControlLoop` seam is
invoked (ordering asserted via a shared call-order log).

---

### TC-174-08, `assembleOrchestrate` reused unmodified

- **Requirement:** REQ-174-06
- **Level:** L2 (regression, code-review-assertable: `runDaemon`'s
  implementation calls the SAME `assembleOrchestrate` function `runOrchestrate`
  calls, no daemon-specific fork)

**Step:** Grep-based structural assertion test (mirrors this codebase's
existing structural fitness-style tests, e.g. `import_graph_test.go`): assert
`internal/cli/daemon.go`'s source calls `assembleOrchestrate(` and does not
define a second, parallel assembly function.

**Expected output:** exactly one `assembleOrchestrate(` call site inside
`runDaemon`'s source; grep confirms no duplicate/forked assembly logic.

---

### TC-174-09, pre-existing subcommands unaffected

- **Requirement:** REQ-174-07
- **Level:** L2 (regression)

**Step:** Run the full pre-existing `internal/cli` suite.

**Expected output:** byte-identical pass; `orchestrate`/`run`/`ask`/`verify`
completely unaffected by the new `daemon` case in `Main`'s dispatch table.

---

### TC-174-10, full regression

- **Requirement:** all
- **Level:** L2/L3/L5

**Step:**
```
go test -race -count=1 ./internal/cli/... ./tests/e2e/...
make check
```

**Expected output:** all `ok`; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L5 (or L6 if an operator manually starts
  `agent-builder daemon` and confirms it survives across a real terminal
  session and shuts down cleanly on `Ctrl-C`), TC-174-06's real-subprocess
  SIGTERM proof is the strongest in-CI evidence.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/cli/... -run TestTC174
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./tests/e2e/... -run TestDaemonGracefulShutdown
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L6 (optional, operator-observed):** `agent-builder daemon` started
  manually, left running, an inbound Telegram/CLI goal message observed to
  dispatch while the process is unattended, then `Ctrl-C` observed to shut
  down cleanly and remove the lock file.

## Out of scope

- Scheduled/recurring goals (task 175).
- Any new control-loop LOGIC (reuses `runControlLoop` verbatim, this task is
  process lifecycle: lock, signals, rehydration-at-startup only).
- Cross-host / multi-instance coordination (ADR 065's re-evaluation trigger:
  multi-node execution reopens the durable-execution decision; single-node
  single-instance is this task's explicit scope).
