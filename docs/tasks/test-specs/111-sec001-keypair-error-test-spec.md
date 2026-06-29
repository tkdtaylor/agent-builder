# Test spec — Task 111: SEC-001 — propagate the discarded `GenerateKeyPair()` error

**Linked task:** `docs/tasks/backlog/111-sec001-keypair-error.md`
**Written:** 2026-06-28
**Status:** ready
**Governing references:** ADR 053 Consequences §"SEC-001 (independent, noticed in passing)";
the task-099 security audit finding SEC-001 (recorded in `coverage-tracker.md` row 099 — the
Low non-blocking finding: discarded `envelope.GenerateKeyPair()` error in the in-process v1
seal path). **No new ADR required** — this is a hardening fix, not a design decision.

## Context

`newTransportDispatch` in `internal/cli/orchestrate_seams.go` generates two X25519 seal
keypairs for the in-process v1 worker wire and **discards both errors**:

```go
orchXPub, orchXPriv, _ := envelope.GenerateKeyPair()      // line ~41
workerXPub, workerXPriv, _ := envelope.GenerateKeyPair()  // line ~42
```

`envelope.GenerateKeyPair()` calls `box.GenerateKey(rand.Reader)` and returns
`([32]byte, [32]byte, error)`. If `crypto/rand` fails, the discarded error means both keypairs
silently become **zero `[32]byte` values**, and the dispatch path then seals every work-item
and result under a **zero (all-bytes-zero) seal key** — confidentiality + tamper-evidence
quietly degraded to nothing, with no signal. This is the task-099 audit's SEC-001 finding.

`newTransportDispatch` currently returns only `orchestrator.DispatchFunc` (no error). Its sole
caller is `assembleOrchestrate` in `internal/cli/orchestrate.go` (step 8), which **already has
an error return path** (`(orchestrateConfig, func(), error)`) and already calls `cleanup()` +
returns the error on every other failure in that function. The fix threads the keygen error up
through `newTransportDispatch` so a `crypto/rand` failure **fails fast at assembly time** —
before any goal is read — instead of producing a degenerate zero-key sealer.

## Requirements coverage

| Req ID      | Description                                                                                                                  | Test cases               |
|-------------|-----------------------------------------------------------------------------------------------------------------------------|--------------------------|
| REQ-111-01  | `newTransportDispatch` returns an error; a failed keypair generation propagates it (no zero-key dispatcher constructed)      | TC-111-01                |
| REQ-111-02  | `assembleOrchestrate` returns the propagated keygen error (and runs `cleanup()`); the goal-intake loop is never entered      | TC-111-02                |
| REQ-111-03  | The happy path is unchanged: keypair generation succeeding yields a working dispatcher; existing orchestrate tests stay green | TC-111-03                |

---

## Test cases

### TC-111-01 — a failed keypair generation propagates out of `newTransportDispatch` (L2)

- **Requirement:** REQ-111-01
- **Level:** L2 (unit, fault-injected keypair generation)

**Setup:** Make the keypair-generation call seam-injectable so a test can force it to fail.
The fix must introduce a seam — e.g. a package-level `var generateSealKeyPair = envelope.GenerateKeyPair`
(unexported, overridable in `_test.go`) — so the test can substitute a function returning
`([32]byte{}, [32]byte{}, errors.New("rand failure"))`. (Direct `crypto/rand` fault injection
is not portable; the seam is the testable, idiomatic equivalent and is the expected
implementation.)

**Input:** Override the seam to fail on the first (or each) call. Call `newTransportDispatch`
(with its now-error-returning signature) with a valid signing key, two non-nil ReplayCaches, a
nil/stub sink, and a discard logger.

**Expected output (assertions):**
- `newTransportDispatch` returns a non-nil error.
- The returned `orchestrator.DispatchFunc` is `nil` (no degenerate zero-key dispatcher is
  handed back).
- The error message identifies the keygen failure (contains the wrapped sentinel, e.g.
  `errors.Is(err, the-injected-error)` is `true`, or the message contains "generate seal
  keypair"/"keypair").
- No seal/sign of any envelope happens (the dispatcher is never built past the failed keygen).

---

### TC-111-02 — `assembleOrchestrate` propagates the error and never enters the loop (L2)

- **Requirement:** REQ-111-02
- **Level:** L2 (unit, fault-injected keypair generation through the assembler)

**Input:** With the keygen seam overridden to fail, call `assembleOrchestrate(config,
assembleOverrides{...})` configured so the live (non-override) `newTransportDispatch` path is
taken — i.e. `ov.dispatch == nil` (the override that would otherwise substitute a test
dispatch is left unset), and the SEC-003 signing-key check is satisfied (e.g. `ov.signingKey`
provided, so assembly reaches step 8 / the dispatch construction).

**Expected output (assertions):**
- `assembleOrchestrate` returns a non-nil error carrying the keygen failure
  (`errors.Is(err, the-injected-error)` true, or message names the keypair failure).
- The returned `orchestrateConfig` is the zero value (no partially-assembled orchestrator
  leaks out on the error path).
- The returned `cleanup` func is safe to call (non-nil) and any started policy daemon would be
  stopped — i.e. the error path runs `cleanup()` consistent with the other failure branches in
  `assembleOrchestrate`. (If the keygen happens before the policy daemon is started, assert
  `cleanup` is the `noop`; the load-bearing assertion is that assembly fails closed and
  `runGoalIntakeLoop` is never reached.)
- `runGoalIntakeLoop` is never called — `runOrchestrate` would return `ExitGeneric` and write
  the error to stderr (the SEC-003-style fail-closed-before-intake contract is preserved for
  this new failure mode too).

---

### TC-111-03 — happy path unchanged (L2)

- **Requirement:** REQ-111-03
- **Level:** L2 (unit; regression)

**Input:** With the keygen seam at its real default (`envelope.GenerateKeyPair`, succeeding),
call `newTransportDispatch` with valid inputs, then exercise the returned dispatcher over a
single sub-goal exactly as the existing task-099 dispatch tests do.

**Expected output (assertions):**
- `newTransportDispatch` returns a non-nil `DispatchFunc` and a `nil` error.
- The seal keys are non-zero (the dispatcher seals under real key material) — asserted
  indirectly by the existing round-trip succeeding (work-item seal → verify → result seal →
  verify all pass, no `ErrReplay`/`ErrBadSignature`).
- The full existing orchestrate assembly + dispatch test suite (task 099's TCs) still passes
  unchanged — the signature change is the only behavioral delta on the success path.

---

## Verification plan

- **Highest level achievable: L2 (unit).** This is internal assembly-path hardening with **no
  new runtime-observable surface**: the change adds an error return and threads it up; the only
  observable behavior is "assembly fails fast on a `crypto/rand` failure", which is not
  reproducible on a healthy host without fault injection. The fault-injection seam is the L2
  mechanism; L5/L6 are not reachable (and not required) because there is no new live behavior to
  observe beyond the assembly error path. The verify commit must state explicitly:
  **"unit-test-only; no runtime surface"** (per the coverage-tracker convention for
  internal-helper tasks).
- **L2 harness commands:**
  ```
  go test -count=1 ./internal/cli/...
  make check
  ```
  Expected: `ok …/internal/cli`; `All checks passed.`
- **L3 fitness commands (regression):**
  ```
  make fitness-orchestrator-no-executor
  make fitness-worker-transport-isolation
  ```
  Expected: `PASS …` for each — the fix does not move any boundary (it only adds an error
  return within `internal/cli`).

## Out of scope

- The out-of-process worker keypair model (the in-process v1 wire owns both halves today; a
  future out-of-process worker supplies its own keypair without changing this seam — ADR 048).
- Rotating or persisting the X25519 seal keys (they are per-assembly ephemeral by design).
- Any change to `envelope.GenerateKeyPair` itself (its signature already returns the error;
  this task stops discarding it).
- The planner wiring (tasks 109/110 — independent; this task shares only the file, not the
  change).
```
