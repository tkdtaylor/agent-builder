# Test spec — Task 146: Dynamic model-tier selection step

**Task:** `docs/tasks/backlog/146-dynamic-model-tier-classifier.md`
**Relates to:** ADR 061 (per-task model selection), ADR 060 (goal analysis & routing), ADR 043 (router). Depends on tasks 144–145.

## Context

Static routing (task 145) works when a recipe declares `MinCapability`. For goals with no
pre-declared tier, this task infers the tier at runtime from the goal and feeds it to the
same static router — extending the goal analyzer (ADR 060) to emit a capability tier
alongside `Kind`, over the existing LLM-vs-heuristic seam.

## Requirements

- **REQ-146-01** — `GoalAnalysis` carries a capability-tier field; both the heuristic and
  LLM analyzers populate it. The LLM path is authoritative where available.
- **REQ-146-02** — The emitted tier is wired into `RoutingSpec.MinCapability` where the
  orchestrator builds the routing spec; absent/zero tier falls back to
  `defaultMinCapability`.
- **REQ-146-03** — A simple/mechanical goal yields a low tier; a complex/design/security
  goal yields a high tier; ambiguous → default.
- **REQ-146-04** — Model tier and `SensitivityHint` remain independent; this task does not
  change sensitivity handling.
- **REQ-146-05** — Spec updated (`interfaces.md` GoalAnalysis contract); `make check` green.

## Test cases

- **TC-146-01** (`TestAnalyzerEmitsTierBySimpleGoal`, table) — trivial goal → low tier;
  design/security goal → high tier; ambiguous → default.
- **TC-146-02** (`TestEmittedTierReachesRoutingSpec`) — analyzer tier N → built
  `RoutingSpec.MinCapability == N`; no tier → `defaultMinCapability`.
- **TC-146-03** (`TestTierIndependentOfSensitivity`) — same goal at different sensitivities
  yields the same tier.

## Verification levels

- **L2** — analyzer + wiring tests above.
- **L3** — `make check` green.
- **L6** — operator observes a trivial goal → cheap model, hard goal → top model via the
  dynamic step.

> Stub spec — refine the tier rubric and analyzer assertions when the task is picked up,
> consistent with ADR 060/061.
