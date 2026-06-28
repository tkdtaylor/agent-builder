# Task 090: Gemini harness adapter

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** completed

## Goal

Implement `internal/executor/gemini_cli.go` — a `supervisor.Executor` adapter for the
Google Gemini CLI. Gemini is a genuinely new harness (ADR 043): it has its own
subprocess interface, its own wire format, and its own auth (Gemini API key) — it
does not reuse the Claude CLI harness.

The adapter follows the same pattern as `claude_cli.go` and `codex_cli.go`.

## Context

ADR 043 identifies Gemini as one of two genuinely new harness adapters. The `GeminiCLI`
adapter receives a `RegistryEntry` (or subset config) and a `secrets.SecretSource`.
It resolves `entry.SecretRef` via `secretSource.NamedProviderToken` to get the Gemini
API key, then injects it into the subprocess env.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                                      | Priority  |
|------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-090-01 | `executor.GeminiCLI` implements `supervisor.Executor`. `executor.NewGeminiCLI(entry RegistryEntry, secretSource secrets.SecretSource)` constructs it. Invokes the `gemini` CLI subprocess with the model, worktree, and auth token injected via env. Returns a `Result` with branch name on success. | must have |
| REQ-090-02 | Auth token is resolved via `secretSource.NamedProviderToken(entry.SecretRef)` at dispatch time. `ErrSecretNotFound` causes `Run` to error before subprocess launch. | must have |
| REQ-090-03 | Subprocess non-zero exit surfaces as an executor error containing stderr. `Result.OK == false`. | must have |
| REQ-090-04 | F-003 supervisor isolation passes after this task: `make fitness-supervisor-isolation` exits 0. | must have |

## Readiness gate

- [x] Test spec `090-gemini-harness-adapter-test-spec.md` exists (written first)
- [x] Task 087 merged (registry type)
- [x] Task 088 merged (`NamedProviderToken`)
- [x] `make check` green before starting

## Acceptance criteria

- [x] [REQ-090-01] TC-090-01: `var _ supervisor.Executor = (*executor.GeminiCLI)(nil)` compiles
- [x] [REQ-090-01] TC-090-02: stub subprocess invoked with auth token, model, worktree; returns OK result
- [x] [REQ-090-02] TC-090-03: `ErrSecretNotFound` → `Run` errors before subprocess invocation
- [x] [REQ-090-03] TC-090-04: subprocess exit 1 → `Run` returns error; `Result.OK == false`
- [x] [REQ-090-04] TC-090-05: `make fitness-supervisor-isolation` → `PASS`; `make check` → `All checks passed.`

## Verification plan

- **Highest level achievable:** L5 — unit tests with stub subprocess.
- **Harness command:**
  ```
  go test -count=1 ./internal/executor/...
  make fitness-supervisor-isolation
  make check
  ```
- **L6 live (operator-run):** `gemini` CLI on PATH + valid Gemini API key.

## Out of scope

- Router selection (task 092).
- End-to-end recipe→Gemini flow (task 095).

## Dependencies

- Task 087 (registry type).
- Task 088 (vault-brokered per-provider auth).
- Informs: task 092 (router constructs adapter from registry entry); task 095.
