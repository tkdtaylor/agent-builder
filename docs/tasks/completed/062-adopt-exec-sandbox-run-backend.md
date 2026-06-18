# Task 062: adopt exec-sandbox block as default run() backend

**Project:** agent-builder
**Created:** 2026-06-18
**Status:** 🔴 (not started)

## Goal

Wire the shipped exec-sandbox block binary as agent-builder's **default** `sandbox.Runner` backend
by adding `internal/sandbox/execsandbox` — a new package that speaks the block's stdin/stdout JSON
`run()` contract — and pointing `internal/runtime` at it. The existing Podman launcher
(`internal/sandbox/podman`) is **demoted to a selectable fallback, not deleted**. Governing
decision: ADR 035.

## Context

- `internal/sandbox/run.go` defines `Runner`, `Request`, `Limits`, `Result` — the seam is unchanged.
- `internal/sandbox/podman/run.go` is the current default backend (wired in `internal/runtime/run.go:188`).
- The exec-sandbox block lives at `~/Code/Public/exec-sandbox`; it is built with `go build -o bin/exec-sandbox ./...`.
- Its stdin/stdout JSON contract is documented in ADR 035 §2 and in `~/Code/Public/exec-sandbox/main.go` + `run.go`.
- Block binary path is sourced from a new config env var `AGENT_BUILDER_EXEC_SANDBOX_BIN`.
- Dependencies: 036 (Podman adapter swap — completed), ADR 035.

## Requirements

| Req ID       | Description                                                                                                                                                                                                       | Priority  |
|--------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-062-01   | `internal/sandbox/execsandbox` implements `sandbox.Runner`. `Run(Request)` marshals `Request`/`Limits` into the block's `RunRequest` JSON (field mapping: `MemoryBytes→memory_mb`, `CPUCount→cpu_count`, `PidsLimit→pids`, `WallClockTimeout→timeout_sec`, `EgressAllowlist→profile.capabilities[NetConnect].allowlist`; `tier` passed through; `secret_refs=[]`, `vault_socket=""`, `injection_mode=""`, `audit_socket=""`, `wiring.origin_map={}`), execs `exec-sandbox run`, writes the JSON to its stdin, and reads the JSON result from its stdout. | must have |
| REQ-062-02   | `Run(Request)` parses the block's JSON result into `sandbox.Result{Stdout, Stderr, Duration}` plus exit code. `Result.Duration` is set from `sandbox_status.duration_ms`. `sandbox_status` is surfaced to the caller (as a returned struct, extended `Result` fields, or a separate output value) — it is not silently discarded.  | must have |
| REQ-062-03   | The block's `{"error":"..."}` JSON response and any non-zero block process exit are **both** surfaced as a loud, non-nil Go error. No silent fallback to another backend. A block exit `0` with stdout that is not valid JSON is also a loud Go error. | must have |
| REQ-062-04   | A missing or unconfigured binary (`AGENT_BUILDER_EXEC_SANDBOX_BIN` empty or pointing to a non-existent path) fails loud **before** invoking anything — naming the missing config or binary in the error. No silent fallback. | must have |
| REQ-062-05   | `internal/runtime.Run` wires `execsandbox.Runner` as the **default** when `AGENT_BUILDER_EXEC_SANDBOX_BIN` is set. The Podman launcher (`internal/sandbox/podman`) is still selectable via explicit config (e.g. `AGENT_BUILDER_EXEC_BOX_LAUNCHER` set and `AGENT_BUILDER_EXEC_SANDBOX_BIN` unset, or an explicit backend selector). A new fitness step `fitness-exec-sandbox-default` confirms the default wiring; it is added to `make fitness`. `docs/spec/` (configuration.md + interfaces.md) is updated in the same commit. | must have |
| REQ-062-06   | The Phase-0 end-to-end capstone (`TestLivePhase0EndToEndAcceptance_TC032`) re-runs green on the exec-sandbox block backend. A live test `TestExecSandboxLive` (gated on `AGENT_BUILDER_LIVE_EXEC_SANDBOX=1`) drives the real block binary under `tier="bubblewrap"` and `tier="gvisor"` with at least one limit that visibly fires (e.g. `WallClockTimeout` → `sandbox_status.status="timeout"`). | must have (L6 gated) |

## Readiness gate

- [x] Test spec `062-adopt-exec-sandbox-run-backend-test-spec.md` exists
- [x] ADR 035 written and accepted
- [x] Blocking tasks complete: 036 (Podman adapter swap), 061 (dep-scan fix / capstone green)

## Acceptance criteria

- [ ] [REQ-062-01] TC-062-01: full `Limits` marshal produces correct `RunRequest` JSON (all field names, values, and allowlist→capabilities mapping verified against the stub binary's recorded stdin)
- [ ] [REQ-062-01] TC-062-02: zero/unset `Limits` produce zero fields; empty allowlist produces no `capabilities` entry; default tier `"bubblewrap"` used when `Request.Tier` is empty
- [ ] [REQ-062-02] TC-062-03: JSON result parsed into `Result`; `Duration` set from `duration_ms`; `sandbox_status` surfaced
- [ ] [REQ-062-03] TC-062-04: `{"error":...}` response and non-zero block exit both produce loud Go errors; invalid JSON output also errors
- [ ] [REQ-062-04] TC-062-05: empty binary path and nonexistent path both fail loud before exec
- [ ] [REQ-062-05] TC-062-06: wiring test confirms `execsandbox.Runner` is the default; Podman is reachable via explicit config
- [ ] [REQ-062-05] TC-062-07: `make fitness` passes the new `fitness-exec-sandbox-default` step; `make check` exit 0
- [ ] [REQ-062-05] spec updated (`docs/spec/configuration.md`, `docs/spec/interfaces.md`) in the same commit as the code change
- [ ] [REQ-062-06] TC-062-08: L6 live — bubblewrap + gvisor runs pass; timeout limit fires with `status="timeout"`; Phase-0 capstone re-runs green on the block backend (pending live run)

## Verification plan

- **Highest level achievable in-repo:** L5 — `go test ./internal/sandbox/execsandbox/... ./tests/cli/...` and `make check` green (TC-062-01 through TC-062-07).
- **L6 (live capstone on block backend):**
  - Harness command:
    ```
    AGENT_BUILDER_LIVE_EXEC_SANDBOX=1 \
    AGENT_BUILDER_EXEC_SANDBOX_BIN=$(go env GOPATH)/bin/exec-sandbox \
    go test -count=1 -v ./internal/sandbox/execsandbox/... -run TestExecSandboxLive
    ```
    Then the Phase-0 capstone:
    ```
    AGENT_BUILDER_LIVE_E2E=1 \
    AGENT_BUILDER_LIVE_E2E_REMOTE=l6 \
    CLAUDE_CODE_OAUTH_TOKEN=<token> \
    AGENT_BUILDER_EXEC_SANDBOX_BIN=$(go env GOPATH)/bin/exec-sandbox \
    go test -count=1 -v ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032
    ```
  - Runtime observation: real exec-sandbox binary executes under bubblewrap and gvisor; `sandbox_status.status="timeout"` observed for the sleep+1s-timeout case; `--- PASS` on the live capstone with a real PR opened and cleaned on l6; the in-box gate (build/vet/test/gofmt/golangci-lint/dep-scan/code-scanner) is fully green.
  - Prerequisites: host has `bwrap` installed, `runsc` installed, exec-sandbox binary built (`go build -o bin/exec-sandbox ./...` in `~/Code/Public/exec-sandbox`), and `CLAUDE_CODE_OAUTH_TOKEN` set.

## Out of scope

- Wiring `secret_refs`, `vault_socket`, `audit_socket`, or `injection_mode` (deferred per ADR 035; vault block is still WIP)
- Deleting `internal/sandbox/podman` or `containment/execution-box/run.sh` (stays as fallback)
- Adding `disk_mb` to `sandbox.Limits` (send `0` for now; additive follow-on)
- Changing the `sandbox.Runner` interface or `sandbox.Limits` type (ADR 020 seam is unchanged)
- Firecracker tier support (`backendFor` in the block hard-errors on unknown tiers; agent-builder inherits that)
- Removing `AGENT_BUILDER_EXEC_BOX_LAUNCHER` from the config contract (Podman fallback keeps it)
