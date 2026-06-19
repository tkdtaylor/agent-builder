# Task 064: ADR-036 — vault integration decision

**Project:** agent-builder
**Created:** 2026-06-19
**Status:** 🔴 (not started)

## Goal

Write `docs/architecture/decisions/036-vault-integration.md`. This is the architectural
decision record for adopting the vault block to broker executor and git/GitHub tokens
through exec-sandbox's egress proxy instead of forwarding them raw into the execution
box. The ADR is a human-reviewable planning artifact; the implementation tasks (065,
066) depend on it being accepted before they begin.

No production code is written or modified in this task.

## Context

CLAUDE.md names the egress allowlist as "the load-bearing control for the accepted
token-in-box risk." That accepted risk is: CLAUDE_CODE_OAUTH_TOKEN and
ANTHROPIC_API_KEY are currently forwarded raw into the execution box via the executor's
`claudeEnv()` subprocess environment, and AGENT_BUILDER_GIT_TOKEN /
AGENT_BUILDER_GITHUB_TOKEN are forwarded via the publisher. The allowlist + revocability
+ scanners mitigate this, but it remains an explicitly accepted risk.

The vault block (`~/Code/Public/vault`, standalone Rust daemon) is now complete. The
exec-sandbox block (`~/Code/Public/exec-sandbox`) already has vault injection hooks:
`RunRequest.wiring.vault_socket`, `RunRequest.run.secret_refs`, and
`RunRequest.wiring.injection_mode`. As of ADR 035, agent-builder sends these fields
empty. This ADR decides how and in what order to fill them in.

**Key feasibility constraint baked into the decision:** exec-sandbox v0 proxy mode is
fully wired — the egress proxy attaches `<scheme> <cred>` to the configured header on
allowlisted hosts. Env mode is a STUB in exec-sandbox v0 — the field is recorded but
NOT loaded into the sandbox env. Any integration targeting exec-sandbox v0 MUST target
proxy mode; env mode is not available as a delivery mechanism.

**Starting scope:** git/GitHub tokens (`api.github.com:443`, `github.com:443`) are the
cleaner proxy targets — their allowlist entries are already present, and the
Authorization/Bearer binding is standard. The provider token (Claude: `api.anthropic.com`)
requires the same Authorization/Bearer binding but the in-box Claude CLI must authenticate
with the token absent from the box env and present only on the proxy — this is unproven
and is named as a risk to be resolved in task 066's Verification plan.

**Roadmap note:** the vault block is sequenced after audit-trail/policy-engine in the
original roadmap. This decision pulls vault forward explicitly because the token-in-box
risk is the most concrete risk agent-builder faces and vault v1 is already complete.

## Requirements

| Req ID     | Description                                                                                                                       | Priority  |
|------------|-----------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-064-01 | ADR file `docs/architecture/decisions/036-vault-integration.md` exists with Status, Context, Decision, and Consequences sections. | must have |
| REQ-064-02 | Decision names injection_mode="proxy" and explains why env mode is excluded (stub in exec-sandbox v0, not loaded into sandbox env). | must have |
| REQ-064-03 | Decision documents the proxy-mode feasibility risk for the Claude provider token and names git/GitHub tokens as the chosen starting scope. | must have |
| REQ-064-04 | Decision documents the vault socket protocol verbs (put/resolve/inject), the Binding shape, the vault_socket and secret_refs RunRequest fields, and the opaque handle model (agent-builder holds handle, not value). | must have |
| REQ-064-05 | ADR references ADR 035 (deferred vault fields) and notes which spec files (configuration.md, interfaces.md) will be updated by the implementation tasks. `make check` exits 0. | must have |

## Readiness gate

- [ ] Test spec `064-adr036-vault-integration-test-spec.md` exists (written first)
- [ ] ADR 035 is in `docs/architecture/decisions/` (already done — task 062)
- [ ] Human has reviewed and approved the scope (this ADR is "Ask-first" per CLAUDE.md)

## Acceptance criteria

- [ ] [REQ-064-01] TC-064-01: ADR file exists, has Status/Context/Decision/Consequences, references ADR 035
- [ ] [REQ-064-02] TC-064-02: injection_mode="proxy" named in Decision; env-mode stub status explicitly called out
- [ ] [REQ-064-03] TC-064-03: feasibility risk for Claude token documented; git/GitHub token starting scope named
- [ ] [REQ-064-04] TC-064-04: vault protocol verbs (put/resolve/inject), Binding, vault_socket, secret_refs, opaque handle model all present
- [ ] [REQ-064-05] TC-064-05: ADR 035 referenced; spec-update note present; `make check` exits 0

## Verification plan

- **Highest level achievable:** L5 — doc-content `grep` assertions + `make check`
  (no runtime surface; ADR is a markdown file).
- **Harness command:**
  ```
  grep -q "Status:" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "## Decision" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "ADR 035" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "proxy" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "injection_mode" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "secret_refs" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "vault_socket" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "resolve" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "inject" docs/architecture/decisions/036-vault-integration.md && \
  make check
  ```
  Expected: all exit 0; `make check` → `All checks passed.`
- **Runtime observation:** N/A — no runtime surface.

## Out of scope

- Writing any Go or Rust code.
- Updating `docs/spec/` files (those land in 065/066 with their code changes).
- Writing the `SecretSource` interface (task 065).
- Writing vault client code or lifecycle management (task 066).
- Env-mode integration (explicitly deferred until exec-sandbox v0 env-mode stub is promoted).
- Changing existing behavior — this task commits only the ADR file.

## Dependencies

- ADR 035 (adopted exec-sandbox block) — already written and accepted (task 062).
- Human approval of the scope before the task begins (CLAUDE.md "Ask first" for ADR authoring).
