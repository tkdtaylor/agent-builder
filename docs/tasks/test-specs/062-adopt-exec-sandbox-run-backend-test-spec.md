# Test spec — Task 062: adopt exec-sandbox block as default run() backend

**Linked task:** `docs/tasks/backlog/062-adopt-exec-sandbox-run-backend.md`
**Written:** 2026-06-18
**Status:** ready

## Context

ADR 035 promotes the shipped exec-sandbox block binary as agent-builder's default `sandbox.Runner`
backend. A new Go package `internal/sandbox/execsandbox` implements `sandbox.Runner` by speaking the
block's stdin/stdout JSON contract (`exec-sandbox run`). The existing Podman launcher
(`internal/sandbox/podman`) is demoted to a selectable fallback, not deleted.

The JSON contract (from exec-sandbox `main.go` + `run.go`):
- **RunRequest** (stdin): `{"run":{"payload":<shell str>,"profile":{"capabilities":[{"type":"NetConnect","allowlist":[...]}],"limits":{"cpu_count","memory_mb","pids","disk_mb","timeout_sec"}},"tier":"bubblewrap"|"gvisor","secret_refs":[]},"wiring":{"vault_socket":"","audit_socket":"","origin_map":{},"request_id":"<uuid>","injection_mode":""}}`
- **Result** (stdout): `{"stdout","stderr","exit_code","sandbox_status":{"sandbox_id","tier","duration_ms","secrets_injected":[...],"status":"clean"|"timeout","limits":{...,"degraded":[...]}}}` or `{"error":"..."}` on early failure
- Block process exit: `0` = handled (payload exit lives in `result.exit_code`); `1` = stdin/JSON error; `2` = usage

Field mapping from `sandbox.Limits`:
- `MemoryBytes` → `profile.limits.memory_mb` (bytes ÷ 1,048,576; 0 if unset)
- `CPUCount` → `profile.limits.cpu_count` (0 if unset)
- `PidsLimit` → `profile.limits.pids` (0 if unset)
- `WallClockTimeout` → `profile.limits.timeout_sec` (truncated to seconds; 0 if unset)
- `EgressAllowlist` → `profile.capabilities[{"type":"NetConnect","allowlist":[...]}]`
- `tier` is a new first-class field on the seam (passed through from `Request.Tier`)

Deferred (ADR 035 §5): `secret_refs=[]`, `vault_socket=""`, `injection_mode=""`,
`audit_socket=""`. Fields are plumbed and sent empty; no credential path is built.

## Requirements coverage

| Req ID       | Test cases                        | Covered? |
|--------------|-----------------------------------|----------|
| REQ-062-01   | TC-062-01, TC-062-02              | yes      |
| REQ-062-02   | TC-062-03                         | yes      |
| REQ-062-03   | TC-062-04                         | yes      |
| REQ-062-04   | TC-062-05                         | yes      |
| REQ-062-05   | TC-062-06, TC-062-07              | yes      |
| REQ-062-06   | TC-062-08 (L6)                    | yes      |

## Test cases

### TC-062-01 — full Limits marshal to correct RunRequest JSON (field names + values)

- **Requirement:** REQ-062-01
- **Level:** L5 (unit, stub binary)
- **Inputs:**
  - `sandbox.Request{Command: []string{"echo", "hi"}, Worktree: "/tmp/wt", Limits: sandbox.Limits{MemoryBytes: 512*1024*1024, CPUCount: 2, PidsLimit: 64, WallClockTimeout: 30*time.Second, EgressAllowlist: []string{"api.github.com:443","registry.npmjs.org:443"}}}`
  - `Request.Tier = "bubblewrap"`
- **Expected output:** the JSON written to the stub binary's stdin (captured via a recording harness) satisfies all of:
  - `run.payload` is a non-empty shell script string (the Command rendered as a shell invocation)
  - `run.tier == "bubblewrap"`
  - `run.profile.limits.memory_mb == 512` (bytes ÷ 1,048,576)
  - `run.profile.limits.cpu_count == 2`
  - `run.profile.limits.pids == 64`
  - `run.profile.limits.timeout_sec == 30`
  - `run.profile.capabilities` is a JSON array with exactly one element `{"type":"NetConnect","allowlist":["api.github.com:443","registry.npmjs.org:443"]}`
  - `run.secret_refs == []` (empty, not null)
  - `wiring.vault_socket == ""`
  - `wiring.audit_socket == ""`
  - `wiring.injection_mode == ""`
  - `wiring.request_id` is a non-empty string (implementation may generate a UUID or random token)
  - `wiring.origin_map` is present (may be `{}`)
- **Test file:** `internal/sandbox/execsandbox/run_test.go`

### TC-062-02 — zero/unset Limits produce zero fields in JSON, no capabilities entry for empty allowlist

- **Requirement:** REQ-062-01
- **Level:** L5 (unit, stub binary)
- **Inputs:** `sandbox.Request{Command: []string{"true"}, Worktree: "/tmp/wt", Limits: sandbox.Limits{}}` (zero value)
- **Expected output:**
  - `run.profile.limits.memory_mb == 0`
  - `run.profile.limits.cpu_count == 0`
  - `run.profile.limits.pids == 0`
  - `run.profile.limits.timeout_sec == 0`
  - `run.profile.capabilities` is absent or an empty array (no NetConnect entry when allowlist is nil/empty)
  - `run.tier == "bubblewrap"` (default when `Request.Tier` is empty string)
- **Test file:** `internal/sandbox/execsandbox/run_test.go`

### TC-062-03 — JSON result parsed back into Result + exit code; sandbox_status surfaced

- **Requirement:** REQ-062-02
- **Level:** L5 (unit, stub binary)
- **Inputs:** stub binary exits `0` and writes to stdout:
  ```json
  {"stdout":"hello\n","stderr":"","exit_code":0,"sandbox_status":{"sandbox_id":"sbx-abc123","tier":"bubblewrap","duration_ms":42,"secrets_injected":[],"status":"clean","limits":{"cpu_count":0,"memory_mb":0,"pids":0,"disk_mb":0,"timeout_sec":0,"degraded":[]}}}
  ```
- **Expected output:**
  - `Result.Stdout == "hello\n"`
  - `Result.Stderr == ""`
  - `Result.Duration > 0` (set from `sandbox_status.duration_ms`)
  - returned exit code `== 0`
  - no error returned
  - `SandboxStatus` (or equivalent struct) surfaces `SandboxID == "sbx-abc123"`, `Tier == "bubblewrap"`, `Status == "clean"` — either as a returned struct alongside `Result`, or as fields on an extended `Result` type that wraps `sandbox.Result`
- **Test file:** `internal/sandbox/execsandbox/run_test.go`

### TC-062-04 — block's `{"error":...}` response and non-zero block process exit both surface as loud Go errors

- **Requirement:** REQ-062-03
- **Level:** L5 (unit, stub binary)
- **Inputs (two sub-cases):**
  1. Stub binary exits `0` and writes `{"error":"tier not implemented: firecracker"}` to stdout
  2. Stub binary exits `1` (stdin/JSON error) and writes an error message to stderr
- **Expected output for both sub-cases:**
  - `Run(req)` returns a non-nil `error`
  - The error message contains the block's error text (sub-case 1: "tier not implemented: firecracker"; sub-case 2: the stderr output or a message naming the exit code)
  - `Result` is not a zero value with no error (i.e., the error is not silently swallowed and treated as a successful empty result)
- **Edge cases:** a block exit `0` with stdout that is not valid JSON must also return a non-nil error (JSON parse failure propagated loud)
- **Test file:** `internal/sandbox/execsandbox/run_test.go`

### TC-062-05 — missing or unconfigured binary fails loud before any attempt to invoke it

- **Requirement:** REQ-062-04
- **Level:** L5 (unit)
- **Inputs:** `execsandbox.New("")` (empty path) or `execsandbox.New("/nonexistent/exec-sandbox")`
- **Expected output:**
  - `New("")` returns an error OR `Run(req)` on a zero-path runner returns a non-nil error immediately naming the configuration problem (not a system exec error about an unrelated program)
  - `New("/nonexistent/exec-sandbox")` — `Run(req)` returns a non-nil error containing the binary path and a message indicating the binary is not found or not executable (fail-loud, not a silent empty result)
  - Neither case silently falls back to the Podman launcher or any other backend
- **Test file:** `internal/sandbox/execsandbox/run_test.go`

### TC-062-06 — execsandbox is the DEFAULT backend wired by `internal/runtime`; Podman is still reachable via config

- **Requirement:** REQ-062-05
- **Level:** L5 (wiring test + import-graph assertion)
- **Inputs:**
  - `runtime.ConfigFromEnv` with `AGENT_BUILDER_EXEC_SANDBOX_BIN` set (non-empty, points to a fake binary) and `AGENT_BUILDER_EXEC_BOX_LAUNCHER` unset
  - `runtime.Run(config, stdout)` with a fake task source returning one task and a fake executor (no real Claude invocation)
  - `go list -deps ./internal/runtime/...` inspected for import of `internal/sandbox/execsandbox` vs `internal/sandbox/podman`
- **Expected output:**
  - The wiring test confirms the box's runner is an `execsandbox.Runner` (or the block binary path is passed to execsandbox, not to the Podman launcher) when `AGENT_BUILDER_EXEC_SANDBOX_BIN` is set
  - `internal/runtime` imports `internal/sandbox/execsandbox` in its default path
  - Setting `AGENT_BUILDER_EXEC_BOX_LAUNCHER` instead (with `AGENT_BUILDER_EXEC_SANDBOX_BIN` unset or with an explicit `AGENT_BUILDER_SANDBOX_BACKEND=podman` env var) wires the Podman backend — confirming the fallback is still reachable
- **Test file:** `tests/cli/run_wiring_test.go` (or new file in that package)

### TC-062-07 — fitness function confirms execsandbox is the default, podman is not the primary import

- **Requirement:** REQ-062-05
- **Level:** L3/L5 (fitness script or Go test)
- **Inputs:** `make fitness` or a dedicated fitness step inspecting the `internal/runtime` import graph
- **Expected output:**
  - A new fitness step `fitness-exec-sandbox-default` exits `0` and prints a PASS line such as:
    `PASS fitness-exec-sandbox-default: internal/runtime wires execsandbox as the default run backend`
  - The fitness step is wired into `make fitness` so `make check` sees it
- **Test file:** `scripts/fitness-exec-sandbox-default.sh` (or inline in Makefile) + verified via `make fitness`

### TC-062-08 — L6 live: real exec-sandbox binary executes payloads under bubblewrap AND gvisor; a limit fires; Phase-0 capstone re-runs green on block backend

- **Requirement:** REQ-062-06
- **Level:** L6 (operator-observed; gated on `AGENT_BUILDER_LIVE_EXEC_SANDBOX=1`)
- **Inputs:**
  - Real built `exec-sandbox` binary at `AGENT_BUILDER_EXEC_SANDBOX_BIN`
  - `tier="bubblewrap"`: `Request{Command: []string{"echo","bubblewrap-ok"}, Limits: sandbox.Limits{WallClockTimeout: 5*time.Second}}`
  - `tier="gvisor"`: `Request{Command: []string{"echo","gvisor-ok"}, Limits: sandbox.Limits{WallClockTimeout: 5*time.Second}}`
  - Limit-fire sub-case: `tier="bubblewrap"` with `WallClockTimeout: 1*time.Second` and `Command: []string{"sleep","10"}` → expect `sandbox_status.status == "timeout"` surfaced as a non-nil error or an explicit timeout result
  - Live capstone: `go test ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032` with `AGENT_BUILDER_EXEC_SANDBOX_BIN=<path>` and `AGENT_BUILDER_EXEC_BOX_LAUNCHER` unset (or set to empty/nonexistent)
- **Expected output:**
  - bubblewrap sub-case: `Result.Stdout == "bubblewrap-ok\n"`, exit code `0`, `sandbox_status.tier == "bubblewrap"`, `sandbox_status.status == "clean"`
  - gvisor sub-case: `Result.Stdout == "gvisor-ok\n"`, exit code `0`, `sandbox_status.tier == "gvisor"`, `sandbox_status.status == "clean"`
  - Limit-fire sub-case: `sandbox_status.status == "timeout"` propagated (the error or result makes the timeout observably different from a clean run)
  - Live capstone: `--- PASS` with a real PR opened and cleaned up on the l6 sandbox; the full in-box gate (build/vet/test/gofmt/golangci-lint/dep-scan/code-scanner) is green; the block backend is confirmed active by the `AGENT_BUILDER_EXEC_SANDBOX_BIN` config presence and the absence of the Podman launcher in the run record
- **Harness command:**
  ```
  AGENT_BUILDER_LIVE_EXEC_SANDBOX=1 \
  AGENT_BUILDER_EXEC_SANDBOX_BIN=$(go env GOPATH)/bin/exec-sandbox \
  go test -count=1 -v ./internal/sandbox/execsandbox/... -run TestExecSandboxLive
  ```
  Followed by the Phase-0 capstone:
  ```
  AGENT_BUILDER_LIVE_E2E=1 \
  AGENT_BUILDER_LIVE_E2E_REMOTE=l6 \
  CLAUDE_CODE_OAUTH_TOKEN=<token> \
  AGENT_BUILDER_EXEC_SANDBOX_BIN=$(go env GOPATH)/bin/exec-sandbox \
  go test -count=1 -v ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032
  ```
- **Note:** L6 is gated on a host with `bwrap` and `runsc` installed, the exec-sandbox binary built, and Claude subscription credentials present. Mark as pending until the live run is executed.

## Verification plan

- **Highest level achievable in-repo:** L5 — `go test ./internal/sandbox/execsandbox/... ./tests/cli/...` and `make check` green (TC-062-01 through TC-062-07).
- **L6 (operator-observed):** live capstone re-runs green on the exec-sandbox block backend (TC-062-08) — pending live run on a host with `bwrap`, `runsc`, and `exec-sandbox` binary built.
- **Harness command for L5:**
  ```
  go test -count=1 -v ./internal/sandbox/execsandbox/... -run 'TestExecSandboxMarshalRequest|TestExecSandboxParseResult|TestExecSandboxBlockError|TestExecSandboxMissingBinary'
  go test -count=1 -v ./tests/cli/... -run 'TestRuntimeRunWiresExecSandboxBackend|TestPodmanBackendReachableViaConfig'
  make check
  ```
  Expected final line: `All checks passed.`

## Out of scope

- Wiring `secret_refs`, `vault_socket`, `audit_socket`, or `injection_mode` (deferred per ADR 035; vault block is still WIP)
- Deleting `internal/sandbox/podman` or `containment/execution-box/run.sh` (they stay as fallback)
- Changing the `sandbox.Runner` interface or the `sandbox.Limits` type (ADR 020 seam is unchanged)
- Firecracker tier support (hard-errors per exec-sandbox `backendFor()`)
- Adding `disk_mb` to `sandbox.Limits` (it is a new field not yet on the Go seam; send `0`)
