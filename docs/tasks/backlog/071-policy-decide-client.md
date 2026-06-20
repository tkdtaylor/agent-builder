# Task 071: `internal/policy` decide client + AuthZEN types (pure leaf)

**Project:** agent-builder
**Created:** 2026-06-19
**Status:** backlog

## Goal

Introduce a new leaf package `internal/policy/` containing:
1. A Go client that speaks the policy-engine IPC protocol: `{"op":"decide","request":{…}}`
   and `{"op":"ping"}` over a newline-delimited JSON Unix domain socket.
2. Typed AuthZEN request and response types: `DecideRequest`, `DecideResponse`,
   `Decision` (enum: `allow | deny | require_approval`), `Obligation`.
3. Fail-closed mapping: any error (unknown decision, malformed response, dial error,
   timeout) → `deny`. The client never silently produces `allow` on a failure path.

No runtime wiring, no daemon lifecycle, no imports from other `agent-builder/internal/`
packages. Unit-tested against a fake in-process Unix-socket server. This is the pure
client seam that tasks 072 and 073 plug into the run pipeline.

## Context

The policy-engine IPC protocol (from `~/Code/Public/policy-engine/ipc.go`):

```
// decide request
{"op":"decide","request":{<AuthZEN>}} \n
// decide response (allow)
{"decision":"allow","context":{"reason":"…","obligations":[…]}} \n
// decide response (deny)
{"decision":"deny","context":{"reason":"…","obligations":[]}} \n
// ping
{"op":"ping"} \n → {"ok":true} \n
// error
{"error":{"code":"bad_request","message":"…","retryable":false}} \n
```

One dial per call — not a persistent connection. Mirror the vault client pattern
(`internal/vault/client.go`): `NewClient(socketPath string) *PolicyClient` with
method `Decide(DecideRequest) (DecideResponse, error)` and `Ping() error`.

### Fail-closed semantics (load-bearing)

The `vault_injection_floor` obligation says obligations RAISE the floor, never lower.
The policy client's fail-closed rule is the analogous invariant for decisions: any
ambiguous outcome is `deny`. This must be implemented at the client level (not just the
caller) so the rule cannot be bypassed by catching the error and defaulting to allow.

Recommended implementation: `Decide` returns `(DecideResponse, error)`. When error is
non-nil, the response's `Decision` field is `DecisionDeny`. When the response contains
an unrecognized decision string, `Decision` is set to `DecisionDeny` before returning.
The caller (task 072) checks `response.Decision` and never inspects the error to
decide "maybe allow anyway."

### Package placement

`internal/policy/` — a new leaf package. `go list -deps ./internal/policy/...` must
contain no `github.com/tkdtaylor/agent-builder/internal/` paths other than
`internal/policy` itself. The fitness check enforced by F-006 (task 074) locks this in.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                              | Priority  |
|------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-071-01 | `internal/policy` package exists and compiles. Exports: `PolicyClient`, `NewClient(socketPath string) *PolicyClient`, `Ping() error`, `Decide(DecideRequest) (DecideResponse, error)`. Typed `Decision` enum with constants `DecisionAllow`, `DecisionDeny`, `DecisionRequireApproval`. Typed `Obligation{Type string, Value any}`. `DecideRequest` and `DecideResponse` cover the AuthZEN shape. | must have |
| REQ-071-02 | `PolicyClient.Decide` returns `DecisionAllow` for a well-formed allow response and `DecisionDeny` for a well-formed deny response; correctly parses the `obligations` array. | must have |
| REQ-071-03 | Fail-closed: any of (unknown decision string, malformed JSON, dial error, empty response, error-shaped response) produces `DecisionDeny` in the returned `DecideResponse.Decision`. The returned decision is never `DecisionAllow` on any error path. | must have |
| REQ-071-04 | `internal/policy` is a leaf: `go list -deps ./internal/policy/...` contains no `github.com/tkdtaylor/agent-builder/internal/` paths. `make check` exits 0. | must have |

## Readiness gate

- [x] Test spec `071-policy-decide-client-test-spec.md` exists (written first)
- [ ] Task 070 (ADR-038) merged and accepted — provides the architectural rationale
- [ ] `make check` green on main before starting

## Acceptance criteria

- [ ] [REQ-071-01] TC-071-01: `PolicyClient.Ping` against fake server → nil error; compile assertions pass
- [ ] [REQ-071-01] TC-071-02: `DecideRequest`, `DecideResponse`, `Decision`, `Obligation` types compile; interface-satisfaction (if applicable) present
- [ ] [REQ-071-02] TC-071-03: `Decide` returns `DecisionAllow` with parsed obligations on allow response
- [ ] [REQ-071-02] TC-071-04: `Decide` returns `DecisionDeny` with empty obligations on deny response
- [ ] [REQ-071-03] TC-071-05: all six fail-closed sub-cases produce `DecisionDeny` (never `DecisionAllow`)
- [ ] [REQ-071-04] TC-071-06: `go list -deps ./internal/policy/...` → `PASS-leaf`; `make check` → `All checks passed.`

## Verification plan

- **Highest level achievable:** L5 — unit tests against in-process fake servers + `make check`.
  No daemon lifecycle is introduced in this task; no real binary is needed.
- **Harness command:**
  ```
  go test -count=1 ./internal/policy/...
  go list -deps ./internal/policy/... | grep 'agent-builder/internal/' && echo FAIL || echo PASS-leaf
  go test -count=1 ./tests/e2e/... -run TestPhase0EndToEndAcceptance
  make check
  ```
  Expected: first command `ok`; leaf check prints `PASS-leaf`; e2e test `PASS`;
  final line `All checks passed.`
- **Runtime observation:** N/A — this task has no daemon or subprocess; no runtime surface.

## Out of scope

- PolicyDaemon lifecycle (`Start`, `Stop`, ping-wait) — task 072.
- Wiring `PolicyClient` into `runtime.Run` — task 072.
- `require_approval` routing and `audit_emit` obligation wiring — task 073.
- The F-006 fitness check — task 074.
- Dynamic risk scoring (deferred per ADR 038).
- Any imports from other `agent-builder/internal/` packages (leaf rule is absolute).

## Dependencies

- Task 070 (ADR-038) — must be merged and human-approved before the executor begins.
