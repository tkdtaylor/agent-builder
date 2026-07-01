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

## Tier rubric (resolved, ADR 061 §4)

`GoalAnalysis.CapabilityTier int` is the single source the answer route's
`RoutingSpec.MinCapability` is built from (0 = unset → caller falls back to the default
floor). The rubric:

| Goal | Tier |
|------|------|
| simple / mechanical | 1 |
| complex (no design/security signal) | 2 |
| design / architecture / security / concurrency / cryptography | 3 |
| ambiguous / unknown | 0 (unset → default floor) |

- **Heuristic analyzer:** `Complexity=simple → 1`, `Complexity=complex → 2`, bumped to 3
  when a design/security keyword is present (`security`, `secure`, `auth`, `crypto`,
  `architecture`, `design`, `concurren…`, `distributed`). It always has a definite
  complexity, so it never emits 0.
- **LLM analyzer:** emits the tier directly in its JSON contract (`"tier": 1|2|3`) and is
  authoritative when valid; a missing/out-of-range tier (or any parse/invoke error) falls
  back to the heuristic result exactly as task 142 does; the lenient string-recovery path
  backfills its unset tier from the heuristic so a successful analysis always carries a
  definite tier.

## Test cases

- **TC-146-01** (`TestAnalyzerEmitsTierBySimpleGoal`, table, `internal/orchestrator`) —
  heuristic: trivial/simple → 1; complex-but-not-security → 2; design/architecture/
  security/auth/crypto/concurrency → 3. Companion `TestHeuristicAnalyzerNeverEmitsUnsetTier`
  pins the heuristic never emits 0. LLM authoritative path asserted in
  `TestLLMGoalAnalyzerParsesWellFormedResponse` (`internal/orchestrator/planner`): the
  emitted `"tier"` reaches `GoalAnalysis.CapabilityTier` verbatim (tiers 1/2/3); malformed/
  missing/out-of-range tier falls back to the heuristic tier
  (`TestLLMGoalAnalyzerFallbackOnMalformed` asserts `CapabilityTier == heuristic`).
- **TC-146-02** (`TestEmittedTierReachesRoutingSpec`, `internal/cli`) — drives the real
  producer `answerMinCapability(tier)` (the single tier→MinCapability resolution inside
  `cliAnswerer.Answer`) and the real consumer `router.Select(RoutingSpec{MinCapability: …})`:
  tier N → floor N → an entry at tier ≥ N is selected; tier 0 → `answerDefaultMinCapability`
  (= 1). Not a hand-set field — the mapping function and Select call are the ones on the
  live answer path.
- **TC-146-03** (`TestTierIndependentOfSensitivity`, `internal/orchestrator` +
  `TestTierMinCapabilityIndependentOfSensitivity`, `internal/cli`) — the analyzer takes no
  sensitivity input (tier is stable across calls), and the same tier yields the same
  `MinCapability` floor at `SensitivityNone` and `SensitivitySensitive` — model tier and
  sensitivity stay orthogonal (REQ-146-04: `SensitivityHint` semantics unchanged).

## Verification levels

- **L2** — analyzer + wiring tests above.
- **L3** — `make check` green (Go tests + 14/14 fitness, incl. F-014
  `internal/orchestrator/planner` has no direct import of `internal/executor`).
- **L6** — operator observes a trivial goal → cheap model, hard goal → top model via the
  dynamic step.
