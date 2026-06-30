# Task 135 — Claude single-shot completer (`claude -p`)

**Status:** backlog
**Spec:** `docs/tasks/test-specs/135-claude-single-shot-completer-test-spec.md`
**Relates to:** ADR 059 (general single-shot execution path), ADR 053 (Completer seam), task 022/091 (`ClaudeCLI` executor + local-proxy auth).

## Goal

Extend `executor.CompleterForEntry` so `HarnessClaudeCLI` returns a working single-shot completer
instead of `ErrSingleShotUnsupported`. The completer runs `claude -p <prompt>` and returns stdout —
the Claude executor minus worktree/branch/commit. This is the first cloud brain on the general
non-coding path (ADR 059 decision 1).

## Scope

- **`internal/executor/completer.go`** (or a new `claude_completer.go`): add a `claudeCompleter`
  implementing `Completer`; switch `HarnessClaudeCLI` in `CompleterForEntry` to construct it.
- **Auth** mirrors `NewClaudeCLIFromEntry`: cloud entry (`SecretRef != ""`) → `ProviderToken()`;
  local entry (`SecretRef == ""`) → `ANTHROPIC_BASE_URL = entry.Endpoint` + the
  `LocalProxyAuthPlaceholder` as `ANTHROPIC_AUTH_TOKEN`. Build the subprocess env via the existing
  `claudeEnv` helper with a fresh temp HOME; run in a temp dir.
- **Invocation:** `claude -p <prompt>` (mirror `claude_cli.go:219`). Capture stdout; return trimmed.
- **Test seam:** a `commandCreator` factory field (mirror the executor) so tests stub the subprocess.
- **Sanitization:** subprocess-failure errors run through `sanitizeCLIOutput` (no token leak).
- **Spec (same commit):** `docs/spec/interfaces.md` — the `Completer` dispatch contract now lists
  `HarnessClaudeCLI → claudeCompleter` (and update the "only Ollama-native" note).

## Out of scope

- `agy` completer (task 136); the `ask` entrypoint (task 137). Codex/Gemini stay fail-closed.

## Verification plan

- **Highest level now: L2/L3** (subprocess stubbed). L6 (live `claude -p`) is exercised by the task-137 smoke.
- **L2:** `go test -race -count=1 ./internal/executor/...` — stubbed cmdFactory asserts argv `[claude, -p, <prompt>]`, returns canned stdout, `Complete` returns it trimmed; auth-env assertions (cloud vs local); non-zero exit → sanitized error.
- **L3:** `make check` green (incl. F-010/F-014 boundary + supervisor isolation fitness).
- **Producer→consumer:** `CompleterForEntry(claude entry)` → `claudeCompleter.Complete` → `claude -p` subprocess → trimmed stdout.

## Boundaries

- `Completer` stays in `internal/executor`; orchestrator/planner never import it (F-010/F-014).
- No worktree, no branch, no gate — single-shot contract (ADR 053).
