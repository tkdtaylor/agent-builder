# Task 089: Codex harness adapter

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Implement `internal/executor/codex_cli.go` — a `supervisor.Executor` adapter for the
Codex CLI. Codex is a genuinely new harness (ADR 043): it has its own subprocess
interface, its own wire format, and its own auth (OpenAI/Codex API key) — it does not
reuse the Claude CLI harness.

The adapter follows the same pattern as `claude_cli.go`: subprocess invocation,
output capture, branch name extraction, and auth token injection via env (resolved
from the entry's `SecretRef` using `secrets.NamedProviderToken`).

## Context

ADR 043 identifies Codex and Gemini as the only two genuinely new harness adapters.
The Claude CLI is the existing harness; the local entry reuses it (task 091). This
task adds Codex.

The `CodexCLI` adapter receives a `RegistryEntry` (or a subset config) and a
`secrets.SecretSource`. It resolves `entry.SecretRef` via `secretSource.NamedProviderToken`
to get the Codex API key, then injects it into the subprocess env.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                                      | Priority  |
|------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-089-01 | `executor.CodexCLI` implements `supervisor.Executor`. `executor.NewCodexCLI(entry RegistryEntry, secretSource secrets.SecretSource)` constructs it. Invokes the `codex` CLI subprocess with the model, worktree, and auth token injected via env. Returns a `Result` with the produced branch name on success. | must have |
| REQ-089-02 | Auth token is resolved via `secretSource.NamedProviderToken(entry.SecretRef)` at dispatch time. A `ErrSecretNotFound` error causes `Run` to return a descriptive error before launching the subprocess. | must have |
| REQ-089-03 | Subprocess non-zero exit is surfaced as an executor error containing stderr. `Result.OK == false`. | must have |
| REQ-089-04 | F-003 supervisor isolation passes after this task: `make fitness-supervisor-isolation` exits 0. `internal/supervisor` does not import `internal/executor`. | must have |

## Readiness gate

- [x] Test spec `089-codex-harness-adapter-test-spec.md` exists (written first)
- [ ] Task 087 merged (registry entry type — provides `RegistryEntry` struct)
- [ ] Task 088 merged (`NamedProviderToken` on `SecretSource`)
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-089-01] TC-089-01: `var _ supervisor.Executor = (*executor.CodexCLI)(nil)` compiles
- [ ] [REQ-089-01] TC-089-02: stub subprocess invoked with auth token in env, model ID, worktree; returns OK result with branch
- [ ] [REQ-089-02] TC-089-03: `ErrSecretNotFound` → `Run` errors before subprocess invocation
- [ ] [REQ-089-03] TC-089-04: subprocess exit 1 → `Run` returns error; `Result.OK == false`
- [ ] [REQ-089-04] TC-089-05: `make fitness-supervisor-isolation` → `PASS fitness-supervisor-isolation: …`; `make check` → `All checks passed.`

## Verification plan

- **Highest level achievable:** L5 — unit tests with stub subprocess.
- **Harness command:**
  ```
  go test -count=1 ./internal/executor/...
  make fitness-supervisor-isolation
  make check
  ```
  Expected:
  - Unit tests → `ok github.com/tkdtaylor/agent-builder/internal/executor`
  - Fitness → `PASS fitness-supervisor-isolation: …`
  - `make check` → `All checks passed.`
- **L6 live (operator-run):** `codex` CLI on PATH + valid Codex API key; operator
  exercises against a real worktree. Not automated; note in verify commit.

## Out of scope

- Router selection (task 092).
- End-to-end recipe→Codex flow (task 095).
- Codex CLI flag names are assumed from published docs; if the CLI API changes, the
  adapter must be updated. The stub test captures the conceptual contract.

## Dependencies

- Task 087 (registry type).
- Task 088 (vault-brokered per-provider auth — `NamedProviderToken`).
- Informs: task 092 (router constructs the adapter from a registry entry); task 095.
