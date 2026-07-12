# Task 174: `agent-builder daemon`, a long-lived control-plane process

**Project:** agent-builder
**Created:** 2026-07-11
**Status:** backlog

## Goal

Add an `agent-builder daemon` subcommand: a long-lived process reusing
`assembleOrchestrate`/`runControlLoop` verbatim, with graceful shutdown
(`signal.NotifyContext`), a single-instance lock file, and startup rehydration
of in-flight runs via task 168's `RehydrateInFlight`/`ResumeFromRecord`.

## Context

`docs/plans/roadmap.md`'s Forward arc item 5 and ADR 065 both name this task.
Today `cmd/agent-builder/main.go` has zero signal handling and
`internal/cli/cli.go`'s dispatch table has no `daemon` case; an operator running
`orchestrate` today must keep a supervising process/terminal open, there is no
"start it and it keeps running" mode. This task is process lifecycle only, it
adds NO new control-loop logic: `runControlLoop` (already built, already
handles inbound channel messages continuously per task 112's async control
loop) is reused unmodified.

**Reference:**
- `cmd/agent-builder/main.go` (no signal handling today, confirmed by grep)
- `internal/cli/cli.go:46-96` (`Main`'s dispatch table, the edit site)
- `internal/cli/orchestrate.go:320-357` (`runOrchestrate`, the exact
  `assembleOrchestrate`/`runControlLoop`/context-cancellation pattern this
  task's `runDaemon` reuses)
- Task 167/168 (`runstore`, `RehydrateInFlight`, `ResumeFromRecord`, consumed
  at daemon startup)
- `docs/architecture/decisions/065-durable-execution-thin-run-journal-temporal-rejected.md`

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-174-01 | `Main` dispatches `"daemon"` to a new `runDaemon`. | must have |
| REQ-174-02 | A single-instance lock file (`O_CREATE\|O_EXCL`) fails fast on a second concurrent instance. | must have |
| REQ-174-03 | The lock file is removed on graceful shutdown and on any startup-error path. | must have |
| REQ-174-04 | SIGINT/SIGTERM cancels the shared control-loop context; returns `ExitOK` after cleanup. | must have |
| REQ-174-05 | RunStore-configured startup rehydrates in-flight runs before entering the steady-state loop. | must have |
| REQ-174-06 | `assembleOrchestrate` reused unmodified, no daemon-specific assembly fork. | must have |
| REQ-174-07 | Pre-existing subcommands and `internal/cli` suites unaffected. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/174-daemon-mode-test-spec.md` exists (written first)
- [x] Task 099 merged (`orchestrate`, `assembleOrchestrate`, `runControlLoop`)
- [x] Task 167/168 merged
- [ ] `make check` green on `main` before branching

## Implementation outline

1. New file `internal/cli/daemon.go`:
   ```go
   func runDaemon(config Config, args []string) int {
       flags := newFlagSet("daemon", config.Stderr)
       // parse -h/--help like orchestrateUsage; no other daemon-specific flags
       // in this task's scope.

       lockPath := daemonLockPathFromEnv() // AGENT_BUILDER_DAEMON_LOCK, default e.g. os.TempDir()/agent-builder-daemon.lock
       lockFile, err := acquireLock(lockPath)
       if err != nil {
           writef(config.Stderr, "error: %v\n", err)
           return ExitGeneric
       }
       defer releaseLock(lockFile, lockPath)

       ctx, cancel := context.WithCancel(context.Background())
       defer cancel()
       sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
       defer stop()

       oc, cleanup, err := assembleOrchestrate(config, assembleOverrides{ctx: sigCtx})
       if err != nil {
           writef(config.Stderr, "error: %v\n", err)
           if errors.Is(err, errUsageConfig) {
               return ExitUsage
           }
           return ExitGeneric
       }
       defer cleanup()

       if oc.runStore != nil { // task 167/168 field on orchestrateConfig, if wired that far
           records, rerr := orchestrator.RehydrateInFlight(oc.runStore)
           if rerr == nil {
               for _, rec := range records {
                   _, _ = oc.orch.ResumeFromRecord(sigCtx, rec)
               }
           }
       }

       if err := runControlLoop(sigCtx, oc); err != nil {
           writef(config.Stderr, "error: %v\n", err)
           return ExitGeneric
       }
       writef(config.Stdout, "daemon: graceful shutdown complete\n")
       return ExitOK
   }
   ```
   (Exact field/function names at the executor's discretion where they depend
   on task 168's precise API; the ORDERING and lock/signal/cleanup semantics
   above are the load-bearing contract.)
2. `acquireLock(path string) (*os.File, error)`: `os.OpenFile(path,
   os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)`; on `os.IsExist(err)`, return a
   clear error naming the path and stating another instance appears to be
   running (do not attempt PID-liveness detection in this task's scope, an
   operator manually clearing a stale lock after a hard crash is an accepted
   v1 limitation, matching ADR 065's single-node/single-operator framing).
   Write the current PID into the file (`fmt.Fprintf(lockFile, "%d\n",
   os.Getpid())`) for operator diagnosis.
3. `releaseLock(f *os.File, path string)`: `f.Close()`, `os.Remove(path)`,
   best-effort (log, do not panic, on removal failure).
4. `internal/cli/cli.go`: add `case "daemon": return runDaemon(config, args)`
   to `Main`'s dispatch table (`cli.go:86` area, alongside the existing
   `"orchestrate"` case).
5. Tests per the test spec.

## Acceptance criteria

- [ ] [REQ-174-01] TC-174-01: `Main` dispatches `"daemon"`.
- [ ] [REQ-174-02] TC-174-02/03: lock prevents a second instance; succeeds when none exists.
- [ ] [REQ-174-03] TC-174-04/05: lock removed on shutdown and on startup error.
- [ ] [REQ-174-04] TC-174-06: SIGTERM cancels the shared context, real subprocess (L5).
- [ ] [REQ-174-05] TC-174-07: RunStore rehydration happens before the steady-state loop.
- [ ] [REQ-174-06] TC-174-08: `assembleOrchestrate` reused unmodified, no fork.
- [ ] [REQ-174-07] TC-174-09: pre-existing subcommands unaffected.
- [ ] TC-174-10: `go test -race -count=1 ./internal/cli/... ./tests/e2e/...` passes; `make check` passes.

## Verification plan

- **Highest level achievable:** L5, TC-174-06's real-subprocess SIGTERM proof.
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
- **L6 (optional, operator-observed):** manual `agent-builder daemon` run,
  observed dispatching an inbound goal unattended, clean `Ctrl-C` shutdown.

## Spec/doc footprint (update in the feat commit)

- `docs/spec/configuration.md`: new `AGENT_BUILDER_DAEMON_LOCK` row.
- `docs/operating.md`: new "Running as a daemon" section.
- `docs/architecture/diagrams.md`: if a components/runtime-flow diagram exists
  for `orchestrate`, add the daemon variant's startup rehydration step.

## Out of scope

- Scheduled/recurring goals (task 175).
- Any new control-loop logic.
- Cross-host / multi-instance coordination.
- PID-liveness stale-lock detection (a documented v1 limitation).

## Dependencies

- **Blocks on:** task 099 (already merged), task 167, 168.
- **Blocks:** task 175.
