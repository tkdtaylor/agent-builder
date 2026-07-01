# Task 146 — Dynamic model-tier selection step (extend goal analyzer)

**Status:** completed (🟡 code merged — awaiting spec-verifier + L6)
**Spec:** `docs/tasks/test-specs/146-dynamic-model-tier-classifier-test-spec.md`
**Relates to:** ADR 061 (per-task model selection), ADR 060 (goal analysis & routing), ADR 043 (router). Depends on tasks 144–145.

## Why

ADR 061's static path routes when a recipe declares its `MinCapability`. But a goal handed
to the general front door often has no pre-declared tier. This task adds the **dynamic
selection step**: infer the required capability tier from the task and feed it to the same
static router, so the model level is chosen at runtime instead of hard-coded. It extends
the goal analyzer (ADR 060), which already classifies a goal's `Kind`, to also emit a
capability tier — reusing the LLM-vs-heuristic classifier seam (cf. task 142, where the LLM
correctly classified "write a haiku" as `KindAnswer` while the heuristic mis-routed it).

## Scope

- **`internal/orchestrator/planner` (goal analyzer):** extend `GoalAnalysis` with a
  capability-tier field; the analyzer emits a tier (e.g. 1/2/3) alongside `Kind`. Heuristic
  and LLM analyzers both populate it; the LLM path is authoritative where available.
- **Wire the emitted tier into `RoutingSpec.MinCapability`** at the point the orchestrator
  builds the routing spec, so the dynamic tier feeds the existing static router. When the
  analyzer yields no tier, fall back to today's default (`defaultMinCapability`).
- **Spec:** `docs/spec/interfaces.md` (GoalAnalysis contract) and
  `docs/spec/configuration.md` / `behaviors` for the routing behavior.

## Out of scope

- Executor `--model` plumbing (task 144) and per-model entries (task 145).
- Changing `SensitivityHint` semantics — model tier and sensitivity remain orthogonal
  (ADR 043/061).

## Verification plan

- **Highest level achievable here:** L6 (operator runs the front door on a trivial vs. a
  hard goal and observes the differing selected model in logs).
- **L2:** analyzer tests — a mechanical/simple goal yields a low tier; a complex/design goal
  yields a high tier; missing/ambiguous → default tier. Wiring test: emitted tier reaches
  `RoutingSpec.MinCapability`.
- **L3:** `make check` green.
- **L6:** operator observes a trivial goal routed to a cheap model and a hard goal to the
  top model via the dynamic step.

> Stub spec — refine the tier rubric and concrete analyzer assertions when picked up,
> consistent with ADR 060/061.
