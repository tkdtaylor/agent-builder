# Test spec — Task 071: `internal/policy` decide client + AuthZEN types

**Linked task:** `docs/tasks/backlog/071-policy-decide-client.md`
**Written:** 2026-06-19
**Status:** ready

## Context

This task introduces a new leaf package `internal/policy/` containing:

1. A typed Go client that speaks the policy-engine IPC protocol: newline-delimited JSON
   over a `0600` Unix domain socket. Operations: `{"op":"decide","request":{…AuthZEN…}}`
   and `{"op":"ping"}`.
2. Typed AuthZEN request and response types: `DecideRequest`, `DecideResponse`,
   `Decision` (typed string enum: `allow | deny | require_approval`), and `Obligation`
   (typed struct with `Type` and `Value` fields).
3. Fail-closed mapping: any of (unknown decision value, malformed/partial response,
   socket/dial error, timeout) must map to `deny`. The client never lets an error
   silently become `allow`.

The package is a pure leaf: no imports from other `agent-builder/internal/` packages.
Unit tests run against a fake in-process Unix-socket server that does not require the
real `policy-engine` binary.

This task is modeled on `internal/vault/client.go` shape. The key difference from the
vault client: the policy client makes a single synchronous `decide` call per run (not
a put-then-resolve flow), and the fail-closed rule is load-bearing security behavior,
not just error handling.

The IPC wire protocol, from the policy-engine source (`ipc.go`, `policy.go`, `main.go`):

```
// Request
{"op":"decide","request":{"subject":…,"action":{"name":"run-task"},"resource":{…},"context":{"risk":"low"}}}
// Response (allow)
{"decision":"allow","context":{"reason":"…","obligations":[{"type":"tier_select","value":"bubblewrap"},{"type":"vault_injection_floor","value":"proxy"},{"type":"audit_emit","value":true}]}}
// Response (deny)
{"decision":"deny","context":{"reason":"…","obligations":[]}}
// Ping request
{"op":"ping"}
// Ping response
{"ok":true}
// Error response
{"error":{"code":"bad_request","message":"…","retryable":false}}
```

## Requirements coverage

| Req ID     | Test cases                   | Covered? |
|------------|------------------------------|----------|
| REQ-071-01 | TC-071-01, TC-071-02         | yes      |
| REQ-071-02 | TC-071-03, TC-071-04         | yes      |
| REQ-071-03 | TC-071-05                    | yes      |
| REQ-071-04 | TC-071-06                    | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-071-01 — `PolicyClient` Ping succeeds against a fake Unix-socket server

- **Requirement:** REQ-071-01
- **Level:** L5 (unit test with in-process fake server)
- **Test file:** `internal/policy/client_test.go`
- **Test name:** `TestPolicyClientPing`

**Setup:**
1. Spawn an in-process fake Unix-socket server (using `net.Listen("unix", …)` in the
   test) that responds to `{"op":"ping"}` with `{"ok":true}`.
2. Construct `PolicyClient{SocketPath: <tempdir>/policy.sock}`.
3. Call `client.Ping()`.

**Assertions:**
- `Ping()` returns `nil` (no error).
- A second `Ping()` also returns `nil` (the connection is not held open between calls —
  each call is one dial→write→read→close cycle, mirroring the vault client pattern).

---

### TC-071-02 — `PolicyClient` typed request/response types compile and satisfy interface

- **Requirement:** REQ-071-01
- **Level:** L2 (compile-time assertion)
- **Test file:** `internal/policy/client_test.go` or `internal/policy/types_test.go`

**Assertions:**
- `DecideRequest` is a Go struct with at least fields for `Subject`, `Action`, `Resource`,
  and `Context` (or equivalent nested struct fields matching the AuthZEN shape).
- `DecideResponse` is a Go struct with at least `Decision Decision` and `Obligations []Obligation`.
- `Decision` is a typed string type with constants `DecisionAllow`, `DecisionDeny`,
  `DecisionRequireApproval`.
- `Obligation` is a Go struct with at least `Type string` and `Value any` (or a typed
  value field).
- A compile-time construction `DecideRequest{Subject: …, Action: …}` succeeds.
- `go build ./internal/policy/...` exits 0.

---

### TC-071-03 — `PolicyClient.Decide` returns typed `DecisionAllow` for an allow response

- **Requirement:** REQ-071-02
- **Level:** L5 (unit test with in-process fake server)
- **Test file:** `internal/policy/client_test.go`
- **Test name:** `TestPolicyClientDecideAllow`

**Setup:**
1. Fake server: on `{"op":"decide", …}`, respond with:
   ```json
   {"decision":"allow","context":{"reason":"host is allowlisted","obligations":[{"type":"tier_select","value":"bubblewrap"},{"type":"vault_injection_floor","value":"proxy"},{"type":"audit_emit","value":true}]}}
   ```
2. Call `client.Decide(DecideRequest{…})`.

**Assertions:**
- No error returned.
- `result.Decision == DecisionAllow`.
- `result.Obligations` has exactly 3 entries.
- Obligations include one with `Type == "tier_select"` and `Value == "bubblewrap"`.
- Obligations include one with `Type == "vault_injection_floor"` and `Value == "proxy"`.
- Obligations include one with `Type == "audit_emit"`.

---

### TC-071-04 — `PolicyClient.Decide` returns typed `DecisionDeny` for a deny response

- **Requirement:** REQ-071-02
- **Level:** L5 (unit test with in-process fake server)
- **Test name:** `TestPolicyClientDecideDeny`

**Setup:**
Fake server responds with:
```json
{"decision":"deny","context":{"reason":"host not in allowlist","obligations":[]}}
```

**Assertions:**
- No error returned.
- `result.Decision == DecisionDeny`.
- `len(result.Obligations) == 0`.

---

### TC-071-05 — `PolicyClient.Decide` maps all error/unknown paths to deny (fail-closed)

- **Requirement:** REQ-071-03
- **Level:** L5 (unit test with in-process fake server + negative cases)
- **Test name:** `TestPolicyClientFailClosed`

Sub-cases (each using a distinct fake server or dial failure):

| Sub-case | Simulated condition | Expected `result.Decision` | Expected error? |
|----------|--------------------|-----------------------------|-----------------|
| A | Server responds `{"decision":"unknown_future_value","context":{…}}` | `DecisionDeny` | No (or wrapped error indicating unknown) |
| B | Server closes connection without responding | `DecisionDeny` | Non-nil OR `DecisionDeny` with nil error (either is acceptable; deny is mandatory) |
| C | Server responds with malformed JSON `{not json` | `DecisionDeny` | Non-nil OR `DecisionDeny` with nil error |
| D | No server listening at the socket path (dial failure) | `DecisionDeny` | Non-nil error naming dial failure |
| E | Server responds with `{"error":{"code":"bad_request","message":"…","retryable":false}}` | `DecisionDeny` | Non-nil OR `DecisionDeny` |
| F | Server responds with `{"ok":true}` (ping shape, not a decide shape) | `DecisionDeny` | Non-nil OR `DecisionDeny` |

**Load-bearing assertion for all sub-cases:**
`result.Decision` is never `DecisionAllow` when the server returns anything other than
a well-formed `{"decision":"allow", …}` response. The fail-closed rule is never violated.

---

### TC-071-06 — `internal/policy` is a leaf package (no internal agent-builder deps)

- **Requirement:** REQ-071-04
- **Level:** L3 (import-graph check)
- **Test file:** CI / Makefile fitness step or in-process `go list -deps` assertion
- **Test name:** `TestPolicyPackageIsLeaf` (or checked via `make check`)

**Assertion:**
- `go list -deps ./internal/policy/...` contains no
  `github.com/tkdtaylor/agent-builder/internal/` package paths other than
  `internal/policy` itself.
- The package imports nothing from `internal/executor`, `internal/runtime`,
  `internal/supervisor`, `internal/sandbox`, `internal/vault`, `internal/audit`,
  or any other agent-builder internal package.
- This confirms the dependency direction: `runtime` depends on `policy`, not the reverse.
- Confirmed by running: `go list -deps ./internal/policy/... | grep 'agent-builder/internal/' && echo FAIL || echo PASS-leaf`
  → output must be `PASS-leaf`.

---

## Verification plan

- **Highest level achievable:** L5 — unit tests against in-process fake servers + `make check` green.
- **L6:** N/A — the client package is a leaf with no daemon lifecycle; the lifecycle
  integration is task 072. A live policy-engine binary is not required for this task's verification.
- **Harness command:**
  ```
  go test -count=1 ./internal/policy/...
  go list -deps ./internal/policy/... | grep 'agent-builder/internal/' && echo FAIL || echo PASS-leaf
  go test -count=1 ./tests/e2e/... -run TestPhase0EndToEndAcceptance
  make check
  ```
  Expected: first command `ok`; leaf check prints `PASS-leaf`; e2e test `PASS`;
  final line `All checks passed.`

## Out of scope

- PolicyDaemon lifecycle (`Start`, `Stop`, ping-wait) — task 072.
- Wiring the client into `runtime.Run` — task 072.
- `require_approval` routing and `audit_emit` wiring — task 073.
- The F-006 fitness check asserting the leaf invariant — task 074.
- Dynamic risk scoring (deferred per ADR 038).
- Any changes to existing behavior — this task produces only a new leaf package.
