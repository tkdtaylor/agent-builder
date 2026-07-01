# Task 145 — Per-model registry entries + recipe capability-tier audit

**Status:** complete (🟡 code merged; L6 pending operator observation)
**Spec:** `docs/tasks/test-specs/145-per-model-registry-entries-test-spec.md`
**Relates to:** ADR 061 (per-task model selection), ADR 043 (router). Depends on task 144 (Claude honors `--model`).

## Why

ADR 061 sets "model level = capability tier": one registry entry per model per brain, and
the router picks the cheapest sufficient one at a recipe's `MinCapability`. Task 144 makes
Claude honor `entry.ModelID`; this task makes the tiering usable — it defines the concrete
per-model entries as documented, loadable config, and audits recipe `MinCapability` values
so mechanical recipes ask for a cheap tier instead of defaulting to the top model.

## Scope

- **`docs/spec/configuration.md`:** document the per-model entry convention and the
  tier↔model table (Claude: `claude-haiku` tier 1 / `claude-sonnet` tier 2 / `claude-opus`
  tier 3; `agy`: one entry per Gemini level under the same tiering), including full
  `AGENT_BUILDER_REGISTRY_*` example blocks and cost weights.
- **`internal/registry/loader.go`:** extend `knownEntries` (and `localHarnessEntries` where
  applicable) so the new per-model IDs load from env like the existing ones. Confirm they
  round-trip through `LoadFromEnv` with the right harness.
- **Recipe capability audit:** review each recipe's `RoutingSpec.MinCapability`
  (`docsfix`=1, `agentbuilderworker`=2, orchestrate/ask/planner defaults) against ADR 061's
  tier semantics; adjust where a recipe is over- or under-asking, with the rationale in the
  task/spec. No behavior change beyond the tier value.
- **Spec:** `docs/spec/data-model.md` / `interfaces.md` if the registry contract text needs
  the new IDs.

## Out of scope

- The executor `--model` plumbing (task 144).
- The dynamic tier classifier (task 146) — this task is static, declared tiers only.

## Verification plan

- **Highest level achievable here:** L6 (operator can export the per-model entries and run
  `agent-builder ask`/a recipe and observe the selected model in logs — this host has a
  real Claude login).
- **L2:** `LoadFromEnv` tests for each new entry ID → correct harness/tier/cost/ModelID;
  router `Select` at `MinCapability` 1/2/3 returns haiku/sonnet/opus respectively when all
  three are enabled.
- **L3:** `make check` green.
- **L6:** operator exports the three Claude entries, runs a low-tier and a high-tier path,
  and observes the differing `--model` in the dispatched command / logs.
