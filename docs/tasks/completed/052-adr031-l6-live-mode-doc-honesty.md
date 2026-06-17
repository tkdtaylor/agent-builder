# Task 052: ADR 031 + doc honesty (no production code)

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** backlog

## Goal

Record the L6 live-mode probe architecture in a new ADR and update two plan files to remove stale guidance, so that operators have an accurate reference for running the live probes after tasks 053–055 land. No production code is written.

Two deliverables:

1. `docs/architecture/decisions/031-l6-live-mode-probes.md` — records that the existing fake L5 tests stay unchanged as the deterministic gate; new env-gated live tests use `AGENT_BUILDER_LIVE_PUBLISH` and `AGENT_BUILDER_LIVE_E2E`; `claude`/gate/publisher run host-side; the Podman box runs only `/bin/true` as a liveness probe; live PRs target the private `l6` sandbox remote and self-clean.
2. Edits to `docs/plans/phase0-l6-verification-checklist.md` and `docs/plans/l6-operator-runbook.md`: purge `AGENT_BUILDER_SANDBOX_RUNTIME=srt` from the 028/032 rows; remove the invalid `--task-root docs/tasks/...` argv from the `run` subcommand; replace the 032/034 Section 3 table entries with the new live-test commands; give 022/028 the full required env contract; document claude-host-side so operators don't expect claude in the box.

## Context

- **No ADR needed for the plan-file edits** — they fix stale content, not design decisions.
- **ADR 031** is the load-bearing artifact: it establishes the `AGENT_BUILDER_LIVE_*` env-gate convention and records the settled architectural fact (host-side execution) that 053/054/055 rely on.
- The doc-honesty test mechanism (TC-005 in `tests/e2e/phase0_end_to_end_acceptance_test.go`) is the established pattern; new sub-tests in that same file validate this task.
- **Model tier: fast (haiku)** — doc-only write; no code judgement needed.
- **Dependency:** none. Do first; 053/054/055 all depend on this.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-052-01 | `docs/architecture/decisions/031-l6-live-mode-probes.md` exists, is non-empty, and records: `AGENT_BUILDER_LIVE_PUBLISH` env gate; `AGENT_BUILDER_LIVE_E2E` env gate; claude/gate/publisher run host-side; Podman box = `/bin/true` liveness probe; live PRs hit `l6` and self-clean; supersedes `AGENT_BUILDER_SANDBOX_RUNTIME=srt` guidance (references ADR 021 + ADR 026); file does NOT contain `AGENT_BUILDER_SANDBOX_RUNTIME=srt` | must have |
| REQ-052-02 | `docs/plans/phase0-l6-verification-checklist.md` and `docs/plans/l6-operator-runbook.md`: `AGENT_BUILDER_SANDBOX_RUNTIME=srt` absent from 028/032 rows; `--task-root docs/tasks` absent from any `run` subcommand argv; 034 and 032 rows updated to the new live-test commands (containing `AGENT_BUILDER_LIVE_PUBLISH` and `AGENT_BUILDER_LIVE_E2E` respectively); 022/028 rows carry the full env contract including `ANTHROPIC_API_KEY`; at least one file documents that claude runs host-side | must have |
| REQ-052-03 | Existing TC-005 doc-honesty sub-test in `TestPhase0EndToEndAcceptance` remains green after all edits — task 052 must not break the `fake-provider L5`, `Podman`, and `srt`-as-removed invariants | must have |
| REQ-052-04 | `make check` passes after all doc edits and new test additions | must have |

## Readiness gate

- [x] Test spec `052-adr031-l6-live-mode-doc-honesty-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [x] No blocking dependencies

## Acceptance criteria

- [ ] [REQ-052-01] TC-052-01: `docs/architecture/decisions/031-l6-live-mode-probes.md` exists and passes all 9 content assertions (presence of `AGENT_BUILDER_LIVE_PUBLISH`, `AGENT_BUILDER_LIVE_E2E`, `host-side`, `/bin/true`, `ADR 021`, `ADR 026`, self-cleanup language; absence of `AGENT_BUILDER_SANDBOX_RUNTIME=srt`)
- [ ] [REQ-052-02] TC-052-02: both plan files pass all 7 content assertions (3 negative: no `AGENT_BUILDER_SANDBOX_RUNTIME=srt`, no `--task-root docs/tasks`; 4 positive: live-test commands for 034/032, `ANTHROPIC_API_KEY` in env contract, claude-host-side documentation)
- [ ] [REQ-052-03] TC-052-03: `go test ./tests/e2e -run TestPhase0EndToEndAcceptance/TC-005` exits 0 — the existing doc-honesty sub-test still passes
- [ ] [REQ-052-04] TC-052-04: `make check` exits 0 after all changes

## Verification plan

- **Highest level achievable:** L5 — doc-honesty sub-tests run inside `go test ./tests/e2e` without any live host.
- **L5 harness command:**
  ```
  go test ./tests/e2e -run TestPhase0EndToEndAcceptance -count=1 -v
  ```
  Expected: all TC-052 sub-tests PASS; TC-005 still PASS; exit 0.
- **Full gate command:**
  ```
  make check
  ```
  Expected: `All checks passed.` exit 0.
- **L6 residual:** none — this task is documentation only; it has no runtime surface.
- **Cross-module state risk:** none — no Go source files are touched.

## Out of scope

- Any new Go test file other than additions inside `tests/e2e/phase0_end_to_end_acceptance_test.go` (or a doc_honesty sibling in the same package).
- Changes to `docs/spec/` — no externally-visible behavior changes.
- Changes to `docs/plans/roadmap.md` or `docs/plans/l6-evidence.txt`.
- The `scripts/l6-probe.sh` rewiring (that is task 055).
- Writing any of the new live test files (that is tasks 053/054).

## Notes

- ADR 031 should be modeled after ADR 030 for structure: Date, Status, Task, Related, Context, Decision, Consequences sections.
- The "Related" field should name ADR 021 (srt removed), ADR 026 (Podman containment), and ADR 016 (tiered runtime seam).
- In the plan-file edits, rewrite the stale rows **in place** — do not append. The ADR keeps the history; the plan files keep the current truth (per the spec-snapshot convention in CLAUDE.md).
- The new live-test commands for the Section 3 table:
  - 034: `env AGENT_BUILDER_LIVE_PUBLISH=1 AGENT_BUILDER_LIVE_PUBLISH_REMOTE=$remote go test -count=1 -v ./tests/publisher -run TestLiveBranchPRPublication_TC034`
  - 032: `env AGENT_BUILDER_LIVE_E2E=1 AGENT_BUILDER_LIVE_E2E_REMOTE=$remote go test -count=1 -v ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032`
  - 028: `env ANTHROPIC_API_KEY=<key> AGENT_BUILDER_TASK_ROOT=<fixture> AGENT_BUILDER_WORKTREE=<fixture> AGENT_BUILDER_PUBLISH_REMOTE=<remote> AGENT_BUILDER_RUN_TIMEOUT=300s AGENT_BUILDER_MAX_ATTEMPTS=1 AGENT_BUILDER_RUN_RECORD=<tmp> go run ./cmd/agent-builder run`
  - 022: same env contract as 028 but note that the executor (claude) produces the branch host-side and the box runs only `/bin/true`
