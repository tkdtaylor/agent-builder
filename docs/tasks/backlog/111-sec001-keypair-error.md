# Task 111: SEC-001 — propagate the discarded `GenerateKeyPair()` error

**Project:** agent-builder
**Created:** 2026-06-28
**Status:** backlog

## Goal

Stop discarding the error from the two `envelope.GenerateKeyPair()` calls in
`newTransportDispatch` (`internal/cli/orchestrate_seams.go`). Thread the error up through
`newTransportDispatch` and `assembleOrchestrate` so a `crypto/rand` failure fails fast at
orchestrate-assembly time instead of silently sealing every work-item and result under a
zero-value seal key.

## Context

This is the task-099 security audit finding **SEC-001** (recorded as the Low, non-blocking
finding in `coverage-tracker.md` row 099, and noted in passing by ADR 053's Consequences §).
It is small, independent of the LLM-planner wiring (tasks 109/110), and needs **no ADR** — it
is a hardening fix, not a design decision. It happens to live in the same file the orchestrate
path owns (`orchestrate_seams.go`), which is why ADR 053 flagged it alongside the planner work.

### The defect

`newTransportDispatch` generates two X25519 seal keypairs for the in-process v1 worker wire
(ADR 048) and discards both errors:

```go
orchXPub, orchXPriv, _ := envelope.GenerateKeyPair()      // ~line 41
workerXPub, workerXPriv, _ := envelope.GenerateKeyPair()  // ~line 42
```

`envelope.GenerateKeyPair()` returns `([32]byte, [32]byte, error)` and only errors when
`box.GenerateKey(rand.Reader)` fails (a `crypto/rand` failure). On that path the discarded
error leaves both keypairs as **zero `[32]byte`** values, and the dispatcher then seals every
work-item and result under an all-zero seal key — confidentiality and tamper-evidence silently
degraded, with no signal. Fail-fast-and-crash-loudly (AGENTS.md design principle) demands this
surface as an assembly error.

### The fix

`newTransportDispatch` currently returns only `orchestrator.DispatchFunc`. Change it to return
`(orchestrator.DispatchFunc, error)` and propagate the keygen error. Its sole caller,
`assembleOrchestrate` (`internal/cli/orchestrate.go` step 8), **already** returns
`(orchestrateConfig, func(), error)` and already runs `cleanup()` + returns on every other
failure in that function — so threading one more error is a local change with an existing
landing site. On the error path, `assembleOrchestrate` returns the zero `orchestrateConfig`,
runs `cleanup()` (consistent with its other branches), and `runOrchestrate` exits non-zero
before the goal-intake loop is ever entered — the same fail-closed-before-intake shape as the
SEC-003 startup key check.

To make the failure testable without non-portable `crypto/rand` fault injection, introduce an
unexported, overridable seam — e.g. `var generateSealKeyPair = envelope.GenerateKeyPair` — that
a `_test.go` can substitute with a function returning an error.

## Requirements

| Req ID      | Description                                                                                                                  | Priority   |
|-------------|-----------------------------------------------------------------------------------------------------------------------------|------------|
| REQ-111-01  | `newTransportDispatch` returns `(DispatchFunc, error)`; a keypair-generation failure propagates (nil dispatcher, non-nil err) | must have |
| REQ-111-02  | `assembleOrchestrate` propagates the keygen error, runs `cleanup()`, returns the zero config; the goal loop is never entered  | must have  |
| REQ-111-03  | Happy path unchanged: successful keygen yields a working dispatcher; existing task-099 orchestrate/dispatch tests stay green   | must have  |

## Readiness gate

- [x] Task 099 merged (`newTransportDispatch`, `assembleOrchestrate`, the orchestrate assembly path, and the SEC-001 audit finding it carries)
- [x] Task 096 merged (`internal/envelope` `GenerateKeyPair` + the seal/sign transport)
- [x] SEC-001 finding from the task-099 security audit read (coverage-tracker row 099)

## Acceptance criteria

- [ ] [REQ-111-01] TC-111-01: fault-injected keygen seam → `newTransportDispatch` returns nil `DispatchFunc` + non-nil error (`errors.Is` the injected error / message names the keypair failure); no envelope sealed
- [ ] [REQ-111-02] TC-111-02: with the live dispatch path (`ov.dispatch==nil`) and SEC-003 satisfied, `assembleOrchestrate` returns the keygen error + zero config; `cleanup` non-nil; `runGoalIntakeLoop` never reached
- [ ] [REQ-111-03] TC-111-03: real keygen → non-nil dispatcher + nil error; existing task-099 round-trip (seal→verify→result→verify) passes with non-zero keys; full `internal/cli` suite green

## Verification plan

- **Highest level achievable: L2 (unit) — no new runtime surface.** This is internal
  assembly-path hardening: it adds an error return and threads it up. The only observable
  behavior is "assembly fails fast on a `crypto/rand` failure", reproducible only via the
  fault-injection seam, not on a healthy host. L5/L6 are not reachable and **not required** —
  there is no new live behavior to observe beyond the assembly error path. The verify commit
  must say explicitly **"unit-test-only; no runtime surface"** (coverage-tracker convention for
  internal-helper tasks).
- **L2 harness commands:**
  ```
  go test -count=1 ./internal/cli/...
  make check
  ```
  Expected: `ok …/internal/cli`; `All checks passed.`
- **L3 fitness commands (regression — no boundary moves):**
  ```
  make fitness-orchestrator-no-executor
  make fitness-worker-transport-isolation
  ```
  Expected: `PASS …` for each.

## Modules touched

- `internal/cli` (`orchestrate_seams.go` — `newTransportDispatch` signature + keygen seam +
  error propagation; `orchestrate.go` — the one caller threads the new error through the
  existing return path).

(One module. No spec/diagram change — the seal-key generation is an internal implementation
detail of the in-process v1 wire, not externally-visible behavior, the data model, an
interface, or configuration. Within the one-task / at-most-two-modules rule.)

## Out of scope

- The out-of-process worker keypair model (in-process v1 owns both halves; a future
  out-of-process worker supplies its own keypair without changing this seam — ADR 048).
- Rotating or persisting the ephemeral X25519 seal keys.
- Any change to `envelope.GenerateKeyPair` (its signature already returns the error).
- The LLM-planner wiring (tasks 109/110) — independent; this task only shares the file.

## Dependencies

- **Independent — no dependency on task 109 or 110** (and they do not depend on it). It may be
  worked in parallel on its own branch.
- **Ordering: prefer landing this task BEFORE task 112 (ADR 054 §Existing-task-updates).** No
  functional dependency, but 112 (the async control-plane core) restructures
  `orchestrate_seams.go`/`orchestrate.go` — the same files this task edits. Getting this small,
  self-contained fail-fast keygen fix in before that churn avoids a merge collision. No scope
  change to this task.
- Task 099 (orchestrate assembly + the SEC-001 finding) — merged.
- Task 096 (`internal/envelope` transport) — merged.
```
