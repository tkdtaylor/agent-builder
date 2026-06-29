# ADR 057 — Antigravity (`agy`) executor harness

**Status:** accepted  
**Date:** 2026-06-29  
**Related:** ADR 056 (Gemini subscription/OAuth pattern), ADR 033 (subscription auth), ADR 052 (gate-failure prompt injection), task 132 (Gemini CLI), task 133 (Antigravity implementation)

## Context

The Google Gemini CLI backend was shut down on 2026-06-18. Its successor is **Antigravity (`agy`)**, an agentic CLI that runs on the same self-managed OAuth model Gemini used (task 132, ADR 056). The Antigravity CLI is already installed and functional on agent-builder's host and supports multiple models (Gemini 3.5/3.1, Claude Sonnet/Opus, GPT-OSS) via `--model`.

The multi-LLM router currently has four harnesses: Claude CLI, Codex CLI, Gemini CLI (now dead), and Ollama Native. Adding Antigravity as the fifth harness provides an immediate replacement for Gemini's operational role and gives the router a third cloud LLM brain (Claude + OpenAI + Google).

## Decision

We add **`HarnessAntigravityCLI`** ("antigravity-cli") as a new executor harness, mirroring the Gemini subscription/OAuth pattern (ADR 056) exactly:

1. **Subscription-only auth**: The `"antigravity"` registry entry has `SecretRef == ""` (no API key). The agy CLI authenticates via `~/.antigravity` (Google Sign-In keyring), inherited from the process environment.

2. **Invocation form**: `agy --print "<prompt>" --model <entry.ModelID> --add-dir <worktree> --dangerously-skip-permissions`. The prompt is the value of `--print`, not a positional argument.

3. **Isolation rationale**: We do **not** pass `agy --sandbox`. Isolation is provided by the outer **exec-sandbox perimeter** (agent-builder runs the executor inside its own container/gVisor boundary). Passing `--dangerously-skip-permissions` is safe **only because** the executor runs in that sandbox; the agent cannot escape it to perform real-world permission changes.

4. **Prompt shape**: Reuse the Gemini prompt structure, including ADR 052's `PriorFailure` section (task 108 parity): when `task.PriorFailure != ""`, the prompt includes "Your previous attempt failed the verification gate" + the verbatim failure details.

5. **Registry/runtime wiring**: 
   - Add `HarnessAntigravityCLI` constant to `internal/registry/types.go` + `String()` method.
   - Add `"antigravity"` entry ID to `localHarnessEntries` in `internal/registry/loader.go` (allow empty `SecretRef`, scoped exactly like task 132's `"gemini"` addition).
   - Add case in `internal/runtime/run.go`'s `buildExecutorForEntry` to route to `executor.NewAntigravityCLI(...)`.
   - Implement `AntigravityCLI` in `internal/executor/antigravity_cli.go`, fully mirroring `GeminiCLI`'s structure.

6. **No key resolution**: Unlike Codex (API-key mode), Antigravity never calls `secretSource.NamedProviderToken()`. The SecretRef check (`if entry.SecretRef != ""`) is absent because the field is always empty for subscription entries.

7. **Error types**: `ErrAntigravityBlankWorktree` (worktree blank, subprocess not invoked) and `ErrAntigravityMissingBranch` (stdout has no `BRANCH:` line), mirroring the Gemini pattern.

## Rationale

**Why add Antigravity now?** The Gemini CLI is dead; Antigravity is its direct replacement with identical auth (self-managed OAuth) and identical invocation patterns. Deferring this task leaves the router without its third brain and increases downstream reliance on the deprecated Gemini executor.

**Why subscription-only?** Antigravity has no API-key mode in its current release. The entry is subscription/OAuth from the ground up. If a future version adds API-key auth, we can add a second mode then (as was originally planned for task 132 but Gemini shut down before it happened).

**Why not agy --sandbox?** The executor runs inside agent-builder's own exec-sandbox (Podman container or gVisor sandbox, depending on config). Nesting `agy --sandbox` would double-wrap the isolation and is unnecessary. The outer boundary is load-bearing; the inner one is redundant. We skip it explicitly via `--dangerously-skip-permissions` to avoid agy prompting for permission on tool use — the outer sandbox is the permission boundary.

## Consequences

- **New harness**: The router gains a 5th candidate. Entry selection is unchanged; the cheapest eligible entry at minimum capability wins (ADR 043).
- **Dead harness retained**: Gemini CLI remains in the codebase as a deprecated reference implementation. Removal is deferred (decision pending; task out of scope).
- **Auth pattern complete**: Subscription/OAuth is now a proven pattern across two harnesses (Gemini + Antigravity). Future harnesses using self-managed OAuth can follow this template.
- **Cross-harness prompt parity**: All executors (Claude, Codex, Gemini, Antigravity, Ollama) now share the `PriorFailure` failure-section structure (ADR 052, task 108). This ensures the router can transparently swap harnesses without the agent seeing a different prompt shape on retry.

## References

- ADR 056 (Gemini self-managed OAuth login) — establishes the subscription-mode pattern.
- ADR 033 (subscription authentication) — defines the auth model.
- ADR 052 (gate-failure prompt injection) — the `PriorFailure` section used here.
- Task 132 (Gemini CLI subscription mode) — direct template for this executor.
- Task 108 (cross-harness parity) — ensures prompt consistency.
