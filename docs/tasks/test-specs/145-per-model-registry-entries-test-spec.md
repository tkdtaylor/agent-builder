# Test spec — Task 145: Per-model registry entries + recipe capability-tier audit

**Task:** `docs/tasks/backlog/145-per-model-registry-entries.md`
**Relates to:** ADR 061 (per-task model selection), ADR 043 (router). Depends on task 144.

## Context

ADR 061 maps model level to capability tier and routes with the existing router. Task 144
made the Claude executor honor `entry.ModelID`. This task defines the concrete per-model
entries (as env-loadable config) and audits recipe `MinCapability` values so the tiering
is actually exercised, with cheap tasks routed to cheap models.

## Requirements

- **REQ-145-01** — Per-model Claude entry IDs load from `AGENT_BUILDER_REGISTRY_*` env
  vars via `LoadFromEnv`, each resolving to `HarnessClaudeCLI` with its declared
  `CapabilityTier`, `CostWeight`, and `ModelID` (e.g. `claude-haiku` → tier 1, cost 1,
  `claude-haiku-4-5-20251001`; `claude-sonnet` → tier 2; `claude-opus` → tier 3).
- **REQ-145-02** — With the three Claude entries enabled, `router.Select` returns the
  cheapest entry at/above the requested `MinCapability`: 1 → haiku, 2 → sonnet, 3 → opus.
- **REQ-145-03** — `agy` per-Gemini-level entries load and route analogously.
- **REQ-145-04** — Recipe `MinCapability` values reflect ADR 061 tier semantics; any change
  is documented with rationale and does not alter recipe behavior beyond model selection.
- **REQ-145-05** — `docs/spec/configuration.md` documents the tier↔model convention and
  full example env blocks; `make check` green.

## Test cases

- **TC-145-01** (`TestLoadFromEnvPerModelClaudeEntries`, table) — env for `claude-haiku`
  / `claude-sonnet` / `claude-opus` → each loads with the expected harness/tier/cost/ModelID.
- **TC-145-02** (`TestRouterSelectsModelByTier`, table) — catalog with all three enabled;
  `Select(MinCapability:1)`→haiku, `2`→sonnet, `3`→opus.
- **TC-145-03** (`TestLoadFromEnvAgyModelLevels`) — agy per-level entries load with the
  correct harness and tiers.
- **TC-145-04** (recipe audit) — assertions that each recipe's `RoutingSpec.MinCapability`
  equals its post-audit value.

## Verification levels

- **L2** — the loader/router tests above.
- **L3** — `make check` green.
- **L6** — operator exports the entries and observes the differing dispatched `--model`.

> Stub spec — flesh out concrete tier/cost numbers and the agy model IDs when the task is
> picked up (task-planner or executor), consistent with ADR 061.
