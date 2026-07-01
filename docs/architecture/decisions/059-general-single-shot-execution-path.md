# ADR 059 — General single-shot execution path (Completer for all brains + `ask` entrypoint)

**Status:** Accepted
**Date:** 2026-06-30
**Relates to:** ADR 053 (single-shot `Completer` seam, Ollama-only), ADR 043 (executor registry + router), ADR 057 (Antigravity/`agy` harness). First slice of the **composed-brain-as-general-executor** named in the roadmap forward arc.

## Context

agent-builder's north star is a **general** autonomous agent — coding is one skill among many.
Today every `supervisor.Executor` harness is **coding-shaped**: it takes a task spec + worktree,
edits files, runs the verification gate, and returns a branch. The only non-coding path is the
single-shot `executor.Completer` (ADR 053), which answers a prompt with raw text — no worktree, no
gate, no branch. But `CompleterForEntry` only constructs a completer for `HarnessOllamaNative`;
`HarnessClaudeCLI`, `HarnessCodexCLI`, and `HarnessGeminiCLI` fail closed with
`ErrSingleShotUnsupported` (ADR 053 §2).

The consequence: a simple general goal ("what is the capital of France?") can only be answered by the
local Ollama brain. The two cloud brains (Claude, `agy`) — and any human-facing way to *ask* the
agent a question — do not exist. There is no CLI entrypoint that returns an answer rather than a
branch; the subcommands (`run`, `orchestrate`, `verify`) all drive the coding path.

(Verified 2026-06-30: the Ollama `Completer` answers live through the agent's own code path —
`CompleterForEntry(ollama-native) → "Paris"`. The seam works; it is just not wired to the cloud
brains or to a human entrypoint.)

## Decision

**1. Extend the single-shot `Completer` to the two cloud CLI brains** by running them in their
existing **print modes** — `claude -p <prompt>` and `agy --print <prompt> --model <model>` — and
returning stdout as the completion. No worktree, no tools, no gate, no branch (the `Completer`
contract). These are the same binaries and the same auth the coding executors already use; the
completer is the executor minus the worktree/branch/commit machinery.

- **Claude completer:** reuses the `claudeEnv` auth construction (ANTHROPIC_API_KEY /
  CLAUDE_CODE_OAUTH_TOKEN, or ANTHROPIC_BASE_URL + `ANTHROPIC_AUTH_TOKEN` placeholder for local
  translation-proxy entries), mirroring `NewClaudeCLIFromEntry`. Runs in a temp HOME/dir.
- **`agy` completer:** subscription/OAuth via the inherited `~/.antigravity` keyring (empty
  `SecretRef`), mirroring `AntigravityCLI`; passes `--model <entry.ModelID>`.
- Each completer takes a `commandCreator` factory so tests stub the subprocess (mirrors the executor
  test seam). `HarnessCodexCLI` and the deprecated `HarnessGeminiCLI` stay fail-closed for now.

**2. Add an `ask` CLI subcommand** — `agent-builder ask [--entry <id>] <prompt>` — that selects a
registry brain, constructs its `Completer`, and prints the raw answer to stdout. Explicit
`--entry <id>` selects a specific brain (so an operator can exercise local / Claude / `agy`
independently); with no `--entry`, it falls back to the router's default selection (or the synthetic
default Claude entry when the registry is empty). This is the first **general (non-coding)
entrypoint**: a goal answered, not a branch produced.

**3. Keep the boundary invariant.** The `Completer` lives in `internal/executor`;
`internal/orchestrator` and its planner never import it directly (F-010 / F-014 unchanged). `ask`
wiring lives in `internal/cli`.

## Why this shape

- **Reuse over rebuild** — print mode + existing auth means no new transport, no new credential
  path. The composability story holds: a new *capability* (answer a question) composed from existing
  seams, not a new feature grown into the assembler.
- **Single-shot is low-risk** — no tool calls, no file edits, no branch; the highest-trust executor
  mode. It is also where the marginal local models are most reliable (a question, not an agentic
  loop).
- **Explicit `--entry`** is the minimum needed to route to a chosen brain for verification; the
  router default keeps the common case ergonomic.

## Consequences

- A general goal can be answered across all three brains. This is the foundation the rest of the
  forward arc (skill system, skill-writing loop) builds on — a non-coding execution mode exists.
- `ask` does **not** go through the verification gate (there is nothing to verify in a single text
  answer) — by design; it is not the coding path. The two human gates still apply to *action*-taking
  goals via `orchestrate`/`run`; `ask` is read-only inference.
- The deprecated `gemini` and the unused `codex` single-shot paths remain fail-closed; extending
  them is deferred (no live `gemini` binary; Codex not a canonical brain).

## Alternatives considered

- **A new general orchestrate goal-type that returns an answer.** Heavier — couples to the
  multi-goal control plane and the approval/dispatch machinery. `ask` is the thin first cut; a
  general path *through* `orchestrate` is a later slice.
- **Reimplement reasoning / a chat loop in Go.** Rejected — violates "compose a brain, don't
  reimplement reasoning." Single-shot print mode delegates entirely to the brain CLI.

---

## Amendment (Task 143): On-disk OAuth fallback for subscription logins

**Date:** 2026-07-01
**Relates to:** ADR 033 (subscription OAuth auth mode), task 101 (`claudeEnv` HOME isolation)

### Context

The Claude completer isolates `HOME` (temp dir) to prevent the child process from reading host
credential files. For a user logged in via subscription OAuth (credentials stored in
`${HOME}/.claude/.credentials.json`), the completer fails auth unless the operator manually
exports `CLAUDE_CODE_OAUTH_TOKEN` to the environment — `ask --entry claude-oauth` cannot access
on-disk login even though other brains (agy with inherited real HOME, ollama with no auth need)
work transparently. For a general agent whose premise is "hand it a goal," requiring the operator
to export a token to use their own logged-in Claude is a usability gap on the general (non-coding)
path.

### Decision

**The completer resolves cloud credentials from a chained source (env → disk)** when constructing
the `NewClaudeCLI` for cloud entries:

1. **Env credentials checked first (ADR 033 preserved):** `ANTHROPIC_API_KEY` and
   `CLAUDE_CODE_OAUTH_TOKEN` are read from the process environment as before. If either is
   non-empty, it is used and disk is **not** consulted (env tokens are never overridden).

2. **Disk credentials consulted only when env is empty:** When both env vars are empty, the parent
   Go process reads `${HOME}/.claude/.credentials.json` (the real HOME, before isolation) and
   extracts `claudeAiOauth.accessToken` as the OAuth token. The token is injected into the child
   env via `CLAUDE_CODE_OAUTH_TOKEN`, and `claudeEnv` constructs the isolated child process as
   before.

3. **Graceful absence:** Missing/malformed/empty disk file → empty tokens, no error. This makes
   the fallback transparent for operators without on-disk logins and preserves backwards
   compatibility.

4. **Implementation:** A new `DiskOAuthSecretSource` reads the disk file. A `ChainedSecretSource`
   wraps env and disk sources and implements the precedence: env tokens returned when present;
   disk tokens returned when env is empty. The completer wiring replaces the bare
   `secrets.NewEnvSecretSource()` with the chained source. Local entries (empty `SecretRef`)
   continue to use the translation-proxy path and ignore the disk source.

### Why this shape

- **Reuse of HOME isolation:** The disk read happens in the parent process (before `claudeEnv`
  strips HOME), so the child still runs isolated and cannot access host config/cache.
- **ADR 033 precedence preserved:** Explicit env tokens always win; disk is only a fallback.
- **Graceful degradation:** Operators without on-disk login experience no regression (no error,
  empty token, continues to use env-only auth as before).
- **Security boundary:** Only `accessToken` is read, never the refresh token. Token lives in env,
  never in argv, and is redacted on CLI failure.

### Consequences

- `ask --entry claude-oauth` can now answer questions when the user has a real Claude Pro/Max
  subscription and no env token is exported (the 2026-06-30 blocker). The general-agent usability
  story is complete for all three brains.
- Two Secret sources are chained for each cloud completer construction (env + disk). The chain is
  lightweight (sequential calls; no I/O until disk source is consulted only when env is empty).
  Disk I/O is still one-shot (one `ReadFile` call per completer; no caching across prompts).
- The on-disk OAuth fallback applies **only** to the completer path (`ask`); the coding-path
  executor (`NewClaudeCLIFromEntry`) continues to use the env source alone (unchanged).
