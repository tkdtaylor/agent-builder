# Test spec for task 057: live-fixtures-clone-remote

**Task:** 057 — Live fixtures clone remote

**Acceptance testing strategy:** TDD — write tests before implementation.

## Test cases

### TC-057-01: seed_live_fixture clones the configured l6 remote

**Objective:** Verify that `seed_live_fixture()` in `scripts/l6-probe.sh` now clones the configured `l6` remote URL into the WORKTREE directory.

**Preconditions:**
- `scripts/l6-probe.sh` is readable and executable
- `scripts/l6-preflight.sh` would report the host ready (or test runs with `--dry-run`)
- Bash is available on PATH

**Test steps:**
1. Call `seed_live_fixture()` from within a Bash script context
2. Capture the output (task-root path, worktree path)
3. Verify the worktree is a real git repository with `.git/` directory
4. Verify the worktree HEAD is on the default branch (typically `main`)
5. Verify the worktree has an `l6` remote (via `git -C <worktree> remote -v`)
6. Verify the local `main` branch descends from `l6/main` (via `git merge-base --is-ancestor l6/main main` exit 0)

**Expected outcome:**
- Both the task-root and worktree are created as temporary directories
- The worktree is a full clone of the l6 remote (no shallow/truncated history)
- `git -C <worktree> remote -v` shows `l6 <resolved-url>` as fetch/push remote
- `git -C <worktree> rev-parse HEAD` outputs a real commit SHA
- The worktree's local `main` branch has shared history with `l6/main`

**Assertion markers:** `TC-057-01`

---

### TC-057-02: newLiveCapstoneFixture clones the configured l6 remote

**Objective:** Verify that `newLiveCapstoneFixture(t)` in `tests/e2e/live_phase0_e2e_test.go` now clones the configured `AGENT_BUILDER_LIVE_E2E_REMOTE` (defaults to `l6`) into the worktree directory.

**Preconditions:**
- `tests/e2e/live_phase0_e2e_test.go` is readable
- The l6 remote URL is resolvable via `git -C <some-repo> remote get-url l6` (typically available in agent-builder repo root)

**Test steps:**
1. Call `newLiveCapstoneFixture(t, repoRoot)` in a Go test context (or equivalently, inspect the code to trace the clone logic)
2. Verify the worktree is a real git repository with `.git/` directory
3. Verify the worktree is on the default branch (typically `main`)
4. Verify the worktree has the `AGENT_BUILDER_LIVE_E2E_REMOTE` as a remote (default: `l6`)
5. Verify the local `main` branch has shared history with `<remote>/main`

**Expected outcome:**
- The worktree is a full clone of the configured remote (not a bare `git init`)
- `git -C <worktree> remote -v` shows the remote URL as fetch/push
- The worktree's `main` branch descends from `<remote>/main`

**Assertion markers:** `TC-057-02`

---

### TC-057-03: runAgentBuilder does not propagate AGENT_BUILDER_LIVE_E2E into the binary

**Objective:** Verify that the capstone live test (`TestLivePhase0EndToEndAcceptance_TC032`) does not pass `AGENT_BUILDER_LIVE_E2E=1` into the binary's environment when driving it via `runAgentBuilder()`.

**Preconditions:**
- `tests/e2e/live_phase0_e2e_test.go` is readable
- `runAgentBuilder()` function is present in the e2e test package

**Test steps:**
1. Inspect the `runAgentBuilder()` function signature and the `filteredEnv()` or env-map logic
2. Verify that the env map passed to `cmd.Env = <map>` does NOT contain `AGENT_BUILDER_LIVE_E2E`
3. Verify that no code path in the live test (`TestLivePhase0EndToEndAcceptance_TC032`) copies the ambient `AGENT_BUILDER_LIVE_E2E` into the fixture's env map

**Expected outcome:**
- The binary's environment inside `runAgentBuilder()` does NOT have `AGENT_BUILDER_LIVE_E2E` set
- The gate (`go test ./...`) can safely set `AGENT_BUILDER_LIVE_E2E=1` without causing recursion when the binary invokes `go test` internally
- The flag only gates the outer test; the binary never receives it

**Assertion markers:** `TC-057-03`

---

### TC-057-04: Fixture clone does not override git user.email

**Objective:** Verify that the clone and initial commit in both `seed_live_fixture()` and `newLiveCapstoneFixture()` do NOT override the ambient git `user.email` to a hardcoded gmail address.

**Preconditions:**
- Both fixture functions are present in their respective files
- A git config with `user.email` is available on the host (default from `~/.gitconfig` or ambient env)

**Test steps:**
1. Inspect `seed_live_fixture()` and `newLiveCapstoneFixture()` for any `git config --local user.email <value>` or `git -c user.email=<value>` invocations
2. Verify neither function sets `user.email` to `a personal address` or any hardcoded value
3. Verify the initial commit uses the ambient git identity from `~/.gitconfig` or `GIT_AUTHOR_EMAIL`

**Expected outcome:**
- No hardcoded `user.email` config in either fixture
- Initial commits resolve the author email from the ambient git config
- The ambient identity (`...@users.noreply.github.com` or similar) is preserved

**Assertion markers:** `TC-057-04`

---

### TC-057-05: seed_live_fixture harness test still passes (TC-055-04 regression)

**Objective:** Verify that the `bash scripts/tests/l6-probe-test.sh` test suite (which includes TC-055-04 for `seed_live_fixture`) still passes after the change from bare `git init` to `l6` clone.

**Preconditions:**
- `scripts/tests/l6-probe-test.sh` is executable
- The test can run without requiring a real l6 remote (e.g., stubs or dry-run mode)

**Test steps:**
1. Run `bash scripts/tests/l6-probe-test.sh`
2. Capture the exit code and the final results line

**Expected outcome:**
- `=== Results: N passed, 0 failed ===` exit 0
- All prior TC-044 and TC-046 test cases remain PASS
- TC-055-04 test (which validates `seed_live_fixture`) now asserts that the fixture produces an l6 clone (with `.git/`, `main` branch, `l6` remote), not a minimal `go.mod`

**Assertion markers:** `TC-057-05`

---

### TC-057-06: go test e2e skips capstone cleanly (flag unset)

**Objective:** Verify that `TestLivePhase0EndToEndAcceptance_TC032` SKIPs cleanly when `AGENT_BUILDER_LIVE_E2E` is unset in the test's environment.

**Preconditions:**
- `go test` is available
- `tests/e2e/live_phase0_e2e_test.go` is present

**Test steps:**
1. Run `go test -count=1 -v ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032` (no `AGENT_BUILDER_LIVE_E2E` in the environment)
2. Capture the exit code and output

**Expected outcome:**
- Test status is `--- SKIP`
- Exit code is 0
- No unrelated e2e tests fail (TestPhase0EndToEndAcceptance, TestPhase1EndToEndAcceptance all PASS)

**Assertion markers:** `TC-057-06`

---

### TC-057-07: Non-Claude clone validation test gates properly

**Objective:** Verify that the optional non-Claude clone validation test (added to `scripts/tests/l6-probe-test.sh` or a separate test file) SKIPs cleanly when l6/gh is not configured, and does NOT fail CI.

**Preconditions:**
- A test exists that invokes the fixture's clone logic against the real l6 remote (if configured) or a mock remote
- The test is gated on environment checks (e.g., `which gh` or configured remote check)

**Test steps:**
1. Run the test when l6/gh is NOT configured (default CI environment)
2. Verify it SKIPs with a clear reason
3. Run the test (or simulate) when l6/gh IS configured
4. Verify the worktree is a real git clone with `.git/`, on `main`, with the remote ancestor relationship

**Expected outcome:**
- Test SKIPs cleanly in CI (exit 0, no failure)
- When l6 is configured, test PASS asserting the clone mechanics
- The test proves the clone works without spending a live Claude call

**Assertion markers:** `TC-057-07`

---

## Spec markers index

| Marker | File | Line | Description |
|--------|------|------|-------------|
| `TC-057-01` | `scripts/tests/l6-probe-test.sh` or inline assertion | N/A | seed_live_fixture clones l6 |
| `TC-057-02` | `tests/e2e/live_phase0_e2e_test.go` or inline assertion | N/A | newLiveCapstoneFixture clones l6 |
| `TC-057-03` | `tests/e2e/live_phase0_e2e_test.go` | Line TBD | AGENT_BUILDER_LIVE_E2E not propagated |
| `TC-057-04` | `scripts/l6-probe.sh`, `tests/e2e/live_phase0_e2e_test.go` | Lines TBD | No hardcoded user.email |
| `TC-057-05` | `scripts/tests/l6-probe-test.sh` | N/A | TC-055-04 regression passes |
| `TC-057-06` | `tests/e2e/live_phase0_e2e_test.go` | N/A | Capstone test skips cleanly |
| `TC-057-07` | `scripts/tests/l6-probe-test.sh` or new test | N/A | Non-Claude clone validation |

