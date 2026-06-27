# Task 092: Router + capability/cost model + escalation-ladder integration

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Implement the model router in `internal/router`. The router:

1. Selects the cheapest eligible registry entry per dispatch
   (eligible = `CapabilityTier >= MinCapability` AND `Availability == available`).
2. Applies the soft `SensitivityHint` as a tie-breaking weight (never a hard filter).
3. On gate failure, escalates UP the capability ladder (quality axis).
4. On quota exhaustion, routes sideways to the next available eligible entry
   (availability axis) — does NOT climb the quality ladder.
5. Never marks a local entry (Budget.Limit=0) as exhausted.
6. Exposes itself as a `supervisor.Executor` from the outside (or hands one back per
   dispatch), preserving F-003 supervisor isolation.

This is the core of ADR 043's "capability/cost-first" policy. Persistent quota state
(file persistence, clock seam) is task 093.

## Context

ADR 043 defines the two-axis fallback model. This task implements the selection and
escalation logic with in-memory state only. Task 093 adds persistence and the injected
clock seam for deterministic testing of reset-window logic.

### Where the router lives

`internal/router` — a sibling of `internal/executor`. The router imports
`internal/registry` (for entry types) and `internal/executor` (to construct concrete
adapters from entries). `internal/supervisor` must NOT import `internal/router`.
The fitness check `make fitness-supervisor-isolation` enforces this.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                                                          | Priority  |
|------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-092-01 | `router.Router` selects the cheapest eligible entry (lowest `CostWeight` among entries where `CapabilityTier >= MinCapability` AND `Availability == available`). Returns `ErrNoEligibleExecutor` when no entry qualifies. | must have |
| REQ-092-02 | The soft `SensitivityHint` is applied as a tie-breaking weight among equally-cost eligible entries (local entry preferred when hint is `sensitive`). It never excludes an otherwise-eligible entry. | must have |
| REQ-092-03 | `router.OnGateFailure(entryID)` escalates to the next-stronger eligible entry (ascending capability-tier order). `router.OnQuotaExhausted(entryID, resetAt)` marks the entry `AvailStatusExhausted` and routes sideways (next cheapest available at sufficient tier). The two axes are independent. | must have |
| REQ-092-04 | An entry with `Budget.Limit == 0` is never marked exhausted by `OnQuotaExhausted` — it is always-available. | must have |
| REQ-092-05 | `internal/supervisor` does not import `internal/router` or `internal/registry`. `make fitness-supervisor-isolation` exits 0 after this task. `internal/router` lives on the executor side of the injection boundary. | must have |

## Readiness gate

- [x] Test spec `092-router-capability-cost-escalation-test-spec.md` exists (written first)
- [ ] Task 087 merged (registry types)
- [ ] Task 089 merged (Codex adapter — router needs to construct it)
- [ ] Task 090 merged (Gemini adapter)
- [ ] Task 091 merged (local entry + `NewClaudeCLIFromEntry`)
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-092-01] TC-092-01: cheapest eligible entry selected with MinCapability=1
- [ ] [REQ-092-01] TC-092-02: ineligible entries filtered by MinCapability; cheapest remaining selected
- [ ] [REQ-092-02] TC-092-03: SensitivitySensitive biases toward local without excluding eligible non-local
- [ ] [REQ-092-03] TC-092-04: OnGateFailure → escalates to next-stronger; all entries exhausted → ErrNoEligibleExecutor
- [ ] [REQ-092-03] TC-092-05: OnQuotaExhausted → marks exhausted, routes sideways; does not escalate quality
- [ ] [REQ-092-04] TC-092-06: Budget.Limit=0 entry ignores OnQuotaExhausted; remains available
- [ ] [REQ-092-05] TC-092-07: `make fitness-supervisor-isolation` → PASS; `go list -deps ./internal/supervisor/...` → no `internal/router`; `make check` → `All checks passed.`

## Verification plan

- **Highest level achievable:** L3 — router logic + fitness isolation check.
- **Harness command:**
  ```
  go test -count=1 ./internal/router/...
  make fitness-supervisor-isolation
  make check
  ```
  Expected:
  - Unit tests → `ok github.com/tkdtaylor/agent-builder/internal/router`
  - Fitness → `PASS fitness-supervisor-isolation: …`
  - `make check` → `All checks passed.`

## Out of scope

- Persistent quota state + clock seam (task 093).
- The quota-tally increment on each dispatch (task 093).
- End-to-end flow with a recipe routing through the real router (task 095).
- The fitness check for router isolation itself (if a new fitness rule is warranted,
  it is a separate task).

## Dependencies

- Task 087 (registry types).
- Tasks 089, 090, 091 (harness adapters the router constructs).
- Informs: task 093 (adds persistence + clock seam on top of this router);
  task 095 (wires the real router into `internal/runtime`).
