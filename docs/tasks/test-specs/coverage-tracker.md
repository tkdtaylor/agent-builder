# Test Coverage Tracker

**Project:** agent-builder

## Rules

- Test specs are written **before** implementation begins — no exceptions
- A task is **not** "complete" because the feat commit landed and tests passed. See the verification ladder below.
- Each row maps a task ID to its spec file, current test status, and the verification level achieved

## Coverage

| Task ID | Feature | Spec file | Tests written | Status | Verified by |
|---------|---------|-----------|---------------|--------|-------------|
| 001 | Walking skeleton & project setup | 001-walking-skeleton-test-spec.md | ✅ | ✅ | spec-verifier APPROVE (audit 2026-06-05, post spec-correction) + L6 operator-observed: `go run ./cmd/agent-builder version` -> `agent-builder 0.0.0-scaffold` exit 0; bare `go run ./cmd/agent-builder` -> CLI usage block, exit 2; `go test -count=1 ./internal/supervisor -run 'TestVersionSet\|TestRunDispatchesOneTaskAndLogsLifecycle\|TestRunRejectsMissingDispatchDependencies'` -> `ok`. |
| 002 | Gate orchestrator core + Verdict model | 002-gate-orchestrator-core-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L2 unit-test-only/no runtime surface (re-confirmed audit 2026-06-05 in a standalone verify commit; original ✅ was bundled into the task-031 feat commit): `go test -count=1 ./internal/gate -run 'TestVerify\|TestNewRejects\|TestGateHasNoSkipSurface'` -> `ok github.com/tkdtaylor/agent-builder/internal/gate 0.002s` |
| 003 | Native Go gate steps (build/vet/test/gofmt) | 003-gate-go-checks-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5: `go test ./internal/gate/... -run TestGoChecks` -> `ok github.com/tkdtaylor/agent-builder/internal/gate` |
| 004 | golangci-lint gate step | 004-gate-golangci-lint-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5: `go test ./internal/gate/... -run TestGolangciLint -count=1` -> `ok github.com/tkdtaylor/agent-builder/internal/gate` |
| 005 | dep-scan blocking gate step | 005-gate-dep-scan-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5: `go test ./internal/gate/... -run TestDepScan -count=1` -> `ok github.com/tkdtaylor/agent-builder/internal/gate` |
| 006 | code-scanner blocking gate step | 006-gate-code-scanner-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5: `go test ./internal/gate/... -run TestCodeScanner -count=1` -> `ok github.com/tkdtaylor/agent-builder/internal/gate` |
| 007 | Fitness F-003: supervisor isolation | 007-fitness-supervisor-isolation-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L6 operator-observed: `make fitness-supervisor-isolation` -> `PASS fitness-supervisor-isolation: supervisor import graph contains no executor/LLM/web-fetch packages.` Negative import-chain failure named `github.com/tkdtaylor/agent-builder/internal/webfetch`; `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.` |
| 008 | Fitness F-001: no-docker dev-env refs | 008-fitness-no-docker-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L6 operator-observed: `make fitness-no-docker` -> `PASS fitness-no-docker: no forbidden dev-environment references found.` Negative fixtures caught root `Dockerfile`, `dockerfile`, `DOCKERFILE`, `docker-compose.yml`, `Docker-compose.yml`, and `docker run` content; `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.` |
| 009 | Fitness F-002: gate is blocking (no skip) | 009-fitness-gate-blocking-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L6 operator-observed: `make fitness-gate-blocking` -> `PASS fitness-gate-blocking: no verification gate bypass affordances found.` Negative fixtures caught CLI flag, scanner skip env-var, and early-return bypass; `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.` |
| 010 | Roadmap task-source reader (read-only) | 010-roadmap-task-source-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5: `go test -count=1 ./internal/tasksource/...` -> `ok github.com/tkdtaylor/agent-builder/internal/tasksource`; `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.` |
| 011 | Task status writer (status-only) | 011-task-status-writer-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5: `go test -count=1 ./internal/tasksource/... ./tests/...` -> `ok github.com/tkdtaylor/agent-builder/tests/tasksource` |
| 012 | Agent loop state machine | 012-agent-loop-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5: `go test -count=1 ./tests/loop/...` -> `ok github.com/tkdtaylor/agent-builder/tests/loop` |
| 013 | Escalation + retry-N + stop condition | 013-escalation-retry-policy-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5: `go test ./tests/loop/... -run 'TestRetryPolicy|TestEscalation' -count=1` -> `ok github.com/tkdtaylor/agent-builder/tests/loop` |
| 014 | Podman containment profile | 014-podman-containment-profile-test-spec.md | ✅ | 🟡 | L3: `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.`; `make fitness` -> `Fitness checks passed.`; Task 030 L6 probe blocked locally with Task 033 toolchain fixture: `containment/execution-box/run.sh --gate-tools /tmp/agent-builder-t033-tools.qIdv9b --worktree . --probe` -> `execution-box: podman unavailable on PATH` |
| 015 | Default-deny egress allowlist | 015-egress-allowlist-test-spec.md | ✅ | 🟡 | L3: `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.`; `make fitness` -> `Fitness checks passed.`; Task 030 L6 probe blocked locally with Task 033 toolchain fixture: `containment/execution-box/run.sh --gate-tools /tmp/agent-builder-t033-tools.qIdv9b --worktree . --egress-probe` -> `execution-box: podman unavailable on PATH` |
| 016 | Tiered OCI runtime selection seam | 016-tiered-runtime-seam-test-spec.md | ✅ | 🟡 | L3: `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.`; Task 030 runtime plan: `containment/execution-box/run.sh --gate-tools /tmp/agent-builder-t033-tools.qIdv9b --worktree . --print-runtime-plan` -> `TC-016 PLAN: workload=agent runtime=runsc source=default`; L6 explicit runtime probe blocked locally: `containment/execution-box/run.sh --gate-tools /tmp/agent-builder-t033-tools.qIdv9b --worktree . --runtime runsc --probe` -> `execution-box: podman unavailable on PATH` |
| 017 | Supervisor dispatch-one-task lifecycle | 017-supervisor-dispatch-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5 fake dispatch harness: `go test -count=1 -v ./internal/supervisor -run TestRunDispatchesOneTaskAndLogsLifecycle` -> `event=box.created` -> `event=loop.started` -> `event=box.torn_down` |
| 018 | Wall-clock timeout / runaway kill | 018-wall-clock-kill-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5: `go test -count=1 -v ./internal/supervisor -run 'TestRunTimeout(UsesConfiguredDeadlineAndKillsBox\|RecordsTimedOutOutcome)'` -> `event=box.kill.timeout` + `"outcome":"timed-out"` |
| 019 | Run log collection (audit-trail seam) | 019-run-log-collection-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5: `go test -v ./tests/supervisor -run TestRunRecordStreamsOutputAndPersistsAfterTeardown -count=1` -> `TC-003-Persist-After-Teardown sample persisted line: {"box_id":"box-019"` |
| 020 | exec-sandbox run() adapter seam | 020-exec-sandbox-adapter-seam-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L2/L3: `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.`; unit-test-only; no runtime surface |
| 021 | sandbox-runtime backing adapter | 021-sandbox-runtime-adapter-test-spec.md | ✅ | 🟡 | L3: `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.`; Task 030 L6 blocked locally: `command -v srt` -> exit 1/no output; `command -v bwrap` -> `/usr/bin/bwrap`; live harness blocked before `srt`: `env AGENT_BUILDER_LIVE_SRT=1 go test -count=1 -v ./tests/sandbox -run TestSandboxRuntimeLiveHarness_TC002_TC003` -> `snap-confine has elevated permissions and is not confined but should be. Refusing to continue to avoid permission escalation attacks` |
| 022 | Claude Code CLI executor adapter | 022-claude-cli-executor-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5 stubbed CLI harness: `go test -count=1 -v ./tests/executor -run TestClaudeCLIRunInvokesSubprocessAgainstWorktreeAndCapturesBranch` -> `Result.Branch = task/022-claude-cli-executor`, `Result.OK == true`; L6 real Claude CLI/auth pending |
| 023 | CLI subcommand surface (run/version/verify) | 023-cli-subcommands-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L6 runtime-visible CLI checks: `agent-builder version`, `agent-builder verify <clean-repo>`, `agent-builder verify <failing-repo>`, and `agent-builder bogus` |
| 024 | Web-ingestion/tool-call boundary seam | 024-ingestion-tool-call-boundary-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5: `go test -count=1 ./internal/ingestion/... ./tests/ingestion/...` -> `ok github.com/tkdtaylor/agent-builder/tests/ingestion`; `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.` |
| 025 | armor guard adapter | 025-armor-guard-adapter-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5: `go test -count=1 ./internal/armor/... ./tests/armor/...` -> `ok github.com/tkdtaylor/agent-builder/tests/armor`; `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.` |
| 026 | armor on web-ingestion / tool-call path | 026-armor-ingestion-wiring-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5 (re-confirmed audit 2026-06-05 in a standalone verify commit; original ✅ was bundled into the task-031 feat commit): `go test -count=1 ./tests/executorharness -run 'TestHarnessProducerConsumerTraceCoversLivePath\|TestArmorGuardedHarnessProducerConsumerTraceCoversLiveExecutorPath'` -> `ok` (TC-005 armor producer-consumer trace) |
| 027 | Executor ingestion/tool-call harness | 027-executor-ingestion-tool-harness-test-spec.md | ✅ | ✅ | Task 031 spec-verifier APPROVE + L5: `go test -count=1 -v ./tests/executorharness -run 'TestHarnessProducerConsumerTraceCoversLivePath\|TestArmorGuardedHarnessProducerConsumerTraceCoversLiveExecutorPath'` -> `TC-005 producer-consumer trace:` |
| 028 | Default run wiring | 028-default-run-wiring-test-spec.md | ✅ | 🟡 | L5: `go test -count=1 -v ./tests/cli ./tests/supervisor -run 'TestRuntimeRunWiresPhase0Pipeline|TestRunConfigFailures'` -> `TC-005 runtime run completed one configured task and persisted run_finished`; final gate `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.` |
| 029 | Claude executor ingestion control | 029-claude-ingestion-control-test-spec.md | ✅ | 🟡 | L5: `go test -count=1 -v ./tests/executor ./tests/executorharness -run 'TestClaude.*Ingestion|TestArmorGuardedHarnessProducerConsumerTraceCoversLiveExecutorPath'` -> `TC-005 Claude executor web/tool route is reviewed or disabled fail-closed`; pending spec-verifier before ✅ |
| 030 | Runtime isolation evidence | 030-runtime-isolation-evidence-test-spec.md | ✅ | 🟡 | Evidence ledger updated only with observed blockers: execution-box probes for tasks 014/015/016 all exited `execution-box: podman unavailable on PATH`; `command -v srt` exited 1/no output; `command -v bwrap` -> `/usr/bin/bwrap`. L6 remains pending on a host with rootless Podman, runsc, and `srt`. |
| 031 | Verification ledger cleanup | 031-verification-ledger-cleanup-test-spec.md | ✅ | 🟡 | L5 docs ledger consistency: `scripts/check-task-state.sh` -> `OK: every task is tracked in exactly one state directory.`; focused ledger check -> `OK: ledger consistency check passed for 34 tracker rows.`; final gate `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.` |
| 032 | Phase 0 end-to-end acceptance | 032-phase0-end-to-end-acceptance-test-spec.md | ✅ | 🟡 | fake-provider L5: `go test -count=1 -v ./tests/e2e -run TestPhase0EndToEndAcceptance` -> `TC-001 Phase 0 accepted: task selected, branch produced, PR recorded, gate passed, run record persisted`; L6 pending because local evidence still lacks rootless Podman, `runsc`, real `srt`, real Claude, and a configured git remote for real PR publication |
| 033 | Execution-box Gate toolchain | 033-execution-box-gate-toolchain-test-spec.md | ✅ | 🟡 | L5: `go test -count=1 -v ./tests/containment ./tests/cli -run 'TestExecutionBoxGateToolchain\|TestVerifyMissingGateTool'` -> `ok github.com/tkdtaylor/agent-builder/tests/cli`; runtime-visible dry-run `containment/execution-box/run.sh --gate-tools <fixture> --print-toolchain-plan` printed mounted Gate tool path/version lines; L6 pending because local Podman is unavailable |
| 034 | Branch and PR publication | 034-branch-pr-publication-test-spec.md | ✅ | 🟡 | L5 fake git/gh publication harness: `go test -count=1 -v ./tests/publisher ./tests/e2e -run 'TestBranchPRPublication\|TestPublisherFailureDoesNotMarkDone'` -> `TC-001 verified branch published as PR artifact`; publisher failure/redaction evidence -> `TC-003 publisher failure preserved task as not done`; L6 real PR pending because no git remote is configured |

## Status key

| Symbol | Meaning |
|--------|---------|
| ✅ | **Verified** — validation harness exercised the live runtime path, or operator observed the targeted behaviour |
| 🟡 | **Code merged** — feat-commit landed, unit tests + fitness + CI green, but runtime/live behaviour not yet observed |
| ⏳ | In progress |
| ❌ | Not started |
| ⚠️ | Blocked |

## Verification ladder

A task earns 🟡 at levels 1–4 and ✅ only at level 5 or 6. The `Verified by` column records which level the row reached.

| Level | Evidence | Status this earns |
|-------|----------|-------------------|
| 1 | Code merged | 🟡 |
| 2 | Unit tests pass (paste verbatim final line of `make check`) | 🟡 |
| 3 | `make fitness` passes (verbatim closing line) | 🟡 |
| 4 | CI passes (`gh run watch <id> --exit-status` → success) | 🟡 |
| 5 | **Validation harness** exercises the live runtime path end-to-end — paste the command and the final assertion line | ✅ |
| 6 | **Operator-observed** — operator (or executor via `cargo run` / `npm start` / etc.) saw the targeted behaviour in stdout / logs / UI | ✅ |

If the task targets runtime-observable behaviour (logging, CLI args, TUI, server endpoints, file outputs, side effects), level 5 or 6 is **required** before flipping to ✅. If the task only adds an internal helper covered by unit tests, level 2 may be sufficient — but in that case the row's `Verified by` should explicitly say "unit-test-only; no runtime surface" so future readers don't mistake silence for verification.

## Rule

**The task-executor commits at 🟡 by default.** Only the main session (after spec-verifier APPROVE + the appropriate level-5/6 evidence) updates the row to ✅, in a separate commit titled `verify: confirm task NNN — <level-5/6 evidence>`. This keeps the verification step visible in git history and prevents "merged ≠ done" drift.
