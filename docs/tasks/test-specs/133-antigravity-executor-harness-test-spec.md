# Test spec — Task 133: Antigravity (`agy`) executor harness

**Task:** `docs/tasks/backlog/133-antigravity-executor-harness.md`
**Relates to:** task 132 / ADR 056 (Gemini self-managed OAuth — the subscription-mode pattern);
task 090 (`GeminiCLI` shape); ADR 033 (subscription auth); ADR 052 (gate-failure prompt injection).

## Context

The Gemini CLI backend was shut down 2026-06-18; its successor is **Antigravity CLI (`agy`)**, a
Claude-Code-style agentic CLI. Verified live on this host (`agy` v1.0.13): `agy --print "<prompt>"`
runs a single prompt headlessly and prints the response; `--model` selects the model; `--add-dir`
adds the worktree to the workspace; `--dangerously-skip-permissions` auto-approves tool use (safe
because agent-builder runs the executor inside its own exec-sandbox perimeter). `agy` authenticates
via its own keyring (`~/.antigravity`, Google Sign-In) — **no API key** — so the registry signals it
with an **empty `SecretRef`**, exactly like the Gemini subscription mode (task 132). `agy` is itself
multi-model (Gemini 3.5/3.1, Claude Sonnet/Opus 4.6, GPT-OSS) selected via `--model`.

This task adds a new `AntigravityCLI` executor (harness `HarnessAntigravityCLI = "antigravity-cli"`)
and wires it through the registry/runtime, so the multi-LLM router gains Antigravity as the third
brain (replacing the dead gemini path operationally).

> **Implementation note (avoid the gemini mistake):** `agy` is installed and works on this host.
> Confirm the EXACT arg conventions live (`agy --help`, a real `agy --print` call) — whether the
> prompt is `--print`'s value or a positional, and the exact `--model` token format from
> `agy models` — and pin them in the stub-based tests. Do NOT guess the interface.

## Requirements

- **REQ-133-01** — `*AntigravityCLI` implements `supervisor.Executor` (compile-time `var _` + runtime
  non-nil constructor).
- **REQ-133-02** — Subscription mode (the only mode for `agy`; `entry.SecretRef == ""`):
  `run` does NOT resolve or inject any API key; it invokes `agy` in print mode with the model, the
  worktree (`--add-dir`), `--dangerously-skip-permissions`, and the task prompt, inheriting the
  process env (so `HOME` is preserved → `agy` uses its `~/.antigravity` keyring). `cmd.Dir == worktree`.
- **REQ-133-03** — On exit 0, the branch is extracted from stdout (the prompt instructs the agent to
  print `BRANCH: <name>` as the last line); a successful run returns `{Branch, OK:true}`. A missing
  `BRANCH:` line returns `ErrAntigravityMissingBranch` with `OK:false`.
- **REQ-133-04** — A non-zero `agy` exit returns `{OK:false}` and a non-nil error containing
  "antigravity" and the sanitized combined output (no secret leakage — though there is no API key in
  subscription mode, sanitization must be a safe no-op).
- **REQ-133-05** — Blank worktree → `ErrAntigravityBlankWorktree`, subprocess not invoked.
- **REQ-133-06** — Registry/runtime: `HarnessAntigravityCLI` is a distinct `HarnessDriver`
  (`"antigravity-cli"`, `String()` returns it); a subscription entry (`SecretRef == ""`) passes
  `ConfigFromEnv` with no cloud credential, and `buildExecutorForEntry` routes it to
  `*executor.AntigravityCLI`.
- **REQ-133-07** — Gate-failure prompt injection (ADR 052 parity, like task 108): when
  `task.PriorFailure != ""` the prompt includes the failure section; first attempt (empty) omits it.

## Test cases

- **TC-133-01** (REQ-133-01) `TestAntigravityCLI_InterfaceSatisfied`: compile-time `var _
  supervisor.Executor = (*AntigravityCLI)(nil)` + `NewAntigravityCLI(...)` non-nil.
- **TC-133-02** (REQ-133-02) `TestAntigravitySubscriptionModeRunsHeadless`: subscription entry
  (`SecretRef == ""`) + a secret source that fails the test if `NamedProviderToken` is called. Stub
  the subprocess (exit 0, stdout containing `BRANCH: task/133-test`). Assert: secret source never
  consulted; the captured argv contains `--print`, `--model <expected>`, `--add-dir <worktree>`,
  `--dangerously-skip-permissions`, and the prompt; `cmd.Dir == worktree`; result
  `{Branch:"task/133-test", OK:true}`.
- **TC-133-03** (REQ-133-03) `TestAntigravityExtractsBranch` + `TestAntigravityMissingBranchErrors`:
  stdout with `BRANCH: feature/x` → `Branch=="feature/x"`; stdout with no BRANCH line → error
  `errors.Is(err, ErrAntigravityMissingBranch)`, `OK:false`.
- **TC-133-04** (REQ-133-04) `TestAntigravityNonZeroExitErrors`: stub exit 1 → non-nil error
  containing "antigravity", `Result.OK == false`.
- **TC-133-05** (REQ-133-05) `TestAntigravityBlankWorktreeErrors`: blank worktree → error wrapping
  `ErrAntigravityBlankWorktree`, subprocess not invoked (flag asserts factory never called).
- **TC-133-06** (REQ-133-06) `TestHarnessAntigravityConstant` (`HarnessAntigravityCLI ==
  "antigravity-cli"`, `String()` match, distinct from the other 4 harness constants) +
  `TestConfigFromEnvAllowsAntigravitySubscriptionEntry` (subscription entry, no cloud key →
  `ConfigFromEnv` ok; `buildExecutorForEntry` → `*executor.AntigravityCLI`).
- **TC-133-07** (REQ-133-07) `TestAntigravityPromptIncludesFailureSectionWhenPriorFailureSet` +
  `...OmitsWhenEmpty`: prompt contains "Your previous attempt failed the verification gate." +
  verbatim `PriorFailure` when set; unchanged when empty.

## Verification levels

- **L2/L3:** `go test -race -count=1 ./internal/executor/... ./internal/runtime/...` + `make check`
  green; all TCs hard-assert exact values (no smoke tests). Use the stub-subprocess pattern from
  `gemini_cli_test.go` / `codex_cli_test.go` (`TestMain`/`runHelperProcess`/`capturedCmd`).
- **L6 (achievable in-session — `agy` works headlessly here):** register an antigravity subscription
  entry, route a scoped goal to it against a real target worktree, and observe a **gate-passing
  branch produced via `agy`** (the agent edits the worktree, prints `BRANCH:`, the verify gate runs).
  Recorded by operator/main-session observation.
