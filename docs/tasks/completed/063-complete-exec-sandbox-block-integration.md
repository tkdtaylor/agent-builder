# Task 063: complete exec-sandbox block integration (renderCommand, toolchain, egress)

**Project:** agent-builder
**Created:** 2026-06-18
**Status:** backlog

## Goal

Close the three remaining gaps that prevent agent-builder's in-box gate from running GREEN
on the exec-sandbox block backend, then promote the block to the default and retire the
bootstrap Podman launcher — realising the ADR 035 / north-star transition from "builds the
blocks" to "runs on the blocks."

## Context

**DEPENDENCY (must ship first):**
exec-sandbox block **task 004** (`docs/tasks/backlog/004-toolchain-mount-and-path.md`) —
`FileRead{paths}` read-only host-path mounts + payload `PATH`/env provisioning. Everything
in REQ-063-02 and REQ-063-03 builds on the block capabilities introduced by that task.
Do **not** start implementation until exec-sandbox task 004 is merged and the block binary
is rebuilt.

**Branch continuation:**
Step 0 (start-task.sh) must continue from branch `task/062-adopt-exec-sandbox-run-backend`
rather than branching off `main`. The `execsandbox.Runner` adapter, the fitness step, and
the wiring tests from task 062 all live there. The diagnostic stash `stash@{0}` on that
branch (`diagnostic: renderCommand sh -c translation`) contains the fix prototype for
REQ-063-01.

**Empirically-mapped gap chain:**
Running the Phase-0 capstone against the block backend surfaced three gaps:

| Gap | Root cause | Status |
|-----|-----------|--------|
| 1 — worktree mount | block had no `run.workdir` support | CLOSED by exec-sandbox task 003 + task-062 adapter forwarding `req.Worktree` |
| 2 — `renderCommand` sh-c translation | block runs payload as `/usr/bin/sh /payload.sh`; adapter produced `'-c' 'true'` → exit 127 | **THIS TASK — REQ-063-01** |
| 3 — toolchain and egress missing in-box | `PATH=/usr/bin:/bin` only; no `go`/`golangci-lint`/scanners; no egress to CVE/malware DBs | **THIS TASK — REQ-063-02, REQ-063-03** |

Governing decision: ADR 035.
Related: ADR 032 (sh entrypoint / probe command form), ADR 034 (dep-scan no-go.sum pass).

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-063-01 | Fix `renderCommand` in `internal/sandbox/execsandbox/run.go`. The adapter currently passes the ADR-032 probe form `["-c","true"]` through naive shell-quoting, producing `'-c' 'true'` as the payload — which the block's `sh /payload.sh` interprets as a command named `-c` (exit 127). The fix: if the command vector begins with an optional shell token (`sh`, `/bin/sh`, `/usr/bin/sh`, `bash`) followed by `-c <script>`, extract and return `<script>` directly as the payload. All other forms fall back to the current shell-quoting path. Unit TCs: `["-c","true"]` → payload `"true"`; `["sh","-c","echo hi"]` → payload `"echo hi"`; `["/bin/sh","-c","gofmt -l ."]` → payload `"gofmt -l ."`; `["go","build","./..."]` → payload `"go build './...'"` (no translation). | must |
| REQ-063-02 | Forward the Go toolchain and gate-tools directory into the block via the `FileRead{paths}` capability (exec-sandbox task 004 required). In `buildRunRequest`, add a `FileRead` capability entry for: (a) the Go toolchain dir (resolved via `go env GOROOT`/`GOPATH` or a configured path, default `/usr/local/go/bin`); (b) the gate-tools dir (`AGENT_BUILDER_GATE_TOOLS` env var or the bundled `containment/execution-box/gate-tools` path). Also set `run.env["PATH"]` (or `run.path`, whichever exec-sandbox task 004 implements) to mirror what `containment/execution-box/run.sh` lines 56-57 provide: `<gate-tools-dir>:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`. The block backend after this change must be able to resolve `go`, `gofmt`, `golangci-lint`, `dep-scan`, and `code-scanner` from within the payload. | must |
| REQ-063-03 | Forward egress through the block's `NetConnect` capability AND populate `wiring.origin_map`. Map `req.Limits.EgressAllowlist` → `profile.capabilities[NetConnect].allowlist` (already done in task 062) AND populate `wiring.origin_map` with the same hosts (the block's egress proxy uses `origin_map` to route requests). Today the adapter sends `wiring.origin_map={}` (empty), which causes the gate's dep-scan / code-scanner CVE+malware DB fetches to fail through the block's proxy. The fix: for each host in `EgressAllowlist`, add an entry to `wiring.origin_map` (key = host, value = the host's `[addr, port]` pair derived from parsing the `host:port` allowlist entry). Deferred (no change): `secret_refs`, `vault_socket`, `audit_socket` remain empty (vault block is still WIP). | must |
| REQ-063-04 | The exec-sandbox backend becomes the DEFAULT when `AGENT_BUILDER_EXEC_SANDBOX_BIN` is set (already wired in task 062 adapter swap). Confirm the existing `fitness-exec-sandbox-default` fitness step still passes after this task's changes. The Podman launcher (`AGENT_BUILDER_EXEC_BOX_LAUNCHER`) remains selectable as a fallback when `AGENT_BUILDER_EXEC_SANDBOX_BIN` is unset. `make check` exit 0. | must |
| REQ-063-05 | Unit tests for REQ-063-01 through REQ-063-03 in `internal/sandbox/execsandbox/run_test.go`. TC-063-01 through TC-063-05 as described in the test spec. `go test ./internal/sandbox/execsandbox/...` green. | must |
| REQ-063-06 | A live-gated adapter integration test (gated on `AGENT_BUILDER_LIVE_EXEC_SANDBOX=1`) runs `command -v go && go version` inside the block via the adapter and asserts the toolchain is resolvable in-box. | must |
| REQ-063-07 | `docs/spec/interfaces.md` updated in the same commit: document the `FileRead` + `run.env`/`run.path` wiring and the `origin_map` population contract. `docs/spec/configuration.md` updated with `AGENT_BUILDER_GATE_TOOLS` if it becomes a new env var. | must |
| REQ-063-08 | L6 live Phase-0 capstone re-runs GREEN on the block backend (real PR opened+cleaned on the l6 sandbox, full in-box gate green: build/vet/test/gofmt/golangci-lint/dep-scan/code-scanner all PASS with the exec-sandbox block as the containment backend, `AGENT_BUILDER_EXEC_SANDBOX_BIN` set, `AGENT_BUILDER_EXEC_BOX_LAUNCHER` unset). | must (L6 gated) |

## Readiness gate

Before writing any code, verify:

- [ ] Test spec `063-complete-exec-sandbox-block-integration-test-spec.md` exists in `docs/tasks/test-specs/`
- [ ] exec-sandbox block task 004 (`FileRead{paths}` + PATH/env provisioning) is merged and the block binary is rebuilt
- [ ] Working on branch `task/062-adopt-exec-sandbox-run-backend` (or a branch cut from it)
- [ ] `stash@{0}` on that branch (`diagnostic: renderCommand sh -c translation`) inspected — apply it as the prototype for REQ-063-01

## Acceptance criteria

- [ ] [REQ-063-01] TC-063-01: `renderCommand(["-c","true"])` → payload `"true"`; `renderCommand(["sh","-c","echo hi"])` → `"echo hi"`; `renderCommand(["/bin/sh","-c","gofmt -l ."])` → `"gofmt -l ."`; `renderCommand(["go","build","./..."])` → shell-quoted fallback (not translated)
- [ ] [REQ-063-01] TC-063-02: probe form `["-c","true"]` sent through `Run()` — stub binary records payload `"true"`, run succeeds (no exit 127)
- [ ] [REQ-063-02] TC-063-03: marshalled `RunRequest` carries a `FileRead` capability entry with the Go toolchain dir and gate-tools dir, and `run.env["PATH"]` (or equivalent) contains those dirs prepended to the default PATH
- [ ] [REQ-063-03] TC-063-04: marshalled `RunRequest` carries non-empty `wiring.origin_map` when `EgressAllowlist` is non-empty; each allowlist host maps to its addr/port pair in `origin_map`
- [ ] [REQ-063-06] TC-063-05: live adapter test runs `command -v go && go version` inside the block and asserts exit code 0 and `go version` in stdout (gated on `AGENT_BUILDER_LIVE_EXEC_SANDBOX=1`)
- [ ] [REQ-063-04] `make fitness` passes `fitness-exec-sandbox-default` (no regression); `make check` exit 0
- [ ] [REQ-063-07] `docs/spec/interfaces.md` and (if applicable) `docs/spec/configuration.md` updated in same commit as code
- [ ] [REQ-063-08] TC-063-06: L6 live — capstone `TestLivePhase0EndToEndAcceptance_TC032` with `AGENT_BUILDER_EXEC_SANDBOX_BIN` set passes with full in-box gate green

## Verification plan

- **Highest level achievable:** L6 — the live Phase-0 capstone passes with the block as the
  containment backend.

- **Level 5 — Validation harness command:**
  ```
  go test -count=1 -v ./internal/sandbox/execsandbox/... -run 'TestRenderCommandTranslation|TestProbeFormPayload|TestFileReadAndPathForwarding|TestOriginMapPopulation'
  make check
  ```
  Expected final assertion: `All checks passed.`

- **Level 5 live-gated adapter test:**
  ```
  AGENT_BUILDER_LIVE_EXEC_SANDBOX=1 \
  AGENT_BUILDER_EXEC_SANDBOX_BIN=$HOME/Code/Public/exec-sandbox/bin/exec-sandbox \
  go test -count=1 -v ./internal/sandbox/execsandbox/... -run TestExecSandboxLiveToolchain
  ```
  Expected: `go version go1.XX` in stdout, exit code 0.

- **Level 6 — Operator observation (full capstone):**
  ```
  AGENT_BUILDER_LIVE_E2E=1 \
  AGENT_BUILDER_LIVE_E2E_REMOTE=l6 \
  AGENT_BUILDER_PUBLISH_REMOTE=l6 \
  AGENT_BUILDER_EXEC_SANDBOX_BIN=$HOME/Code/Public/exec-sandbox/bin/exec-sandbox \
  CLAUDE_CODE_OAUTH_TOKEN=<from .env> \
  go test -count=1 -v ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032
  ```
  Prerequisites: `l6` git remote configured (`git remote add l6 https://github.com/tkdtaylor/agent-builder-l6-sandbox.git`); `ANTHROPIC_API_KEY` unset (subscription OAuth path); exec-sandbox binary built (`go build -o bin/exec-sandbox ./...` in `~/Code/Public/exec-sandbox`); exec-sandbox task 004 merged and block rebuilt; host has `bwrap` installed.

  Targeted runtime observations:
  - `--- PASS` for `TestLivePhase0EndToEndAcceptance_TC032`
  - Full in-box gate green: `PASS go build`, `PASS go vet`, `PASS go test`, `PASS gofmt`, `PASS golangci-lint`, `PASS dep-scan`, `PASS code-scanner`
  - Real PR opened and cleaned on l6 sandbox (PR URL logged, then CLOSED + branch deleted by t.Cleanup)
  - `AGENT_BUILDER_EXEC_SANDBOX_BIN` set and `AGENT_BUILDER_EXEC_BOX_LAUNCHER` absent from the run confirms block is the active backend

- **Cross-module state risk:** `wiring.origin_map` and `FileRead` capability are new fields in the marshalled `RunRequest` — must verify they appear in the stub binary's recorded stdin (TC-063-03, TC-063-04).

- **Runtime-visible surface:** in-box gate output (build/vet/test/gofmt/lint/dep-scan/code-scanner) and real PR on l6 sandbox.

## Out of scope

- Wiring `secret_refs`, `vault_socket`, `audit_socket`, or `injection_mode` (deferred per ADR 035; vault block still WIP)
- Deleting `internal/sandbox/podman` or `containment/execution-box/run.sh` (Podman launcher stays as fallback; deletion is a separate Ask-first decision)
- Adding `disk_mb` to `sandbox.Limits` (send `0`; additive follow-on)
- Changing the `sandbox.Runner` interface or `sandbox.Limits` type (ADR 020 seam unchanged)
- Firecracker tier support (hard-errors in block's `backendFor`)
- gVisor tier in the live-gated toolchain test (bwrap is sufficient to verify toolchain forwarding; gVisor path is a nice-to-have)

## Notes

- The `renderCommand` stash fix on `task/062-adopt-exec-sandbox-run-backend` is at `stash@{0}`: `git stash show -p stash@{0}` shows the prototype. Unstash, clean up, and write the unit tests before applying.
- `origin_map` key/value shape: the block's `wiring.origin_map` type is `map[string][2]string` (already in the `wiringData` struct on the task-062 adapter). Parse each `EgressAllowlist` entry as `host:port` and map `host → [host, port]`.
- `AGENT_BUILDER_GATE_TOOLS` should mirror the existing `AGENT_BUILDER_EXEC_BOX_LAUNCHER` convention: an env var pointing to the gate-tools dir; default is the bundled `containment/execution-box/gate-tools` path relative to the binary's location or `$GOPATH/src/github.com/tkdtaylor/agent-builder/containment/execution-box/gate-tools`.
- The exec-sandbox task 004 shape for env provisioning (`run.env` map vs `run.path` list) is not yet decided; ADR 005 in the exec-sandbox repo will settle it during that task. Mirror whatever shape ships — do not assume `run.env`.
