# Test Spec 161: record dispatch usage on the live path (`Router.RecordDispatch` wiring)

**Linked task:** [`docs/tasks/backlog/161-router-record-dispatch-usage.md`](../backlog/161-router-record-dispatch-usage.md)
**Written:** 2026-07-02
**Status:** ready for implementation

## Context

Task 160 wired `Router.OnGateFailure`/`Select` into the live retry/escalation hook
(the QUALITY axis). This task wires the AVAILABILITY axis's proactive half:
`Router.RecordDispatch(entryID)` (`internal/router/router.go:240-265`) increments an
entry's `Usage` tally on every dispatch and proactively marks it exhausted once
`Usage >= Budget.Limit`, so the router's `Select` naturally routes SIDEWAYS to the
next available entry once a budgeted entry's quota is spent — a distinct axis from
`OnGateFailure`'s quality-based escalation (task 160). `RecordDispatch` has zero
non-test call sites anywhere in the codebase today (confirmed via `grep`): no dispatch
attempt, successful or failed, is ever recorded, so `Usage`/`Budget.Limit`-based
proactive exhaustion NEVER fires on the live path regardless of how many times a
budgeted entry (e.g. a rate-limited cloud API key) is actually dispatched.

**The fix:** every attempt inside a task's bounded retry loop calls
`router.RecordDispatch(entryID)` exactly once per attempt (unconditional of the
attempt's outcome — `RecordDispatch`'s own contract already handles success/failure
uniformly: it is a usage-accounting call, not an outcome classifier). This is wired
into the SAME closure/`Router` reference task 160 already threads through
`resolveExecutor`/`Run` — this task extends that wiring, it does not introduce a new
seam.

**Module boundaries touched:** `internal/runtime` only (the same closure/call site
task 160 introduced).

**Explicitly excluded (see Out of scope in the task file):** `OnQuotaExhausted` and
`OnRateLimit` are the REACTIVE half of the availability axis — they require a
provider-reported quota/429 signal that no current executor (`ClaudeCLI`, `CodexCLI`,
`GeminiCLI`, `AntigravityCLI`, `OllamaNative`) surfaces in a machine-parseable form
today (each returns a generic wrapped subprocess/HTTP error with no structured
rate-limit classification). Wiring those two calls without a real signal to drive them
would be a call with no live trigger — i.e., another dead wire, not a fix. This task
covers ONLY the proactive `RecordDispatch` accounting call, which needs no such signal
(it fires on every attempt, unconditionally).

---

## Requirements coverage

| Req ID     | Description                                                                                                                    | Test cases            |
|------------|-------------------------------------------------------------------------------------------------------------------------------------|--------------------------|
| REQ-161-01 | Every dispatch attempt inside a task's bounded retry loop calls `router.RecordDispatch(entryID)` exactly once, for the entry actually used on that attempt | TC-161-01               |
| REQ-161-02 | `RecordDispatch` fires regardless of the attempt's outcome (gate pass, gate fail, or executor error) — it is a usage-accounting call, not conditioned on success | TC-161-02               |
| REQ-161-03 | A budgeted entry that reaches `Budget.Limit` via repeated `RecordDispatch` calls is proactively marked exhausted, and the NEXT `Select` (whether from task 160's escalation hook or the initial `resolveExecutor` call on a subsequent task) routes sideways to the next available eligible entry | TC-161-03               |
| REQ-161-04 | A local/unlimited entry (`Budget.Limit == 0`) is never marked exhausted regardless of `RecordDispatch` call count (regression of the existing `Router.RecordDispatch` no-op-on-local-entry contract, task 093) | TC-161-04               |
| REQ-161-05 | The single-entry (synthetic default Claude entry) deployment path is unaffected — `RecordDispatch` on an unlimited synthetic entry is a no-op, matching pre-task behavior exactly | TC-161-05               |
| REQ-161-06 | Pre-existing `internal/runtime`, `internal/router`, `internal/loop` suites continue to pass unchanged | TC-161-06               |

---

## Pre-implementation checklist

- [x] Task 160 merged (the router-backed escalation closure and the retained
  `*router.Router` reference this task extends)
- [x] Task 093 merged (`RecordDispatch` itself already exists and is unit-tested in isolation)
- [ ] `make check` green before branching

---

## Test cases

### TC-161-01 — Every attempt records exactly one dispatch for its entry

- **Requirement:** REQ-161-01
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/run_161_test.go` (new)

**Setup:** A two-entry catalog (cheap fails once then the run completes on a second
attempt with the SAME cheap entry allowed to retry — i.e. `MaxAttempts=2`, no
escalation needed for this specific test, isolating `RecordDispatch` from task 160's
escalation logic). Instrument the catalog's `Router` (or the fake dispatch counter) to
record `RecordDispatch` calls by entry ID.

**Step:** Drive the retry loop through 2 attempts (both using the same entry, since it
never reaches `Budget.Limit` in this setup).

**Expected output:** `RecordDispatch("cheap-entry")` was called exactly 2 times — once
per attempt — not 0 (the pre-task state) and not more than once per attempt.

---

### TC-161-02 — `RecordDispatch` fires on both a passing and a failing attempt

- **Requirement:** REQ-161-02
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/run_161_test.go`

**Setup:** Two independent single-attempt scenarios: (a) the executor/gate both
succeed on attempt 1; (b) the executor errors outright on attempt 1 (no gate reached).

**Step:** Drive each scenario through one attempt.

**Expected output:** In BOTH (a) and (b), `RecordDispatch` was called exactly once for
the entry used — usage accounting is unconditional on outcome.

---

### TC-161-03 — A budgeted entry proactively exhausts and Select routes sideways

- **Requirement:** REQ-161-03
- **Level:** L2 (unit test, using `router.NewWithClock`'s `FakeClock` seam — no real sleeping)
- **Test file:** `internal/runtime/run_161_test.go`

**Setup:** A two-entry catalog: entry A has `Budget.Limit = 2` and always passes the
gate (so no escalation-driven `OnGateFailure` interferes); entry B is unlimited and
always passes. `MaxAttempts` high enough to span multiple independent task dispatches
in the test (each `resolveExecutor` call simulating one dispatch), OR — more directly
— construct the `Router` once (bypassing the per-`Run`-call `resolveExecutor`
re-construction, per task 160's per-dispatch scoping) and call `RecordDispatch("A")`
twice directly, then call `Select` a third time.

**Step:** After `RecordDispatch("A")` has been called `Budget.Limit` (2) times,
call `router.Select(spec)` again.

**Expected output:** `Select` no longer returns entry A (proactively exhausted,
`Availability.Status == AvailStatusExhausted`); it returns entry B instead — the
availability axis routes sideways, distinct from task 160's quality-axis climbing
(entry A never gate-failed in this scenario; it was budget-exhausted).

---

### TC-161-04 — A local/unlimited entry is never exhausted by `RecordDispatch`

- **Requirement:** REQ-161-04
- **Level:** L2 (unit test — regression of the existing router-level contract, now proven wired end-to-end)
- **Test file:** `internal/runtime/run_161_test.go`

**Step:** Drive many attempts (e.g. 10) against a catalog containing only an
unlimited local entry.

**Expected output:** The entry's `Availability.Status` remains
`AvailStatusAvailable` throughout — `RecordDispatch`'s existing no-op-on-unlimited
contract holds when invoked from the live wiring, not just in `internal/router`'s own
unit tests.

---

### TC-161-05 — The synthetic default Claude entry path is unaffected

- **Requirement:** REQ-161-05
- **Level:** L2 (unit test — regression)
- **Test file:** `internal/runtime/run_test.go` (existing zero-registry tests)

**Step:** Run the existing zero-`AGENT_BUILDER_REGISTRY_*`-env test(s).

**Expected output:** Unchanged behavior — `defaultClaudeEntry` is unlimited
(`Budget` zero value), so `RecordDispatch` calls against it are no-ops, matching
pre-task behavior exactly for the most common single-provider deployment shape.

---

### TC-161-06 — Full regression

- **Requirement:** REQ-161-06
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/runtime/... ./internal/router/... ./internal/loop/...
make check
```

**Expected output:** All packages `ok`; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L2 — this task's REQs are usage-accounting call-site
  wiring, fully provable via unit tests with a `FakeClock`-backed `Router` and a fake
  multi-entry catalog; no live subprocess or L5 production-chain harness adds
  meaningfully distinct confidence over task 160's already-established L5 pattern
  (this task reuses the same wiring point, adding one call).
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/runtime/... ./internal/router/...
  ```
  Expected: all TC-161-01..05 pass.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- `OnQuotaExhausted`/`OnRateLimit` (the REACTIVE availability-axis calls) — no current
  executor surfaces a machine-parseable quota/429 signal; wiring these without a real
  trigger would itself be a dead wire. Flagged as a follow-on task contingent on an
  executor-level rate-limit-signal task landing first (not scoped here).
- `SaveState`/`LoadState` (cross-process persistence) — task 162.
- Any change to the quality-axis escalation hook (task 160, already merged/sequenced
  before this task).
