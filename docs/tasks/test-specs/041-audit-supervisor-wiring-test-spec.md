# Test Spec 041: Wire audit.Sink into the supervisor's action events

**Linked task:** [`docs/tasks/backlog/041-audit-supervisor-wiring.md`](../backlog/041-audit-supervisor-wiring.md)
**Written:** 2026-06-16
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-041-01 | TC-041-01, TC-041-02 | ⏳ |
| REQ-041-02 | TC-041-03 | ⏳ |
| REQ-041-03 | TC-041-04 | ⏳ |
| REQ-041-04 | TC-041-05 | ⏳ |
| REQ-041-05 | TC-041-06 | ⏳ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

## Test cases

### TC-041-01: supervisor projects every action-class lifecycle event through the Sink

- **Requirement:** REQ-041-01
- **Input:** a `Supervisor` (or the runtime in-box loop) configured with a `FakeSink` (task 038) for a run that selects one task, attempts it, passes the gate, and publishes a branch.
- **Expected output:** `FakeSink.Events()` contains one typed `AuditEvent` per action-class lifecycle event, in order: `containment`, `pick`, `attempt`, `verify` (verdict `pass`), `publish`, `finish` (outcome `completed`). The verify event's verdict and the finish event's outcome match the run result.
- **Edge cases:** an escalated run emits `attempt`(s) + `escalate` + `finish` (outcome `failed`) and no `publish`.

### TC-041-02: raw stdout/stderr stay in the 019 RunRecord, NOT in the Sink

- **Requirement:** REQ-041-01
- **Input:** the same run as TC-041-01 with both a RunRecord and a `FakeSink` attached.
- **Expected output:** the `FakeSink` receives only typed action events — no event carries raw stdout/stderr payload bytes. The existing RunRecord still contains the `stdout`/`stderr`/`command` stream lines, unchanged from task 019. The two artifacts are distinct (action chain vs raw stream) and the RunRecord assertions from task 019/028 still pass.
- **Edge cases:** when no `Sink` is configured, the supervisor behaves exactly as before (RunRecord-only); the Sink is optional, mirroring the optional RunRecord path.

### TC-041-03: the Sink is Sealed before containment teardown

- **Requirement:** REQ-041-02
- **Input:** a run with a `FakeSink`; the box teardown order is observed.
- **Expected output:** `Seal()` is called on the Sink before the containment box is torn down (mirrors the RunRecord close-before-teardown durability rule). `FakeSink.Sealed()` is true after the run, and the seal happens before the teardown hook fires.
- **Edge cases:** on a failed/escalated run the Sink is still Sealed (the finish event + seal are written on the failure path too).

### TC-041-04: BlockSink is wired into internal/runtime behind AGENT_BUILDER_AUDIT_RECORD

- **Requirement:** REQ-041-03
- **Input:** `runtime.ConfigFromEnv` with `AGENT_BUILDER_AUDIT_RECORD` set to a path and a resolvable `audit-trail` binary (`AGENT_BUILDER_AUDIT_BIN` or `$PATH`); a run through the default wiring with a fake launcher / fake executor / gate-passing worktree.
- **Expected output:** the run produces the block's chain file at that path via the `audit.BlockSink` adapter (one `audit-trail emit` per action event); the file is the block's non-empty chain carrying the action events in order. When `AGENT_BUILDER_AUDIT_RECORD` is blank/absent, no chain file is written and the run still completes (mirrors `AGENT_BUILDER_RUN_RECORD`).
- **Edge cases:** an unresolvable `audit-trail` binary or an unwritable audit path fails the run with a clear configuration error before dispatch, not silently — auditing is never skipped when configured.

### TC-041-05: the produced run's chain verifies (block-severity end-to-end)

- **Requirement:** REQ-041-04
- **Input:** the chain file produced by TC-041-04, passed to `VerifyChain` (task 040 — the block's `audit-trail verify`).
- **Expected output:** the block reports `Valid == true` for the freshly produced chain — a real run yields a valid, verifiable audit chain. (Consistent with ADR 026 decision 3: verify over a produced chain is block-severity.)
- **Edge cases:** the e2e harness asserts the action sequence in the chain matches the run (pick → attempt → verify → publish → finish), not just that it verifies. (CI-without-binary: the e2e gates the real-binary path behind an opt-in and otherwise asserts the emitted `audit-trail emit` argv per action via a recorded-exec stub.)

### TC-041-06: wiring does not widen the F-003 supervisor isolation boundary

- **Requirement:** REQ-041-05
- **Input:** `go list -deps ./internal/supervisor/...` after the wiring lands.
- **Expected output:** the supervisor's transitive import graph gains `internal/audit` (a leaf) but no executor/LLM/web package; `make fitness-supervisor-isolation` still passes. `internal/audit` imports nothing from executor/LLM/web (the precondition the task-042 F-005 check will assert).
- **Edge cases:** if `internal/audit` accidentally pulled an executor/LLM/web dep transitively, both `fitness-supervisor-isolation` (existing) and `fitness-audit-isolation` (task 042) would fail — this TC is the existing-boundary guard; task 042 adds the new dedicated guard.

## Post-implementation verification

- [ ] All test cases above pass
- [ ] No regressions in task 019/028/037 RunRecord and e2e assertions
- [ ] L5 e2e shows a run produces a chain the block's `verify` reports valid (or records the recorded-exec fallback + opt-in real-binary command)

## Test framework notes

Framework: Go `testing`. Supervisor-level tests use `FakeSink` (task 038) to assert the typed action projection without subprocesses. The runtime/e2e test sets `AGENT_BUILDER_AUDIT_RECORD` (+ `AGENT_BUILDER_AUDIT_BIN`), drives the real wiring with the existing fake launcher/executor/gate pattern (as in tests/e2e for task 037), then runs `VerifyChain` (the block's `audit-trail verify`) over the produced file — or, without the binary in CI, asserts the per-action `audit-trail emit` argv via a recorded-exec stub and gates the real-binary path behind an opt-in. The F-003 guard is `go list -deps` + the existing `make fitness-supervisor-isolation`; the new dedicated `fitness-audit-isolation` (also covering the `audit-trail` block-package token) is task 042.
