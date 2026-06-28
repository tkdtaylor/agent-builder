# Test spec — Task 099: Orchestrator CLI wiring

**Linked task:** `docs/tasks/backlog/099-orchestrator-cli-wiring.md`
**Written:** 2026-06-28
**Status:** ready
**Governing ADRs:** ADR 042 (two-tier model, self-repo bright line), ADR 046 (Handle/Resume,
PlanStore, Planner seam), ADR 048 (worker transport, ReplayCache invariant), ADR 049
(NewPlanStoreFromEnv, degraded-mode fallback), ADR 050 (spawn-plan/spawn-worker, fleet audit).

## Context

Tasks 081/083/084/085/086 built the orchestrator, worker transport, memory-guard PlanStore,
policy/containment/fleet-audit, and concurrent dispatch — all verified to L2/L3. They
remain at 🟡 because the orchestrator is **not on the live binary path**: `cmd/agent-builder`
invokes `internal/cli`, which invokes `runtime.RunFromEnv` / `runtime.Run` — neither
references `internal/orchestrator`. This task adds an `orchestrate` subcommand to
`internal/cli` that assembles the full orchestrator stack from environment and drives the
`Orchestrator.Handle` / `Orchestrator.Resume` loop, making L5 (validation harness) and L6
(live operator observation of a 2-sub-goal plan) achievable.

### Assembly the wired subcommand must perform

1. `NewPlanStoreFromEnv` — returns a `MemoryGuardPlanStore` when
   `AGENT_BUILDER_MEMORY_GUARD_BIN` is set, else `MemoryPlanStore` + structured warning.
2. Worker transport: `worker.NewWorkItemSenderFromEnv` for each direction, sharing **one**
   `envelope.ReplayCache` per direction (not a fresh cache per dispatch).
3. `Orchestrator.New` with the assembled Planner (StructuredPlanner by default),
   PolicyClient, Reporter, PlanStore, audit.Sink, and base runtime.Config.
4. A goal-intake loop: read one goal per iteration from env or stdin (the validation
   harness feeds goals via stdin/env), call `Handle`, optionally call `Resume` on an
   approval token.

### Key security invariants carried through to this wiring layer

- **ReplayCache invariant (083 SEC-001):** one shared `*envelope.ReplayCache` per direction
  (work-item direction and result direction), created once at startup and passed to ALL
  dispatch calls, not freshly constructed per dispatch.
- **083 SEC-003 X25519 fail-closed startup:** `worker.LoadSigningKey` (called by
  `NewWorkItemSenderFromEnv`) MUST run before the subcommand accepts any goals. A missing or
  unreadable key exits non-zero with a named error before blocking for input.
- **Policy fail-closed (ADR 050):** `spawn-plan` fires once (pre-dispatch) and `spawn-worker`
  fires per sub-goal on the live path. deny-on-error.
- **Fleet audit chain (ADR 050 §4):** one `audit.Sink` shared between the orchestrator and all
  workers it dispatches.

## Requirements coverage

| Req ID      | Description                                                                                                | Test cases            |
|-------------|------------------------------------------------------------------------------------------------------------|-----------------------|
| REQ-099-01  | `orchestrate` subcommand registered in `internal/cli`; `agent-builder orchestrate -h` prints usage; `agent-builder orchestrate` drives Handle/Resume | TC-099-01, TC-099-06 |
| REQ-099-02  | Assembly: `NewPlanStoreFromEnv` selects MemoryGuardPlanStore or MemoryPlanStore; existing e2e unbroken | TC-099-02             |
| REQ-099-03  | ReplayCache invariant: one shared cache per direction; replayed nonce rejected on the wired path          | TC-099-03             |
| REQ-099-04  | Policy gates fire on live path: allow→dispatch, deny→skip+record, require_approval→pause                  | TC-099-04             |
| REQ-099-05  | Fleet audit chain: orchestrator + N worker events append to ONE sink; `audit-trail verify valid=true`     | TC-099-05             |
| REQ-099-06  | SEC-003 startup check: missing worker signing key exits before accepting goals                            | TC-099-06             |
| REQ-099-07  | docs/spec/ updated: configuration.md lists new env knobs; interfaces.md lists `orchestrate` subcommand   | TC-099-07             |
| REQ-099-08  | docs/architecture/diagrams.md updated: live runtime flow reaches the orchestrator                         | TC-099-08             |

---

## Test cases

### TC-099-01 — `orchestrate` subcommand registered; help prints usage (L2)

- **Requirement:** REQ-099-01
- **Level:** L2 (unit test on `internal/cli`)

**Input:** Call `cli.Main(cli.Config{Args: []string{"orchestrate", "-h"}, Stdout: &buf,
Stderr: &errbuf})`.

**Expected output (assertions):**
- Return value is `cli.ExitOK` (0).
- `buf.String()` contains `"orchestrate"` and a non-empty usage synopsis (the subcommand is
  registered and has a help path).
- `errbuf.String()` is empty (help goes to stdout, not stderr).

**Sub-case — unknown subcommand is still rejected:**
- `cli.Main(Config{Args: []string{"orchestrate-bad"}})` returns `cli.ExitUsage` (2).

---

### TC-099-02 — `NewPlanStoreFromEnv` selects the right backend; existing e2e unbroken (L2 + L5)

- **Requirement:** REQ-099-02
- **Level:** L2 (unit) + L5 (existing `TestPhase0EndToEndAcceptance` still passes)

**Input (L2, unset):** Call `orchestrator.NewPlanStoreFromEnv(logFn)` with
`AGENT_BUILDER_MEMORY_GUARD_BIN` unset.

**Expected output (assertions, L2 unset):**
- Returns a non-nil `PlanStore` whose dynamic type is `*orchestrator.MemoryPlanStore`
  (the in-memory fallback).
- `logFn` was called with a message containing `"memory-guard"` and `"degraded"` (the
  structured warning — REQ-084-04).

**Input (L2, set):** Set `AGENT_BUILDER_MEMORY_GUARD_BIN` to a temp path (the file need not
exist for this construction assertion). Call `NewPlanStoreFromEnv(logFn)`.

**Expected output (assertions, L2 set):**
- Returns a non-nil `PlanStore` whose dynamic type is `*orchestrator.MemoryGuardPlanStore`.
- `logFn` is NOT called with a degraded warning.

**Input (L5):** Run `go test -count=1 ./tests/e2e/...` (the existing Phase 0 end-to-end
acceptance test) with `AGENT_BUILDER_MEMORY_GUARD_BIN` unset.

**Expected output (L5):** All existing e2e tests pass unchanged — the new `orchestrate`
subcommand is additive and does not alter the `run` path.

---

### TC-099-03 — ReplayCache invariant: one shared cache per direction; replay rejected on the wired path (L2)

- **Requirement:** REQ-099-03
- **Level:** L2 (unit test injecting a shared cache into the CLI wiring)

**Design rationale:** the CLI assembly function (`assembleOrchestrator` or equivalent)
must be extractable and injectable for testing. Exposing a constructor that accepts
pre-built `ReceiverConfig` (including `ReplayCache`) allows this test to verify the shared
cache property without an integration harness.

**Input:** Construct the `orchestrate` assembly with an explicit `*envelope.ReplayCache`
injected via a test hook (or by calling the assembler function directly with config
overrides). Drive two goal round-trips through the assembly's dispatch path using the SAME
envelope nonce for the second goal's work-item (i.e. replay the first envelope).

**Expected output (assertions):**
- First dispatch: succeeds (work-item accepted).
- Second dispatch with the replayed nonce: returns an error satisfying
  `errors.Is(err, envelope.ErrReplay)` — the shared `ReplayCache` rejected the replay.
- The `ReplayCache` used is the SAME instance across both dispatches (assert by pointer
  identity or by verifying the nonce is already registered after the first dispatch).
- No sub-test may construct a fresh `ReplayCache` inside the dispatch loop — the assembly
  creates it once and reuses it.

**Violation sub-case:** if the assembly is refactored to construct a new `ReplayCache` per
dispatch, the same nonce is accepted twice (test must fail in that scenario — the test
itself validates the invariant).

---

### TC-099-04 — Policy gates fire on the live wired path: allow / deny / require_approval (L2)

- **Requirement:** REQ-099-04
- **Level:** L2 (stub policy client injected into the assembled orchestrator)

**Input:** Assemble an orchestrator via the `orchestrate` wiring with a stub `PolicyClient`
and a spy `DispatchFunc`. Feed a 2-sub-goal goal (`coding-agent: A`, `docs-fix: B`).

**Sub-case A — allow:**
- Stub returns `allow` for both `spawn-plan` and both `spawn-worker` decisions.
- Assertion: spy called twice, `PlanResult.Outcomes` has 2 entries both `Success==true`.

**Sub-case B — deny on `spawn-worker` for second sub-goal:**
- Stub returns `allow` for `spawn-plan`, `allow` for `coding-agent` `spawn-worker`, `deny`
  for `docs-fix` `spawn-worker`.
- Assertion: spy called exactly once (`coding-agent`), PlanResult has `docs-fix` outcome
  with `Success==false` and `Detail` containing `"policy"` or `"denied"`.

**Sub-case C — require_approval:**
- Stub returns `require_approval` for `spawn-plan`.
- After `Handle` returns: `orchestrator.HasPendingPlan(goalID)` is true; spy not called.
- Call `Resume` with a matching `Approval{Approved: true}`.
- Assertion: spy now called twice; `PlanResult` has 2 successful outcomes.

**Sub-case D — deny-on-error (fail-closed):**
- Stub returns an error for `spawn-plan`.
- Assertion: spy not called; `Handle` returns an error or a zero-dispatch result (deny
  path taken, not panicked).

---

### TC-099-05 — Fleet audit chain: orchestrator + N worker events in ONE sink; `audit-trail verify` valid (L2 + L5)

- **Requirement:** REQ-099-05
- **Level:** L2 (FakeSink coverage) + L5 (`audit-trail verify` real binary)

**Input (L2):** Assemble the orchestrator via the `orchestrate` wiring with a `FakeSink`
injected as the shared `audit.Sink`. Use a spy dispatch that appends two worker audit
events (`containment`, `finish`) per dispatched sub-goal to the SAME `FakeSink`. Feed a
2-sub-goal goal, policy `allow` for everything.

**Expected output (assertions, L2):**
- The single `FakeSink` contains, in order for a 2-worker run:
  1. `goal-intake` (1 event, orchestrator)
  2. `plan-decided` (1 event, orchestrator)
  3. `spawn-decided` × 2 (orchestrator, one per worker)
  4. `containment` × 2 + `finish` × 2 (worker events per sub-goal)
  5. `completion` (1 event, orchestrator)
- Total ≥ 9 events in the ONE sink instance (same pointer used by the orchestrator and the
  spy dispatch).
- `orchestrator spawn-decided` event for worker *i* appears before that worker's
  `containment` event in the chain.

**Expected output (assertions, L5):** When `AGENT_BUILDER_AUDIT_BIN` resolves a real
`audit-trail` binary: replay the 9-event sequence through a `BlockSink` to a temp logfile,
call `audit.VerifyChain`, assert `Valid == true`. When the binary is absent, `t.Skip` with
an explicit "L5 audit-trail binary not present — deferred" message. The L2 FakeSink
assertions still run.

---

### TC-099-06 — SEC-003 startup check: missing worker signing key exits before accepting goals (L2 + L5)

- **Requirement:** REQ-099-06
- **Level:** L2 (unit) + L5 (binary smoke test)

**Input (L2):** Call the `orchestrate` assembler function (or CLI dispatch path) with
`AGENT_BUILDER_WORKER_SIGNING_KEY` unset.

**Expected output (assertions, L2):**
- The assembler returns a non-nil error before any goal-intake loop begins.
- The error satisfies `errors.Is(err, worker.ErrMissingSigningKey)`.
- The error string contains `"AGENT_BUILDER_WORKER_SIGNING_KEY"`.
- No `supervisor.GoalSource.Next()` / `Orchestrator.Handle` call was made (the startup
  check fires unconditionally before goal intake).

**Input (L5):** Run `agent-builder orchestrate` in a subprocess with
`AGENT_BUILDER_WORKER_SIGNING_KEY` unset (all other required env vars set to stub values).

**Expected output (L5 subprocess):**
- Exit code is non-zero (not 0).
- `stderr` contains the error naming `AGENT_BUILDER_WORKER_SIGNING_KEY` and the
  `ErrMissingSigningKey` sentinel string.
- No goal-intake prompt or output appears on stdout before the failure.

---

### TC-099-07 — Spec docs updated: configuration.md and interfaces.md reflect new surface (L2 documentary)

- **Requirement:** REQ-099-07
- **Level:** L2 (file content assertions in a Go test)

**Input:** Read `docs/spec/configuration.md` and `docs/spec/interfaces.md` from the repo
root (use `os.ReadFile` in a test with `testdata` or repo-relative path via
`runtime.Caller`).

**Expected output (assertions):**
- `configuration.md` contains an entry for every new env variable the `orchestrate`
  subcommand introduces beyond those already documented (e.g. any
  `AGENT_BUILDER_ORCHESTRATE_*` var, or note that the existing vars are reused).
  Specifically, if no new vars are added, the test asserts that `configuration.md`
  already documents `AGENT_BUILDER_WORKER_SIGNING_KEY` and `AGENT_BUILDER_MEMORY_GUARD_BIN`
  (it does — this is a regression guard).
- `interfaces.md` CLI table row for `orchestrate` exists: contains the literal string
  `"orchestrate"` and a non-empty description column.

---

### TC-099-08 — diagrams.md updated: orchestrator appears in the live runtime flow (L2 documentary)

- **Requirement:** REQ-099-08
- **Level:** L2 (file content assertion)

**Input:** Read `docs/architecture/diagrams.md`.

**Expected output (assertions):**
- The file contains `"orchestrate"` (the subcommand name) OR `"Orchestrator"` as a
  component in the live flow diagram (not merely in an "out of scope" or "future" section).
- The file's **updated date** at the top is not before the date of this task's feat commit
  (guards against a stale diagram being carried forward without update).

---

## Verification plan

- **Highest level achievable:** L5 (validation harness exercises the wired `orchestrate`
  path end-to-end with stub executors — `go test ./tests/e2e/...` or a new harness test
  that drives the subcommand with a stub goal source and records PlanResult). L6 (operator
  runs `agent-builder orchestrate` with a real goal, two sub-goals, real policy and
  audit-trail binary) is achievable on the dev host post-merge.
- **L2 harness commands:**
  ```
  go test -count=1 ./internal/cli/... ./internal/orchestrator/... ./internal/channel/worker/...
  ```
  Expected: `ok` for all packages.
- **L3 fitness commands:**
  ```
  make fitness-orchestrator-no-executor
  make fitness-worker-transport-isolation
  make fitness-memoryguard-isolation
  make fitness-no-self-repo-sink
  ```
  Expected: all `PASS`.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L5 subprocess test (TC-099-06 and existing e2e):**
  ```
  go test -count=1 ./tests/e2e/...
  go test -count=1 -run TestOrchestrateSubcommandStartupKeyCheck ./internal/cli/...
  ```
- **L6 (operator-observed):** run `agent-builder orchestrate` against a real goal with two
  sub-goals; observe both sub-goals dispatched; observe audit-trail chain `valid=true` from
  `agent-builder verify-checkpoint` / `audit-trail verify`. Record in the verify commit.

## Security-sensitive surface

This task touches the ReplayCache wiring (083 SEC-001), the fail-closed startup check (083
SEC-003), the policy gates (ADR 050), and the self-repo bright line (ADR 050 §2). Both
**spec-verifier** and **security-auditor** roles must APPROVE before the feat commit is
promoted to ✅. The security-auditor must verify:
- The shared `ReplayCache` invariant holds (no fresh cache per dispatch path).
- The startup key check fires before any goal-intake loop.
- The policy gates remain fail-closed in the assembled path (deny on error, not allow).
- The self-repo bright line is not bypassed by the new wiring.

## Out of scope

- LLMPlanner implementation (task 100).
- Out-of-process worker transport (future A2A, ADR 048 §5).
- Key management / rotation for worker signing keys.
- The outbound Telegram `sendMessage` concrete (task 098 — already merged; this task wires
  the seam to the real CLI path but does not build a new outbound transport).
- Concurrent dispatch changes (task 086 — already merged; this task wires the existing
  concurrent orchestrator).
