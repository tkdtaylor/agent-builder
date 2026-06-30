# Task 136 — agy single-shot completer (`agy --print --model`)

**Status:** backlog
**Spec:** `docs/tasks/test-specs/136-agy-single-shot-completer-test-spec.md`
**Relates to:** ADR 059 (general single-shot path), ADR 057 (Antigravity harness), task 133/134 (`AntigravityCLI` executor).

## Goal

Extend `executor.CompleterForEntry` so `HarnessAntigravityCLI` returns a working single-shot
completer. It runs `agy --print <prompt> --model <model>` and returns stdout — the agy executor minus
worktree/branch/commit. Second cloud brain on the general non-coding path (ADR 059).

## Scope

- **`internal/executor/completer.go`** (or `agy_completer.go`): add an `antigravityCompleter`
  implementing `Completer`; switch `HarnessAntigravityCLI` in `CompleterForEntry` to construct it.
- **Invocation:** `agy --print <prompt> --model <entry.ModelID>` (no `--add-dir` /
  `--dangerously-skip-permissions` — there is no worktree and no tool use in single-shot).
  **Confirm the exact arg order live** (`agy --help`, a real `agy --print` call) before pinning stub
  assertions — same caution as task 133.
- **Auth:** subscription/OAuth only (empty `SecretRef`); inherit the process env so `agy` reads its
  `~/.antigravity` keyring. Run in a temp dir; no key injected.
- **Test seam:** a `commandCreator` factory (mirror `AntigravityCLI`) for subprocess stubbing.
- **Sanitization:** failure errors run through the existing agy output sanitizer.
- **Spec (same commit):** `docs/spec/interfaces.md` — dispatch contract lists `HarnessAntigravityCLI
  → antigravityCompleter`.

## Out of scope

- The `ask` entrypoint (task 137). Codex/Gemini stay fail-closed.

## Verification plan

- **Highest level now: L2/L3** (subprocess stubbed). L6 (live `agy --print`) via the task-137 smoke.
- **L2:** `go test -race -count=1 ./internal/executor/...` — stub asserts argv includes `--print`,
  the prompt, `--model`, the model id; returns canned stdout; `Complete` returns it trimmed; non-zero
  exit → sanitized error.
- **L3:** `make check` green.
- **Producer→consumer:** `CompleterForEntry(agy entry)` → `antigravityCompleter.Complete` → `agy
  --print` subprocess → trimmed stdout.

## Boundaries

- Single-shot contract: no worktree/branch/gate. `Completer` stays in `internal/executor` (F-010/F-014).
- Subscription-only; never inject a placeholder key.
