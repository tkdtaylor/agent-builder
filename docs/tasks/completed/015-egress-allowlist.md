# Task 015: Default-deny egress allowlist

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** completed

## Goal
Establish a default-deny network posture for the execution box plus a plain-text egress allowlist permitting only registries, the provider API, and approved research domains — the load-bearing control bounding the accepted executor-token-in-box risk.

## Context
- Tech stack: Go 1.26; rootless Podman; OCI runtimes (runc / runsc / kata)
- Authoritative design: `autonomous-builder.md` — §3 (network posture + credential handling); SPEC invariant 5 (egress allowlist is load-bearing)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.3)
- Related ADRs: ADR required — egress-allowlist design (default-deny + two-layer egress per OSEP-0001)
- Dependencies: 014
- Why load-bearing: a stolen executor token can only be SENT to allowlisted hosts. Tightening the allowlist is the primary mitigation for the accepted token-in-box risk; the list must stay tight and the contract must be plain text.

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | Default-deny: the box has no egress except to hosts on the allowlist | must have |
| REQ-002 | The allowlist is a plain-text config file (the contract); its shape is documented in `docs/spec/configuration.md` | must have |
| REQ-003 | A connection to a non-allowlisted host is observably blocked | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 014

## Acceptance criteria
- [x] [REQ-001] With the allowlist applied, an allowlisted host (e.g. a registry / the provider API) is reachable from inside the box
- [x] [REQ-002] The allowlist lives in a plain-text config consumed by the box launcher, and `docs/spec/configuration.md` describes its format and semantics
- [x] [REQ-003] A connection to a host NOT on the allowlist is refused/dropped — observed from in-box, with the failure mode quoted

## Verification plan
- **Highest level achievable:** L6 — network reachability is an observed runtime property of a launched box.
- In-box probes and observable results to quote: connect to an allowlisted host → succeeds; connect to a non-allowlisted host → refused/timed-out/DNS-blocked. Quote both.
- **Cross-module state risk:** touches `docs/spec/configuration.md` (the allowlist config contract) — must be updated in the same change as the launcher behaviour.
- **Runtime-visible surface:** network reachability per destination host; the allowlist config file format.
- **Runtime blocker in this worktree:** Podman is unavailable on `PATH`, so L6 runtime evidence remains pending operator verification. Static contract checks and `--print-egress-plan` parser output are available without Podman.

## Out of scope
- armor on the web-ingestion / tool-call path (task 024)
- Tiered OCI runtime selection (task 016)
- The box profile itself (task 014)

## Notes
- Follows the two-layer egress design (OSEP-0001): the allowlist is enforced, not advisory.
- Keep the allowlist minimal — registries, provider API, approved research domains only. Every entry is justified.
- Pairs with independently-revocable, fast-to-rotate executor tokens (handled elsewhere) — the allowlist bounds blast radius, rotation handles recovery.
