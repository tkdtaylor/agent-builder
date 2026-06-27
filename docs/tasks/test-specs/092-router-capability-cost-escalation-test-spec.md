# Test spec — Task 092: Router + capability/cost model + escalation-ladder integration

**Linked task:** `docs/tasks/backlog/092-router-capability-cost-escalation.md`
**Written:** 2026-06-27
**Status:** ready

## Context

ADR 043 defines the model router: it selects a registry entry per dispatch using a
capability/cost-first policy, handles escalation on gate failure (quality axis), and
routes around exhausted entries (availability axis). The router lives in
`internal/router` (a sibling of `internal/executor`) and is injected into the
supervisor as a `supervisor.Executor` from the runtime's perspective.

The two fallback axes are kept distinct:
- **Gate failure** → escalate UP the capability ladder (walk to next-stronger entry).
- **Quota exhaustion** → fall SIDEWAYS to next available eligible entry at
  same-or-sufficient capability.

The eligibility predicate: `CapabilityTier >= MinCapability` AND `Availability.Status == AvailStatusAvailable`.
The selection policy: cheapest eligible entry (lowest `CostWeight`), with the soft
`SensitivityHint` applied as a tie-breaking weight (never a hard filter).

F-003 must be preserved: `internal/supervisor` must not import `internal/router`.

## Requirements coverage

| Req ID     | Test cases                              | Covered? |
|------------|-----------------------------------------|----------|
| REQ-092-01 | TC-092-01, TC-092-02                    | yes      |
| REQ-092-02 | TC-092-03                               | yes      |
| REQ-092-03 | TC-092-04, TC-092-05                    | yes      |
| REQ-092-04 | TC-092-06                               | yes      |
| REQ-092-05 | TC-092-07                               | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-092-01 — Router selects cheapest eligible entry

- **Requirement:** REQ-092-01
- **Level:** L2 (unit test)
- **Test file:** `internal/router/router_test.go`

**Input:** Registry with three entries:
- `{ID:"local", CapabilityTier:1, CostWeight:1, Availability:Available}`
- `{ID:"claude-oauth", CapabilityTier:3, CostWeight:10, Availability:Available}`
- `{ID:"codex", CapabilityTier:2, CostWeight:5, Availability:Available}`

Dispatch with `RoutingSpec{MinCapability:1}`.

**Expected output:**
- Router selects `"local"` (cheapest eligible: CostWeight=1).
- All three pass the `CapabilityTier >= 1` filter; `"local"` wins on cost.

---

### TC-092-02 — MinCapability filters ineligible entries

- **Requirement:** REQ-092-01
- **Level:** L2 (unit test)
- **Test file:** `internal/router/router_test.go`

**Input:** Same registry. Dispatch with `RoutingSpec{MinCapability:2}`.

**Expected output:**
- `"local"` (tier=1) is excluded.
- Router selects `"codex"` (tier=2, CostWeight=5 — cheapest among eligible).

---

### TC-092-03 — SensitivityHint biases toward local without filtering

- **Requirement:** REQ-092-02
- **Level:** L2 (unit test)
- **Test file:** `internal/router/router_test.go`

**Input:** Registry with `"local"` (tier=1, cost=1) and `"claude-oauth"` (tier=3, cost=10).
Dispatch with `RoutingSpec{MinCapability:1, SensitivityHint:SensitivitySensitive}`.

**Expected output:**
- Router selects `"local"` (already the cheapest eligible, and the sensitive hint
  agrees — local preferred for sensitive work).
- If the hint tips a tie: with two entries at equal cost, the sensitive hint picks the
  local entry. The hint never excludes an otherwise-eligible non-local entry.
- A dispatch with `SensitivityHint:SensitivityNone` also selects `"local"` (it is
  cheapest regardless). The hint's effect is only visible when cost is tied.

---

### TC-092-04 — Gate failure escalates to next-stronger entry (quality axis)

- **Requirement:** REQ-092-03
- **Level:** L2 (unit test)
- **Test file:** `internal/router/router_test.go`

**Input:** Registry with `"local"` (tier=1) and `"claude-oauth"` (tier=3). Router
first selects `"local"`. Simulate gate failure by calling `router.OnGateFailure(attemptID)`.

**Expected output:**
- Router escalates: next selection returns `"claude-oauth"` (tier=3, the next-stronger
  eligible entry).
- After exhausting all eligible entries (gate fails on `"claude-oauth"` too), the
  router returns `ErrNoEligibleExecutor`.

---

### TC-092-05 — Quota exhaustion routes sideways (availability axis)

- **Requirement:** REQ-092-03
- **Level:** L2 (unit test)
- **Test file:** `internal/router/router_test.go`

**Input:** Registry with `"claude-oauth"` (tier=3, available) and `"codex"` (tier=2,
available). Router selects `"claude-oauth"` first (highest cost, but say it's the
only tier-3). Simulate quota exhaustion: call `router.OnQuotaExhausted("claude-oauth",
resetAt)`.

**Expected output:**
- `"claude-oauth"` transitions to `Availability{Status:AvailStatusExhausted, ResetAt:resetAt}`.
- Next selection with `RoutingSpec{MinCapability:2}` returns `"codex"` (still
  available, meets MinCapability=2).
- The router does NOT escalate to a stronger entry — it routes sideways.

---

### TC-092-06 — Local entry is never marked exhausted (Budget zero)

- **Requirement:** REQ-092-04
- **Level:** L2 (unit test)
- **Test file:** `internal/router/router_test.go`

**Input:** Registry with only `"local"` (Budget.Limit=0). Call
`router.OnQuotaExhausted("local", someTime)`.

**Expected output:**
- The call is silently ignored (or returns a specific error indicating local entries
  are unlimited). `"local"` remains `AvailStatusAvailable`.
- A subsequent selection still returns `"local"`.

**Rationale:** A local entry has `Budget.Limit == 0` (unlimited). The router must
never mark it exhausted — it is the always-available fallback.

---

### TC-092-07 — F-003 preserved: internal/supervisor does not import internal/router

- **Requirement:** REQ-092-05
- **Level:** L3 (fitness check + import-graph)
- **Test file / harness:** `make fitness-supervisor-isolation`

**Input:** `make fitness-supervisor-isolation` after the router is added.

**Expected output:**
- `PASS fitness-supervisor-isolation: …` (exit 0).
- `go list -deps ./internal/supervisor/...` does NOT contain `internal/router` or
  `internal/registry`.
- The router is injected via the `supervisor.Executor` interface (the same boundary
  as `claude_cli.go` today); the supervisor sees a seam, not a router.

---

## Verification plan

- **Highest level achievable:** L3 — the router has no runtime-observable surface on
  its own. Unit tests prove the selection/escalation logic; the fitness check proves
  isolation.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/router/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/router`
- **L3 fitness:**
  ```
  make fitness-supervisor-isolation
  ```
  Expected: `PASS fitness-supervisor-isolation: …`
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Persistent quota state across runs (task 093 — the router in this task holds state
  in memory only; task 093 adds file persistence and the injected clock seam).
- End-to-end recipe→router flow (task 095).
- Live executor dispatch through the router (task 095).
