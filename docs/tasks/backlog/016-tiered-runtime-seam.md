# Task 016: Tiered OCI runtime selection seam

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** backlog

## Goal
Add a `--runtime` selection seam under the same rootless Podman that maps a configured tier to an OCI runtime (`runc` for normal dev / reproducibility, gVisor `runsc` for agent / untrusted code, Kata+Firecracker later), with `runsc` as the agent default — gated on a tested Go-toolchain compatibility result.

## Context
- Tech stack: Go 1.26; rootless Podman; OCI runtimes (runc / runsc / kata)
- Authoritative design: `autonomous-builder.md` — §4 (tiered runtime + gVisor gotcha)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.3)
- Related ADRs: ADR required — runtime tiering decision + recorded gVisor compatibility finding
- Dependencies: 014
- gVisor gotcha: some Go build toolchains hit unimplemented syscalls under `runsc`. The actual toolchain MUST be tested under gVisor before it is committed as the agent default; record a bubblewrap/Kata fallback if a syscall gap is found.

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | Runtime is selectable via config/flag, mapping to Podman `--runtime` | must have |
| REQ-002 | The Go-toolchain compatibility result under `runsc` is documented and tested (or a fallback is recorded if a syscall gap is hit) | must have |
| REQ-003 | A default tier is set per workload: dev = `runc`, agent = `runsc` | must have |

## Readiness gate
- [ ] Test spec exists in `docs/tasks/test-specs/`
- [ ] All acceptance criteria have a linked REQ ID
- [ ] Blocking tasks complete: 014

## Acceptance criteria
- [ ] [REQ-001] A config value / `--runtime` flag selects the OCI runtime and the launched box runs under the selected runtime (observable)
- [ ] [REQ-002] `go build` of a trivial module is run under `runsc` and the result (success, or the specific unimplemented-syscall gap + chosen fallback) is recorded in the ADR
- [ ] [REQ-003] With no override, dev workloads launch under `runc` and agent workloads launch under `runsc`

## Verification plan
- **Highest level achievable:** L6 — active runtime and build success/failure are observed on a launched box.
- In-box / host probes and observable results to quote: launch under `runc` and (if available) `runsc`; observe the active runtime; run `go build` of a trivial module — succeeds, or record the syscall gap and the fallback chosen. Quote results for each runtime exercised.
- **Cross-module state risk:** none — selection seam is config-driven and additive over the task 014 profile.
- **Runtime-visible surface:** which OCI runtime is active for a box; whether the Go toolchain builds under that runtime.

## Out of scope
- The box profile itself (task 014)
- Egress allowlist / network posture (task 015)
- Kata+Firecracker tier implementation (later)

## Notes
- Same rootless Podman across all tiers — only the OCI runtime changes.
- Route by quota + sensitivity + cost lives elsewhere; this task only provides the selectable seam + default mapping.
- If `runsc` fails the toolchain probe, the recorded fallback (bubblewrap / Kata) is part of the deliverable, not a blocker.
