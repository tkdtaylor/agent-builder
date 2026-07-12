# Task 167: `internal/runstore`, a file-backed durable run journal

**Project:** agent-builder
**Created:** 2026-07-11
**Status:** completed

## Goal

Build `internal/runstore`, a new stdlib-only leaf package implementing the
crash-safe, file-backed run journal `docs/architecture/decisions/065-durable-execution-thin-run-journal-temporal-rejected.md`
commits to: append-only JSONL with snapshot/compaction, temp+rename atomic
snapshot writes, recording goal, plan, per-task attempt state, pending approvals,
and terminal status. This task builds the storage primitive only; no wiring into
the orchestrator happens here (that is task 168).

## Context

ADR 065 (accepted 2026-07-11) evaluated and rejected Temporal and every other
external durable-execution framework for the orchestrator, citing operational
weight, trusted-core exposure, and lock-in risk disproportionate to a
single-node, single-operator system. Its Decision section explicitly names this
task by number: "A `RunStore` seam plus a stdlib file-backed run journal
(append-only JSONL with snapshot/compaction, crash-safe temp+rename writes)
recording goal, plan, per-task attempt state, pending approvals, and terminal
status (task 167)."

Today the orchestrator's only state is `orchestrator.MemoryPlanStore`
(`internal/orchestrator/store.go:11-38`), a `sync.Mutex`-guarded in-memory
`map[string]Plan`. A crash mid-goal loses everything: no attempt history, no
record of which sub-goal was mid-dispatch, no pending-approval record. This task
does not touch that store (task 173 eventually layers a related durable store on
top of memory-guard for a DIFFERENT purpose, general cross-session memory); it
builds a new, independent package purpose-built for run/attempt state, matching
ADR 065's naming.

**Reference:**
- `docs/architecture/decisions/065-durable-execution-thin-run-journal-temporal-rejected.md` (the governing ADR, already accepted)
- `internal/router/router.go:318-386` (`SaveState`/`LoadState`, the closest
  existing precedent for "fail loud on corruption, don't silently reset" and
  `0600`-mode plain-JSON persistence, though that mechanism is a full-file
  overwrite, not append-only; this task's journal is a materially different,
  stronger mechanism per ADR 065)
- `internal/memoryguard/memoryguard.go` (the leaf-package structural convention:
  stdlib-only imports, `ExecRunner`-style seams for testability)
- `docs/spec/fitness-functions.md` (F-012 is the closest existing leaf-isolation
  fitness function to mirror for this task's new F-015)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-167-01 | `Status`, `AttemptState`, `PendingApproval`, `Record` types, JSON-tagged, exported. | must have |
| REQ-167-02 | `Store` interface + `FileStore` (`NewFileStore(dir) (*FileStore, error)`) implementing `Save`/`Load`/`ListInFlight`/`Delete`/`Compact`. | must have |
| REQ-167-03 | `Save` appends one crash-safe (`fsync`'d), `0600`-mode JSONL line; concurrent `Save` calls never interleave. | must have |
| REQ-167-04 | `Load`/`ListInFlight` return the latest record per goal ID (last-write-wins); `ListInFlight` excludes terminal statuses. | must have |
| REQ-167-05 | A truncated FINAL journal line is tolerated (crash-mid-append); a malformed line anywhere else is a fail-loud error. | must have |
| REQ-167-06 | `Compact()` atomically (temp+rename) snapshots and truncates the journal; an interrupted compact leaves prior state untouched. | must have |
| REQ-167-07 | `NewFileStore` rebuilds correct state from a prior, independently-constructed `FileStore`'s durable writes (the cross-restart proof). | must have |
| REQ-167-08 | `internal/runstore` is a strict leaf (stdlib only); a new `make fitness-runstore-isolation` (F-015) enforces it. | must have |
| REQ-167-09 | `Delete(goalID)` removes the goal from future `Load`/`ListInFlight` results, durably (tombstone survives replay). | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/167-runstore-run-journal-test-spec.md` exists (written first)
- [x] ADR 065 accepted
- [ ] `make check` green on `main` before branching

## Implementation outline

1. New package `internal/runstore`:
   - `record.go`: `Status` (string enum), `AttemptState`, `PendingApproval`,
     `Record` types, all JSON round-trippable.
   - `store.go`: the `Store` interface.
   - `filestore.go`: `FileStore` struct with a `sync.Mutex`, an in-memory
     `map[string]Record` index (rebuilt at construction, updated on every
     mutating call), `dir string`, and unexported `renameFunc func(oldpath,
     newpath string) error` (defaulting to `os.Rename`, overridable in tests for
     TC-167-10).
2. `NewFileStore(dir string) (*FileStore, error)`:
   - `os.MkdirAll(dir, 0700)`.
   - If `<dir>/snapshot.json` exists, parse it into the index (fail loud on
     malformed JSON, mirroring `router.LoadState`).
   - Replay `<dir>/journal.jsonl` line by line (`bufio.Scanner`), applying each
     line's `Record` into the index (last-write-wins per `GoalID`). On the LAST
     line only, a parse failure is silently discarded (crash-mid-append
     tolerance, REQ-167-05); a parse failure on any earlier line is a fail-loud
     error naming the file and line number.
3. `Save(rec Record) error`: lock the mutex, set `rec.UpdatedAt = time.Now()`
   (set `CreatedAt` only if zero), marshal to one JSON line, open
   `<dir>/journal.jsonl` with `O_APPEND|O_CREATE|O_WRONLY, 0600`, write the line,
   `f.Sync()`, close, update the in-memory index, unlock.
4. `Load(goalID string) (Record, bool, error)`: lock, look up the index, return.
5. `ListInFlight() ([]Record, error)`: lock, filter the index for `Status` not in
   `{StatusCompleted, StatusFailed}`, return a stable-ordered (e.g. sorted by
   `GoalID`) slice.
6. `Delete(goalID string) error`: append a tombstone (a `Record` with a
   `deleted bool` field, or reuse `Status` with a documented sentinel value,
   executor's choice, document it in the package doc comment either way), remove
   from the in-memory index, mirror `Save`'s crash-safety.
7. `Compact() error`: lock, marshal the current full index to JSON, write to
   `<dir>/snapshot.json.tmp`, `Sync`, close, call `renameFunc(tmp, snapshot.json)`
   (atomic on POSIX filesystems), then truncate `journal.jsonl` to empty
   (`os.Truncate` or reopen with `O_TRUNC`). On any error before the rename
   succeeds, leave `snapshot.json`/`journal.jsonl` untouched and return the error
   (do not truncate the journal until the rename is confirmed).
8. Add `fitness-runstore-isolation` to `Makefile` (copy F-012's `fitness-memoryguard-isolation`
   target, substitute the package path) and a matching F-015 row in
   `docs/spec/fitness-functions.md`.
9. Tests per the test spec.

## Acceptance criteria

- [ ] [REQ-167-01] TC-167-01: types round-trip JSON exactly.
- [ ] [REQ-167-02] TC-167-02: `FileStore` satisfies `Store`; basic Save/Load round trip.
- [ ] [REQ-167-03] TC-167-03/04: crash-safe append, no interleaving under `-race`.
- [ ] [REQ-167-04] TC-167-05/06: last-write-wins, terminal-status exclusion.
- [ ] [REQ-167-05] TC-167-07/08: truncated-final-line tolerated, mid-file corruption fails loud.
- [ ] [REQ-167-06] TC-167-09/10: atomic compact, interrupted compact leaves prior state untouched.
- [ ] [REQ-167-07] TC-167-11: cross-construction durability (L5).
- [ ] [REQ-167-08] TC-167-12: `make fitness-runstore-isolation` passes.
- [ ] [REQ-167-09] TC-167-13: `Delete` durably removes a goal.
- [ ] TC-167-14: `go test -race -count=1 ./internal/runstore/...` passes; `make check` passes.

## Verification plan

- **Highest level achievable:** L5, TC-167-11's two-independently-constructed
  `FileStore` proof, mirroring task 162's `SaveState`/`LoadState` cross-invocation
  proof pattern.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/runstore/...
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/runstore/... -run TestTC167_11
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/fitness-functions.md`: new F-015 row (leaf isolation for
  `internal/runstore`, mirrors F-012's structure/rationale).
- `docs/spec/interfaces.md`: new `internal/runstore` seam section documenting
  `Store`, `Record`, and the on-disk `journal.jsonl`/`snapshot.json` layout.
- `docs/spec/data-model.md`: new `runstore.Record`/`AttemptState`/`PendingApproval`
  entries.
- `docs/architecture/diagrams.md`: no runtime flow changes yet (this task adds no
  caller), skip unless the executor judges a components diagram addition
  clarifies the new leaf's position ahead of task 168.

## Out of scope

- Wiring into `internal/orchestrator` or `internal/runtime` (task 168).
- Resume/rehydration logic, idempotent re-dispatch semantics (task 168).
- A retry/re-plan budget loop (task 169).
- Automatic/scheduled `Compact()` invocation (this task provides the primitive;
  a size/age-triggered auto-compact is a follow-on if needed).

## Dependencies

- **Blocks on:** ADR 065 (already accepted).
- **Blocks:** task 168 (resume-after-restart), task 169 (sustained-autonomy-loop),
  task 170 (approval-pause), task 174 (daemon-mode).
