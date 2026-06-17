# Plan Context

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
