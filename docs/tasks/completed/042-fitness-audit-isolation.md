# Task 042: fitness-audit-isolation (F-005)

**Project:** agent-builder
**Created:** 2026-06-16
**Status:** backlog

## Goal

Add a `make fitness-audit-isolation` target (fitness rule F-005) asserting that `internal/audit` imports no executor/LLM/web packages (and no `audit-trail` Go package — the block is reached only over `os/exec`) AND that wiring it into the supervisor (task 041) did not drag those into the supervisor's transitive import graph — making the ADR 026 leaf-package discipline an executable, blocking invariant.

## Context

- Tech stack: Go (shell + `go list` Makefile target)
- Governing ADR: `docs/architecture/decisions/026-audit-trail-consume-shipped-block.md` (supersedes ADR 025) — `internal/audit` stays a leaf reaching the block over a process boundary, so it must stay free of executor/LLM/web imports *and* free of any `audit-trail` Go import (the adapter uses `os/exec`, not a module import — ADR 026 Option A over Option C). A fitness check asserts this.
- Pattern to mirror: `Makefile` `fitness-supervisor-isolation` (line ~111) and `fitness-no-srt` (line ~123) — `go list -deps` piped through `awk`/`grep` for forbidden path segments, with a PASS/FAIL line. Wire into the `fitness` umbrella target and the `.PHONY` list; document in `docs/spec/fitness-functions.md` as F-005.
- **Model tier: balanced (sonnet)** — mechanical, pattern-matches existing fitness checks.
- Dependencies: 041 (the supervisor must already depend on `internal/audit` for the supervisor-graph half of the check to be meaningful).

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-042-01 | `make fitness-audit-isolation` asserts `go list -deps ./internal/audit/...` contains no executor/LLM/web path segment (same token set as F-003) nor any `audit-trail` block package, and prints a PASS line | must have |
| REQ-042-02 | The same target asserts the supervisor's transitive graph gained no executor/LLM/web package via the audit dependency (audit stayed a leaf through the supervisor seam) | must have |
| REQ-042-03 | A forbidden import in the audit graph makes the target exit non-zero with a FAIL line naming the offending package | must have |
| REQ-042-04 | The target is a prerequisite of `make fitness` (and `.PHONY`); `docs/spec/fitness-functions.md` gains an F-005 row + source-of-truth link to ADR 026, in the same commit | must have |

## Readiness gate

- [x] Test spec `042-fitness-audit-isolation-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [ ] Blocking task 041 complete

## Acceptance criteria

- [ ] [REQ-042-01] `make fitness-audit-isolation` exits zero on the clean tree and prints `PASS fitness-audit-isolation: …`; the check covers `go list -deps ./internal/audit/...` with the F-003 forbidden-token set
- [ ] [REQ-042-02] The check also inspects `go list -deps ./internal/supervisor/...` and confirms the audit wiring dragged in no executor/LLM/web package; the PASS line states both confirmations
- [ ] [REQ-042-03] A synthetic forbidden import into the audit graph produces a non-zero exit and a FAIL line naming the offending import chain (demonstrated per the F-001..F-003 negative-evidence convention, without committing a bad import)
- [ ] [REQ-042-04] `fitness-audit-isolation` is in the `fitness:` prerequisite list and `.PHONY`; `docs/spec/fitness-functions.md` has the F-005 row (asserts, threshold `0 violations`, command, severity `block`, why) and an ADR 026 source-of-truth link, in the feat commit

## Verification plan

- **Highest level achievable:** L6 — operator-observed PASS line. The fitness check's observable surface is its own stdout; running it and seeing PASS (plus a demonstrated FAIL on a synthetic violation) is the verification.
- **Level 5 — Validation harness command (if applicable):** N/A as a Go test; the harness is the Make target itself (see L6).
- **Level 6 — Operator observation (if applicable):**
  - Binary path: `make fitness-audit-isolation`
  - Targeted behaviour to observe: `PASS fitness-audit-isolation: internal/audit import graph contains no executor/LLM/web-fetch packages and the supervisor's audit dependency drags none in.` plus `make fitness` -> `Fitness checks passed.` (F-005 runs in the umbrella). Negative: a synthetic forbidden import yields a `FAIL fitness-audit-isolation:` line and non-zero exit.
- **Cross-module state risk:** none — the check is a static import-graph assertion. It consumes the import graph produced by task 041's wiring; if 041 widened the boundary, this check is what catches it.
- **Runtime-visible surface:** the fitness target's PASS/FAIL stdout. The executor must run the target and quote the PASS line (and the demonstrated FAIL).

## Out of scope

- Any change to `internal/audit` or the supervisor wiring (tasks 038–041) — this task only adds the guard.
- Re-implementing F-003 (`fitness-supervisor-isolation`) — F-005 is the audit-specific complement, deliberately overlapping at the supervisor graph.
- **Egress-attempt audit events** — deferred and spike-gated per ADR 026 decision 2.

## Notes

- Reuse the exact forbidden-token set from `fitness-supervisor-isolation` so the two checks agree on what "executor/LLM/web" means; add an `audit-trail` block-package token so an accidental Go import of the block (instead of the `os/exec` boundary) is caught.
- Follow the F-001..F-004 negative-evidence convention: demonstrate the FAIL path against a synthetic import list rather than committing a bad import.
- Update `docs/spec/fitness-functions.md` (F-005 row + source link) in the same commit. Do not edit spec during backlog authoring.
