# Task 162: persist and load router quota state across process invocations

**Project:** agent-builder
**Created:** 2026-07-02
**Status:** backlog

## Goal

Wire `Router.SaveState`/`LoadState` into the live dispatch path via a new optional env
var, so a budgeted entry's accumulated `Usage`/`Availability` state survives across
successive `agent-builder run`/`orchestrate` process invocations instead of resetting
to fresh every time — closing the last of the review's three router dead-wire gaps
("quota state is never recorded/persisted").

## Context

**Root cause (full-project review, verified 2026-07-02):** tasks 160/161 wire
`OnGateFailure` (quality axis) and `RecordDispatch` (availability axis, proactive)
into the live dispatch path, but the `*router.Router` — and therefore the `Usage`/
`Availability` state task 161 now populates — is constructed fresh on every
`resolveExecutor` call (correctly scoped for the per-task `escalated` set, per task
160's TC-160-05, but this ALSO means quota state is lost the instant one process
exits). `Router.SaveState`/`LoadState` (`internal/router/router.go:318-390`) already
implement correct plain-text JSON persistence and are fully unit-tested in isolation
(`internal/router/quota_test.go`); zero non-test call sites exist anywhere in the
codebase.

**The fix:** a new optional env var `AGENT_BUILDER_ROUTER_STATE_PATH`. When set,
`resolveExecutor` calls `router.LoadState(path)` immediately after constructing the
router and before the first `Select` (a missing file — first run — is tolerated, not
a fail-fast error; a MALFORMED file IS a fail-fast error, matching `LoadState`'s
existing contract). After the dispatch's `RecordDispatch`/`OnGateFailure` calls
complete, `Run` calls `router.SaveState(path)` so the next process invocation observes
the accumulated state. When unset (the default), behavior is byte-for-byte identical
to pre-task — this is additive/opt-in, never a default-on behavior change.

**Scope note on "across dispatches":** `runtime.Run` is one-task-per-process-
invocation; there is no always-running daemon in the current architecture (see
`docs/architecture/overview.md`'s "Known missing" list). File-based persistence
across successive process invocations is therefore the correct mechanism for this
scope, not an in-memory long-lived singleton.

**Reference:**
- `internal/router/router.go:318-390` (`SaveState`, `LoadState` — already correct,
  already unit-tested; this task is pure wiring)
- `internal/runtime/run.go` (`resolveExecutor`, `Run`, `ConfigFromEnv` — the wiring
  site, extending tasks 160/161's closure)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-162-01 | `AGENT_BUILDER_ROUTER_STATE_PATH` is read by `ConfigFromEnv`; unset means no persistence (pre-task behavior). | must have |
| REQ-162-02 | When set, `resolveExecutor` calls `LoadState(path)` before the first `Select`; a missing file (first run) is tolerated. | must have |
| REQ-162-03 | When set, `SaveState(path)` is called after the dispatch's `RecordDispatch`/`OnGateFailure` calls complete. | must have |
| REQ-162-04 | A corrupted state file is a fail-fast `Run` error, never a silent reset to fresh state. | must have |
| REQ-162-05 | Unset: byte-for-byte pre-task behavior; no state leaks across two sequential in-process `Run` invocations, no file created. | must have |
| REQ-162-06 | End-to-end: an entry exhausted by `RecordDispatch` in one `Run` invocation is correctly reported exhausted at the very first `Select` of a subsequent, independently-constructed invocation sharing the same state path. | must have |
| REQ-162-07 | Pre-existing `internal/runtime`/`internal/router` suites continue to pass unchanged. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/162-router-quota-state-persistence-test-spec.md` exists (written first)
- [x] Task 161 merged (this task persists the `Usage` state task 161 now populates)
- [x] Task 093 merged (`SaveState`/`LoadState` themselves already exist and are unit-tested)
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [ ] [REQ-162-01] TC-162-01: env var wiring; unset means no persistence.
- [ ] [REQ-162-02] TC-162-02: `LoadState` runs before the first `Select`; a missing file is tolerated.
- [ ] [REQ-162-03] TC-162-03: state is saved to disk after dispatch, reflecting incremented usage.
- [ ] [REQ-162-04] TC-162-04: a corrupted state file is a fail-fast error, not a silent reset.
- [ ] [REQ-162-05] TC-162-05: unset path leaves behavior byte-for-byte unchanged, no cross-invocation leak.
- [ ] [REQ-162-06] TC-162-06: exhaustion recorded in one invocation is observed by the very first `Select` of a subsequent invocation sharing the state path.
- [ ] [REQ-162-07] TC-162-07: `go test -race -count=1 ./internal/runtime/... ./internal/router/...` passes in full; `make check` passes.

## Verification plan

- **Highest level achievable:** L5 — two sequential, independently-constructed `Run`
  invocations sharing one on-disk state file, inside a single Go test binary. A true
  two-OS-process L6 adds no additional confidence (the mechanism is file I/O + JSON
  marshal/unmarshal, already fully exercised).
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/runtime/... -run TestTC162
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/runtime/... -run TestTC162_06
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L6 (optional, operator-observed):** run `agent-builder run` twice against a
  tightly-budgeted registry entry with `AGENT_BUILDER_ROUTER_STATE_PATH` set; confirm
  the second invocation's routing reflects the first's recorded usage.

## Spec/doc footprint (update in the feat commit)

- `docs/spec/configuration.md` — new row for `AGENT_BUILDER_ROUTER_STATE_PATH`
  (optional, default unset = no persistence).
- `docs/spec/architecture.md` — the Model Router row's "In-memory state only
  (persistence + clock seam are a follow-on)" note is rewritten in place: persistence
  is now wired (task 162); the clock seam already existed (task 093) and remains
  in-memory-real-clock in production.

## Out of scope

- `OnQuotaExhausted`/`OnRateLimit` wiring (task 161's Out of scope, unchanged here).
- Any daemon/always-running-process design.
- Concurrent-writer safety for two simultaneous processes sharing one state path
  (last-writer-wins accepted for this task's scope).

## Dependencies

- **Blocks on:** task 161 (persists the state task 161 populates), task 093 (already
  merged).
- **Blocks:** none.
