# Fix the L6 live probes so they exercise real Claude/PR paths, then run them

## Context

The deferred L6 suite was supposed to drive a **real Claude build** and **real PR
publication** for tasks 022/028/032/034. Running probe 034 surfaced that this premise
is false for three of the four:

- **034** (`go test ./tests/publisher -run TestBranchPRPublication`) hardcodes `git`/`gh`
  shims and asserts a **faked** URL `acme/repo/pull/34` ([publisher_test.go:23-50](../../agent-builder/tests/publisher/publisher_test.go#L23-L50)).
  It ignores `AGENT_BUILDER_PUBLISH_REMOTE`/`GH_CLI`/token entirely. Ran in 0.005s; **0 PRs/branches** exist on `l6`.
- **032** (`go test ./tests/e2e -run TestPhase0EndToEndAcceptance`) runs through a fixture
  that prepends a shim dir to PATH (so `claude` resolves to a fake), asserts a faked URL,
  and TC-005 *requires* the docs say `"fake-provider L5"` ([phase0_end_to_end_acceptance_test.go:18-52,156](../../agent-builder/tests/e2e/phase0_end_to_end_acceptance_test.go#L18-L52)).
  No env-gated live path.
- **028** (`go run ./cmd/agent-builder run --task-root docs/tasks/`) is the real binary but
  (a) passes an **invalid `--task-root` flag** that `run` rejects ([cli.go:104-106](../../agent-builder/internal/cli/cli.go#L104-L106)),
  and (b) supplies none of the required env (`ANTHROPIC_API_KEY`, `WORKTREE`, `RUN_TIMEOUT`,
  `MAX_ATTEMPTS`, `PUBLISH_REMOTE`), so it hard-fails `ConfigFromEnv` ([run.go:80-148](../../agent-builder/internal/runtime/run.go#L80-L148)).

Plus doc drift: the checklist + runbook still show the stale `AGENT_BUILDER_SANDBOX_RUNTIME=srt`
(removed by ADR 021) for 028/032.

**Outcome wanted:** add real, env-gated LIVE probes that actually invoke real Claude + real
`gh` against the private `l6` sandbox, wire them into `l6-probe.sh` + the docs, then run them
live and promote 022/028/032/034 🟡→✅ with genuine L6 evidence.

**Key architectural fact (settled):** `claude`, the gate, and the publisher all run
**host-side** ([claude_cli.go:145](../../agent-builder/internal/executor/claude_cli.go#L145), [run.go:179-207,372-390](../../agent-builder/internal/runtime/run.go#L179-L207)).
The Podman box runs only `/bin/true` as a containment liveness probe ([run.go:331-350](../../agent-builder/internal/runtime/run.go#L331-L350)) —
so the alpine box lacking Claude is irrelevant; the live capstone is feasible.

## Approach (decided)

Mirror the codebase's own live-test idiom — `AGENT_BUILDER_LIVE_PODMAN` /
`AGENT_BUILDER_LIVE_SRT` ([sandbox_runtime_adapter_test.go:210-218](../../agent-builder/tests/sandbox/sandbox_runtime_adapter_test.go#L210), [phase1_end_to_end_acceptance_test.go:109-172](../../agent-builder/tests/e2e/phase1_end_to_end_acceptance_test.go#L109)):
skip when the env flag ≠ `1`, `t.Skipf` when a prereq binary is absent, `t.Fatalf` only on a
genuine config error. **The existing fake L5 tests stay unchanged** — they remain the
deterministic acceptance gate; L6 evidence comes from NEW live tests. Real Claude work targets
a **controlled throwaway fixture task** (not the real backlog), and live PRs target the private
`l6` remote with **self-cleanup** (close PR + delete remote branch).

## Work breakdown (one responsibility each; next free IDs — 052+ at time of writing)

Each task gets a paired test spec written first, lands on its own `task/NNN-*` branch via
`scripts/start-task.sh`, is implemented by **task-executor**, verified by **spec-verifier**,
then merged. The plan-mode hook will scaffold these into `docs/tasks/backlog/`.

### Task 052 — ADR 031 + doc honesty (no code)
- Write `docs/architecture/decisions/031-l6-live-mode-probes.md` recording: fakes stay as L5;
  L6 evidence comes from env-gated `AGENT_BUILDER_LIVE_PUBLISH`/`AGENT_BUILDER_LIVE_E2E` tests;
  claude/gate/publisher run host-side, box = liveness probe; live PRs hit `l6` and self-clean.
  Supersede the stale `srt` guidance (references ADR 021 + ADR 026).
- Update `docs/plans/phase0-l6-verification-checklist.md` and `docs/plans/l6-operator-runbook.md`
  (Section 3 table): purge `AGENT_BUILDER_SANDBOX_RUNTIME=srt` from 028/032, remove the invalid
  `--task-root docs/tasks/...` argv, replace 032/034 commands with the new live-test commands,
  give 022/028 the full env contract, and document claude-host-side so operators don't expect
  claude-in-box.
- Highest level: L5 (doc-honesty assertions). Dependency: none. **Do first.**

### Task 053 — live publisher test (034)
- New `tests/publisher/live_publish_test.go`: `TestLiveBranchPRPublication_TC034`, gated on
  `AGENT_BUILDER_LIVE_PUBLISH=1`; `t.Skipf` if `git`/`gh` absent or the remote
  (`AGENT_BUILDER_LIVE_PUBLISH_REMOTE`, default `l6`) isn't configured / `gh` unauthenticated.
  Body: temp `git init`, add the same remote URL, commit one file on a unique branch
  `task/034-live-<ts>-<pid>`, call `publisher.NewGitHubCLI(...).Publish(...)` ([publisher.go:67,87](../../agent-builder/internal/publisher/publisher.go#L67)),
  assert `Result.PRURL` matches `github.com/.+/pull/\d+`. `t.Cleanup`: `gh pr close <branch>
  --delete-branch` + `git push <remote> --delete <branch>`. `t.Log` the real URL.
- Highest level achievable in-repo: L5 (skip-shape green); L6 residual = operator runs it live.
  Dependency: 052. Independent of 054.

### Task 054 — live capstone test (032) + fixture helper
- New `tests/e2e/live_phase0_e2e_test.go`: `TestLivePhase0EndToEndAcceptance_TC032`, gated on
  `AGENT_BUILDER_LIVE_E2E=1`; `t.Skipf` if `claude`/`git`/`gh`/`podman` absent or
  `ANTHROPIC_API_KEY` unset; `t.Fatalf` if the gate-tools dir is missing (config error).
- New helper `newLiveCapstoneFixture(t)`: seeds a temp task-root (`docs/plans/roadmap.md` + one
  `**Status:** ready` task instructing Claude to create `LIVE_OK.txt` with one line) and a temp
  **real git** worktree containing a minimal gate-passing Go module (copy the shape from
  [branch_pr_publication_test.go:133-154](../../agent-builder/tests/e2e/branch_pr_publication_test.go#L133)).
  Drive the built binary via `runAgentBuilder` with the full env contract +
  `AGENT_BUILDER_RUN_RECORD`. Assert exit 0, `run completed: task NNN`, run-record
  `publication recorded: branch=` + `run_finished outcome=completed`; `t.Log` the real PR URL.
  `t.Cleanup`: read branch from the record, close PR + delete remote branch.
- Reuses `tasksource` readiness rules ([source.go:120-202](../../agent-builder/internal/tasksource/source.go#L120)).
  Highest level in-repo: L5; L6 residual = operator runs it live. Dependency: 052. Independent of 053.

### Task 055 — rewire `scripts/l6-probe.sh` (022/028/032/034) + fixture-seed helper
- Add a `seed_live_fixture()` helper (bash) that emits a temp task-root + gate-passing git
  worktree for the `go run` probes (022/028).
- 022/028: full env contract (`ANTHROPIC_API_KEY`, `TASK_ROOT`=fixture, `WORKTREE`=fixture,
  `PUBLISH_REMOTE`, `RUN_TIMEOUT=300s`, `MAX_ATTEMPTS=1`, `RUN_RECORD`=tmp), **drop the invalid
  `--task-root` arg**; gate adds an `ANTHROPIC_API_KEY` presence skip.
- 034 → `env AGENT_BUILDER_LIVE_PUBLISH=1 AGENT_BUILDER_LIVE_PUBLISH_REMOTE=$remote go test ...
  -run TestLiveBranchPRPublication_TC034`.
- 032 → `env AGENT_BUILDER_LIVE_E2E=1 AGENT_BUILDER_LIVE_E2E_REMOTE=$remote go test ...
  -run TestLivePhase0EndToEndAcceptance_TC032` (keep `AGENT_BUILDER_PUBLISH_REMOTE` in the env
  prefix so TC-046-02 keeps matching; add no `srt`).
- Keep `scripts/tests/l6-probe-test.sh` green: closing order `014 015 016 021 030 022 028 033 034
  032`, 10 evidence rows, no-`srt` (TC-046-03), and PUBLISH_REMOTE threading (TC-046-02) all
  preserved; adjust only the TC-046-02 expectation if it greps a literal that moved.
- Highest level in-repo: L5 (`bash scripts/tests/l6-probe-test.sh` green). Dependency: 053 + 054 (+052).
  **Do last.**

Merge order: **052 → (053 ∥ 054) → 055**.

## Live run + promotion (after 052–055 merged)

Source the key from the repo `.env` (gitignored — never commit it). On the provisioned host
(`srt` PATH export, rootless Podman + runsc + gate-tools, `claude`/`gh` authed, `l6` remote):

1. **034** — `env $(grep -v '^#' .env | xargs) AGENT_BUILDER_LIVE_PUBLISH=1
   AGENT_BUILDER_LIVE_PUBLISH_REMOTE=l6 go test -count=1 -v ./tests/publisher -run
   TestLiveBranchPRPublication_TC034`. Proof: PASS + real `…/pull/N` URL; `gh pr list` shows the
   branch cleaned up.
2. **028 / 022** — via `make l6-probe` (or the seeded-fixture `go run` argv). Proof: stdout
   `run completed: task NNN`, run-record `run_finished outcome=completed`; 028 records a real PR.
3. **032** — `env $(grep -v '^#' .env | xargs) AGENT_BUILDER_LIVE_E2E=1
   AGENT_BUILDER_LIVE_E2E_REMOTE=l6 go test -count=1 -v ./tests/e2e -run
   TestLivePhase0EndToEndAcceptance_TC032`. Proof: PASS, run-record `run_finished
   outcome=completed`, real PR on `l6`, cleanup confirmed. Last (most expensive).

For each green probe: promote its row 🟡→✅ in `docs/tasks/test-specs/coverage-tracker.md` in a
**separate** `verify: confirm task NNN — <real-host command + final assertion + exit 0>` commit
(one per task, not batched). Close 022's "real Claude" note at the same time. Clean up any PR
that cleanup didn't.

## Verification (no live host needed, for 052–055 themselves)

- `bash scripts/l6-probe.sh --dry-run` → 10 rows in closing order, exit 0.
- `bash scripts/tests/l6-probe-test.sh` → `=== Results: N passed, 0 failed ===`.
- `go build ./...`, `make check`, `make fitness` green.
- New live tests **skip cleanly** when their `AGENT_BUILDER_LIVE_*` flag is unset
  (`go test ./tests/publisher ./tests/e2e` stays green in CI).

## Out of scope / notes

- Do not delete or alter the fake L5 tests — they stay as the deterministic gate.
- `.env` holds the real key: keep it gitignored; the protect-secrets hook guards commits.
- Each task is its own branch + spec + spec-verifier pass per CLAUDE.md; tracker promotions are
  standalone `verify:` commits (`[allow-main]` if done on main, after confirming the branch).
