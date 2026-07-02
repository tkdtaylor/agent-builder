# Task 161: record dispatch usage on the live path (`Router.RecordDispatch` wiring)

**Project:** agent-builder
**Created:** 2026-07-02
**Status:** backlog

## Goal

Call `Router.RecordDispatch(entryID)` exactly once per dispatch attempt on the live
`run`/`orchestrate` path, so a budgeted entry's `Usage` tally reflects real dispatch
volume and proactively exhausts (routing `Select` sideways to the next available
entry) once `Budget.Limit` is reached — currently a dead wire with zero non-test call
sites.

## Context

**Root cause (full-project review, verified 2026-07-02):** task 160 wires the
QUALITY axis (`OnGateFailure`/`Select` climbing the capability ladder on a gate
failure) into the live retry/escalation hook, but the AVAILABILITY axis's proactive
half — `Router.RecordDispatch` (`internal/router/router.go:240-265`) — is never
called anywhere outside `internal/router`'s own tests (confirmed via `grep`). Every
dispatch attempt, successful or failed, goes unrecorded, so `Usage`-based proactive
exhaustion (`Usage >= Budget.Limit` → `Availability.Status = AvailStatusExhausted`)
never fires regardless of how many times a budgeted entry is actually used.

**The fix:** the retry loop calls `router.RecordDispatch(entryID)` exactly once per
attempt, for the entry actually used on that attempt, regardless of the attempt's
outcome (it is a usage-accounting call, not conditioned on success — matching
`RecordDispatch`'s own documented contract). This extends the SAME
closure/`*router.Router` wiring point task 160 introduced in `internal/runtime`; it
does not add a new seam.

**Deliberately excluded:** `OnQuotaExhausted`/`OnRateLimit` (the REACTIVE half of the
availability axis) require a provider-reported quota/429 signal no current executor
(`ClaudeCLI`, `CodexCLI`, `GeminiCLI`, `AntigravityCLI`, `OllamaNative`) surfaces in a
machine-parseable form — each returns a generic wrapped subprocess/HTTP error. Wiring
those two calls with no real trigger to drive them would itself be a dead wire, not a
fix; that requires an executor-level rate-limit-signal task first, out of this
review's scope.

**Reference:**
- `internal/router/router.go:240-265` (`RecordDispatch` — already correct, already
  unit-tested; this task is pure wiring)
- `internal/runtime/run.go` (the closure/call site task 160 introduces, extended here)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-161-01 | Every dispatch attempt calls `router.RecordDispatch(entryID)` exactly once, for the entry used on that attempt. | must have |
| REQ-161-02 | `RecordDispatch` fires unconditionally of the attempt's outcome (pass, gate-fail, executor error). | must have |
| REQ-161-03 | A budgeted entry that reaches `Budget.Limit` via `RecordDispatch` calls is proactively marked exhausted, and the next `Select` routes sideways to the next available eligible entry. | must have |
| REQ-161-04 | A local/unlimited entry is never exhausted by `RecordDispatch`, proven wired end-to-end (not just in `internal/router`'s own tests). | must have |
| REQ-161-05 | The single-entry synthetic default Claude path is behaviorally unaffected. | must have |
| REQ-161-06 | Pre-existing `internal/runtime`/`internal/router`/`internal/loop` suites continue to pass unchanged. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/161-router-record-dispatch-usage-test-spec.md` exists (written first)
- [x] Task 160 merged (this task extends its closure/`*router.Router` wiring)
- [x] Task 093 merged (`RecordDispatch` itself already exists and is unit-tested)
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [ ] [REQ-161-01] TC-161-01: `RecordDispatch` is called exactly once per attempt, for the entry used.
- [ ] [REQ-161-02] TC-161-02: `RecordDispatch` fires on both a passing and a failing attempt.
- [ ] [REQ-161-03] TC-161-03: a budget-exhausted entry causes the next `Select` to route sideways to the next available entry.
- [ ] [REQ-161-04] TC-161-04: an unlimited entry is never exhausted, proven via the live wiring.
- [ ] [REQ-161-05] TC-161-05: the zero-registry single-provider path is unaffected.
- [ ] [REQ-161-06] TC-161-06: `go test -race -count=1 ./internal/runtime/... ./internal/router/... ./internal/loop/...` passes in full; `make check` passes.

## Verification plan

- **Highest level achievable:** L2 — usage-accounting call-site wiring, fully provable
  via unit tests with a `FakeClock`-backed `Router` and a fake multi-entry catalog.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/runtime/... ./internal/router/...
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/architecture.md` — the Model Router row gains: "dispatch usage is
  recorded on the live path via `RecordDispatch` as of task 161, so budgeted entries
  proactively exhaust and the router routes sideways (availability axis)."
- `docs/spec/behaviors.md` — the escalation/retry-policy entry (extended by task 160)
  gains a sentence distinguishing the quality axis (task 160) from the availability
  axis (this task).

## Out of scope

- `OnQuotaExhausted`/`OnRateLimit` — no current executor surfaces a machine-parseable
  quota/429 signal; wiring these without a real trigger would itself be a dead wire.
- `SaveState`/`LoadState` — task 162.
- Any change to the quality-axis escalation hook (task 160).

## Dependencies

- **Blocks on:** task 160 (extends its wiring point), task 093 (already merged).
- **Blocks:** task 162 (persists the `Usage`/`Availability` state this task now
  actually populates).
