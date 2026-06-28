# Test spec — Task 101: Local-proxy auth fix (placeholder API key + local-only config gate)

**Linked task:** `docs/tasks/backlog/101-local-proxy-auth-fix.md`
**Written:** 2026-06-28
**Status:** ready

## Context

Two confirmed bugs were found during a live L6 run of task 094 on 2026-06-28 using a
LiteLLM translation proxy → qwen local model → Claude Code CLI stack. The proxy
returned 7×200 OK but the Claude Code CLI refused to run, printing:

```
Not logged in · Please run /login
```

Root cause 1 (FINDING 1): `claudeEnv` with `baseURL != ""` (local entry) creates a
fresh temp `HOME` (no stored OAuth) AND injects NO `ANTHROPIC_API_KEY`. The CLI has
zero credentials and refuses to talk to the custom `ANTHROPIC_BASE_URL`, even though
the translation proxy does not validate the key value. The fix is to inject a fixed
placeholder sentinel as `ANTHROPIC_API_KEY` for local entries. The proxy ignores the
key value; its presence satisfies the CLI's local auth-state check.

Root cause 2 (FINDING 2): `ConfigFromEnv` in `internal/runtime/run.go` unconditionally
errors when neither `ANTHROPIC_API_KEY` nor `CLAUDE_CODE_OAUTH_TOKEN` is set, even
when only local entries are enabled. A pure-local operator must not need a dummy cloud
credential to start the orchestrator.

**Security invariant (load-bearing):** For local entries, the placeholder injected as
`ANTHROPIC_API_KEY` MUST be a fixed sentinel that is clearly not a real Anthropic
key (e.g. `"local-proxy-no-auth"`). The operator's real `authToken` / `oauthToken`
MUST NOT be forwarded to a local entry. These are two separate claims that must both
hold.

**Design decision for FINDING 2 — how ConfigFromEnv knows it's local-only:**
`configFromEnvWithSource` calls `registry.LoadFromEnv()` (the same function the router
calls at dispatch time) to inspect which entries are enabled. If `LoadFromEnv` returns
an error, the existing strict gate applies (fail-closed). If `LoadFromEnv` succeeds
and ALL enabled entries have `SecretRef == ""` (i.e. every enabled entry is local),
the cloud-credential check is skipped. If ANY enabled entry has a non-empty `SecretRef`
(cloud entry), the check is enforced as before. If `LoadFromEnv` returns an empty
slice (no entries at all), the existing behavior applies (cloud credential check
enforced — no local entries means no evidence of a local-only config). This keeps
`ConfigFromEnv` and the loader in sync by construction without introducing a new env
var or a new interface.

**Why not a new env var?** A new `AGENT_BUILDER_LOCAL_ONLY=true` var would duplicate
the information already in `AGENT_BUILDER_REGISTRY_*_ENABLED` + `*_SECRET_REF` and
would drift. Consulting the loader directly keeps the single source of truth.

**Const name for the sentinel:** `LocalProxyAuthPlaceholder = "local-proxy-no-auth"`
defined in `internal/executor/claude_cli.go` alongside the existing auth-env constants.
The value is deliberately non-secret, documented, and obviously not a real credential.

## Requirements coverage

| Req ID     | Test cases                     | Covered? |
|------------|--------------------------------|----------|
| REQ-101-01 | TC-101-01, TC-101-02, TC-101-03 | yes     |
| REQ-101-02 | TC-101-04, TC-101-05           | yes      |
| REQ-101-03 | TC-101-06                      | yes      |

---

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-101-01 — claudeEnv (local mode) injects placeholder sentinel as ANTHROPIC_API_KEY

- **Requirement:** REQ-101-01
- **Level:** L2 (unit test)
- **Test file:** `internal/executor/claude_cli_test.go`

**Input:** Call `claudeEnv(baseEnv, authToken="real-operator-key", oauthToken="real-oauth",
baseURL="http://localhost:8080", tempHome="/tmp/h")`.

The `baseEnv` slice contains arbitrary prior values for `ANTHROPIC_API_KEY` and
`CLAUDE_CODE_OAUTH_TOKEN` (to verify they are stripped).

**Expected output:**
- The returned `env` slice contains exactly one entry matching
  `"ANTHROPIC_API_KEY=" + executor.LocalProxyAuthPlaceholder`.
- The value of `ANTHROPIC_API_KEY` equals `executor.LocalProxyAuthPlaceholder`
  (assert `==`, not merely non-empty).
- `executor.LocalProxyAuthPlaceholder` is a non-empty named constant (`"local-proxy-no-auth"`
  or equivalent fixed sentinel).
- The returned env also contains `"ANTHROPIC_BASE_URL=http://localhost:8080"`.
- The returned env does NOT contain any entry with the prefix `"CLAUDE_CODE_OAUTH_TOKEN="`.
- Neither `"real-operator-key"` nor `"real-oauth"` appears anywhere in the returned env
  (assert via `strings.Join(env, " ")` contains neither string).

**Rationale:** The placeholder satisfies the CLI's auth check; the real credentials
are not forwarded to the local entry.

---

### TC-101-02 — claudeEnv (local mode) placeholder is distinct from any real operator token

- **Requirement:** REQ-101-01
- **Level:** L2 (unit test)
- **Test file:** `internal/executor/claude_cli_test.go`

**Input:** Call `claudeEnv(baseEnv, authToken="sk-ant-realkey", oauthToken="",
baseURL="http://localhost:8080", tempHome="/tmp/h")`.

**Expected output:**
- The `ANTHROPIC_API_KEY` value in the returned env equals
  `executor.LocalProxyAuthPlaceholder` — NOT `"sk-ant-realkey"`.
- Assert `apiKeyValue != "sk-ant-realkey"` (the operator's real key was NOT forwarded).
- Assert `apiKeyValue == executor.LocalProxyAuthPlaceholder`.

**Rationale:** The invariant "no real cloud credential reaches a local entry" is
explicitly checked as a negative assertion, not just implied by the positive one.

---

### TC-101-03 — claudeEnv (cloud mode) is unchanged: no placeholder, real credential injected

- **Requirement:** REQ-101-01
- **Level:** L2 (unit test — regression)
- **Test file:** `internal/executor/claude_cli_test.go`

**Input:** Call `claudeEnv(baseEnv, authToken="sk-ant-prod", oauthToken="", baseURL="",
tempHome="/tmp/h")` (empty baseURL = cloud mode).

**Expected output:**
- The returned env contains `"ANTHROPIC_API_KEY=sk-ant-prod"` (exact real value).
- The returned env does NOT contain `executor.LocalProxyAuthPlaceholder`.
- The returned env does NOT contain `"ANTHROPIC_BASE_URL="` (no base-URL set for cloud).
- `CLAUDE_CODE_OAUTH_TOKEN` is absent (no OAuth token passed).

Call `claudeEnv(baseEnv, authToken="sk-ant-prod", oauthToken="oauth-tok", baseURL="",
tempHome="/tmp/h")` (OAuth preferred):

- The returned env contains `"CLAUDE_CODE_OAUTH_TOKEN=oauth-tok"`.
- The returned env does NOT contain `"ANTHROPIC_API_KEY="` (OAuth preferred per ADR 033).
- The returned env does NOT contain `executor.LocalProxyAuthPlaceholder`.

**Rationale:** This is a regression guard. Task 101 must not alter the cloud-entry
auth path that the existing production pipeline uses.

---

### TC-101-04 — ConfigFromEnv accepts a local-only registry (no cloud credential in env)

- **Requirement:** REQ-101-02
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/run_test.go`

**Input:** Call `ConfigFromEnv(getenv)` with a `getenv` that:
- Sets `AGENT_BUILDER_REGISTRY_LOCAL_ENABLED=true`,
  `AGENT_BUILDER_REGISTRY_LOCAL_ENDPOINT=http://localhost:8080`,
  `AGENT_BUILDER_REGISTRY_LOCAL_MODEL=qwen2.5-coder:7b`,
  `AGENT_BUILDER_REGISTRY_LOCAL_CAPABILITY_TIER=1`,
  `AGENT_BUILDER_REGISTRY_LOCAL_COST_WEIGHT=1`.
- Returns `""` for `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN`.
- Returns valid values for all other required vars (`AGENT_BUILDER_TASK_ROOT`,
  `AGENT_BUILDER_WORKTREE`, `AGENT_BUILDER_RUN_TIMEOUT`, `AGENT_BUILDER_MAX_ATTEMPTS`,
  `AGENT_BUILDER_PUBLISH_REMOTE`).

**Expected output:**
- `ConfigFromEnv` returns `(Config, nil)` — no error.
- The returned `Config.ClaudeToken == ""` and `Config.ClaudeOAuthToken == ""`.

**Rationale:** A pure-local operator should not need to export `ANTHROPIC_API_KEY`
to start the orchestrator.

---

### TC-101-05 — ConfigFromEnv still errors when a cloud entry is configured but no credential is present

- **Requirement:** REQ-101-02
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/run_test.go`

**Input A (cloud entry, no credential):** Call `ConfigFromEnv(getenv)` with a `getenv` that:
- Sets `AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENABLED=true`,
  `AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENDPOINT=https://api.anthropic.com`,
  `AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_SECRET_REF=claude-oauth-token`,
  `AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_MODEL=claude-opus-4-5`,
  `AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_CAPABILITY_TIER=3`,
  `AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_COST_WEIGHT=10`.
- Returns `""` for `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN`.
- Returns valid values for all other required vars.

**Expected output A:** `ConfigFromEnv` returns a non-nil error containing both
`executor.ClaudeCLIAuthEnv` (`"ANTHROPIC_API_KEY"`) and `executor.ClaudeCLIOAuthEnv`
(`"CLAUDE_CODE_OAUTH_TOKEN"`) in the error message (same gate as before this task).

**Input B (no registry entries at all):** Call `ConfigFromEnv(getenv)` with `getenv`
that returns `""` for all registry vars AND `""` for cloud credentials.

**Expected output B:** `ConfigFromEnv` returns a non-nil error (no enabled entries
means the existing cloud-credential check is preserved — fail-closed when there is no
evidence of a local-only config).

**Rationale:** The gate must not be silently dropped for mixed or cloud configs.
Fail-closed when any cloud entry is present or when no entries are configured at all.

---

### TC-101-06 — End-to-end intent: local entry env is sufficient for CLI auth against proxy

- **Requirement:** REQ-101-03
- **Level:** L2 documented as operator-confirmable L6
- **Test file:** `internal/executor/claude_cli_test.go` (L2 part); L6 is operator-run

**L2 part (CI-automatable):** Call `NewClaudeCLIFromEntry` with a local entry
(`SecretRef=""`, `Endpoint="http://localhost:8080"`) and a stub subprocess that:
1. Checks that `ANTHROPIC_API_KEY` is set and non-empty in the subprocess env.
2. Checks that `ANTHROPIC_BASE_URL` is set to `"http://localhost:8080"` in the
   subprocess env.
3. Writes a valid branch name to the branch-file path and exits 0.

Assert: `Result.OK == true` and the stub captured both expected env vars.

This test replaces TC-091-03's negative assertion ("no cloud auth vars") with the
corrected behavior: a placeholder key IS present, ANTHROPIC_BASE_URL IS present.

**L6 operator confirmation (not CI-automatable):** On the 2026-06-28 live run,
`ANTHROPIC_BASE_URL` pointed at the LiteLLM proxy at `http://localhost:4000` and the
proxy returned 7×200 OK. The ONLY failure was `"Not logged in · Please run /login"`,
which is the exact symptom fixed by FINDING 1. Re-running the same task 094 live
round-trip after this fix (LiteLLM proxy + qwen → claude CLI with placeholder key set)
constitutes L6 confirmation. See also TC-094-02 in task 094's spec (the live round-trip
requirement).

**Reference:** Live run 2026-06-28, task 094 session. Failure log: `"Not logged in ·
Please run /login"` at first attempt. Proxy log: 7×200 OK before failure.

---

## Verification plan

- **Highest level achievable in CI:** L2/L3 — unit tests on `claudeEnv` (executor)
  and `ConfigFromEnv` (runtime), plus the existing supervisor-isolation fitness
  (`make fitness-supervisor-isolation`) must remain green.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/executor/... ./internal/runtime/...
  ```
  Expected: both packages `ok`
- **L3 import-graph / fitness:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L5/L6 (deferred, operator-run):** Re-run the task 094 live round-trip
  (LiteLLM proxy + `qwen2.5-coder:7b` on Ollama + Claude Code CLI + RTX 4060)
  after this fix is merged. Successful completion (branch produced, gate green) is
  the L6 evidence. Reference: TC-094-02 in `docs/tasks/test-specs/094-local-model-evaluation-test-spec.md`.

## Security review flags

This task modifies the executor auth path. Both **spec-verifier** and
**security-auditor** must review before merge. The load-bearing invariant to verify:

> For local entries, the injected `ANTHROPIC_API_KEY` value MUST equal
> `executor.LocalProxyAuthPlaceholder` and MUST NOT equal the operator's
> real `authToken` or `oauthToken`. TC-101-01 and TC-101-02 assert this directly.

## Out of scope

- Changing how cloud entries authenticate (unchanged path, protected by TC-101-03).
- Vault-brokered credential resolution for local entries (no vault is needed; the
  placeholder is a fixed constant, not a secret).
- A new harness or a new executor type for local entries (the existing `ClaudeCLI`
  with the placeholder is sufficient — the proxy ignores the key value).
- Changes to the router or registry entry types (only `claudeEnv` and
  `configFromEnvWithSource` change).
