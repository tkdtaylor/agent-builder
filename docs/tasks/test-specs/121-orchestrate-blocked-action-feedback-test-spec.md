# Test Spec 121: Blocked-action feedback + reevaluation

**Linked task:** [`docs/tasks/backlog/121-orchestrate-blocked-action-feedback.md`](../backlog/121-orchestrate-blocked-action-feedback.md)
**Written:** 2026-06-29
**ADR:** [055](../../architecture/decisions/055-orchestrate-plan-derived-authorization.md) (seam 4)

## Requirements coverage

| Req ID     | Test cases        | Covered? |
|------------|-------------------|----------|
| REQ-121-01 | TC-001            | ⏳ |
| REQ-121-02 | TC-002, TC-003    | ⏳ |
| REQ-121-03 | TC-004            | ⏳ |

## Unit under test

The retry/escalation policy (`internal/loop`) and the orchestrate feedback path. A
failure caused by a **policy denial of a necessary action** must be a typed failure
distinct from a gate failure or executor error, and must route to bounded
reevaluation (replan) and then independent human escalation — never to a self-grant.

## Test cases

### TC-001: a denied necessary action surfaces as a typed BlockedAction failure

- **Requirement:** REQ-121-01
- **Setup:** a worker outcome where a required action was denied by policy (the denial reason + resource/action are available).
- **Expected:** the failure is classified as a distinct `BlockedAction` (or equivalently-named) failure kind carrying the denied resource/action + reason — **not** `FailureGate` / `FailureExecutorError`. Assert the failure kind and that the reason/resource are populated.

### TC-002: bounded reevaluation before escalation

- **Requirement:** REQ-121-02
- **Setup:** a goal whose attempts keep hitting the same blocked action; the retry policy bound is N.
- **Expected:** the orchestrator reevaluates (replans) up to N times — each replan re-derives the plan and therefore the plan-derived allow set (task 118 `AllowedResources`) — and only after N does it escalate. Assert exactly N reevaluations precede escalation (no infinite loop, no immediate give-up).

### TC-003: escalation carries the denied action + reason to a human

- **Requirement:** REQ-121-02
- **Setup:** reevaluation exhausted (TC-002 path).
- **Expected:** the orchestrator routes a needs-human escalation whose payload names the denied action and the deny reason (so a human can decide whether to grant independently). Assert the escalation status is `needs-human` and the reason text is present.

### TC-004: the agent never self-grants

- **Requirement:** REQ-121-03
- **Setup:** drive the full blocked-action → reevaluate → escalate cycle.
- **Expected:** at no point is the authorization widened from within the worker/executor — the only ways authorization changes are (a) a replan re-deriving the plan's allow set, or (b) an explicit human grant. Assert there is no code path where a denied resource becomes allowed without a new plan or human action (e.g. the allow set on a retry is exactly the re-derived `Plan.AllowedResources()`, never the previous set ∪ the denied resource).

## Post-implementation verification

- [ ] `go test ./internal/loop/... ./internal/cli/... ./internal/orchestrator/...` passes
- [ ] `make check` passes
- [ ] Cross-module trace: producer = worker/policy denial; consumer = retry policy + escalation. Assert the denial reason reaches escalation and no self-grant occurs.

## Test framework notes

- Go `testing`. Build on tasks 118 (plan-derived allow to re-derive on replan) and 120
  (real result to classify). Use the existing loop retry-policy test harness. L5/L6 in
  the end-to-end run: a goal needing a denied action → escalation carrying the reason.
