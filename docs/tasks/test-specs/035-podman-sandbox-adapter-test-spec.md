# Test Spec 035: Podman sandbox.Runner adapter

**Linked task:** [`docs/tasks/backlog/035-podman-sandbox-adapter.md`](../backlog/035-podman-sandbox-adapter.md)
**Written:** 2026-06-16
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-035-01 | TC-035-01, TC-035-06 | ✅ |
| REQ-035-02 | TC-035-02 | ✅ |
| REQ-035-03 | TC-035-03 | ✅ |
| REQ-035-04 | TC-035-04 | ✅ |
| REQ-035-05 | TC-035-05 | ✅ |

## Test cases

### TC-035-01: adapter satisfies sandbox.Runner interface at compile time

- **Requirement:** REQ-035-01
- **Input:** `*podman.Runner` value assigned to a `sandbox.Runner` variable.
- **Expected output:** code compiles without error; no type assertion required.
- **Edge cases:** interface check is enforced via a blank `var _ sandbox.Runner = (*podman.Runner)(nil)` assignment in the package.

### TC-035-02: valid request translates to correct launcher flags

- **Requirement:** REQ-035-02
- **Input:** `sandbox.Request` with a non-empty command, a valid worktree path, and populated `Limits` (wall-clock timeout, memory, CPU count, and a two-entry egress allowlist).
- **Expected output:** the fake launcher subprocess receives flags matching the expected set — `--worktree <path>`, `--egress-allowlist <tmpfile>`, `EXEC_BOX_CPUS`, `EXEC_BOX_MEMORY`, and `EXEC_BOX_PIDS_LIMIT` environment overrides. The temporary egress allowlist file contains the two allowlisted entries with required justification comments.
- **Edge cases:** zero memory and zero CPU leave those env vars unset (or at default); empty allowlist writes an empty file rather than omitting the flag.

### TC-035-03: non-zero launcher exit returns exit code with nil adapter error

- **Requirement:** REQ-035-03
- **Input:** fake launcher that exits with code `2`.
- **Expected output:** `Run` returns `exitCode == 2` and `err == nil`; `Result.Stdout` and `Result.Stderr` are populated from the fake launcher's output.
- **Edge cases:** exit code `1` and exit code `127` (command not found) are both treated as non-nil exit code, nil error.

### TC-035-04: invalid request returns adapter error without invoking launcher

- **Requirement:** REQ-035-04
- **Input:** `sandbox.Request` with an empty `Command` slice, or a blank worktree string.
- **Expected output:** `Run` returns a non-nil error wrapping `sandbox.ErrInvalidCommand` or a worktree-validation error; the fake launcher subprocess call count remains zero.
- **Edge cases:** a request with a blank string as `Command[0]` is treated the same as an empty slice.

### TC-035-05: wall-clock timeout surfaces as adapter error

- **Requirement:** REQ-035-05
- **Input:** `sandbox.Request` with `Limits.WallClockTimeout` set to a short duration; fake launcher that sleeps past the deadline.
- **Expected output:** `Run` returns a non-nil error identifying the timeout (context deadline exceeded or equivalent); `exitCode` is `-1` or `0` (implementation-defined sentinel).
- **Edge cases:** a zero-duration timeout leaves the timeout disabled; the launcher runs to completion.

### TC-035-06: live adapter probe runs inside the execution-box

- **Requirement:** REQ-035-01
- **Input:** real `podman.Runner` configured to run `echo hello` inside the execution-box; Podman and `runsc` must be available on the host.
- **Expected output:** `Result.Stdout` contains `hello`; `exitCode == 0`; `err == nil`.
- **Edge cases:** missing Podman skips the test with a `t.Skip`; the probe must exercise the real `run.sh` launcher path, not a fake.

## Notes

Framework: Go `testing`. TC-035-01 through TC-035-05 use a fake launcher subprocess (a minimal Go helper binary or a stub shell script) to avoid requiring Podman. TC-035-06 is an optional live harness gated by `AGENT_BUILDER_LIVE_PODMAN=1` and requires rootless Podman with `runsc`.
