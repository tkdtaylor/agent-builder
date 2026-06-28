# Task 101: Local-proxy auth fix (placeholder API key + local-only config gate)

**Status:** backlog
**Priority:** must-have (blocks pure-local deployment)
**Created:** 2026-06-28
**Paired spec:** `docs/tasks/test-specs/101-local-proxy-auth-fix-test-spec.md`
**Review flags:** spec-verifier + security-auditor (executor auth path — see Security invariant below)

## Goal

Fix two confirmed bugs that blocked the pure-local (translation-proxy) deployment from
driving the Claude Code CLI during the live L6 run of task 094 on 2026-06-28. Both
bugs are reproducible in isolation.

**FINDING 1 (the blocker):** For a local registry entry (`entry.SecretRef == ""`),
`claudeEnv` in `internal/executor/claude_cli.go` injects NO `ANTHROPIC_API_KEY` AND
creates a fresh temp `HOME` (no stored OAuth). The Claude Code CLI has zero credentials
and refuses to run, printing `"Not logged in · Please run /login"`. The LiteLLM
translation proxy does not validate the key value — it already returned 7×200 OK
before the CLI failure. Fix: inject a fixed placeholder sentinel as `ANTHROPIC_API_KEY`
for local entries. The placeholder satisfies the CLI's local auth check; the proxy
ignores it.

**FINDING 2:** `ConfigFromEnv` in `internal/runtime/run.go` (line ~224)
unconditionally errors `"run config: missing at least one of ANTHROPIC_API_KEY or
CLAUDE_CODE_OAUTH_TOKEN"` even when only local entries are enabled. A pure-local
operator must not need a dummy cloud credential to start the orchestrator.

## Requirements

### REQ-101-01 — Placeholder sentinel for local entries in claudeEnv

`claudeEnv` (in `internal/executor/claude_cli.go`) MUST, when `baseURL != ""`
(local entry mode):
1. Inject `ANTHROPIC_API_KEY` set to the value of a named package-level constant
   `LocalProxyAuthPlaceholder` (value: `"local-proxy-no-auth"` or a similarly
   unambiguous fixed string).
2. Continue injecting `ANTHROPIC_BASE_URL=baseURL` (unchanged).
3. NOT inject `CLAUDE_CODE_OAUTH_TOKEN`.
4. NOT inject the operator's real `authToken` or `oauthToken` arguments as
   `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN`.

The sentinel MUST be:
- Non-empty (satisfies the CLI's auth check).
- A fixed constant, not derived from any operator credential.
- Documented at the definition site explaining it is a placeholder for the local-proxy
  path and is not a real Anthropic credential.

The existing cloud-entry path (when `baseURL == ""`) MUST be unchanged: inject exactly
one real credential (OAuth preferred over API key per ADR 033), no placeholder.

### REQ-101-02 — Local-only config accepted without cloud credentials

`configFromEnvWithSource` (in `internal/runtime/run.go`) MUST:
1. Call `registry.LoadFromEnv()` to determine which entries are enabled.
2. If the call returns a non-nil error, apply the existing strict gate
   (fail-closed: cloud credential check still enforced).
3. If the call succeeds and ALL enabled entries have `SecretRef == ""` (all local),
   skip the `"missing ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN"` check.
4. If the call succeeds but ANY enabled entry has a non-empty `SecretRef` (cloud
   entry), enforce the cloud-credential check exactly as before.
5. If the call succeeds but returns an empty slice (no entries enabled), enforce the
   cloud-credential check (fail-closed: no evidence of a local-only config).

`registry.LoadFromEnv()` is called unconditionally within `configFromEnvWithSource`
only to inspect entry types; if the registry loader itself fails (e.g. malformed
tier), the config parse still uses the fail-closed path. The returned entries are
not stored in `Config` — that is the router's job.

### REQ-101-03 — End-to-end intent documented and L6-operator-confirmable

The combination of REQ-101-01 and REQ-101-02 must be sufficient for a pure-local
operator to:
1. Start `agent-builder run` without setting `ANTHROPIC_API_KEY` or
   `CLAUDE_CODE_OAUTH_TOKEN` in the host environment.
2. Have the Claude Code CLI subprocess start and authenticate against the translation
   proxy at `ANTHROPIC_BASE_URL` (using the placeholder key).

The L6 operator confirmation path is: re-run the task 094 live round-trip
(LiteLLM proxy + qwen + Claude Code CLI) after this fix is merged. See
TC-101-06 and TC-094-02.

## Security invariant (load-bearing)

The invariant "no real cloud credential reaches a local entry" must be preserved:

- `LocalProxyAuthPlaceholder` MUST NOT be derived from `authToken` or `oauthToken`.
- The test TC-101-02 MUST assert `injectedKey != realOperatorKey` explicitly.
- spec-verifier AND security-auditor must both review this task before merge.
- If the sentinel value ever needs to change, it is a non-breaking change (the proxy
  ignores the value) but must update the constant, any documentation referencing the
  old value, and the test assertions.

## Acceptance criteria

The task is done when:

1. `executor.LocalProxyAuthPlaceholder` is defined as a named constant in
   `internal/executor/claude_cli.go`.
2. `claudeEnv` injects `ANTHROPIC_API_KEY=LocalProxyAuthPlaceholder` for local
   entries and does not inject `CLAUDE_CODE_OAUTH_TOKEN` or the operator's real tokens.
3. `configFromEnvWithSource` skips the cloud-credential check when all enabled
   registry entries are local (empty `SecretRef`).
4. `go test -count=1 ./internal/executor/... ./internal/runtime/...` passes, with
   TC-101-01 through TC-101-06 all asserting the specific values described in the
   spec (not smoke tests).
5. `make check` passes (all fitness checks green, including `fitness-supervisor-isolation`).
6. `docs/spec/configuration.md` is updated in the same commit:
   - `ANTHROPIC_API_KEY` row: note that for local entries a placeholder sentinel is
     injected (not the real operator key); cite `LocalProxyAuthPlaceholder`.
   - `CLAUDE_CODE_OAUTH_TOKEN` row: note it is never injected for local entries.
   - Executor Registry Configuration section: note that a pure-local registry (all
     entries with empty `SecretRef`) requires no `ANTHROPIC_API_KEY` or
     `CLAUDE_CODE_OAUTH_TOKEN` in the host environment.
7. `docs/spec/behaviors.md` or `docs/spec/interfaces.md` is updated if the executor
   subprocess-env contract is stated there (check the `ANTHROPIC_BASE_URL` entry in
   `configuration.md` — update its description to reflect the placeholder injection).
8. The task 094 coverage-tracker row is NOT promoted to ✅ in this commit (that
   requires a separate L6 operator run per AGENTS.md rules).

## Verification plan

| Level | Gate | Command | Expected |
|-------|------|---------|----------|
| L2 | Unit tests (claudeEnv + ConfigFromEnv) | `go test -count=1 ./internal/executor/... ./internal/runtime/...` | both `ok` |
| L3 | Fitness + import graph | `make check` | `All checks passed.` |
| L5/L6 | Live round-trip (deferred, operator-run) | Re-run task 094 live round-trip (LiteLLM + qwen + Claude CLI) | branch produced, gate green |

**Highest CI-achievable level:** L2/L3. L5/L6 is operator-deferred (live hardware,
LiteLLM proxy, Ollama on RTX 4060 — same environment as the 2026-06-28 L6 run).

## Modules touched

- `internal/executor/claude_cli.go` — add `LocalProxyAuthPlaceholder` const; fix
  `claudeEnv` local-entry branch.
- `internal/runtime/run.go` — fix `configFromEnvWithSource` cloud-credential gate.
- `internal/executor/claude_cli_test.go` — add TC-101-01, TC-101-02, TC-101-03,
  TC-101-06 (L2 part).
- `internal/runtime/run_test.go` — add TC-101-04, TC-101-05.
- `docs/spec/configuration.md` — update as described in acceptance criterion 6.
- (Conditional) `docs/spec/behaviors.md` or `docs/spec/interfaces.md` — update if
  executor-env contract appears there.

This task touches exactly two production code modules (executor + runtime), within the
two-module limit per AGENTS.md design principles.

## Dependencies

- Task 091 (MERGED) — established `NewClaudeCLIFromEntry` and the local-entry
  registry pattern. This task extends 091's work; it does not replace it.
- Task 094 (🟡) — the live round-trip that surfaced both bugs. L6 confirmation of
  this task closes REQ-094-02 (pending operator re-run).
- Task 095 (MERGED) — router wires the registry at dispatch time; the
  `registry.LoadFromEnv()` call added by this task in `configFromEnvWithSource` uses
  the same function the router already depends on.

## Out of scope

- Vault-brokered credentials for local entries (none needed; the placeholder is a constant).
- Changes to the router, registry type, or entry loading logic.
- A new harness type for local models.
- Promoting task 094's tracker row to ✅ (separate operator run required).
