# Test spec — Task 060: live capstone accepts subscription OAuth token

## Context

Task 059 / ADR 033 taught the executor and `ConfigFromEnv` to authenticate with either
`ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` (subscription, preferred). But the live capstone
test fixture (`tests/e2e/live_phase0_e2e_test.go`) still (a) skip-gates only on `ANTHROPIC_API_KEY`
and (b) forwards only `ANTHROPIC_API_KEY` into the agent-builder subprocess env. So a
subscription-only operator cannot run the capstone — it either skips (no API key) or runs against
a dead-credit API key and fails. This task threads the OAuth credential through the fixture so the
capstone matches the executor's accept-either contract. This is the path that runs probes
022/028/032 on a subscription without API credit.

## Test cases

### TC-060-01 — skip-guard accepts either credential
- **Assertion:** `TestLivePhase0EndToEndAcceptance_TC032` skips (TC-054-02 style) only when **both**
  `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN` are unset/blank; it proceeds when **either** is
  set. The skip message names both credentials.

### TC-060-02 — fixture forwards the OAuth token to the subprocess
- **Assertion:** `liveCapstoneFixture.env()` includes a `CLAUDE_CODE_OAUTH_TOKEN` entry carrying
  `os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")` alongside `ANTHROPIC_API_KEY`, so the agent-builder
  subprocess receives whichever credential the operator set. (The executor then applies its own
  OAuth-preferred precedence — the fixture does not pre-select.)

### TC-060-03 — gate green; skip discipline intact
- **Assertion:** `go test ./...` + `make check` pass. With neither live flag/credential set, the
  capstone test still SKIPs cleanly (no regression to TC-054-02).

### TC-060-04 — live subscription capstone (L6, operator/observed)
- **Assertion:** with `AGENT_BUILDER_LIVE_E2E=1`, `AGENT_BUILDER_LIVE_E2E_REMOTE=l6`, and
  `CLAUDE_CODE_OAUTH_TOKEN` set (no usable `ANTHROPIC_API_KEY`), the capstone runs end-to-end on
  subscription auth: real task selected, real branch produced, PR recorded against l6, gate passed,
  run record persisted, PR + branch cleaned up. Recorded as operator observation; **stays pending
  until actually run** — never marked from code alone.

## Verification plan

- **Highest level achievable in-repo:** L5 — `go test ./...` + `make check` green (TC-060-01..03).
- **L6 (observed):** the live subscription capstone run (TC-060-04), which is simultaneously the
  unblocked end-to-end evidence for probes 022/028/032.

## Out of scope

- Executor/`ConfigFromEnv` auth logic (delivered by task 059 / ADR 033).
- The publisher and box-launch legs (proven by tasks 034 and 058).
