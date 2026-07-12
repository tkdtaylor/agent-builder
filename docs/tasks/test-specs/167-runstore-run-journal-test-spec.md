# Test Spec 167: `internal/runstore`, a file-backed durable run journal

**Linked task:** [`docs/tasks/backlog/167-runstore-run-journal.md`](../backlog/167-runstore-run-journal.md)
**Written:** 2026-07-11
**Status:** ready for implementation

## Context

`docs/architecture/decisions/065-durable-execution-thin-run-journal-temporal-rejected.md`
(accepted 2026-07-11) rejects Temporal and every external durable-execution engine
for the orchestrator and commits to "a `RunStore` seam plus a stdlib file-backed run
journal (append-only JSONL with snapshot/compaction, crash-safe temp+rename writes)
recording goal, plan, per-task attempt state, pending approvals, and terminal
status (task 167)." Today the orchestrator's only state is
`orchestrator.MemoryPlanStore` (`internal/orchestrator/store.go:11-38`), an
in-memory `map[string]Plan`. A crash mid-goal loses everything: no attempt
history, no record that a sub-goal was mid-dispatch, no pending-approval record
that survives a restart.

This task builds ONLY the storage primitive: a new leaf package,
`internal/runstore`, implementing the journal ADR 065 specifies. It does not wire
anything into the orchestrator (task 168), does not add resume logic (task 168),
and does not add a retry/re-plan loop (task 169). It is a pure, independently
testable seam, mirroring how `internal/policy`/`internal/vault`/`internal/audit`/
`internal/memoryguard` are each a stdlib-only leaf the orchestrator/runtime layer
composes on top of.

**Module boundary:** `internal/runstore` is a NEW strict leaf: stdlib only
(`encoding/json`, `os`, `path/filepath`, `sync`, `time`, `fmt`, `errors`, `bufio`).
No `agent-builder/internal/*` import, mirroring `internal/memoryguard`'s F-012
convention. This task also adds a matching fitness function, F-015, to
`Makefile`/`docs/spec/fitness-functions.md`.

---

## Requirements coverage

| Req ID     | Description | Test cases |
|------------|--------------|------------|
| REQ-167-01 | `runstore` package exports `Status` (a closed string enum: `StatusPending`, `StatusRunning`, `StatusAwaitingApproval`, `StatusCompleted`, `StatusFailed`, `StatusNeedsHuman`), `AttemptState{TaskID, Attempt int, Status, Detail, UpdatedAt}`, `PendingApproval{TaskID, Reason, RequestedAt}`, and `Record{GoalID, Goal, Plan json.RawMessage, Attempts []AttemptState, Pending []PendingApproval, Status, CreatedAt, UpdatedAt}`, all JSON-tagged and exported. | TC-167-01 |
| REQ-167-02 | `Store` interface: `Save(Record) error`, `Load(goalID string) (Record, bool, error)`, `ListInFlight() ([]Record, error)`, `Delete(goalID string) error`, `Compact() error`. `FileStore` (constructed via `NewFileStore(dir string) (*FileStore, error)`) implements it. | TC-167-02 |
| REQ-167-03 | `Save` appends exactly one JSON line to `<dir>/journal.jsonl` (`O_APPEND\|O_CREATE\|O_WRONLY`, mode `0600`) and calls `Sync()` before returning; concurrent `Save` calls from multiple goroutines never interleave or corrupt a line (internal mutex). | TC-167-03, TC-167-04 |
| REQ-167-04 | `Load`/`ListInFlight` return the LATEST record per `GoalID` (last-write-wins by append order across `snapshot.json` + `journal.jsonl`, in that order). `ListInFlight` returns only records whose `Status` is NOT `StatusCompleted`/`StatusFailed` (the two terminal statuses). | TC-167-05, TC-167-06 |
| REQ-167-05 | A truncated/partial FINAL line in `journal.jsonl` (simulating a crash mid-append) is tolerated silently ONLY when it is the last line in the file; a malformed line anywhere else in the file is a fail-loud corruption error from `NewFileStore`/`Load`/`ListInFlight`, never silently skipped or reset to empty state. | TC-167-07, TC-167-08 |
| REQ-167-06 | `Compact()` atomically (temp file + `fsync` + `os.Rename`) writes `<dir>/snapshot.json` from the current replayed state, then truncates `journal.jsonl` to empty. A process interrupted between writing the temp file and the rename leaves `snapshot.json`/`journal.jsonl` byte-for-byte as they were before `Compact()` started (atomicity: no partial snapshot is ever visible). | TC-167-09, TC-167-10 |
| REQ-167-07 | `NewFileStore` rebuilds correct state by replaying `snapshot.json` (if present) then `journal.jsonl` once at construction time, so a freshly constructed `FileStore` immediately reflects everything durably written by a PRIOR, independently-constructed `FileStore` sharing the same directory (the load-bearing cross-process-restart proof). | TC-167-11 |
| REQ-167-08 | `internal/runstore` is a strict leaf: `go list -deps ./internal/runstore/...` reports no `agent-builder/internal/*` path other than itself. A new `make fitness-runstore-isolation` target (F-015) enforces this, mirroring F-012's exact grep pattern. | TC-167-12 |
| REQ-167-09 | `Delete(goalID)` removes the goal from future `Load`/`ListInFlight` results (implemented as a tombstone record appended to the journal, `Status` unused, matched by a dedicated boolean, or an explicit `deleted` marker; executor's choice of wire representation, but the observable contract is: after `Delete`, `Load(goalID)` returns `(Record{}, false, nil)`). | TC-167-13 |

---

## Pre-implementation checklist

- [x] ADR 065 accepted (`docs/architecture/decisions/065-durable-execution-thin-run-journal-temporal-rejected.md`)
- [ ] `make check` green on `main` before branching

---

## Test cases

### TC-167-01, exported types are JSON round-trippable

- **Requirement:** REQ-167-01
- **Level:** L2 (unit test)
- **Test file:** `internal/runstore/record_test.go` (new)

**Step:** Construct a `Record` with a non-empty `Attempts` and `Pending` slice,
`Plan: json.RawMessage(`{"goal":"x"}`)`, marshal to JSON, unmarshal into a fresh
`Record`.

**Expected output:** field-for-field equality with the original (including
`Plan`'s raw bytes, `Attempts`/`Pending` slice contents, and `Status` string
values matching the documented constants exactly, e.g. `string(StatusRunning) ==
"running"`).

---

### TC-167-02, `FileStore` satisfies `Store`

- **Requirement:** REQ-167-02
- **Level:** L2 (compile-time assertion + basic Save/Load round trip)
- **Test file:** `internal/runstore/filestore_test.go` (new)

**Step:** `var _ Store = (*FileStore)(nil)` (compile-time). Construct
`NewFileStore(t.TempDir())`, `Save(Record{GoalID: "g1", Status: StatusRunning})`,
`Load("g1")`.

**Expected output:** `Load` returns the saved record, `ok == true`, `err == nil`.
`Load("unknown")` returns `(Record{}, false, nil)`.

---

### TC-167-03, `Save` writes one crash-safe JSONL line

- **Requirement:** REQ-167-03
- **Level:** L2

**Step:** `Save` a record, then read `<dir>/journal.jsonl` directly with
`os.ReadFile` and count newline-terminated lines.

**Expected output:** exactly 1 line; the line parses as valid JSON matching the
saved record; the file's mode is `0600` (`os.Stat` + `fi.Mode().Perm()`).

---

### TC-167-04, concurrent `Save` calls never interleave

- **Requirement:** REQ-167-03
- **Level:** L2 (race-sensitive, run under `-race`)

**Step:** Spawn 50 goroutines, each calling `Save` with a distinct `GoalID` (`g0`
through `g49`), `sync.WaitGroup` to join. Then `ListInFlight()`.

**Expected output:** `go test -race` reports no data race; `journal.jsonl` has
exactly 50 well-formed lines (no torn/interleaved bytes); `ListInFlight` returns
all 50 records (each `Status` defaults to non-terminal in this test, e.g.
`StatusRunning`), one per `GoalID`, no duplicates, no missing entries.

---

### TC-167-05, `Load`/`ListInFlight` return the latest record (last-write-wins)

- **Requirement:** REQ-167-04
- **Level:** L2

**Step:** `Save(Record{GoalID: "g1", Status: StatusRunning, Goal: "v1"})`, then
`Save(Record{GoalID: "g1", Status: StatusRunning, Goal: "v2"})` (same GoalID,
simulating an attempt-state update). `Load("g1")`.

**Expected output:** `Goal == "v2"` (the later write wins); `journal.jsonl` still
has 2 lines (append-only, no in-place mutation) confirming the "latest wins on
replay" semantics, not "only one write is ever allowed."

---

### TC-167-06, `ListInFlight` excludes terminal statuses

- **Requirement:** REQ-167-04
- **Level:** L2

**Step:** `Save` four records with `Status` values `StatusPending`,
`StatusAwaitingApproval`, `StatusCompleted`, `StatusFailed` respectively (distinct
`GoalID`s). `ListInFlight()`.

**Expected output:** exactly 2 records returned (`StatusPending`,
`StatusAwaitingApproval`); `StatusCompleted`/`StatusFailed` goals are absent.

---

### TC-167-07, a truncated FINAL line is tolerated

- **Requirement:** REQ-167-05
- **Level:** L2 (simulated crash-mid-append)

**Step:** `Save` two valid records, then directly truncate `journal.jsonl` (via
`os.OpenFile` + `Truncate`) to cut off the last few bytes of the SECOND line
(simulating a crash mid-`Write`, before `Sync` completed, leaving a partial final
line). Construct a fresh `NewFileStore` on the same directory.

**Expected output:** no error from `NewFileStore`; `ListInFlight`/`Load` reflect
ONLY the first (complete) record; the truncated second line is silently dropped,
not surfaced as a corruption error (a crash mid-append is an expected, recoverable
event per ADR 065's crash-safety requirement).

---

### TC-167-08, a malformed line NOT at the end is a fail-loud error

- **Requirement:** REQ-167-05
- **Level:** L2

**Step:** `Save` three valid records, then directly corrupt the SECOND line
(overwrite it with `"not json at all"` while keeping the newline, so a THIRD,
well-formed line follows it). Construct a fresh `NewFileStore` on the same
directory.

**Expected output:** `NewFileStore` returns a non-nil error whose message
identifies the journal file and mentions corruption/parse failure (mirrors
`router.LoadState`'s existing "state file may be corrupted" convention,
`internal/router/router.go:359`); it is never a silent reset to empty state, and
it is never silently skipped the way TC-167-07's LAST-line case is.

---

### TC-167-09, `Compact()` produces an atomic snapshot and truncates the journal

- **Requirement:** REQ-167-06
- **Level:** L2

**Step:** `Save` three records for three distinct goals, `Compact()`, then read
`<dir>/journal.jsonl` and `<dir>/snapshot.json` directly.

**Expected output:** `journal.jsonl` is empty (0 bytes) after `Compact`;
`snapshot.json` exists, mode `0600`, and parses as a JSON object/array containing
exactly the 3 latest records; a fresh `NewFileStore` on the same directory
reflects all 3 records correctly (replaying the snapshot alone, since the journal
is now empty).

---

### TC-167-10, `Compact()` is atomic under a simulated interruption

- **Requirement:** REQ-167-06
- **Level:** L2 (interruption simulated via an injectable filesystem seam, NOT a
  real process kill, mirrors how `internal/router`/`internal/audit` test
  crash-adjacent behavior without an actual `SIGKILL`)

**Step:** Provide `FileStore` with an internal seam (e.g. an unexported
`renameFunc` field defaulting to `os.Rename`, overridable in tests) that fails the
`os.Rename` call `Compact()` makes AFTER the temp file is fully written. Call
`Compact()` and assert it returns a non-nil error.

**Expected output:** the pre-existing `snapshot.json` (or its absence, on a
first-ever compact) and `journal.jsonl` are BYTE-FOR-BYTE unchanged from before
the failed `Compact()` call (read both files before and after, compare); the
temp file the failed rename left behind (if any) is either cleaned up by
`Compact()`'s error path or is verifiably NOT `snapshot.json` (never a
partially-written snapshot visible under the real name).

---

### TC-167-11, cross-construction durability (the load-bearing proof)

- **Requirement:** REQ-167-07
- **Level:** L5 (two independently-constructed `FileStore` values sharing one
  on-disk directory inside a single Go test binary, the strongest achievable
  proof of "survives a process restart" without a real second OS process, mirrors
  task 162's TC-162-06 pattern for `router.SaveState`/`LoadState`)

**Step:** Construct `store1 := NewFileStore(dir)`, `Save` two records (one
`StatusRunning`, one `StatusCompleted`), do NOT call `Compact`. Discard `store1`
(let it go out of scope, no explicit close/teardown needed since writes are
already fsync'd per-call). Construct `store2 := NewFileStore(dir)` fresh, call
`store2.ListInFlight()`.

**Expected output:** `store2.ListInFlight()` returns exactly the 1 non-terminal
record from `store1`'s writes, with all fields matching; `store2.Load` for the
completed goal also succeeds and returns `StatusCompleted` (present, just excluded
from `ListInFlight`). This proves state is durable across independent
construction, not just accumulated in one `FileStore`'s in-memory cache.

---

### TC-167-12, leaf isolation (F-015)

- **Requirement:** REQ-167-08
- **Level:** L3 (fitness)

**Step:** `make fitness-runstore-isolation`

**Expected output:** `PASS fitness-runstore-isolation: internal/runstore import
graph contains no agent-builder/internal/* dependency.` (exact wording at
executor's discretion, mirroring F-012's message shape); a deliberately introduced
`agent-builder/internal/orchestrator` import (negative-fixture proof, added
temporarily during development and reverted, or proven via a documented manual
check) causes the target to fail.

---

### TC-167-13, `Delete` removes a goal from future reads

- **Requirement:** REQ-167-09
- **Level:** L2

**Step:** `Save(Record{GoalID: "g1", ...})`, `Delete("g1")`, `Load("g1")`.

**Expected output:** `Load("g1")` returns `(Record{}, false, nil)`. A fresh
`NewFileStore` on the same directory also does not surface `"g1"` (the tombstone
survives replay, matching REQ-167-07's durability guarantee).

---

### TC-167-14, full regression

- **Requirement:** all
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/runstore/...
make check
```

**Expected output:** `ok`; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L5, TC-167-11's two-independently-constructed-`FileStore`
  proof (the same pattern task 162 used for router state persistence). No real
  second OS process needed; the mechanism under test is file I/O, fully exercised
  in-process.
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
  Expected: `All checks passed.` (including new `fitness-runstore-isolation`).

## Out of scope

- Any wiring into `internal/orchestrator` (task 168).
- Resume/rehydration logic, idempotent re-dispatch (task 168).
- A retry/re-plan budget loop (task 169).
- Automatic/scheduled `Compact()` calls (this task provides the primitive; callers
  decide when to compact, a follow-on can add a size/age-triggered auto-compact if
  operationally needed).
