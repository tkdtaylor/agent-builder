# Test Coverage Tracker

**Project:** agent-builder

## Rules

- Test specs are written **before** implementation begins — no exceptions
- A task is **not** "complete" because the feat commit landed and tests passed. See the verification ladder below.
- Each row maps a task ID to its spec file, current test status, and the verification level achieved

## Coverage

| Task ID | Feature | Spec file | Tests written | Status | Verified by |
|---------|---------|-----------|---------------|--------|-------------|
| 001 | Walking skeleton & project setup | 001-walking-skeleton-test-spec.md | ✅ | 🟡 | — (pending spec-verifier) |
| 002 | Gate orchestrator core + Verdict model | 002-gate-orchestrator-core-test-spec.md | ✅ | 🟡 | `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.` Unit-test-only; no runtime surface |
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
| 014 | Podman containment profile | 014-podman-containment-profile-test-spec.md | ✅ | 🟡 | L3: `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.`; `make fitness` -> `Fitness checks passed.`; L6 probe blocked locally: `containment/execution-box/run.sh --worktree . --probe` -> `execution-box: podman unavailable on PATH` |
| 015 | Default-deny egress allowlist | 015-egress-allowlist-test-spec.md | ✅ | 🟡 | L3: `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.`; `make fitness` -> `Fitness checks passed.`; L6 probe blocked locally: `containment/execution-box/run.sh --worktree . --egress-probe` -> `execution-box: podman unavailable on PATH` |
| 016 | Tiered OCI runtime selection seam | 016-tiered-runtime-seam-test-spec.md | stub | ❌ | — |
| 017 | Supervisor dispatch-one-task lifecycle | 017-supervisor-dispatch-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5 fake dispatch harness: `go test -count=1 -v ./internal/supervisor -run TestRunDispatchesOneTaskAndLogsLifecycle` -> `event=box.created` -> `event=loop.started` -> `event=box.torn_down` |
| 018 | Wall-clock timeout / runaway kill | 018-wall-clock-kill-test-spec.md | ✅ | ❌ | — |
| 019 | Run log collection (audit-trail seam) | 019-run-log-collection-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L5: `go test -v ./tests/supervisor -run TestRunRecordStreamsOutputAndPersistsAfterTeardown -count=1` -> `TC-003-Persist-After-Teardown sample persisted line: {"box_id":"box-019"` |
| 020 | exec-sandbox run() adapter seam | 020-exec-sandbox-adapter-seam-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L2/L3: `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.`; unit-test-only; no runtime surface |
| 021 | sandbox-runtime backing adapter | 021-sandbox-runtime-adapter-test-spec.md | stub | ❌ | — |
| 022 | Claude Code CLI executor adapter | 022-claude-cli-executor-test-spec.md | stub | ❌ | — |
| 023 | CLI subcommand surface (run/version/verify) | 023-cli-subcommands-test-spec.md | ✅ | ✅ | spec-verifier APPROVE + L6 runtime-visible CLI checks: `agent-builder version`, `agent-builder verify <clean-repo>`, `agent-builder verify <failing-repo>`, and `agent-builder bogus` |
| 024 | armor on web-ingestion / tool-call path | 024-armor-ingestion-wiring-test-spec.md | stub | ❌ | — |

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
