# Test spec for task 054: live capstone test (TC-032) + fixture helper

**Task:** 054 — Live capstone test (TC-032) + fixture helper

**Acceptance testing strategy:** TDD — write tests before implementation.

> **Note (2026-07-01):** this spec file was reconstructed during a project drift
> audit. Task 054 shipped, was spec-verified, and merged (coverage-tracker rows for
> 054/057/060 all reference it), but the spec file itself was missing on disk — the
> only completed task lacking its paired spec. It is rebuilt verbatim from the task
> file's REQ-054-01…04 / acceptance criteria and the shipped test
> (`tests/e2e/live_phase0_e2e_test.go`, `TestLivePhase0EndToEndAcceptance_TC032`),
> to restore the "every task has a paired test spec" invariant. The tests it
> describes already exist and pass; nothing here changes behavior.

## Scope

Adds an env-gated live end-to-end capstone test that drives the built
`agent-builder` binary with a real Claude executor, real `git`/`gh`, and real Podman
containment against the private `l6` sandbox remote — closing the L6 gap for task 032
(Phase 0 end-to-end acceptance) — plus a `newLiveCapstoneFixture(t)` helper. The test
must skip cleanly in CI when the live flag is unset, and must never modify the
existing deterministic L5 fakes.

## Test cases

### TC-054-01: live capstone drives the real binary end-to-end

**Objective:** Verify `TestLivePhase0EndToEndAcceptance_TC032` in
`tests/e2e/live_phase0_e2e_test.go`, when `AGENT_BUILDER_LIVE_E2E=1` and all
prerequisites are present, drives the real built binary through the full Phase-0 loop
and asserts a real, verified, published outcome.

**Preconditions:**
- `AGENT_BUILDER_LIVE_E2E=1`
- `claude`, `git`, `gh`, `podman` all present on PATH
- `ANTHROPIC_API_KEY` (or `CLAUDE_CODE_OAUTH_TOKEN`) set
- The gate-tools directory and the exec-box launcher resolve on disk
- The `l6` sandbox remote is configured

**Test steps:**
1. Build the binary via `buildAgentBuilder(t)`.
2. Seed the fixture via `newLiveCapstoneFixture(t, root)` — a temp task-root (roadmap
   + one `ready` task instructing Claude to create `LIVE_OK.txt` with one line) and a
   temp real git worktree that is a full clone of the configured remote.
3. Drive the real binary via `runAgentBuilder(t, binary, fixture.env(t), "run")`.

**Expected outcome:**
- Exit code 0.
- Stdout contains `run completed: task 001`.
- The run record contains a `stdout` event whose `data` includes `publication
  recorded: branch=` and a `run_finished` event with `outcome=completed`.
- The real PR URL / branch is logged via `t.Log`.
- `t.Cleanup` closes the PR (`gh pr close --delete-branch`) and deletes the remote
  branch (`git push <remote> --delete <branch>`); cleanup errors are logged, never
  fatal.

**Assertion markers:** `TC-054-01`

---

### TC-054-02: clean skip when the live flag or a tool prerequisite is absent

**Objective:** Verify the test skips (never fails, never invokes the binary) when the
live gate is off or a required tool is missing.

**Test steps / cases:**
1. `AGENT_BUILDER_LIVE_E2E` unset or empty → `t.Skip`.
2. `AGENT_BUILDER_LIVE_E2E=1` but any of `claude`/`git`/`gh`/`podman` absent on PATH →
   `t.Skipf` naming the specific missing tool.
3. `ANTHROPIC_API_KEY` (and OAuth token) unset → `t.Skipf` naming the missing
   credential.

**Expected outcome:**
- Every subcase exits 0 with the test SKIPPED; the binary is never built or invoked.
- `go test ./tests/e2e` without the live flag reports
  `--- SKIP: TestLivePhase0EndToEndAcceptance_TC032`.

**Assertion markers:** `TC-054-02`

---

### TC-054-03: missing gate-tools directory is fatal, not a skip

**Objective:** Verify that a missing gate-tools directory (or an
`EXEC_BOX_GATE_TOOLS`/launcher path pointing at a nonexistent location) with the live
flag set and all tools present is treated as a **configuration error**, not a prereq
gap.

**Test steps:**
1. `AGENT_BUILDER_LIVE_E2E=1`, all tool prerequisites present.
2. The gate-tools directory / exec-box launcher does not exist on disk.

**Expected outcome:**
- `t.Fatalf` (not `t.Skipf`), naming the missing gate-tools / launcher path, with
  language distinguishing a config error from an environment gap.

**Assertion markers:** `TC-054-03`

---

### TC-054-04: existing e2e suite stays green without the live flag

**Objective:** Verify the new file compiles and skips cleanly, and does not disturb
the existing deterministic L5 acceptance tests.

**Test steps:**
1. Run `go test -count=1 -v ./tests/e2e` with no live flag.
2. Run `make check`.

**Expected outcome:**
- `TestPhase0EndToEndAcceptance` and `TestPhase1EndToEndAcceptance` PASS.
- `TestLivePhase0EndToEndAcceptance_TC032` SKIP.
- No compilation error; exit 0.
- `make check` → `All checks passed.`
- The pre-existing L5 fakes (`phase0_end_to_end_acceptance_test.go`,
  `phase1_end_to_end_acceptance_test.go`) are unmodified.

**Assertion markers:** `TC-054-04`

## Out of scope

- Modifying the existing L5 fake acceptance tests.
- Changes to `internal/` or `cmd/` production code.
- `docs/spec/` changes (no externally-visible behavior change).
- The live publisher-only test (task 053) and `scripts/l6-probe.sh` rewiring (task
  055).
