# Task 099: Orchestrator CLI wiring

**Project:** agent-builder
**Created:** 2026-06-28
**Status:** backlog

## Goal

Wire the Tier-1 orchestrator onto the live binary path by adding an `orchestrate`
subcommand to `internal/cli`, assembling the full orchestrator stack from environment
configuration, and enabling tasks 081/083/084/085/086 to be promoted from L2/L3 to L5/L6.

## Context

Tasks 081/083/084/085/086 built `internal/orchestrator` (Handle/Resume, PlanStore, Planner),
`internal/channel/worker` (transport, ReplayCache, key material), `internal/memoryguard`
(write-gate adapter), containment/policy/fleet-audit, and concurrent dispatch. All are merged
and verified to L2/L3. They remain 🟡 because the orchestrator is **not on the live binary
path**: `cmd/agent-builder` → `internal/cli` → `runtime.RunFromEnv` does not reference
`internal/orchestrator`. This task makes the orchestrator reachable via
`agent-builder orchestrate`, enabling L5 validation-harness runs and L6 operator observation.

### Chosen approach: new `orchestrate` subcommand (not extending `run`)

`run` is the Phase 0 single-task supervisor loop (`runtime.RunFromEnv`). The orchestrator is
the Tier-1 multi-goal coordinator — a structurally distinct role with its own assembly
(PlanStore, Planner, transport, fleet audit). A new subcommand keeps the two roles cleanly
separated (`run` = one worker dispatch; `orchestrate` = goal-intake → plan → N workers) and
avoids entangling the existing `run` path, which must stay stable for existing e2e tests.
This mirrors how `verify` and `verify-checkpoint` are separate subcommands rather than flags
on `run`.

### Assembly the `orchestrate` subcommand performs

1. **PlanStore:** `orchestrator.NewPlanStoreFromEnv(logFn)` — returns a
   `MemoryGuardPlanStore` when `AGENT_BUILDER_MEMORY_GUARD_BIN` is set, else `MemoryPlanStore`
   with a structured warning (ADR 049 §3; REQ-084-04).
2. **Worker transport:** `worker.NewWorkItemSenderFromEnv` for each key direction, creating
   **one** `*envelope.ReplayCache` per direction at startup and sharing it across all
   dispatches (083 SEC-001 invariant — a fresh cache per dispatch would defeat replay
   rejection). Key-material check fires here, before any goal intake.
3. **Planner:** `orchestrator.NewStructuredPlanner()` by default; selectable via
   `AGENT_BUILDER_PLANNER` env var (`"structured"` or `"llm"` once task 100 is merged).
4. **PolicyClient:** constructed from `AGENT_BUILDER_POLICY_BIN` / `AGENT_BUILDER_POLICY_SOCKET`
   (same env vars as the `run` path).
5. **AuditSink:** one `audit.Sink` constructed from `AGENT_BUILDER_AUDIT_RECORD` +
   `AGENT_BUILDER_AUDIT_BIN` (same env vars as the `run` path), shared across the
   orchestrator and all dispatched workers via `WithAuditSink` (ADR 050 §4).
6. **Reporter:** `supervisor.Reporter` wired from the outbound channel (task 098); falls
   back to a log-to-stderr reporter when the outbound channel is not configured.
7. **Orchestrator:** `orchestrator.New(planner, pol, reporter, baseConfig, WithPlanStore(s),
   WithAuditSink(sink))`.
8. **Goal-intake loop:** reads goals from a `supervisor.GoalSource` (the configured inbound
   channel or, for the validation harness, from stdin / env), calls `Handle`, calls `Resume`
   on approval tokens.

### Security invariants that must hold in the assembled path

- **ReplayCache invariant (083 SEC-001):** one shared `*envelope.ReplayCache` per direction
  (work-item direction, result direction), created once at startup and passed to ALL dispatch
  calls — never freshly constructed per dispatch.
- **SEC-003 fail-closed startup:** `worker.LoadSigningKey` runs before the goal-intake loop
  begins. Missing or unreadable key material exits non-zero with `errors.Is(err,
  worker.ErrMissingSigningKey)` and names `AGENT_BUILDER_WORKER_SIGNING_KEY`.
- **Policy fail-closed (ADR 050):** `spawn-plan` fires once (pre-dispatch, in `Handle`) and
  `spawn-worker` fires per sub-goal (in `dispatchPlan`). On any transport/parse error the
  policy client returns `deny` (existing behaviour, preserved in the assembled path).
- **Fleet audit chain (ADR 050 §4):** the one `audit.Sink` is shared between the
  orchestrator (`WithAuditSink`) and the `DispatchFunc` that invokes `runtime.Run` — so
  worker events and orchestrator events append to the same chain.
- **Self-repo bright line (ADR 050 §2/§5):** the `spawn-worker` deny for `TargetRepo ==
  OwnRepo` already lives in `orchestrator.decideSpawnWorker`; the new wiring does not bypass it.

## Requirements

| Req ID      | Description                                                                                                              | Priority   |
|-------------|--------------------------------------------------------------------------------------------------------------------------|------------|
| REQ-099-01  | `orchestrate` subcommand registered in `internal/cli`; `agent-builder orchestrate -h` prints usage; the subcommand drives Handle/Resume | must have |
| REQ-099-02  | Assembly calls `NewPlanStoreFromEnv`; selects MemoryGuardPlanStore or MemoryPlanStore per env; existing `run` e2e tests pass unchanged | must have |
| REQ-099-03  | One shared `*envelope.ReplayCache` per direction at startup; replayed nonce rejected on the wired path                  | must have  |
| REQ-099-04  | Policy gates (`spawn-plan`, `spawn-worker`) fire on the live assembled path; allow→dispatch, deny→skip+record, require_approval→pause; fail-closed on error | must have |
| REQ-099-05  | Orchestrator + worker events append to ONE `audit.Sink`; FakeSink coverage at L2; `audit-trail verify valid=true` at L5 | must have  |
| REQ-099-06  | SEC-003: missing `AGENT_BUILDER_WORKER_SIGNING_KEY` at startup causes exit before goal intake; named error naming the missing var | must have |
| REQ-099-07  | `docs/spec/configuration.md` lists new env knobs (if any); `docs/spec/interfaces.md` CLI table includes `orchestrate`  | must have  |
| REQ-099-08  | `docs/architecture/diagrams.md` updated to show the live runtime flow reaching the orchestrator                         | must have  |

## Readiness gate

- [x] Task 081 merged (orchestrator core — Handle/Resume, StructuredPlanner, PlanStore)
- [x] Task 083 merged (worker transport — Sender/Receiver, ReplayCache, ErrMissingSigningKey)
- [x] Task 084 merged (memory-guard PlanStore — NewPlanStoreFromEnv, MemoryGuardPlanStore)
- [x] Task 085 merged (policy gating + fleet audit chain — WithAuditSink, spawn-worker gate)
- [x] Task 086 merged (concurrent dispatch — dispatchPlan goroutine model)
- [x] Task 098 merged (outbound Reporter seam — wired to the orchestrator)

## Acceptance criteria

- [ ] [REQ-099-01] TC-099-01: `cli.Main(Args{"orchestrate", "-h"})` → ExitOK + non-empty usage
- [ ] [REQ-099-02] TC-099-02: `NewPlanStoreFromEnv` with MEMORY_GUARD_BIN unset → MemoryPlanStore + warning; set → MemoryGuardPlanStore; existing e2e pass
- [ ] [REQ-099-03] TC-099-03: shared ReplayCache rejects replayed nonce on the wired path; no fresh cache per dispatch
- [ ] [REQ-099-04] TC-099-04: all three policy decision paths (allow/deny/require_approval/error) produce correct orchestrator behaviour on the assembled path
- [ ] [REQ-099-05] TC-099-05: L2 FakeSink has ≥9 events in correct order; L5 `audit-trail verify` → valid=true when binary present
- [ ] [REQ-099-06] TC-099-06: missing AGENT_BUILDER_WORKER_SIGNING_KEY → non-zero exit with named error before goal intake, both L2 and L5 subprocess
- [ ] [REQ-099-07] TC-099-07: `docs/spec/configuration.md` and `docs/spec/interfaces.md` contain the expected strings for the `orchestrate` subcommand
- [ ] [REQ-099-08] TC-099-08: `docs/architecture/diagrams.md` updated date is current and contains "orchestrate" or "Orchestrator" in the live flow section

## Verification plan

- **Highest level achievable:** L5 (validation harness drives `agent-builder orchestrate`
  with stub executors and records PlanResult) + L6 (operator observes live 2-sub-goal plan
  dispatch on dev host, records `audit-trail verify valid=true` output in verify commit).
- **L2 harness commands:**
  ```
  go test -count=1 ./internal/cli/... ./internal/orchestrator/... ./internal/channel/worker/...
  ```
- **L3 fitness commands:**
  ```
  make fitness-orchestrator-no-executor
  make fitness-worker-transport-isolation
  make fitness-memoryguard-isolation
  make fitness-no-self-repo-sink
  make check
  ```
- **L5 subprocess:**
  ```
  go test -count=1 -run TestOrchestrateSubcommandStartupKeyCheck ./internal/cli/...
  go test -count=1 ./tests/e2e/...
  ```
- **L6 (operator-run):** `agent-builder orchestrate` with a real 2-sub-goal goal; observe
  both sub-goals dispatched; record `audit-trail verify valid=true` output in verify commit.

## Security review requirements

This task is security-sensitive: it wires the ReplayCache, fail-closed startup, policy gates,
and fleet audit chain onto the live binary path. **Both spec-verifier and security-auditor
must APPROVE before the feat commit is promoted to ✅.** The security-auditor must verify:
- The shared `ReplayCache` invariant holds (no fresh cache per dispatch).
- The SEC-003 startup key check fires before any goal-intake loop.
- Policy gates remain fail-closed in the assembled path.
- The self-repo bright line is not bypassed by the new wiring.

## Modules touched

- `internal/cli` — new `orchestrate` case + assembler function
- `docs/spec/configuration.md`, `docs/spec/interfaces.md`, `docs/architecture/diagrams.md`

(Two modules + spec docs — within the one-task, at-most-two-modules rule.)

## Out of scope

- LLMPlanner implementation (task 100).
- Out-of-process worker transport (ADR 048 §5 — future).
- Key management / rotation for worker signing keys.
- New inbound channel beyond the existing Telegram adapter (task 080).
- Promoting tasks 081/083/084/085/086 to ✅ — that is done by running spec-verifier on each
  and recording L5/L6 evidence, which this task's wiring enables but does not perform.

## Dependencies

- Tasks 081, 083, 084, 085, 086, 098 (all must be merged — see readiness gate).
- Does NOT block task 100 (LLMPlanner — parallel work on a different seam).
