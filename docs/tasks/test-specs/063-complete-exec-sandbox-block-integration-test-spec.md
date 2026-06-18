# Test spec — Task 063: complete exec-sandbox block integration

**Linked task:** `docs/tasks/backlog/063-complete-exec-sandbox-block-integration.md`
**Written:** 2026-06-18
**Status:** ready

## Context

The exec-sandbox adapter (`internal/sandbox/execsandbox/run.go`, task 062) wires the block
binary as the default `sandbox.Runner`. Running the real Phase-0 capstone against the block
exposed three remaining gaps after the worktree-mount gap was closed (exec-sandbox task 003
+ task-062 `run.workdir` forwarding):

1. **`renderCommand` sh-c translation.** agent-builder commands follow ADR 032's
   `/bin/sh`-entrypoint convention. The probe is `["-c","true"]`; a gate step might be
   `["sh","-c","go build ./..."]`. The adapter's current `renderCommand` naively
   shell-quotes every token, producing `'-c' 'true'` as the payload. The block runs
   `/usr/bin/sh /payload.sh`, which treats the payload as a script body — so `sh` sees a
   command named `-c` and exits 127. The fix: detect the `[-c <script>]` and
   `[sh|-c <script>]` forms and return `<script>` directly.

2. **Toolchain missing in-box.** The block sandbox runs `--clearenv` with
   `PATH=/usr/bin:/bin` only. `go`, `gofmt`, `golangci-lint`, `dep-scan`, `code-scanner`
   are absent. The adapter must pass a `FileRead{paths}` capability entry (exec-sandbox
   task 004) for the Go toolchain dir and the gate-tools dir, and set `PATH` via the
   block's env-provisioning input to mirror `containment/execution-box/run.sh` lines 56-57.

3. **Egress missing.** `wiring.origin_map` is sent empty today. The block's egress proxy
   routes via `origin_map`; without it, the gate's dep-scan / code-scanner network steps
   cannot reach CVE/malware DBs. The adapter must populate `origin_map` from the
   `EgressAllowlist`.

Exec-sandbox task 004 (`FileRead{paths}` + PATH/env provisioning) is a hard dependency for
gap 2. Gaps 1 and 3 are independent of task 004 and can be unit-tested against stubs.

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-063-01 | TC-063-01, TC-063-02 | yes |
| REQ-063-02 | TC-063-03 | yes |
| REQ-063-03 | TC-063-04 | yes |
| REQ-063-04 | TC-063-07 | yes |
| REQ-063-05 | TC-063-01 through TC-063-04 | yes |
| REQ-063-06 | TC-063-05 | yes |
| REQ-063-07 | TC-063-07 (spec-update assertion) | yes |
| REQ-063-08 | TC-063-06 | yes |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-063-01 — `renderCommand` sh-c translation (pure unit, no binary)

- **Requirement:** REQ-063-01, REQ-063-05
- **Level:** L5 (unit, no stub binary — tests `renderCommand` directly)
- **Test file:** `internal/sandbox/execsandbox/run_test.go`

Sub-cases (table-driven, one test function `TestRenderCommandTranslation`):

| Input `cmd` | Expected payload | Notes |
|-------------|-----------------|-------|
| `["-c","true"]` | `"true"` | ADR 032 probe form — must NOT produce `'-c' 'true'` |
| `["sh","-c","echo hi"]` | `"echo hi"` | shell prefix + -c form |
| `["/bin/sh","-c","gofmt -l ."]` | `"gofmt -l ."` | absolute shell path prefix |
| `["/usr/bin/sh","-c","go test ./..."]` | `"go test ./..."` | /usr/bin/sh prefix |
| `["bash","-c","golangci-lint run"]` | `"golangci-lint run"` | bash prefix |
| `["go","build","./..."]` | `"go build './...'"` | no -c; fallback shell-quoting |
| `["-c","echo $HOME"]` | `"echo $HOME"` | -c with dollar sign in script — not re-quoted |
| `[]` | `""` | empty command |
| `["-c"]` | fallback-quoted `"'-c'"` | -c with no script arg — not translated, fallback path |

- **Expected:** all sub-cases pass; notably `["-c","true"]` → `"true"` (not `"'-c' 'true'"`).

---

### TC-063-02 — probe form `["-c","true"]` through `Run()` produces payload `"true"` to the block

- **Requirement:** REQ-063-01, REQ-063-05
- **Level:** L5 (unit, stub binary that records stdin and returns a valid result)
- **Test file:** `internal/sandbox/execsandbox/run_test.go`

- **Input:** `sandbox.Request{Command: []string{"-c","true"}, Worktree: <tempDir>, Limits: sandbox.Limits{}}` with a stub binary that records the JSON written to its stdin and exits 0 with a valid `{"stdout":"","stderr":"","exit_code":0,"sandbox_status":{...}}` response.
- **Expected output:**
  - The recorded `RunRequest.Run.Payload` is exactly `"true"` (not `"'-c' 'true'"` or `"'-c'\n'true'"`).
  - `Run()` returns no error.
- **Why:** this is the capstone probe command; getting `exit_code 127` from the block meant the payload was wrong. This TC catches a regression of that exact form.

---

### TC-063-03 — marshalled `RunRequest` carries `FileRead` capability + `PATH` env for toolchain and gate-tools

- **Requirement:** REQ-063-02, REQ-063-05
- **Level:** L5 (unit, stub binary recording stdin)
- **Test file:** `internal/sandbox/execsandbox/run_test.go`

- **Input:**
  - `AGENT_BUILDER_GATE_TOOLS` env var set to a temp directory (or the runner constructed with explicit gate-tools path).
  - Go toolchain dir available (resolved by the adapter from `go env GOROOT` or a configured path; test may use a temp dir as a stand-in).
  - `sandbox.Request{Command: []string{"-c","true"}, Worktree: <tempDir>, Limits: sandbox.Limits{}}` with a stub binary.
- **Expected output (from the recorded RunRequest JSON):**
  - `run.profile.capabilities` contains at least one element with `"type":"FileRead"`.
  - The `FileRead` entry's `paths` array includes both the gate-tools dir path and the Go toolchain dir path (or a parent directory that contains it).
  - The env provisioning field (`run.env["PATH"]` or `run.path`, per whatever exec-sandbox task 004 implements) is non-empty and contains the gate-tools dir and the Go toolchain dir (e.g. `/usr/local/go/bin`).
  - The existing `NetConnect` capability (if `EgressAllowlist` is non-empty) is still present alongside `FileRead` in the capabilities array.
- **Edge case:** if `AGENT_BUILDER_GATE_TOOLS` is unset, the adapter falls back to the bundled default path; the test may skip the gate-tools assertions if the default path does not exist on the test host, but must assert that the PATH env field contains `/usr/local/go/bin` at minimum.

---

### TC-063-04 — marshalled `RunRequest` carries non-empty `wiring.origin_map` from `EgressAllowlist`

- **Requirement:** REQ-063-03, REQ-063-05
- **Level:** L5 (unit, stub binary recording stdin)
- **Test file:** `internal/sandbox/execsandbox/run_test.go`

- **Input:** `sandbox.Request{Command: []string{"-c","true"}, Worktree: <tempDir>, Limits: sandbox.Limits{EgressAllowlist: []string{"api.github.com:443","registry.npmjs.org:443"}}}` with a stub binary.
- **Expected output (from the recorded RunRequest JSON):**
  - `wiring.origin_map` is non-empty (not `{}`).
  - `wiring.origin_map["api.github.com"]` is present and equals `["api.github.com","443"]` (or equivalent `[addr, port]` pair).
  - `wiring.origin_map["registry.npmjs.org"]` is present and equals `["registry.npmjs.org","443"]`.
  - `run.profile.capabilities` still contains the `NetConnect` entry with the same allowlist (no regression of REQ-062-01).
- **Edge case:** empty `EgressAllowlist` → `wiring.origin_map` remains `{}` (no spurious entries).
- **Edge case:** malformed entry without `:port` → the adapter produces a non-nil error before invoking the binary (fail-loud, not a silent skip).

---

### TC-063-05 — live adapter toolchain probe: `command -v go && go version` resolves inside the block

- **Requirement:** REQ-063-06
- **Level:** L6 (live, gated on `AGENT_BUILDER_LIVE_EXEC_SANDBOX=1`)
- **Test file:** `internal/sandbox/execsandbox/live_test.go`
- **Test name:** `TestExecSandboxLiveToolchain`

- **Inputs:**
  - Real `exec-sandbox` binary at `AGENT_BUILDER_EXEC_SANDBOX_BIN` (built from `~/Code/Public/exec-sandbox`).
  - exec-sandbox task 004 merged and block binary rebuilt.
  - `AGENT_BUILDER_GATE_TOOLS` set to the agent-builder gate-tools dir (or the bundled path used by default).
  - `sandbox.Request{Command: []string{"-c","command -v go && go version"}, Worktree: <tempDir>, Tier: "bubblewrap", Limits: sandbox.Limits{WallClockTimeout: 30*time.Second}}`.
- **Expected output:**
  - `Run()` returns no error.
  - `Result.Stdout` contains `go version go` (the output of `go version`).
  - `Result.ExitCode` (or returned exit code) is `0`.
  - `sandbox_status.status == "clean"`.
- **Why this is not a smoke test:** it drives the REAL block binary with a REAL toolchain path, exercises the `FileRead` + PATH-provisioning wiring end-to-end, and asserts that the toolchain is callable from inside the sandbox — not just that the adapter doesn't error.

---

### TC-063-06 — L6 live Phase-0 capstone passes on block backend with full gate green

- **Requirement:** REQ-063-08
- **Level:** L6 (operator-observed; gated on `AGENT_BUILDER_LIVE_E2E=1`)
- **Test name:** `TestLivePhase0EndToEndAcceptance_TC032` (existing test, no new test needed)

- **Harness command:**
  ```
  AGENT_BUILDER_LIVE_E2E=1 \
  AGENT_BUILDER_LIVE_E2E_REMOTE=l6 \
  AGENT_BUILDER_PUBLISH_REMOTE=l6 \
  AGENT_BUILDER_EXEC_SANDBOX_BIN=$HOME/Code/Public/exec-sandbox/bin/exec-sandbox \
  CLAUDE_CODE_OAUTH_TOKEN=<from .env> \
  go test -count=1 -v ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032
  ```
  Prerequisites: l6 remote configured; `ANTHROPIC_API_KEY` unset (subscription OAuth path); exec-sandbox binary rebuilt after exec-sandbox task 004 merges; host has `bwrap` installed.

- **Expected output:**
  - `--- PASS: TestLivePhase0EndToEndAcceptance_TC032`
  - In-box gate fully green: `PASS go build`, `PASS go vet`, `PASS go test`, `PASS gofmt`, `PASS golangci-lint`, `PASS dep-scan`, `PASS code-scanner` — all seven steps passing inside the exec-sandbox block.
  - Real PR URL logged (e.g. `https://github.com/tkdtaylor/agent-builder-l6-sandbox/pull/N`), then closed and branch deleted by `t.Cleanup`.
  - `AGENT_BUILDER_EXEC_SANDBOX_BIN` present in the run environment and `AGENT_BUILDER_EXEC_BOX_LAUNCHER` absent — confirming block backend active.
- **Note:** stays `pending` until the live run is executed and the operator records the evidence.

---

### TC-063-07 — `make check` green + spec updated + fitness-exec-sandbox-default still passes

- **Requirement:** REQ-063-04, REQ-063-07
- **Level:** L3/L5
- **Test file:** n/a (fitness script + `make check`)

- **Expected:**
  - `make fitness` exits 0; output includes `PASS fitness-exec-sandbox-default: internal/runtime wires execsandbox as the default run backend` (no regression from task 062).
  - `go test ./...` → all packages green including `internal/sandbox/execsandbox`.
  - `make check` → `All checks passed.`
  - `docs/spec/interfaces.md` documents the `FileRead` + env/PATH provisioning wiring for the execsandbox adapter.
  - If `AGENT_BUILDER_GATE_TOOLS` is a new env var (not previously documented), `docs/spec/configuration.md` includes it.
  - Both spec files are updated **in the same commit as the code changes** (no stale spec).

---

## Verification plan

- **Highest level achievable in-repo:** L5 — TC-063-01 through TC-063-05 (minus the live sub-case) + `make check` green.
- **L6 (operator-observed):** live Phase-0 capstone with the block backend passes with full gate green (TC-063-06) — pending live run after exec-sandbox task 004 ships.
- **L5 harness command:**
  ```
  go test -count=1 -v ./internal/sandbox/execsandbox/... \
    -run 'TestRenderCommandTranslation|TestProbeFormPayload|TestFileReadAndPathForwarding|TestOriginMapPopulation'
  make check
  ```
  Expected final line: `All checks passed.`

## Out of scope

- Wiring `secret_refs`, `vault_socket`, `audit_socket`, `injection_mode` (deferred per ADR 035; vault WIP).
- Deleting `internal/sandbox/podman` or `containment/execution-box/run.sh` (launcher stays as fallback).
- gVisor tier for TC-063-05 live toolchain probe (bubblewrap is sufficient; gVisor is nice-to-have).
- Changing the `sandbox.Runner` interface or `sandbox.Limits` type (ADR 020 seam unchanged).
- Firecracker tier (`backendFor` in the block hard-errors on it; out of scope here).
