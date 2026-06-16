# Phase 0 — L6 Operator Verification Checklist

**Project:** agent-builder
**Created:** 2026-06-16
**Purpose:** Close the 9 🟡 rows in [coverage-tracker.md](../tasks/test-specs/coverage-tracker.md) to ✅ by running each task's live runtime path on a properly provisioned host. None of this requires code changes — it is environment provisioning plus operator-observed probe runs.

Phase 0 is currently *accepted at fake-provider L5* (Task 032 harness). Every row below is held at 🟡 because its existing L5 evidence exercises **fakes/stubs**, not the live path. This checklist is what an operator runs to promote them.

## Prerequisites — provision the host

Run on a Linux host with these present on `PATH`. Verify each before starting:

```bash
command -v podman        # rootless Podman (substrate — NOT Docker)
command -v runsc         # gVisor runtime for the agent workload tier
command -v bwrap         # bubblewrap (already present in dev env)
command -v srt           # @anthropic-ai/sandbox-runtime  (npm i -g @anthropic-ai/sandbox-runtime)
command -v claude        # Claude Code CLI, authenticated
command -v gh            # GitHub CLI, authenticated (gh auth status)
git remote -v            # a real remote configured for PR publication
podman info --format '{{.Host.Security.Rootless}}'   # must print: true
```

If `srt` hits `snap-confine has elevated permissions and is not confined` (the blocker seen in dev), run on a host where Node/`srt` is not installed via snap, or relocate the install off snap.

Baseline gate must still be green before any L6 run:

```bash
make check     # -> All checks passed.
make fitness   # -> Fitness checks passed.
```

## Per-task probes

Each block lists: the **success criterion** to observe, and the **command**. Record the verbatim final line in the tracker's `Verified by` column and flip 🟡 → ✅ in a separate `verify:` commit per task (per CLAUDE.md commit rules).

### Task 014 — Podman containment profile (L6)
Observe the execution box actually start under rootless Podman with read-only rootfs / dropped caps and the probe succeed.
```bash
containment/execution-box/run.sh --gate-tools <gate-tools-dir> --worktree . --probe
# success: probe runs inside the box and exits 0 (no "podman unavailable on PATH")
```

### Task 015 — Default-deny egress allowlist (L6)
Observe a non-allowlisted host blocked and an allowlisted host reachable from inside the box.
```bash
containment/execution-box/run.sh --gate-tools <gate-tools-dir> --worktree . --egress-probe
# success: allowlisted host reachable, denied host refused (egress-probe.sh assertions pass)
```

### Task 016 — Tiered OCI runtime seam (L6)
Plan already verifiable (`--print-runtime-plan` → `runtime=runsc source=default`); the open item is the live runtime probe.
```bash
containment/execution-box/run.sh --gate-tools <gate-tools-dir> --worktree . --runtime runsc --probe
# success: box starts under runsc and probe exits 0
```

### Task 021 — sandbox-runtime backing adapter (L6)
Run the live `srt` harness (gated behind the env flag so it only runs when intended).
```bash
env AGENT_BUILDER_LIVE_SRT=1 \
    AGENT_BUILDER_LIVE_SRT_ALLOW_HOST=<allow> \
    AGENT_BUILDER_LIVE_SRT_DENY_HOST=<deny> \
  go test -count=1 -v ./tests/sandbox -run TestSandboxRuntimeLiveHarness_TC002_TC003
# success: ok ./tests/sandbox  (real srt invoked, allow/deny network behaviour observed)
```

### Task 022 — Claude CLI executor (already ✅, L6 note still open)
Row is ✅ on a stubbed CLI but flagged "L6 real Claude CLI/auth pending." Confirm a real authenticated `claude` produces a branch.
```bash
env AGENT_BUILDER_CLAUDE_CLI=claude go run ./cmd/agent-builder run ...   # one real task
# success: executor invokes real claude, returns Result.Branch and Result.OK == true
```

### Task 028 — Default run wiring (L6)
Drive the full Phase 0 pipeline with real providers (not fakes).
```bash
env AGENT_BUILDER_CLAUDE_CLI=claude \
    AGENT_BUILDER_SANDBOX_RUNTIME=srt \
  go run ./cmd/agent-builder run --task-root docs/tasks/...
# success: one configured task selected, run executed in box, run_finished persisted
```

### Task 030 — Runtime isolation evidence (L6)
This row *is* the evidence ledger; it closes when 014/015/016/021 probes above are observed green. Re-run the three execution-box probes and `command -v srt`, record real (non-blocker) output.

### Task 032 — Phase 0 end-to-end acceptance (L6)
The capstone. Same harness, but with the live runtime path wired (real Podman + runsc + srt + claude + configured remote).
```bash
env AGENT_BUILDER_CLAUDE_CLI=claude AGENT_BUILDER_SANDBOX_RUNTIME=srt \
    AGENT_BUILDER_PUBLISH_REMOTE=<remote> AGENT_BUILDER_GH_CLI=gh \
  go test -count=1 -v ./tests/e2e -run TestPhase0EndToEndAcceptance
# success: task selected, branch produced, PR recorded LIVE, gate passed, run record persisted
```

### Task 033 — Execution-box gate toolchain (L6)
Plan verifiable today (`--print-toolchain-plan`); open item is running the gate *inside* the box under Podman.
```bash
containment/execution-box/run.sh --gate-tools <gate-tools-dir> --worktree . --probe
# success: gate toolchain mounted and `make check` runs to completion inside the box
```

### Task 034 — Branch & PR publication (L6)
Publish a real PR against a configured remote.
```bash
env AGENT_BUILDER_PUBLISH_REMOTE=<remote> AGENT_BUILDER_GH_CLI=gh AGENT_BUILDER_GITHUB_TOKEN=<token> \
  go test -count=1 -v ./tests/publisher -run TestBranchPRPublication
# success: a real PR is opened (capture PR URL); failure path still preserves task as not-done
```

## Closing order

1. Provision host (prereqs).
2. 014 → 015 → 016 (containment layer) → 021 (sandbox backend).
3. 030 ledger update once the above are green.
4. 022 / 028 (executor + wiring) → 033 (gate-in-box).
5. 034 (publication) → 032 (full e2e capstone) last.

Each promotion is its own `verify: confirm task NNN — <L6 evidence>` commit on a task branch, then merged. Do **not** batch.
