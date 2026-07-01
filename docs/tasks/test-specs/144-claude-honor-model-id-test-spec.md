# Test spec — Task 144: Claude executor + completer honor entry.ModelID via --model

**Task:** `docs/tasks/backlog/144-claude-honor-model-id.md`
**Relates to:** ADR 061 (per-task model selection), ADR 043 (capability/cost router), ADR 059 (single-shot Completer).

## Context

The router selects a `registry.RegistryEntry` carrying a `ModelID`, but the Claude
executor (`internal/executor/claude_cli.go`) and single-shot completer
(`internal/executor/claude_completer.go`) build `claude -p <prompt>` and never pass
`--model`, so `entry.ModelID` is ignored and Claude always runs the CLI default model.
The codex/gemini/agy executors already pass `--model entry.ModelID`. This task brings
Claude to parity: pass `--model <ModelID>` when the entry sets one, and preserve today's
exact behavior (no flag) when it does not.

The existing tests capture argv via a stubbed `cmdFactory` (see
`claude_cli_test.go`, `claude_completer_test.go`); this spec extends that pattern.

## Requirements

- **REQ-144-01** — `ClaudeCLIConfig` gains a `Model string` field and `ClaudeCLI` a
  `model` field, mapped in `NewClaudeCLI` (trimmed).
- **REQ-144-02** — `NewClaudeCLIFromEntry` sets `Model: entry.ModelID` in **both** the
  cloud (`SecretRef != ""`) and local/translation-proxy (`SecretRef == ""`) branches.
- **REQ-144-03** — `ClaudeCLI.RunContext` builds argv `-p <prompt> --model <model>` when
  `model` is non-empty, and exactly `-p <prompt>` (no `--model`) when empty. The
  `--model` flag and its value are adjacent and in that order.
- **REQ-144-04** — `newClaudeCompleter` stores `entry.ModelID` (trimmed) as `model`;
  `Complete` appends `--model <model>` when non-empty and omits it when empty. No change
  to the `Complete`/`newClaudeCompleter` signatures.
- **REQ-144-05** — Empty `ModelID` reproduces current behavior byte-for-byte (argv is
  `-p <prompt>` only) for both executor and completer — the synthetic default entry and
  any entry without `_MODEL` are unaffected.
- **REQ-144-06** — Auth/env behavior is unchanged (cloud token injection, local
  `ANTHROPIC_BASE_URL` + placeholder, HOME isolation); `make check` (F-010/F-014) green.

## Test cases

- **TC-144-01** (`TestClaudeCLIRunPassesModelWhenSet`) — construct `ClaudeCLI` via
  `NewClaudeCLI(ClaudeCLIConfig{..., Model: "claude-haiku-4-5-20251001"})` with a stubbed
  `cmdFactory`; run a task → captured argv equals `["-p", <prompt>, "--model",
  "claude-haiku-4-5-20251001"]` (the two model elements adjacent, in order).
- **TC-144-02** (`TestClaudeCLIRunOmitsModelWhenEmpty`) — same but `Model: ""` → captured
  argv equals `["-p", <prompt>]`; no element equals `"--model"`.
- **TC-144-03** (`TestNewClaudeCLIFromEntryThreadsModelID`, table) — (a) cloud entry
  (`SecretRef="anthropic-key"`, `ModelID="claude-opus-4-8"`) and (b) local entry
  (`SecretRef=""`, `Endpoint="http://localhost:4000"`, `ModelID="qwen2.5-coder-7b"`) →
  each resulting executor, when run under a stubbed `cmdFactory`, emits `--model <that
  ModelID>` in argv.
- **TC-144-04** (`TestClaudeCompleterPassesModelWhenSet`) — `newClaudeCompleter` on an
  entry with `ModelID="claude-sonnet-5"`, stubbed `cmdFactory` → `Complete` argv equals
  `["-p", <prompt>, "--model", "claude-sonnet-5"]`.
- **TC-144-05** (`TestClaudeCompleterOmitsModelWhenEmpty`) — entry with `ModelID=""` →
  `Complete` argv equals `["-p", <prompt>]`; no `"--model"` element. (Guards the existing
  `wantArgs := []string{"-p", prompt}` assertion in `claude_completer_test.go` stays true
  for the empty-model case.)
- **TC-144-06** (`TestClaudeModelFlagLeavesEnvUnchanged`) — with `Model` set, the captured
  child env still carries the expected auth/HOME (cloud: `CLAUDE_CODE_OAUTH_TOKEN`/key
  and isolated `HOME`; local: `ANTHROPIC_BASE_URL` + placeholder) — the flag change does
  not disturb env construction.

## Verification levels

- **L2/L5** — the argv-capture tests above (stubbed `cmdFactory`).
- **L3** — `env PATH=/tmp/agent-builder-tools:$PATH make check` → `All checks passed.`
- **Mutation** — reverting the `--model` append in either file makes TC-144-01/03/04 fail.
