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

## Concrete tier/cost/model values (ADR 061 §Decision)

| Entry ID | Harness | `CAPABILITY_TIER` | `COST_WEIGHT` | `MODEL` |
|----------|---------|---:|---:|---|
| `claude-haiku` | `claude-cli` | 1 | 1 | `claude-haiku-4-5-20251001` |
| `claude-sonnet` | `claude-cli` | 2 | 5 | `claude-sonnet-5` |
| `claude-opus` | `claude-cli` | 3 | 10 | `claude-opus-4-8` |
| `agy-gemini-flash` | `antigravity-cli` | 1 | 1 | `Gemini 3.5 Flash (High)` |
| `agy-gemini-pro` | `antigravity-cli` | 3 | 8 | `Gemini 3 Pro (High)` |

All five are cloud/subscription entries with no hardcoded tier/cost/model in
`internal/registry/loader.go` — the loader only registers the ID→harness mapping in
`knownEntries` (and, for the two `agy-gemini-*` IDs, adds them to `localHarnessEntries`
so an empty `SECRET_REF` is accepted, matching `antigravity`). Tier/cost/model are read
per-entry from `AGENT_BUILDER_REGISTRY_<ID>_*` env vars exactly like every other entry.

## Recipe capability-tier audit conclusion (REQ-145-04)

- `docsfix` (`internal/recipe/docsfix/docsfix.go`): `MinCapability=1`. Mechanical
  markdown-lint fix, no design judgment required — base/haiku tier is sufficient.
  **Confirmed correct; unchanged.**
- `agentbuilderworker` (`internal/recipe/agentbuilderworker/agentbuilderworker.go`):
  `MinCapability=2`. Authors a new, gated coding recipe (self-modifies the agent's own
  recipe surface); a broken commit or missing gate binding is costly, so it asks for
  the mid/sonnet tier one step above the mechanical floor. **Confirmed correct;
  unchanged.**
- `coding-agent` default recipe (`internal/runtime/run.go`), `ask`/`orchestrate-answer`
  CLI paths (`internal/cli/ask.go`, `internal/cli/orchestrate_answer.go`), and the LLM
  planner/clarifier/analyzer (`internal/orchestrator/planner/*.go`) all declare
  `MinCapability=1` as their floor — the cheapest-sufficient default for base coding and
  single-shot-answer paths. **Confirmed correct; unchanged.** No recipe was found
  over- or under-asking relative to ADR 061's tier semantics.

## Verification levels

- **L2** — the loader/router tests above; all pass (`go test ./internal/registry/...
  ./internal/router/... ./internal/recipe/...`).
- **L3** — `make check` → `All checks passed.`
- **L6** — operator exports the entries and observes the differing dispatched `--model`
  (pending operator run; not exercised by this task's executor pass).
