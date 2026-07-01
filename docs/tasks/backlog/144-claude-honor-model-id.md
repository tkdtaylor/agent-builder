# Task 144 — Claude executor + completer honor entry.ModelID via --model

**Status:** backlog
**Spec:** `docs/tasks/test-specs/144-claude-honor-model-id-test-spec.md`
**Relates to:** ADR 061 (per-task model selection), ADR 043 (capability/cost router), ADR 059 (single-shot Completer). Unblocks tasks 145–146.

## Why

The router selects a `registry.RegistryEntry` (which carries a `ModelID`) by capability
tier and cost, but the **Claude** executor and completer never pass `--model` — they
build `claude -p <prompt>` and drop `entry.ModelID` entirely (`ClaudeCLI.RunContext`,
`claudeCompleter.Complete`, `NewClaudeCLIFromEntry`). So **every Claude dispatch uses the
CLI's default model (Opus)** no matter which entry was selected. The `codex-cli`,
`gemini-cli`, and `antigravity-cli` executors already pass `--model entry.ModelID`;
Claude is the outlier. Until Claude honors the selected model, per-task model routing
(ADR 061) is unreachable for the default brain. This is the load-bearing fix the rest of
the feature depends on.

## Scope

- **`internal/executor/claude_cli.go`:** add a `Model` field to `ClaudeCLIConfig` and a
  `model` field to `ClaudeCLI`; map it in `NewClaudeCLI`. Set it from `entry.ModelID` in
  **both** branches of `NewClaudeCLIFromEntry` (cloud and local/translation-proxy).
  In `RunContext`, build the argv as `claude -p <prompt> --model <model>` **only when**
  `model` is non-empty; when empty, emit `claude -p <prompt>` exactly as today.
- **`internal/executor/claude_completer.go`:** add a `model` field set from
  `entry.ModelID` in `newClaudeCompleter`; in `Complete`, append `--model <model>` only
  when non-empty. (The completer already receives the entry — no signature change.)
- **Spec:** `docs/spec/configuration.md` — note that the Claude executor/completer pass
  `--model` when the entry's `_MODEL` is set (parity with the other harnesses); empty ⇒
  CLI default. `docs/spec/interfaces.md` if the executor construction contract is
  described there.

## Out of scope

- Defining the per-model registry entries (`claude-haiku`/`sonnet`/`opus`) and auditing
  recipe `MinCapability` values — **task 145**.
- The dynamic tier classifier — **task 146**.
- `agy`/codex/gemini executors — they already pass `--model`.

## Verification plan

- **Highest level achievable here:** L5 (stubbed `cmdFactory` capturing argv — the
  established pattern for these executors; a live L6 run would consume real quota and is
  covered by task 145's operator step).
- **L2/L5:** executor test (stubbed `cmdFactory`) — entry with `ModelID` set ⇒ captured
  argv contains `--model <id>` adjacent and correct; entry with empty `ModelID` ⇒ argv is
  exactly `-p <prompt>` (no `--model`). Same two cases for the completer. Round-trip
  through `NewClaudeCLIFromEntry` (cloud and local) proving `ModelID` reaches argv.
- **L3:** `env PATH=/tmp/agent-builder-tools:$PATH make check` green.
- **Mutation check:** reverting the `--model` append makes the ModelID-set cases fail.
