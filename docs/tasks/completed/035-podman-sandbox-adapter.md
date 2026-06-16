# Task 035: Podman sandbox.Runner adapter

**Project:** agent-builder
**Created:** 2026-06-16
**Status:** ready

## Goal

Implement a new `sandbox.Runner` backend (`internal/sandbox/podman/`) that drives `containment/execution-box/run.sh` to run commands inside the rootless Podman execution-box, so the existing `run()` seam can be backed by the produced exec-sandbox profile instead of the rented `@anthropic-ai/sandbox-runtime`.

## Context

- Tech stack: Go, rootless Podman, `containment/execution-box/run.sh`
- Roadmap: `docs/plans/roadmap.md` Phase 1 — build exec-sandbox v0 behind the adapter seam
- Related ADRs: ADR 020 (exec-sandbox adapter seam), ADR 014 (Podman containment profile), ADR 015 (egress allowlist), ADR 016 (tiered runtime seam)
- Authoritative design: `autonomous-builder.md` §4 (substrate: rootless Podman, tiered runtimes runc→runsc→Kata)
- Dependencies: 014 (Podman profile), 015 (egress allowlist), 016 (tiered runtime), 020 (seam), 033 (gate toolchain)

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-035-01 | `*podman.Runner` satisfies the `sandbox.Runner` interface defined in `internal/sandbox`. The adapter runs commands inside the execution-box profile by invoking `containment/execution-box/run.sh`. | must have |
| REQ-035-02 | The adapter translates `sandbox.Request` fields to launcher flags and environment overrides: `Worktree` → `--worktree`; `Limits.EgressAllowlist` → a per-invocation temp file passed as `--egress-allowlist`; `Limits.CPUCount` → `EXEC_BOX_CPUS`; `Limits.MemoryBytes` → `EXEC_BOX_MEMORY`; `Limits.WallClockTimeout` → context deadline on the subprocess call. | must have |
| REQ-035-03 | A non-zero launcher exit code is returned as the integer exit code with a nil adapter error; non-zero exit is not an adapter failure. | must have |
| REQ-035-04 | Invalid requests (empty command, blank worktree) return a non-nil adapter error without invoking the launcher subprocess. | must have |
| REQ-035-05 | When `Limits.WallClockTimeout` is positive and the launcher exceeds it, `Run` returns a non-nil adapter error (context deadline exceeded or equivalent) and the subprocess is killed. | must have |

## Readiness gate

- [ ] Test spec `035-podman-sandbox-adapter-test-spec.md` exists in `docs/tasks/test-specs/`
- [ ] All acceptance criteria below have a linked REQ ID
- [ ] Blocking tasks complete: 014, 015, 016, 020, 033

## Acceptance criteria

- [ ] [REQ-035-01] `var _ sandbox.Runner = (*podman.Runner)(nil)` compiles; the adapter runs a trivial `echo` command inside the execution-box and returns the output.
- [ ] [REQ-035-02] A fake-launcher unit test confirms that `--worktree`, `--egress-allowlist`, and resource env-var overrides are present in the subprocess invocation for a fully-populated `sandbox.Request`.
- [ ] [REQ-035-03] A fake-launcher that exits `2` causes `Run` to return `exitCode == 2` and `err == nil`.
- [ ] [REQ-035-04] An empty `Command` causes `Run` to return a non-nil error and invoke the launcher zero times.
- [ ] [REQ-035-05] A fake-launcher that sleeps past a short `Limits.WallClockTimeout` causes `Run` to return a non-nil timeout error.

## Verification plan

- **Highest level achievable:** L6 — live Podman probe runs `echo` inside the execution-box with `runsc`; `Result.Stdout` contains the expected output.
- **Level 5 - Validation harness command:**
  ```
  go test -count=1 -v ./internal/sandbox/podman/... -run 'TestPodmanRunner'
  ```
  Expected final assertion: `TC-035-01 podman.Runner satisfies sandbox.Runner interface`
- **Level 6 - Operator observation:**
  - Binary path: `AGENT_BUILDER_LIVE_PODMAN=1 go test -count=1 -v ./internal/sandbox/podman/... -run TestPodmanRunnerLive`
  - Targeted behaviour to observe: stdout contains the fixture command output; `exit code 0`; Podman label `agent-builder.profile=execution-box` visible in `podman ps --all`.
- **Cross-module state risk:** per-invocation temp egress allowlist file must be cleaned up even on error; test must confirm no tmpfile leak.
- **Runtime-visible surface:** subprocess invocation flags, stdout/stderr capture, exit code, temp file lifecycle.

## Out of scope

- Swapping the Podman adapter into the default `runtime.Config` wiring (task 036).
- Building or modifying the execution-box `Containerfile` or launcher script (those are tasks 014–016, 033).
- vault or policy-engine integration (Phase 2+).

## Notes

- New package path: `internal/sandbox/podman/`; no new external Go dependencies — the adapter invokes `run.sh` as a subprocess.
- The per-invocation egress allowlist file must use `os.CreateTemp` and be removed in a `defer`; the format follows `containment/execution-box/egress.allowlist` (one `host:port # comment` per line).
- Zero `MemoryBytes` or zero `CPUCount` should leave the corresponding env var unset (letting the launcher use its defaults) rather than passing `0`.
- `docs/spec/interfaces.md` must be updated in the same commit to add `podman.Runner` as a concrete `sandbox.Runner` implementor.
