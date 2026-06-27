# Test spec — Task 088: Vault-brokered per-provider auth

**Linked task:** `docs/tasks/backlog/088-vault-per-provider-auth.md`
**Written:** 2026-06-27
**Status:** ready

## Context

ADR 043 requires each registry entry's secret to be resolved independently via vault
at dispatch time, keyed by the entry's `SecretRef`. Today `internal/secrets` has a
single `ProviderToken()` method that returns one provider's pair of credentials.

This task extends `SecretSource` (or adds a sibling interface) with a
`NamedProviderToken(ref string) (string, error)` method that resolves a secret by
name. Each provider token is independently revocable because each is a distinct vault
secret keyed by `SecretRef`.

The existing `ProviderToken()` method must not change behavior — this is an additive
extension, not a replacement.

## Requirements coverage

| Req ID     | Test cases           | Covered? |
|------------|----------------------|----------|
| REQ-088-01 | TC-088-01, TC-088-02 | yes      |
| REQ-088-02 | TC-088-03            | yes      |
| REQ-088-03 | TC-088-04            | yes      |
| REQ-088-04 | TC-088-05            | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-088-01 — NamedProviderToken resolves a named secret via the env fallback

- **Requirement:** REQ-088-01
- **Level:** L2 (unit test)
- **Test file:** `internal/secrets/secrets_test.go`

**Input:** Set env var `AGENT_BUILDER_SECRET_CODEX_TOKEN=sk-test-codex-key`. Call
`EnvSecretSource{}.NamedProviderToken("codex-token")`.

**Expected output:**
- Returns `("sk-test-codex-key", nil)`.
- The env var name is derived from the `SecretRef` by uppercasing and replacing `-`
  with `_`, prefixed with `AGENT_BUILDER_SECRET_` (e.g. `"codex-token"` →
  `AGENT_BUILDER_SECRET_CODEX_TOKEN`).

**Edge case:**
- `NamedProviderToken("unknown-ref")` with no matching env var returns
  `("", ErrSecretNotFound)` (or equivalent sentinel error — the caller must not
  treat an empty token as valid).

---

### TC-088-02 — Existing ProviderToken behavior is unchanged

- **Requirement:** REQ-088-01
- **Level:** L2 (unit test — regression)
- **Test file:** `internal/secrets/secrets_test.go`

**Input:** Call `EnvSecretSource{}.ProviderToken()` with
`ANTHROPIC_API_KEY=test-api-key` set.

**Expected output:**
- Returns `("test-api-key", "")` (unchanged from pre-task behavior).
- The existing OAuth-preferred logic (ADR 033) is preserved.

**Rationale:** This is a regression guard. Task 088 must not alter the behavior of
the existing `ProviderToken()` path that the current coding-agent pipeline uses.

---

### TC-088-03 — VaultSecretSource resolves NamedProviderToken via vault put/resolve

- **Requirement:** REQ-088-02
- **Level:** L2 (unit test with fake vault server)
- **Test file:** `internal/secrets/vault_source_test.go`

**Input:** In a test using the existing fake vault server pattern (see task 066),
call `vault.Put("gemini-api-key", "secret-gemini-value")`. Then construct a
`VaultSecretSource` and call `NamedProviderToken("gemini-api-key")`.

**Expected output:**
- Returns `(handle, nil)` where `handle` is an opaque string (vault reference, not
  the plaintext `"secret-gemini-value"`).
- The handle can be passed to a harness adapter as the auth token; the harness
  adapter resolves it via vault's injected-secret mechanism at subprocess launch.
- `NamedProviderToken` for a ref not in vault returns `("", ErrSecretNotFound)`.

**Note:** This matches the existing vault round-trip pattern from task 066. The
opaque handle is what gets injected into the subprocess env; vault resolves it inside
the box. This is the independently-revocable property: revoking `"gemini-api-key"`
does not affect `"claude-oauth-token"`.

---

### TC-088-04 — SecretSource interface compile-time assertion

- **Requirement:** REQ-088-03
- **Level:** L2 (compile-time)
- **Test file:** `internal/secrets/secrets_test.go`

**Input:** Compile-time assertion that both `EnvSecretSource` and `VaultSecretSource`
implement the extended `SecretSource` interface.

**Expected output:**
```go
var _ secrets.SecretSource = (*secrets.EnvSecretSource)(nil)
var _ secrets.SecretSource = (*secrets.VaultSecretSource)(nil)
```
Both compile without error. `SecretSource` now declares `NamedProviderToken(ref string) (string, error)`
in addition to the existing methods.

---

### TC-088-05 — internal/secrets remains a leaf after extension

- **Requirement:** REQ-088-04
- **Level:** L3 (import-graph)
- **Test file / harness:** `go list -deps ./internal/secrets/...`

**Input:** `go list -deps ./internal/secrets/...`

**Expected output:**
- The output contains `github.com/tkdtaylor/agent-builder/internal/secrets`.
- May contain `github.com/tkdtaylor/agent-builder/internal/vault` (allowed — vault is
  an existing allowed dependency of `VaultSecretSource`).
- Does NOT contain `internal/registry`, `internal/router`, `internal/executor`,
  `internal/supervisor`, `internal/runtime`.
- `go list` exits 0 and `make check` passes.

---

## Verification plan

- **Highest level achievable:** L3 — no runtime-observable surface for the extended
  interface alone. The live vault round-trip (TC-088-03) is L5 with the real vault
  binary (same gate as task 066).
- **L2 harness command:**
  ```
  go test -count=1 ./internal/secrets/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/secrets`
- **L3 import-graph check:**
  ```
  go list -deps ./internal/secrets/...
  ```
  Expected: no `internal/registry`, `internal/router`, `internal/executor`,
  `internal/supervisor`, `internal/runtime` in output.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Changing how the existing coding-agent pipeline resolves its Claude credentials
  (that remains via `ProviderToken()` until task 095 wires the real router).
- Per-provider env-var injection into the subprocess (harness adapters handle that
  in tasks 089/090/091 using the handle returned here).
- The router selecting which entry to use (task 092).
