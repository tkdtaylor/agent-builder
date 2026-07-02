# Test Spec 162: persist and load router quota state across process invocations

**Linked task:** [`docs/tasks/backlog/162-router-quota-state-persistence.md`](../backlog/162-router-quota-state-persistence.md)
**Written:** 2026-07-02
**Status:** ready for implementation

## Context

Tasks 160/161 wire `OnGateFailure` (quality axis) and `RecordDispatch` (availability
axis, proactive) into the live dispatch path, but the `*router.Router` (and therefore
its `Usage`/`Availability` state) is constructed FRESH on every `resolveExecutor`
call — task 160's TC-160-05 explicitly confirms this per-dispatch scoping is correct
for the ESCALATION set (`escalated`, which must reset per task) but it also means
`Usage`/`Availability.Status` (the QUOTA state `RecordDispatch` populates) is lost the
moment one `agent-builder run` process exits. Since each `run` invocation (and,
functionally, each successive goal dispatch inside one `orchestrate` process — see
Note below) rebuilds the catalog from scratch via `buildCatalog`, a budgeted entry
that was exhausted in a PREVIOUS invocation is reported `AvailStatusAvailable` again
on the next invocation, even though its real-world quota window has not reset.
`Router.SaveState`/`LoadState` (`internal/router/router.go:318-390`) already implement
correct plain-text (JSON) persistence and are fully unit-tested in isolation
(`internal/router/quota_test.go`) — this task is the wiring gap: zero non-test call
sites exist anywhere.

**The fix:** a new optional env var (`AGENT_BUILDER_ROUTER_STATE_PATH`) configures a
file path. When set: `resolveExecutor` calls `router.LoadState(path)` immediately
after constructing the router and BEFORE the first `Select` (a missing file — first
run — is NOT an error, matching `LoadState`'s existing graceful-absence-tolerant
contract if it has one, or is explicitly tolerated at the call site if not); after
`RecordDispatch`/`OnGateFailure`/`Select` calls for the dispatch complete (i.e., once
per `Run` invocation, at a point that captures the post-dispatch state), the router
calls `SaveState(path)` so the next invocation observes accumulated `Usage`/
`Availability` state. When unset, behavior is byte-for-byte identical to pre-task
(in-memory-only, reset every invocation) — this is an ADDITIVE, opt-in feature, not a
default-on behavior change.

**Note on "across dispatches" scope:** `runtime.Run` is a single-task-per-process-
invocation function; "long-lived across dispatches" in the review's framing means
across SUCCESSIVE PROCESS INVOCATIONS of `run`/`orchestrate` (the real-world operating
pattern — the supervisor's outer loop and, per-goal, `orchestrate`'s dispatch path
both eventually re-invoke `runtime.Run`), not a single always-running daemon holding
one Go value in memory for the whole process's lifetime (no such daemon exists in the
current architecture — see `docs/architecture/overview.md`'s "Known missing" list).
File-based persistence is the correct mechanism for this scope, matching the existing
`docs/spec` emphasis on plain-text intermediate artifacts.

**Module boundaries touched:** `internal/runtime` only (`resolveExecutor`/`Run` gain
the load/save calls; `Config`/`ConfigFromEnv` gain the new env var).

---

## Requirements coverage

| Req ID     | Description                                                                                                                 | Test cases            |
|------------|-----------------------------------------------------------------------------------------------------------------------------|--------------------------|
| REQ-162-01 | A new env var `AGENT_BUILDER_ROUTER_STATE_PATH` is read by `ConfigFromEnv`; unset means no persistence (pre-task behavior, in-memory only) | TC-162-01               |
| REQ-162-02 | When set, `resolveExecutor` calls `router.LoadState(path)` before the first `Select`; a missing file (first run) is tolerated, not a fail-fast error | TC-162-02               |
| REQ-162-03 | When set, the router's state is persisted via `SaveState(path)` after the dispatch's `RecordDispatch`/`OnGateFailure` calls complete, so the NEXT process invocation observes the accumulated `Usage`/`Availability` state | TC-162-03               |
| REQ-162-04 | A corrupted/malformed state file at the configured path is a fail-fast `Run` error at load time (matching `LoadState`'s own existing malformed-file contract, `internal/router/quota_test.go`'s `TestSaveStateAndLoadState`/corrupt-file case) — never a silent reset to fresh state | TC-162-04               |
| REQ-162-05 | When unset (the default), behavior is byte-for-byte identical to pre-task: no state file is read or written, and `Usage`/`Availability` state does not survive across two sequential `Run` invocations in the test process | TC-162-05               |
| REQ-162-06 | End-to-end across two SEQUENTIAL `Run` invocations sharing the same configured state path: an entry exhausted by `RecordDispatch` in invocation 1 is reported exhausted (not reset) at the start of invocation 2, and `Select` in invocation 2 correctly routes to the next available entry from the very first attempt | TC-162-06               |
| REQ-162-07 | Pre-existing `internal/runtime`, `internal/router` suites continue to pass unchanged | TC-162-07               |

---

## Pre-implementation checklist

- [x] Task 161 merged (`RecordDispatch` wiring this task's persistence makes durable)
- [x] Task 093 merged (`SaveState`/`LoadState` themselves already exist and are unit-tested)
- [ ] `make check` green before branching

---

## Test cases

### TC-162-01 — Env var wiring, unset means no persistence

- **Requirement:** REQ-162-01
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/run_test.go` (extend the existing `ConfigFromEnv` table) or `run_162_test.go`

**Step:** Call `ConfigFromEnv` with `AGENT_BUILDER_ROUTER_STATE_PATH` unset, then set
to a path.

**Expected output:** Unset → `Config.RouterStatePath == ""` (or equivalent zero
value), no error. Set → the value is captured verbatim (path cleaning per the
existing `cleanPath` convention used by other path-shaped config fields in this file).

---

### TC-162-02 — `LoadState` runs before the first `Select`; a missing file is tolerated

- **Requirement:** REQ-162-02
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/run_162_test.go` (new)

**Setup:** `RouterStatePath` set to a path in a fresh temp dir (file does not exist yet).

**Step:** Call `resolveExecutor(spec, config)`.

**Expected output:** No error from the missing-file load (first-run case); `Select`
still runs and returns a normal result — proving the load attempt does not fail
assembly when there is nothing to load yet.

---

### TC-162-03 — State is saved after dispatch completes

- **Requirement:** REQ-162-03
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/run_162_test.go`

**Setup:** `RouterStatePath` set to a fresh temp path; a budgeted catalog entry.

**Step:** Run one full `Run(ctx, config, stdout)` invocation that dispatches (and thus
`RecordDispatch`s) the budgeted entry at least once.

**Expected output:** After `Run` returns, the configured path exists on disk and, when
parsed directly as JSON (or loaded via a fresh `router.NewWithClock(...).LoadState(path)`),
reflects the entry's incremented `Usage`.

---

### TC-162-04 — A corrupted state file is a fail-fast error, not a silent reset

- **Requirement:** REQ-162-04
- **Level:** L2 (unit test, mirrors `TestSaveStateAndLoadState`'s corrupt-file case)
- **Test file:** `internal/runtime/run_162_test.go`

**Setup:** `RouterStatePath` set to a path containing deliberately malformed JSON
(written directly by the test before calling `resolveExecutor`).

**Step:** Call `resolveExecutor(spec, config)` (or the full `Run` path).

**Expected output:** A non-nil error is returned BEFORE any sandbox/box creation
(mirroring the existing "fail before any sandbox.Create" pattern this function already
follows for other resolution errors) — never a silent fallback to fresh in-memory
state that masks the corruption.

---

### TC-162-05 — Unset path: byte-for-byte pre-task behavior, no cross-invocation persistence

- **Requirement:** REQ-162-05
- **Level:** L2 (unit test — regression)
- **Test file:** `internal/runtime/run_162_test.go`

**Setup:** `RouterStatePath` unset. A budgeted catalog entry.

**Step:** Run TWO sequential `Run` invocations in the same test process, each
dispatching the budgeted entry enough times to reach `Budget.Limit` if state persisted.

**Expected output:** The SECOND invocation's first `Select` still sees the entry as
`AvailStatusAvailable` (fresh router each call, exactly as before this task) — no
state leaks across invocations when persistence is not configured. No file is created
anywhere.

---

### TC-162-06 — End-to-end: exhaustion survives across two sequential invocations

- **Requirement:** REQ-162-06
- **Level:** L5 (two real, sequential `Run` invocations sharing one state file — the strongest achievable proof of "persists across dispatches" without a second OS process)
- **Test file:** `internal/runtime/run_162_test.go`

**Setup:** `RouterStatePath` set to one shared temp path. A two-entry catalog: entry A
`Budget.Limit = 1` (exhausts after one dispatch), entry B unlimited. Both entries
pass the gate.

**Step:** (1) Run invocation 1: `Select` picks A (cheaper), dispatches it once
(`RecordDispatch` fires, A now exhausted), `Run` completes and saves state. (2) Run
invocation 2 (a FRESH `resolveExecutor`/`Router` construction, simulating a new OS
process by not reusing any in-memory router value from step 1 — only the shared file):
`resolveExecutor` loads state, then `Select` is called.

**Expected output:** Invocation 2's `Select` returns entry B directly on the FIRST
call — A is correctly reported exhausted from invocation 1's persisted state, not
reset to available. This is the load-bearing assertion: "quota state is never
recorded/persisted" (the review's exact finding) is now false.

---

### TC-162-07 — Full regression

- **Requirement:** REQ-162-07
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/runtime/... ./internal/router/...
make check
```

**Expected output:** All packages `ok`; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L5 — two sequential, independently-constructed
  `Run`/`resolveExecutor` invocations sharing one on-disk state file is the strongest
  proof of cross-process persistence achievable inside a single Go test binary (a true
  two-OS-process L6 test is possible but adds no additional confidence over this L5
  harness, since the mechanism under test is file I/O + JSON marshal/unmarshal,
  already fully covered).
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/runtime/... -run TestTC162
  ```
  Expected: TC-162-01..05 pass.
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/runtime/... -run TestTC162_06
  ```
  Expected: exhaustion from invocation 1 is observed by invocation 2's very first `Select`.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L6 (optional, operator-observed):** run `agent-builder run` twice in a row against
  a registry with a tightly-budgeted entry and `AGENT_BUILDER_ROUTER_STATE_PATH` set;
  confirm the second invocation's chosen executor reflects the first invocation's
  recorded usage (e.g. via `--verbose`/log output naming the selected entry ID, if
  available, or by inspecting the state file's contents directly).

## Out of scope

- `OnQuotaExhausted`/`OnRateLimit` wiring — no current executor surfaces a
  machine-parseable rate-limit signal (see task 161's Out of scope).
- Any daemon/always-running-process design — this task uses file-based persistence
  across process invocations, matching the current architecture (no daemon exists).
- Concurrent-writer safety for two SIMULTANEOUS `agent-builder run` processes sharing
  one state path (last-writer-wins is acceptable for this task's scope; a file-lock
  seam is a follow-on if concurrent supervisors sharing one state path becomes a real
  deployment shape).
