# Task 037: Phase 1 end-to-end acceptance

**Project:** agent-builder
**Created:** 2026-06-16
**Status:** ready

## Goal

Prove that the complete Phase 1 adapter swap is correct and observable: `agent-builder run` dispatches tasks through rootless Podman execution-box containment instead of `@anthropic-ai/sandbox-runtime`, closing the chicken-and-egg loop described in the roadmap.

## Context

- Tech stack: Go, rootless Podman, `containment/execution-box/run.sh`
- Roadmap: `docs/plans/roadmap.md` Phase 1 — "resolves the chicken-and-egg"
- Related ADRs: ADR 020, ADR 014, ADR 015, ADR 016
- Authoritative design: `autonomous-builder.md` §1 (adopt-to-bootstrap, build-to-ship), §3 (containment skeleton is exec-sandbox v0 Tier 1)
- Dependencies: 035, 036

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-037-01 | A fake-Podman end-to-end harness proves the full `runtime.Run` pipeline — task selection, containment, executor, Gate, publication — completes without invoking `srt` or reading `AGENT_BUILDER_SANDBOX_RUNTIME`. | must have |
| REQ-037-02 | The run record (when configured) contains containment evidence referencing the Podman launcher and no reference to `srt` or `sandbox-runtime`. | must have |
| REQ-037-03 | A live-Podman harness proves a command runs inside the execution-box profile (`runsc` or `runc` depending on host availability) and the probe output names the expected runtime. | nice to have (L6 evidence) |
| REQ-037-04 | The Phase 1 acceptance milestone is recorded in `docs/plans/roadmap.md` with the evidence level reached (L5 fake-Podman or L6 live-Podman). | must have |

## Readiness gate

- [ ] Test spec `037-phase1-end-to-end-acceptance-test-spec.md` exists in `docs/tasks/test-specs/`
- [ ] All acceptance criteria below have a linked REQ ID
- [ ] Blocking tasks complete: 035, 036

## Acceptance criteria

- [ ] [REQ-037-01] `go test -count=1 -v ./tests/e2e -run TestPhase1EndToEndAcceptance` passes; the test asserts that the fake Podman runner was invoked (not `sandboxruntime.Runner`) and `srt` subprocess count is zero.
- [ ] [REQ-037-02] The run record contains a field or event naming `containment=podman` or equivalent launcher evidence; no `srt` string appears in the run record.
- [ ] [REQ-037-03] `AGENT_BUILDER_LIVE_PODMAN=1 go test -count=1 -v ./tests/e2e -run TestPhase1LivePodman` passes on a host with rootless Podman; test is skipped when Podman is unavailable.
- [ ] [REQ-037-04] `docs/plans/roadmap.md` Phase 1 section is updated with acceptance status and evidence level (matching the coverage-tracker convention).

## Verification plan

- **Highest level achievable:** L6 — live Podman launches execution-box; probe confirms `runsc` or `runc` runtime; `agent-builder run` completes without `srt`.
- **Level 5 - Validation harness command:**
  ```
  go test -count=1 -v ./tests/e2e -run TestPhase1EndToEndAcceptance
  ```
  Expected final assertion: `TC-037-01 Phase 1 accepted: task selected, Podman containment used, no srt invocation, run record clean`
- **Level 6 - Operator observation:**
  - Binary path: `AGENT_BUILDER_LIVE_PODMAN=1 go test -count=1 -v ./tests/e2e -run TestPhase1LivePodman` on a host with rootless Podman and `runsc`.
  - Targeted behaviour to observe: probe output line `TC-016 HOST: workload=agent runtime=runsc`; `Result.Stdout` contains expected fixture output; no `srt` in any subprocess log.
- **Cross-module state risk:** this is the integration test of the full pipeline; if any adapter in the chain still references `srt`, the TC-037-02 assertion fails.
- **Runtime-visible surface:** run record NDJSON, CLI stdout, Podman probe output.

## Out of scope

- Building vault, policy-engine, or audit-trail integration (Phase 2+).
- Testing the executor seam itself (covered by task 022).
- Publishing a PR to a real GitHub remote (covered by task 034; L6 evidence for that remains pending).

## Notes

- The `docs/plans/roadmap.md` Phase 1 acceptance note should follow the same format as the Phase 0 acceptance note: evidence level, harness command, and outstanding L6 blockers.
- If live Podman is available during this task, record L6 evidence here and in the coverage tracker; if not, record L5 and leave L6 as pending (same pattern as Phase 0).
- The fake-Podman harness should reuse `sandbox.FakeRunner` from `internal/sandbox` to confirm the Podman adapter is the only runner in the pipeline.
