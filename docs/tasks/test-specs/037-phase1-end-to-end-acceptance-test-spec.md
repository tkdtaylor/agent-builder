# Test Spec 037: Phase 1 end-to-end acceptance

**Linked task:** [`docs/tasks/backlog/037-phase1-end-to-end-acceptance.md`](../backlog/037-phase1-end-to-end-acceptance.md)
**Written:** 2026-06-16
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-037-01 | TC-037-01, TC-037-04 | ✅ |
| REQ-037-02 | TC-037-02 | ✅ |
| REQ-037-03 | TC-037-03 | ✅ |
| REQ-037-04 | TC-037-04 | ✅ |

## Test cases

### TC-037-01: fake-Podman harness proves the pipeline selects a task, runs the box, and records evidence

- **Requirement:** REQ-037-01
- **Input:** full `runtime.Run` pipeline with a fake task source returning one ready task, a fake Podman adapter returning a successful containment result, a fake Gate returning OK, and a fake publisher recording a PR artifact; no real Podman or `srt` involved.
- **Expected output:** `runtime.Run` returns nil; the run record (when configured) contains `pick_task`, `attempt`, `verify`, `publish`, and `run_finished` events; `run_finished` has `outcome == "completed"`; no `srt` or `AGENT_BUILDER_SANDBOX_RUNTIME` reference appears in any output.
- **Edge cases:** a failed containment probe from the fake Podman adapter causes `run_finished` with `outcome == "failed"` and the error names the containment failure.

### TC-037-02: srt is not invoked anywhere in the pipeline

- **Requirement:** REQ-037-02
- **Input:** same fake pipeline as TC-037-01.
- **Expected output:** the fake subprocess recorder shows zero invocations of any path containing `srt`; `AGENT_BUILDER_SANDBOX_RUNTIME` is absent from all recorded subprocess environment snapshots.
- **Edge cases:** the fake Podman adapter must be confirmed to be the only `sandbox.Runner` in the pipeline, not a fallback to `sandboxruntime.Runner`.

### TC-037-03: live Podman harness proves containment launches and runs a command inside the box

- **Requirement:** REQ-037-03
- **Input:** real `podman.Runner` running `echo phase1-ok` with `--workload agent` (i.e. `runsc`) against a clean fixture worktree; Podman, `runsc`, and the Gate toolchain must be available.
- **Expected output:** `Result.Stdout` contains `phase1-ok`; exit code `0`; no adapter error; Podman probe output contains `TC-016 HOST: workload=agent runtime=runsc`.
- **Edge cases:** missing Podman or `runsc` skips the test with `t.Skip` and records the blocker in the coverage tracker; missing Gate toolchain directory fails the test (not a skip) because it names a configuration error, not an environment availability gap.

### TC-037-04: Phase 1 acceptance: agent-builder run uses Podman, not srt

- **Requirement:** REQ-037-01, REQ-037-04
- **Input:** `agent-builder run` invoked with all required env vars; `AGENT_BUILDER_SANDBOX_RUNTIME` absent; a fixture task root containing one ready task; a fake executor that writes a branch file; a fixture worktree passing the Gate; a fake git remote for publication.
- **Expected output:** command exits `0`; stdout contains `run completed: task <id>`; no mention of `srt` anywhere in stdout, stderr, or the run record; the run record's containment evidence references the Podman launcher.
- **Edge cases:** if the fixture task root has no ready task, the command exits `0` with `run idle: no ready task` — this is acceptable and is not a Phase 1 failure.

## Notes

Framework: Go `testing`. TC-037-01 and TC-037-02 are fake-Podman unit tests that run in CI without Podman. TC-037-03 is an optional live harness gated by `AGENT_BUILDER_LIVE_PODMAN=1`. TC-037-04 is a CLI-level integration test using fake executor and fake git/gh subprocesses. The L5 milestone is TC-037-01 through TC-037-04 green; L6 is TC-037-03 observed on a host with rootless Podman and `runsc`.
