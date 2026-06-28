# Task 093: Usage/quota tracking

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** completed

## Goal

Extend the router (task 092) with persistent, clock-seam-driven quota tracking:

1. **Usage tally + proactive budget check** — `RecordDispatch` increments `Usage`;
   `Select` pre-emptively skips over-budget entries (before sending a dispatch).
2. **Reactive exhaustion** — `OnRateLimit` marks an entry exhausted and sets `ResetAt`
   from `Retry-After` header or configured cooldown.
3. **Clock seam** — a `Clock` interface with `Now() time.Time`; the router is
   constructed with an injected clock for deterministic testing.
4. **Rolling-window recovery** — when `now > ResetAt`, the entry auto-recovers
   (`Usage` resets, `Status` → `AvailStatusAvailable`).
5. **File persistence** — `SaveState(path)` / `LoadState(path)` persist `Usage` and
   `Availability` as plain-text (JSON/TOML) across process restarts. Corrupted file
   → descriptive error (fail loud).

## Context

ADR 043 states that `Usage` and `Availability` must persist across dispatches and
across process restarts. For a single host-local run, a plain-text file is sufficient.
The memory-guarded store for the orchestrator is a noted forward-link (ADR 042's
write-gate + delete-verify), not built here.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                                           | Priority  |
|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-093-01 | `router.RecordDispatch(entryID)` increments `Usage` for the named entry. When `Usage >= Budget.Limit`, `Select` excludes the entry (proactive check) and marks it `AvailStatusExhausted` with `ResetAt = now + Budget.Window`. When `now > ResetAt`, the entry auto-recovers and `Usage` resets to 0. | must have |
| REQ-093-02 | `router.OnRateLimit(entryID, retryAfterHeader string)` marks the entry `AvailStatusExhausted`; `ResetAt = now + parsedRetryAfter` when the header is present, else `now + configuredCooldown`. | must have |
| REQ-093-03 | The router accepts an injected `Clock` interface with `Now() time.Time`; the production clock calls `time.Now()`; tests inject a `FakeClock` that advances programmatically. No `time.Sleep` in tests. | must have |
| REQ-093-04 | `router.SaveState(path string) error` persists `Usage` and `Availability` for all entries as plain text (JSON or TOML). `router.LoadState(path string) error` restores state; a corrupted file returns a descriptive error (not a silent zero value). | must have |
| REQ-093-05 | A local entry (`Budget.Limit == 0`) is never marked exhausted by `RecordDispatch` or `OnRateLimit`. The availability-axis fallback test (TC-093-06) shows the local entry is the always-available fallback when all cloud entries are exhausted. | must have |

## Readiness gate

- [x] Test spec `093-usage-quota-tracking-test-spec.md` exists (written first)
- [x] Task 092 merged (router with in-memory selection + escalation logic)
- [x] `make check` green before starting

## Acceptance criteria

- [x] [REQ-093-01] TC-093-01: Usage tally increments; proactive budget check excludes over-budget entry
- [x] [REQ-093-01] TC-093-02: clock advanced past ResetAt → entry auto-recovers; Usage resets; no sleep
- [x] [REQ-093-02] TC-093-03: OnRateLimit with Retry-After header → ResetAt from header; missing header → configuredCooldown
- [x] [REQ-093-03] TC-093-04: injected FakeClock controls now(); entry excluded then re-eligible on clock advance; no time.Sleep
- [x] [REQ-093-04] TC-093-05: SaveState + LoadState round-trip preserves exhausted state; corrupted file → descriptive error
- [x] [REQ-093-05] TC-093-06: exhausted cloud entry → local entry selected as always-available fallback

## Verification plan

- **Highest level achievable:** L3 — no runtime-observable surface for quota tracking
  alone. Unit tests with the injected clock seam.
- **Harness command:**
  ```
  go test -count=1 ./internal/router/...
  make check
  ```
  Expected:
  - Unit tests → `ok github.com/tkdtaylor/agent-builder/internal/router`
  - `make check` → `All checks passed.`

## Out of scope

- Memory-guarded store for the orchestrator (forward-link per ADR 043 Consequences).
- Per-entry clock configuration.
- The 429 signal path from the executor subprocess to the router
  (the executor adapter calls `router.OnRateLimit` — wired in task 095).

## Dependencies

- Task 092 (router with in-memory selection + escalation).
- Informs: task 095 (full wiring — the router with persistence replaces the stub
  resolver in `internal/runtime`).
