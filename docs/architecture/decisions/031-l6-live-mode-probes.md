# ADR 031: L6 live-mode probe architecture — env-gated tests, host-side execution, `/bin/true` in the box, self-cleaning PRs

**Date:** 2026-06-17
**Status:** Accepted
**Task:** 052 — documents the L6 live-mode probe architecture so operators have an accurate reference for running the probes after tasks 053–055 land
**Related:** ADR 021 (srt removal from run path), ADR 026 (Podman containment — the substrate for these probes), ADR 016 (tiered runtime seam — the runtime tiers the probes exercise)

## Context

Phase 0 and Phase 1 are both accepted at **fake-provider L5** (stubbed executors, stubbed sandboxes, stubbed publication). The missing evidence is L6 — running the probes on a real host with real credentials and real dependencies, observing the orchestrator work end-to-end outside the laboratory. The prior tasks (014–029, 033–050) built the orchestrator code and the containerized gates; **tasks 053–055 write the three L6 test files** that exercise the live paths. This ADR records the architectural choice for how those tests are gated, how the executors and sandbox runtime run, where the prove lives, and how the PRs they produce clean themselves.

The core invariant: **fake-provider L5 tests are deterministic and gate every merge; new L6 tests are opt-in (env-gated) and run only when an operator explicitly intends to validate against the real host.** The operator's host has real credentials (Claude API key, GitHub token) and real endpoints (the GitHub API for PR publication); these are never baked into the CI harness, and the tests that use them are not part of the standard `make check` gate.

## Architecture sketch

### The fake L5 path (unchanged, gate-keeping)

The existing `TestPhase0EndToEndAcceptance` and `TestPhase1EndToEndAcceptance` tests in `tests/e2e/` run the orchestrator against **stubbed out** `claude` CLI, `git` / `gh` publishing, and sandbox runtimes (`srt` stubs). They use a fixture with in-memory git repos and local socket binaries — zero external dependencies, repeatable on CI without credentials. **These tests are not env-gated and must pass on every commit.** They are the load-bearing gate that proves the orchestrator's state machine works at the laboratory L5 level.

### The live L6 path (new, env-opt-in)

Three new test files (tasks 053–055) run the same orchestrator code against **real** dependencies:
- **Task 053:** `tests/publisher/live_publisher_test.go` — real `gh` CLI, real GitHub API, publishes PRs against a live remote.
- **Task 054:** `tests/e2e/live_e2e_test.go` — real `claude` CLI (authenticated with `ANTHROPIC_API_KEY`), real sandbox runtime (`srt` CLI), real Podman box, real `gh` publication. The capstone L6 test: the orchestrator works end-to-end on a real host.
- **Task 055:** updates `scripts/l6-probe.sh` — the harness that operators call to run all 10 closing-order L6 probes (014–034, including the new 053–054 tests) on their provisioned host.

**Env-gating:** Each live test reads an env flag that is **never** set by CI or by the default `make check` gate. The flags are:

| Env flag | Meaning | Consumed by | Default (unset) |
|----------|---------|-------------|-----------------|
| `AGENT_BUILDER_LIVE_PUBLISH` | Run the real live-publish L6 probe (task 053) | `tests/publisher/live_publisher_test.go` | test skips |
| `AGENT_BUILDER_LIVE_E2E` | Run the real live e2e L6 probe (task 054) | `tests/e2e/live_e2e_test.go` | test skips |

When the operator runs `make l6-probe` (the harness in task 055), those env flags are set by the shell invocation, awakening the live tests. On CI and in developer `make check` runs, the flags are absent, and the tests skip — only the L5 fake-provider tests run.

### Host-side execution (claude and gate run on the operator's machine)

The `claude` CLI executor **cannot** run inside the Podman execution-box. The orchestrator's architecture requires the executor to **produce a branch** (either on-disk or in git), and to **record the run in the supervisor's audit trail**; both of these are operations on the orchestrator's *host* filesystem and git, not inside the sandbox. When the orchestrator runs `podman pod create ... podman run ...`, it is the *orchestrator process* that reads `AGENT_BUILDER_CLAUDE_CLI` and invokes the executor CLI (e.g. `claude run --prompt "..." --message-limit 10`); the executor then writes a branch (to the *host's* `.git/`) and returns `Result.Branch = "task/XXX"`. The orchestrator then runs the `gate` (linter + tests + scanners) **inside** the sandbox box, consuming the produced branch.

This is the load-bearing reason `claude` runs host-side: the Podman sandbox is **read-only rootfs** with network-isolated `--dns none` — it is not a development environment. It is a build sandbox, where code is compiled and tested; it is not where the agent's editing and decision-making happen.

Similarly, the `gate` process (golangci-lint, `go test`, code-scanner, etc.) runs **inside** the box. The box is the trusted execution environment for the gate; the host is where decision-making and branch production happen. This separation preserves the containment boundary: the host sees the agent's reasoning (logs, branch edits, git history); the box sees only compiled code, test outputs, and linting reports. Blurring that boundary (e.g. running both the executor *and* the gate host-side) weakens the auditability of what the agent saw vs. what the agent ran.

### The Podman box runs only `/bin/true` (liveness probe, no application workload)

In the **current phase** (Phase 0), the L6 proof is that the orchestrator's state machine works end-to-end and that the publishing path is real. The workload is a **stub**. The Podman execution-box runs `/bin/true` — the trivial liveness probe — so the test can verify the box starts (confirming the Podman substrate and the containment profile work), but the test does **not** attempt to run real agent code inside the box. That is a Phase 1+ advance.

(In Phase 1+, tasks 053–055 will be re-run against the exec-sandbox v0 block, which provides the full in-box agent sandbox; that version would run the real agent loop inside the box. The L6 proof would be that the agent loop itself runs in the sandbox. For now, Phase 0 is the orchestrator + a liveness probe.)

Concretely, when the test calls `containment/execution-box/run.sh --worktree . --probe` (in task 054's `live_e2e_test.go`), the script launches:
```bash
podman pod create ...
podman run --pod <pod> <image> /bin/true
```
The `/bin/true` exits 0 immediately, signaling the box is alive. The gate never runs on the `/bin/true` workload (gates require compiled code to lint/test). This is fine: the fake L5 test already proved the gate runs correctly on staged code; the L6 test proves the box itself works on a real host.

### Live PRs target the `l6` sandbox remote and self-clean

When the live publication test (task 053) runs, it invokes:
```bash
AGENT_BUILDER_LIVE_PUBLISH=1 \
  AGENT_BUILDER_LIVE_PUBLISH_REMOTE=l6 \
  go test -count=1 -v ./tests/publisher -run TestLiveBranchPRPublication_TC034
```

The test produces a **real PR** against the GitHub API. It opens against the private sandbox remote (`l6 → tkdtaylor/agent-builder-l6-sandbox`, per [l6-operator-runbook.md](../../plans/l6-operator-runbook.md) Section 1c), not the main project repo. This keeps real PR noise (from repeated L6 validation runs) out of the project's PR list.

**Self-cleaning:** Each PR created by the live tests is expected to be **immediately closed** by the test (or by the operator after inspection). The test writes the PR URL to the evidence file, but does **not** push the branch to a persistent remote — it is a one-off branch for that validation run. After the operator inspects the PR (confirming the branch name, the commit history, and the GitHub metadata), they can close it and discard the branch, and the test run is complete. No stale PR artifacts accumulate in the sandbox remote; the L6 probes are ephemeral validation steps, not persistent CI results.

### Reopening conditions (none specified)

This architecture is **locked in for Phase 0 and Phase 1**. The env-gate convention and the host-side / box-side boundaries are stable and should not change within the bootstrap scope. If a future phase (Phase 2+) changes the containment model, the agent provisioning, or the publication target, a new ADR will record that decision.

## Decision

Adopt the following architecture for L6 live-mode probes:

1. **Env-gated L6 tests.** New `tests/publisher/live_publisher_test.go` (task 053) reads `AGENT_BUILDER_LIVE_PUBLISH` and skips if unset. New `tests/e2e/live_e2e_test.go` (task 054) reads `AGENT_BUILDER_LIVE_E2E` and skips if unset. Fake-provider L5 tests remain in their current files (`TestPhase0EndToEndAcceptance`, `TestPhase1EndToEndAcceptance`, etc.) with no env gates — they pass unconditionally on every run.

2. **Host-side executor and gate-on-box.** The `claude` CLI executor runs on the operator's host (as `AGENT_BUILDER_CLAUDE_CLI=claude`), producing a branch on the host's git. The execution-box mounts the branch as read-only and runs the gate (linter/test/scanner) inside the box, with the workload set to `/bin/true` for Phase 0. This preserves the auditability boundary: the host sees decision-making; the box sees only code compilation and testing.

3. **Podman workload = `/bin/true` in Phase 0.** The live e2e test (task 054) proves the orchestrator + containment + publishing work end-to-end by running a simple liveness probe. The workload is `/bin/true`, not a real agent loop. Phase 1+ will upgrade the workload to the exec-sandbox v0 block, proving in-box agent execution.

4. **Live PRs target the `l6` sandbox remote.** The publication probes open PRs against the private `tkdtaylor/agent-builder-l6-sandbox` remote (configured in Section 1c of [l6-operator-runbook.md](../../plans/l6-operator-runbook.md)), not the main project remote. This keeps validation PR noise out of the project history.

5. **Self-cleaning discipline for live PRs.** The tests produce real PRs (writing the PR URL to the evidence file), but do not push the branch to a persistent remote; each PR is ephemeral. The operator inspects the PR, closes it, discards the branch, and the test run is done. No persistent artifacts accumulate in the sandbox remote.

6. **L6 harness calls the live tests via env-set in `scripts/l6-probe.sh` (task 055).** The operator never invokes `AGENT_BUILDER_LIVE_PUBLISH=1 go test ...` by hand; instead, they run `make l6-probe`, which calls `scripts/l6-probe.sh` with the env flags set, running the closing-order steps (014–034) in sequence and collecting evidence. Operators have one, documented entry point; the harness shields them from accidental env mis-settings.

## Consequences

**What is preserved.** The L5 fake-provider gate is **the definition of done for merging to main** (per CLAUDE.md invariants) and is unaffected. Every commit is gated by the fake-provider tests; developers running `make check` see no L6 steps and no credential expectations. The orchestrator's state machine is proven correct in the laboratory before any operator runs it on a real host.

**What becomes possible.** Operators have a documented, env-gated path to validate the orchestrator against real hosts, real credentials, and real endpoints (GitHub API, Anthropic API). The three new test files (053–055) explicitly separate the laboratory path (L5) from the real-world path (L6), with no confusion about which credentials are needed when, and no risk of accidentally running a test against the wrong remote.

**What becomes easier.** The operator journey is scripted via `make l6-probe` (task 055), which runs all 10 closing-order probes and collects evidence in a paste-ready format. Operators do not manually invoke the tests — they call one make target, and the harness sets the env flags, calls the right test command, and surfaces the results. This reduces the operational cognitive load and the risk of subtle env-var misconfigurations.

**What requires discipline.** The env-gate convention (`AGENT_BUILDER_LIVE_PUBLISH` and `AGENT_BUILDER_LIVE_E2E`) must never be set by CI, `make check`, or developer defaults. The tests must unconditionally skip when the flags are unset, with no "smart" fallback to a default sandbox remote or stub credentials. This is critical: a live test that silently falls back to a fake provider is a silent failure mode that erodes the boundary between laboratory and real-world proof. The test framework (Go's `t.Skip()`) is the control; skipping is not a failure — it is an explicit declaration that the test is not running.

**Verification.** The L6 paths are verified by the operator running the probes on a real host via the harness (task 055), not by CI. The evidence produced by the harness is the operator's attestation that the orchestrator worked on their machine. The `docs/tasks/test-specs/coverage-tracker.md` rows are promoted from 🟡 to ✅ when the operator pastes the evidence (e.g. `PASS | TC-034 real PR opened: https://github.com/tkdtaylor/agent-builder-l6-sandbox/pull/NNN`) into the `Verified by` column. This is a human attestation step, not an automated promotion — consistent with the *no unattended self-modification* invariant (CLAUDE.md).

**Spec updates land with the code (flagged, not edited here).** None required for this ADR — no externally-visible behavior or interfaces change. The L6 tests are **internal** L5→L6 closure steps, not user-facing features. The operator runbook and the verification checklist (already written by task 044 in Phase 1) document the live path; no `docs/spec/` updates are needed.

## Accepted by the orchestrator on 2026-06-17 (concurring with the recommendation).

The separation between L5 laboratory tests and L6 real-world probes is explicit and env-gated. No credentials leak into CI. The host-side / box-side boundary is preserved. Live PRs are ephemeral and self-cleaning. The operator journey is one entry point (`make l6-probe`), which is documented and repeatable.
