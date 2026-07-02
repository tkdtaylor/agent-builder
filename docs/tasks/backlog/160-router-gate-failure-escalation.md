# Task 160: wire `Router.OnGateFailure` into the live retry/escalation hook

**Project:** agent-builder
**Created:** 2026-07-02
**Status:** backlog

## Goal

Make a gate-failing attempt inside a task's bounded retry loop actually climb the
capability ladder — replacing the constant `agentloop.BootstrapEscalationHook` (which
always returns the SAME executor unchanged) with a router-backed hook on the live
`run`/`orchestrate` dispatch path, wiring `router.Router.OnGateFailure`/`Select` (which
already implement the ladder-climbing logic correctly, task 093) into the one place
that currently ignores them.

## Context

**Root cause (full-project review, verified 2026-07-02):**
`resolveExecutor` (`internal/runtime/run.go:445-462`) constructs a fresh
`router.New(catalog)`, resolves ONE executor via `r.Select(...)`, and discards the
router — it is referenced nowhere else. `Run` (`internal/runtime/run.go:616`) then
always constructs the retry policy with `agentloop.BootstrapEscalationHook`
(`internal/loop/retry_policy.go:38-41`), which unconditionally returns
`request.CurrentExecutor` unchanged. So every gate-failing attempt inside
`RetryingLoop.RunOnce`'s bounded retry loop
(`internal/loop/retry_policy.go:140-186`) retries with the IDENTICAL executor up to
`MaxAttempts`, then escalates straight to `needs-human` — NEVER climbing the quality
ladder `router.Router.OnGateFailure`/`Select` already implement (task 093,
`internal/router/router.go:195-238`): `OnGateFailure(entryID)` marks an entry
tried-and-failed for the current dispatch, and the next `Select` naturally returns the
next-cheapest eligible entry that hasn't failed — climbing exactly as ADR 043 designs.
`RecordDispatch`/`OnQuotaExhausted`/`OnRateLimit`/`SaveState`/`LoadState` have zero
non-test call sites anywhere (confirmed via `grep`); this task wires the highest-
severity of the five (a gate-failing task never improving its odds across retries).

**The fix:** `resolveExecutor` returns the `*router.Router` it constructs (not just
the initial executor/entry). `Run` builds a real `agentloop.EscalationHook` closure
— replacing `BootstrapEscalationHook` on the production dispatch path — that on each
gate failure calls `router.OnGateFailure(currentEntryID)`, then `router.Select(spec)`
again; if a next eligible entry is found, it constructs that entry's concrete
executor via the existing `buildExecutorForEntry` and returns it (tracking the new
`currentEntryID` in its own closure state — the closure's OWN tracked ID, not derived
from the opaque `supervisor.Executor` interface value, stays correctly in sync because
`RetryingLoop.RunOnce` only ever sets `currentExecutor` from a previous call to this
same hook); if `Select` returns `router.ErrNoEligibleExecutor` (every eligible entry
already gate-failed), the hook returns the CURRENT executor unchanged — graceful
degradation, matching `BootstrapEscalationHook`'s behavior for the remaining attempts,
never a hard unhandled error out of `RunOnce`.

**Reference:**
- `internal/runtime/run.go:397-524` (`buildCatalog`, `resolveExecutor`,
  `buildExecutorForEntry`)
- `internal/runtime/run.go:562-629` (`Run`, the `agentloop.NewRetryPolicy` construction site)
- `internal/loop/retry_policy.go` (`EscalationHook`, `EscalationRequest`,
  `BootstrapEscalationHook`, `RetryingLoop.RunOnce`)
- `internal/router/router.go:195-238` (`OnGateFailure`, `Select`, `ResetEscalation` —
  already correct, already unit-tested; this task is pure wiring)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-160-01 | `resolveExecutor` returns the `*router.Router` it constructs, alongside the existing executor/entry/error. | must have |
| REQ-160-02 | `Run` wires a router-backed `EscalationHook` closure in place of `agentloop.BootstrapEscalationHook` on the live `run`/`orchestrate` dispatch path. | must have |
| REQ-160-03 | A gate failure calls `router.OnGateFailure(currentEntryID)`, and the next attempt uses the concrete executor for the router's next-cheapest eligible entry — proven with a multi-entry catalog where a cheap entry deterministically fails and a stronger entry deterministically passes. | must have |
| REQ-160-04 | When every eligible entry has already gate-failed, the hook returns the current executor unchanged (graceful degradation), never a hard error out of `RetryingLoop.RunOnce`. | must have |
| REQ-160-05 | Escalation state (`router.escalated`) is scoped to one task's retry loop — a second, independent dispatch starts with a clean slate. | must have |
| REQ-160-06 | `agentloop.BootstrapEscalationHook` remains exported, unchanged, and independently usable by any caller not wanting router-driven escalation. | must have |
| REQ-160-07 | `make fitness-orchestrator-no-executor` and `make fitness-llm-planner-no-executor` remain PASS. | must have |
| REQ-160-08 | The single-entry (synthetic default Claude entry, zero-registry) deployment path is behaviorally unaffected. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/160-router-gate-failure-escalation-test-spec.md` exists (written first)
- [x] Task 093 merged (`Router.OnGateFailure`/`Select`/escalation-set semantics)
- [x] Task 155 merged (lands first to avoid `internal/runtime/run.go` merge conflicts;
  unaffected functionally by this task's closure)
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [ ] [REQ-160-01] TC-160-01: `resolveExecutor` returns the actual constructed `*router.Router`.
- [ ] [REQ-160-02] TC-160-02: the live dispatch path visibly escalates across a gate failure (cheap fails → stronger succeeds) — impossible under the old constant hook.
- [ ] [REQ-160-03] TC-160-03: a three-entry ladder climbs monotonically cheap → mid → strong across attempts.
- [ ] [REQ-160-04] TC-160-04: exhausted escalation degrades gracefully to `needs-human` after `MaxAttempts`, not a hard `RunOnce` error.
- [ ] [REQ-160-05] TC-160-05: escalation state does not leak across independent task dispatches.
- [ ] [REQ-160-06] TC-160-06: `BootstrapEscalationHook` is unchanged and independently testable.
- [ ] [REQ-160-07] TC-160-07: `make fitness-orchestrator-no-executor` and `make fitness-llm-planner-no-executor` both PASS.
- [ ] [REQ-160-08] TC-160-08: the zero-registry single-provider path is unaffected.

## Verification plan

- **Highest level achievable:** L5 — a fake multi-entry-catalog harness spanning the
  real `Run`/`resolveExecutor`/retry-policy/`RetryingLoop` chain proves gate-failure
  escalation is genuinely live. No live Claude/Codex/Ollama subprocess is required.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/runtime/... ./internal/loop/... ./tests/loop/...
  ```
- **L3 fitness commands:**
  ```
  make fitness-orchestrator-no-executor
  make fitness-llm-planner-no-executor
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/interfaces.md` — the `resolveExecutor`/router description (grep
  "resolve executor for routing spec") updates to note the returned `*router.Router`
  is now retained and used to drive gate-failure escalation across retry attempts.
- `docs/spec/behaviors.md` — the escalation/retry-policy behavior entry gains: "a
  gate-failing attempt escalates via the router's quality axis (`OnGateFailure` +
  `Select`) to the next-stronger eligible executor, not a blind retry of the same
  executor (task 160)."
- `docs/spec/architecture.md` — the Model Router row's "In-memory state only" note
  gains: "wired into the live retry/escalation hook as of task 160" (persistence
  remains task 162's scope — do not overclaim it here).

## Out of scope

- `RecordDispatch` — task 161.
- `OnQuotaExhausted`/`OnRateLimit` — no executor currently surfaces a machine-parseable
  rate-limit signal; flagged as future work, not this batch.
- `SaveState`/`LoadState` — task 162.
- Any change to `Executor.Run`'s signature (tasks 155-156).

## Dependencies

- **Blocks on:** task 093 (already merged), task 155 (must land first — same
  `internal/runtime/run.go` file, per the review's explicit ordering note).
- **Blocks:** task 161 (builds on this task's retained `*router.Router` reference).
