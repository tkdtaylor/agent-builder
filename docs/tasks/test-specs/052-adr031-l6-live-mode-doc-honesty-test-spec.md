# Test Spec 052: ADR 031 + doc honesty (no production code)

**Linked task:** [`docs/tasks/backlog/052-adr031-l6-live-mode-doc-honesty.md`](../backlog/052-adr031-l6-live-mode-doc-honesty.md)
**Written:** 2026-06-17
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-052-01 | TC-052-01 | ⏳ |
| REQ-052-02 | TC-052-02 | ⏳ |
| REQ-052-03 | TC-052-03 | ⏳ |
| REQ-052-04 | TC-052-04 (regression) | ⏳ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

---

## Context: doc-honesty assertion mechanism

Task 052 creates only documentation artifacts — ADR 031, plus edits to two plan files. The existing doc-honesty mechanism is TC-005 in `tests/e2e/phase0_end_to_end_acceptance_test.go`, which reads live project docs at `filepath.Join("..", "..")` and asserts specific content invariants. New test cases follow that same pattern: they are additions to (or new assertions in) the existing `TestPhase0EndToEndAcceptance` test under a new `TC-052_*` sub-test, OR they are static content assertions run via `go test ./tests/e2e` that read the docs. No live host is required.

The assertions in this spec are the **inputs** to those test additions. The executor must add sub-tests or assertions inside `tests/e2e/phase0_end_to_end_acceptance_test.go` (or in a new `tests/e2e/doc_honesty_052_test.go`) that verify each content invariant named below.

---

## Test cases

### TC-052-01: ADR 031 exists and records the correct architectural facts

- **Requirement:** REQ-052-01
- **Mechanism:** `go test ./tests/e2e` reads `docs/architecture/decisions/031-l6-live-mode-probes.md` and asserts its presence and key content strings.
- **Assertions (all must hold):**
  1. The file exists and is non-empty.
  2. Contains the string `AGENT_BUILDER_LIVE_PUBLISH` (the new env gate for 034).
  3. Contains the string `AGENT_BUILDER_LIVE_E2E` (the new env gate for 032).
  4. Contains the string `host-side` (records that claude/gate/publisher run on the host, not in the box).
  5. Contains the string `/bin/true` (records that the Podman box runs only the liveness probe).
  6. Contains the string `ADR 021` (supersession reference — srt removal).
  7. Contains the string `ADR 026` (Podman containment reference).
  8. Contains the string `self-clean` OR `self-cleanup` OR `close PR` (records cleanup discipline for live PRs).
  9. Does NOT contain the string `AGENT_BUILDER_SANDBOX_RUNTIME=srt` (the stale srt env var must be absent from the ADR).
- **Edge cases:**
  - The file must exist at the expected path `docs/architecture/decisions/031-l6-live-mode-probes.md`; a missing file is a test failure (not a skip).

### TC-052-02: phase0-l6-verification-checklist.md and l6-operator-runbook.md are free of stale srt and invalid argv

- **Requirement:** REQ-052-02
- **Mechanism:** `go test ./tests/e2e` reads `docs/plans/phase0-l6-verification-checklist.md` and `docs/plans/l6-operator-runbook.md` and asserts the following negative and positive content invariants.
- **Negative assertions (all must be absent):**
  1. `docs/plans/phase0-l6-verification-checklist.md` does NOT contain `AGENT_BUILDER_SANDBOX_RUNTIME=srt` in any command block for rows 028 or 032.
  2. `docs/plans/l6-operator-runbook.md` does NOT contain `AGENT_BUILDER_SANDBOX_RUNTIME=srt` in the Section 3 table rows for 028 or 032.
  3. Neither file contains the substring `--task-root docs/tasks` in any `run` subcommand invocation (the `run` subcommand takes no positional arguments — `internal/cli/cli.go:104-106`; this was the invalid argv in the stale docs).
- **Positive assertions (all must be present):**
  4. `docs/plans/l6-operator-runbook.md` Section 3 table row for 034 contains `AGENT_BUILDER_LIVE_PUBLISH` (updated to the new live-test command).
  5. `docs/plans/l6-operator-runbook.md` Section 3 table row for 032 contains `AGENT_BUILDER_LIVE_E2E` (updated to the new live-test command).
  6. At least one of the two files contains the string `ANTHROPIC_API_KEY` in a context that documents the required env contract for 022 or 028 (giving 022/028 the full env contract, as stated in the plan).
  7. At least one of the two files documents that claude runs host-side (contains the string `host-side` or `host side` or `runs on the host`).
- **Edge cases:**
  - When grepping for `AGENT_BUILDER_SANDBOX_RUNTIME=srt`, also check the `phase0-l6-verification-checklist.md` 028 and 032 probe blocks — those were the stale entries.
  - The `--task-root docs/tasks` substring is the minimal negative discriminator; matching `--task-root docs/tasks/...` (with trailing ellipsis) is equally forbidden.

### TC-052-03: existing TC-005 doc-honesty assertions still pass

- **Requirement:** REQ-052-03
- **Mechanism:** run `go test ./tests/e2e -run TestPhase0EndToEndAcceptance/TC-005` (or the full test). TC-005 asserts `fake-provider L5`, `Podman`, and that `srt` appears only framed as `removed`/`historical`. These must remain green — task 052's doc edits must not accidentally break TC-005 invariants.
- **Expected output:** `PASS` on the TC-005 sub-test, exit 0.
- **Edge cases:**
  - Task 052 adds/changes plan-file content. TC-005 reads the roadmap and SPEC but NOT the plan files, so the risk is low. Confirm the executor does not accidentally modify `docs/spec/SPEC.md` or `docs/plans/roadmap.md` in ways that violate TC-005.

### TC-052-04: `make check` passes (regression guard)

- **Requirement:** REQ-052-04
- **Mechanism:** after all doc edits, run `make check`. The gate runs `go test ./...` which includes the new TC-052 assertions; it must pass cleanly with no new test failures.
- **Expected output:** `All checks passed.` exit 0.
- **Edge cases:**
  - No Go source files are changed by task 052. Compilation and lint must stay green with no dependency on task 052's changes.

---

## Post-implementation verification

- [ ] All four test cases above pass via `go test ./tests/e2e` (L5, no live host required)
- [ ] `make check` passes after the doc and test additions
- [ ] TC-052-01: ADR 031 file exists, `docs/architecture/decisions/031-l6-live-mode-probes.md`
- [ ] TC-052-02: no `AGENT_BUILDER_SANDBOX_RUNTIME=srt` in 028/032 rows; no `--task-root docs/tasks` in any `run` argv; new live-test commands present for 034/032
- [ ] TC-052-03: existing TC-005 sub-test still PASS
- [ ] L6 residual: none — this task has no runtime surface (doc-only)

## Test framework notes

Framework: add new sub-tests `TC-052_adr031_exists`, `TC-052_plan_files_no_stale_srt`, and `TC-052_plan_files_live_commands` inside `TestPhase0EndToEndAcceptance` in `tests/e2e/phase0_end_to_end_acceptance_test.go`. These follow the exact same pattern as TC-005 — `readFile(t, filepath.Join(root, "<path>"))` then `strings.Contains`/`!strings.Contains` assertions with `t.Fatalf` on failure. No new test infrastructure needed.

For TC-052-01 negative assertion 9 (`AGENT_BUILDER_SANDBOX_RUNTIME=srt` absent from ADR 031): use `strings.Contains(adr031, "AGENT_BUILDER_SANDBOX_RUNTIME=srt")` and fail if true.

For TC-052-02 negative assertions: use `!strings.Contains(text, "AGENT_BUILDER_SANDBOX_RUNTIME=srt")` and `!strings.Contains(text, "--task-root docs/tasks")`.

Highest achievable level: **L5** (doc-honesty assertions run in `go test ./tests/e2e` without any live host). L6 N/A for a doc-only task.
