# ADR 061 — Per-task model selection (capability tier = model level)

**Status:** Accepted
**Date:** 2026-07-01
**Relates to:** ADR 043 (capability/cost router + registry), ADR 057 (Antigravity `agy` multi-model harness), ADR 059/053 (single-shot Completer + `ask`), ADR 060 (goal analysis & routing). Supersedes nothing; extends the router's per-entry model into the executor path.

## Context

The multi-LLM router (ADR 043) selects a `registry.RegistryEntry` by capability
tier (higher = stronger) and cost weight (lower = cheaper), returning the cheapest
eligible entry at or above a recipe's `RoutingSpec.MinCapability`. Each entry carries
a `ModelID`.

Two gaps make this inert for the Claude brain — the default brain and the one used
for almost every dispatch today:

1. **The Claude executor and completer never pass `--model`.** `ClaudeCLI.RunContext`
   builds `claude -p <prompt>` and `claudeCompleter.Complete` builds the same — neither
   threads `entry.ModelID`. `NewClaudeCLIFromEntry` drops `ModelID` on the floor. So
   **every Claude dispatch uses the CLI's built-in default model (the most powerful one,
   Opus)** regardless of which entry the router selected. The `codex-cli`, `gemini-cli`,
   and `antigravity-cli` executors already pass `--model entry.ModelID`; Claude is the
   outlier.
2. **There is no notion of "this task only needs a cheaper model."** Recipes declare a
   `MinCapability`, but with a single Claude entry (and no `--model`) the tier collapses
   to one model.

This is the opposite of how the project routes its *own* Claude Code subagents, where
role maps to model by difficulty:

| Model | Subagents |
|-------|-----------|
| haiku | task-executor, docs-writer, dependency-auditor |
| sonnet | code-reviewer, qa, spec-verifier, task-planner |
| opus | architect, security-auditor |

agent-builder should route the *same way* — mechanical/cheap work to a small model,
review to a mid model, deep design/security to the top model — so it does not burn the
most expensive model on every task. The same shape applies to `agy`, which exposes
multiple Gemini model levels (`agy` already passes `--model`, so it is ready).

The required tier is not always known ahead of time. A goal handed to the general
front door may need a runtime **selection step** that infers the tier from the task,
rather than a statically declared one.

## Decision

**1. Model level *is* the capability tier.** Do not add a new selection axis. Register
one registry entry per model per brain, each with a `CapabilityTier`/`CostWeight` that
reflects the model, and let the existing router (ADR 043) pick the cheapest sufficient
one. For Claude:

```
claude-haiku   CapabilityTier=1  CostWeight=1   ModelID=claude-haiku-4-5-20251001
claude-sonnet  CapabilityTier=2  CostWeight=5   ModelID=claude-sonnet-5
claude-opus    CapabilityTier=3  CostWeight=10  ModelID=claude-opus-4-8
```

For `agy`, one entry per Gemini level under the same tiering. A recipe with
`MinCapability=1` routes to haiku; `MinCapability=3` routes to opus.

**2. Executors honor `entry.ModelID`.** The Claude executor **and** the Claude
single-shot completer pass `--model <ModelID>` to the CLI when `ModelID` is non-empty,
reaching parity with the codex/gemini/agy executors. When `ModelID` is empty (the
synthetic default entry, and any entry that omits `_MODEL`), the flag is omitted and
the CLI's own default is used — so current behavior is preserved exactly for callers
that do not set a model. This is the one load-bearing code change; without it the
tiering above is unreachable for Claude.

**3. Static routing (pre-selected).** Recipes/tasks declare `MinCapability`; the router
selects. This is the mechanism the request asks to mirror ("route like the tasks do"),
and it is unchanged from ADR 043.

**4. Dynamic selection step.** When the tier is *not* pre-declared, a classification
step infers it at runtime and feeds the same static router. This extends the goal
analyzer (ADR 060), which already classifies a goal's `Kind`, to also emit a capability
tier. The static router remains the single place selection is resolved — the dynamic
step only chooses the `MinCapability` input.

`SensitivityHint` (ADR 043) stays an orthogonal soft axis: model level answers "how
strong," sensitivity answers "how private." They compose; neither replaces the other.

## Consequences

- **Cost control becomes real for Claude.** A mechanical task can run on haiku instead
  of silently paying for opus.
- **No duplicate machinery.** The router, registry, and `RoutingSpec` are reused as-is;
  only the executor/completer gain a flag and the config gains entries.
- **Backward compatible.** Empty `ModelID` ⇒ no `--model` ⇒ today's default-model
  behavior, so the synthetic default entry and single-entry deployments are unchanged.
- **Follow-on work.** (a) Per-model registry entries + a recipe capability-tier audit
  so recipes ask for the tier they actually need (task 145). (b) The dynamic tier
  classifier (task 146).
- **Operator-facing.** `docs/spec/configuration.md` documents the per-model entry
  pattern and the tier↔model convention.

## Alternatives considered

- **Explicit per-task `ModelClass` field threaded independently of capability tier.**
  Rejected: it duplicates the tier mechanism the router already implements and adds a
  parallel axis the operator must keep consistent with capability. Model *is* capability
  for routing purposes; a separate field earns its keep only if a task ever needs a weak
  model at high sensitivity or vice-versa, which `SensitivityHint` already covers.
- **Hard-coding a per-recipe model string.** Rejected: bypasses the router, breaks the
  quota/availability fallback (ADR 043), and re-introduces provider coupling into
  recipes.
