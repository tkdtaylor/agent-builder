# Test spec — Task 089: Codex harness adapter

**Linked task:** `docs/tasks/backlog/089-codex-harness-adapter.md`
**Written:** 2026-06-27
**Status:** ready

## Context

ADR 043 identifies Codex as one of two genuinely new harness adapters (the other is
Gemini, task 090). The Codex CLI is its own harness: it has its own wire format, its
own auth (OpenAI/Codex API key), and its own subprocess interface — it does not reuse
the Claude CLI harness.

This task introduces `internal/executor/codex_cli.go`, a `supervisor.Executor`
implementation that invokes the `codex` CLI subprocess, captures the branch it
produces, and handles auth via the `SecretRef` resolved by `secrets.NamedProviderToken`
(task 088).

The implementation pattern mirrors `internal/executor/claude_cli.go`: subprocess
invocation, output capture, branch extraction, auth token injection via env.

## Requirements coverage

| Req ID     | Test cases                     | Covered? |
|------------|--------------------------------|----------|
| REQ-089-01 | TC-089-01, TC-089-02           | yes      |
| REQ-089-02 | TC-089-03                      | yes      |
| REQ-089-03 | TC-089-04                      | yes      |
| REQ-089-04 | TC-089-05                      | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-089-01 — CodexCLI executor satisfies supervisor.Executor interface

- **Requirement:** REQ-089-01
- **Level:** L2 (compile-time + unit test)
- **Test file:** `internal/executor/codex_cli_test.go`

**Input:** Compile-time assertion:
```go
var _ supervisor.Executor = (*executor.CodexCLI)(nil)
```

**Expected output:**
- Compiles without error.
- `executor.NewCodexCLI(cfg, secretSource)` returns a value satisfying `supervisor.Executor`.
- `cfg` carries the `RegistryEntry` fields needed: `ModelID`, `Endpoint`, `SecretRef`.

---

### TC-089-02 — CodexCLI invokes the codex subprocess with correct argv and env

- **Requirement:** REQ-089-01
- **Level:** L2 (unit test with stub subprocess)
- **Test file:** `internal/executor/codex_cli_test.go`

**Input:** Construct a `CodexCLI` with a fake `secretSource` that returns
`"sk-test-codex-key"` for the configured `SecretRef`. Inject a fake subprocess
launcher (same `AGENT_BUILDER_EXEC_BOX_LAUNCHER` pattern as `claude_cli.go`).
Call `executor.Run(ctx, task, worktree)`.

**Expected output:**
- The stub subprocess is invoked with at least:
  - `OPENAI_API_KEY=sk-test-codex-key` (or `CODEX_API_KEY` — whichever the Codex CLI
    uses; implementation picks the correct var name for the CLI version targeted).
  - The worktree path as an argument (or current-directory equivalent).
  - The model ID from the config.
- The stub subprocess exits 0 and outputs a branch name in the expected format.
- `executor.Run` returns `(Result{Branch: "task/089-test-branch", OK: true}, nil)`.

**Note:** If the exact Codex CLI API (flag names, output format) is not yet confirmed
at implementation time, the test should capture argv/env and assert the conceptual
contract (auth token injected, model set, worktree passed). The implementation may
need updating once a live CLI is confirmed.

---

### TC-089-03 — Auth token is resolved via NamedProviderToken, not hardwired

- **Requirement:** REQ-089-02
- **Level:** L2 (unit test)
- **Test file:** `internal/executor/codex_cli_test.go`

**Input:** Construct a `CodexCLI` with two fake `secretSource` variants:
- Variant A: `NamedProviderToken("codex-openai-token")` → `"sk-test-key"`
- Variant B: `NamedProviderToken("codex-openai-token")` → `("", ErrSecretNotFound)`

**Expected output:**
- Variant A: subprocess is invoked with the auth token in env; `Run` returns OK.
- Variant B: `Run` returns a non-nil error before invoking any subprocess; the error
  names the failed secret resolution.

**Rationale:** This test proves the auth token is resolved per-entry via `SecretRef`,
not hardwired. Revoking the Codex key (making it return `ErrSecretNotFound`) causes
the run to fail fast before the subprocess is launched.

---

### TC-089-04 — Subprocess non-zero exit surfaces as executor error

- **Requirement:** REQ-089-03
- **Level:** L2 (unit test)
- **Test file:** `internal/executor/codex_cli_test.go`

**Input:** Configure the stub subprocess to exit 1 with stderr `"Codex API error"`.

**Expected output:**
- `executor.Run` returns a non-nil error containing the stderr text.
- `Result.OK == false`.
- No branch name is returned.

---

### TC-089-05 — F-003 supervisor isolation preserved after adding CodexCLI

- **Requirement:** REQ-089-04
- **Level:** L3 (fitness check)
- **Test file / harness:** `make fitness-supervisor-isolation`

**Input:** `make fitness-supervisor-isolation` after `CodexCLI` is added to
`internal/executor/`.

**Expected output:**
- The fitness check exits 0 with `PASS fitness-supervisor-isolation: …`.
- `internal/supervisor` does NOT import `internal/executor` or `codex_cli.go`.
- The import direction remains: `runtime` → `executor` → `supervisor` (never the reverse).

---

## Verification plan

- **Highest level achievable:** L5 — unit tests with stub subprocess. Live Codex run
  (L6) requires an OpenAI/Codex API key and is operator-run; it is not automated here.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/executor/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/executor`
- **L3 fitness:**
  ```
  make fitness-supervisor-isolation
  ```
  Expected: `PASS fitness-supervisor-isolation: …`
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L6 live (operator-run, not automated):** requires `codex` CLI on PATH and a valid
  Codex API key. Operator sets `AGENT_BUILDER_SECRET_CODEX_TOKEN=<real-key>` and
  exercises the adapter against a real worktree.

## Out of scope

- The router selecting the Codex entry (task 092).
- End-to-end flow with a recipe routing to Codex (task 095).
- Codex CLI availability as a hard gate for CI (the CLI may not be installed; tests
  use a stub subprocess, not the real CLI).
