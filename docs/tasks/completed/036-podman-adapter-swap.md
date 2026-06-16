# Task 036: Podman adapter swap in default run wiring

**Project:** agent-builder
**Created:** 2026-06-16
**Status:** ready

## Goal

Remove the `@anthropic-ai/sandbox-runtime` (`srt`) requirement from the default `agent-builder run` pipeline and replace it with the Podman-backed `sandbox.Runner` from task 035, completing the adapter swap so the rented isolation is no longer a runtime dependency.

## Context

- Tech stack: Go, `internal/runtime/run.go`, `internal/sandbox/podman/`
- Roadmap: `docs/plans/roadmap.md` Phase 1 — "swap the rented isolation for it"
- Related ADRs: ADR 020 (exec-sandbox adapter seam), ADR 014 (Podman profile)
- Authoritative design: `autonomous-builder.md` §1 (adopt-to-bootstrap, build-to-ship) and §4 (substrate is rootless Podman)
- Dependencies: 028 (default run wiring), 035 (Podman adapter)

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-036-01 | `internal/runtime.ConfigFromEnv` no longer reads or requires `AGENT_BUILDER_SANDBOX_RUNTIME`; Podman adapter configuration (image tag, workload tier, egress allowlist path) is sourced from existing `EXEC_BOX_*` env vars or sensible defaults. | must have |
| REQ-036-02 | `internal/runtime.Run` constructs a `podman.Runner`-backed containment box instead of a `sandboxruntime.Runner`-backed one; `internal/runtime` no longer imports `internal/sandbox/sandboxruntime`. | must have |
| REQ-036-03 | `docs/spec/configuration.md` and `docs/spec/interfaces.md` are updated in the same commit: `AGENT_BUILDER_SANDBOX_RUNTIME` removed from the required variables table; `srt` removed from the outbound interfaces table; Podman adapter configuration documented in its place. | must have |
| REQ-036-04 | A new fitness function (or extension of existing fitness checks) confirms `internal/runtime` does not transitively import `internal/sandbox/sandboxruntime`; this fitness step is wired into `make fitness`. | must have |

## Readiness gate

- [ ] Test spec `036-podman-adapter-swap-test-spec.md` exists in `docs/tasks/test-specs/`
- [ ] All acceptance criteria below have a linked REQ ID
- [ ] Blocking tasks complete: 028, 035

## Acceptance criteria

- [ ] [REQ-036-01] `ConfigFromEnv` with `AGENT_BUILDER_SANDBOX_RUNTIME` absent returns a valid `Config` when Podman adapter env vars (or defaults) are available; existing tests that set `AGENT_BUILDER_SANDBOX_RUNTIME` are updated or removed.
- [ ] [REQ-036-02] `go list -json ./internal/runtime/...` shows no import path containing `sandboxruntime`; `make check` passes.
- [ ] [REQ-036-03] `docs/spec/configuration.md` contains no `AGENT_BUILDER_SANDBOX_RUNTIME` row; `docs/spec/interfaces.md` contains no `srt` CLI call in the outbound interfaces table; the Podman adapter's config surface is documented.
- [ ] [REQ-036-04] `make fitness` includes a step that passes with the message `PASS fitness-no-srt: internal/runtime does not import sandboxruntime`.

## Verification plan

- **Highest level achievable:** L5 — wiring test confirms the Podman adapter is constructed by `runtime.Run` and `srt` is never invoked, even through a fake subprocess recorder.
- **Level 5 - Validation harness command:**
  ```
  go test -count=1 -v ./tests/cli ./tests/runtime -run 'TestRuntimeRunWiresPodmanAdapter|TestNoSrtInvocation'
  ```
  Expected final assertion: `TC-036-02 runtime uses Podman adapter; sandboxruntime not imported`
- **Level 6 - Operator observation:**
  - Binary path: `agent-builder run` with real Podman available; no `AGENT_BUILDER_SANDBOX_RUNTIME` in the environment.
  - Targeted behaviour to observe: command completes without `srt` error; no `srt`-related output in stdout or stderr.
- **Cross-module state risk:** removing `AGENT_BUILDER_SANDBOX_RUNTIME` from required config is a breaking change to the Phase 0 `agent-builder run` contract; all callers that set this var must be updated and the spec updated in the same commit.
- **Runtime-visible surface:** `make fitness` output, `agent-builder run` stdout/stderr.

## Out of scope

- Removing the `internal/sandbox/sandboxruntime` package itself (it may be kept for reference or future deletion).
- Changing Gate semantics or the executor seam.
- Modifying the execution-box launcher or Containerfile.

## Notes

- `runtime.Config.SandboxRuntime` field should be removed or repurposed; the Podman adapter uses `EXEC_BOX_IMAGE`, `EXEC_BOX_WORKLOAD`, and the existing egress allowlist path — not a CLI path to `srt`.
- The fitness function can be a short shell script or Go test; pattern matches the existing `make fitness-supervisor-isolation` approach using `go list -json`.
- `docs/spec/configuration.md` and `docs/spec/interfaces.md` must be updated **in the same commit** as the code change per CLAUDE.md spec-update rule.
