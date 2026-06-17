#!/usr/bin/env bash
# scripts/l6-probe.sh — L6 evidence collector and probe runner
#
# Runs (or in --dry-run simulates) all 10 Phase 0 L6 probe steps (9 binary
# probes + the 030 ledger step) in the prescribed closing order from
# docs/plans/phase0-l6-verification-checklist.md.
#
# Each probe is gated on its prerequisites. A missing prerequisite produces a
# SKIP status (not FAIL) and execution continues. Exit 0 unless a real
# prerequisite check or probe invocation encounters an unexpected error.
#
# After the run, writes a structured evidence file with one row per closing-order step (10 rows)
# containing: task ID, probe command, verbatim final output line (or
# "[dry-run: not executed]"), and status (PASS/SKIP/FAIL). The file is
# paste-ready for the Verified-by column of coverage-tracker.md.
#
# The script calls scripts/l6-preflight.sh before running any real probes and
# refuses (exits non-zero) if preflight is NOT READY. --dry-run bypasses the
# preflight gate.
#
# Testability seam:
#   Set L6_PROBE_PATH to a directory of stub binaries. This value is prepended
#   to PATH so every command -v / invocation uses stubs first.
#   Set L6_EVIDENCE_FILE to override the default evidence file path.
#
# Usage:
#   bash scripts/l6-probe.sh [--dry-run] [--help]
#   L6_PROBE_PATH=/tmp/stubs bash scripts/l6-probe.sh --dry-run
#
# Evidence file:
#   Default path: docs/plans/l6-evidence.txt (fixed, simpler for tests)
#   Override:     L6_EVIDENCE_FILE=/tmp/my-evidence.txt
#   Format:       10 rows, one per closing-order step, pipe-delimited:
#                 TASK-<id> | <command> | <output-line> | <status> [| SKIP-REASON: <reason>]

set -euo pipefail

# ─── compute repo root ────────────────────────────────────────────────────────

_SCRIPT="${BASH_SOURCE[0]}"
_SCRIPT_DIR="${_SCRIPT%/*}"
case "$_SCRIPT_DIR" in
    /*) ;;
    *) _SCRIPT_DIR="$(cd "$_SCRIPT_DIR" && pwd)" ;;
esac
REPO_ROOT="$(cd "$_SCRIPT_DIR/.." && pwd)"

# ─── gate-tools directory ─────────────────────────────────────────────────────
#
# Bug 1 fix: resolve the gate-tools directory once here rather than passing ""
# to run.sh (which causes "Gate toolchain directory does not exist:" error).
# Honor EXEC_BOX_GATE_TOOLS when set — the same default run.sh uses at line ~49:
#   gate_tools="${EXEC_BOX_GATE_TOOLS:-$box_dir/gate-tools}"
GATE_TOOLS_DIR="${EXEC_BOX_GATE_TOOLS:-${REPO_ROOT}/containment/execution-box/gate-tools}"

# ─── argument parsing ─────────────────────────────────────────────────────────

DRY_RUN=0
for arg in "$@"; do
    case "$arg" in
        --dry-run) DRY_RUN=1 ;;
        --help|-h)
            cat <<USAGE
Usage: bash scripts/l6-probe.sh [--dry-run] [--help]

Runs (or simulates) all 10 Phase 0 L6 probe steps (9 binary probes + the 030
ledger step) in the closing order prescribed by
docs/plans/phase0-l6-verification-checklist.md.

Options:
  --dry-run   Simulate all probes without invoking real commands. Bypasses
              the preflight gate. Useful for testing ordering and gating logic.
  --help, -h  Print this help and exit.

Environment:
  L6_PROBE_PATH       Directory of stub binaries prepended to PATH (for tests).
  L6_EVIDENCE_FILE    Override the evidence file path (default: docs/plans/l6-evidence.txt).

Evidence file:
  Path: ${REPO_ROOT}/docs/plans/l6-evidence.txt  (or L6_EVIDENCE_FILE)
  Format: 10 rows, pipe-delimited (task ID | command | output | status)
  Paste-ready for the Verified-by column of docs/tasks/test-specs/coverage-tracker.md.

Closing order:
  014 → 015 → 016 → 021 → 030 (ledger) → 022 → 028 → 033 → 034 → 032 (capstone)
USAGE
            exit 0
            ;;
    esac
done

# ─── injectable PATH seam ─────────────────────────────────────────────────────
#
# When L6_PROBE_PATH is set, use it as the EXCLUSIVE PATH for all command
# lookups. This ensures test stubs fully replace real system tools so that
# TC-044-01 through TC-044-04 can run without any live host tooling leaking in.
#
# When L6_PROBE_PATH is unset, use the normal host PATH unchanged.

if [ -n "${L6_PROBE_PATH:-}" ]; then
    export PATH="${L6_PROBE_PATH}"
fi

# ─── evidence file path ───────────────────────────────────────────────────────

EVIDENCE_FILE="${L6_EVIDENCE_FILE:-${REPO_ROOT}/docs/plans/l6-evidence.txt}"

# ─── state ────────────────────────────────────────────────────────────────────

# Track which probes have RUN (not skipped) for 030 prerequisite
PROBE_014_RAN=0
PROBE_015_RAN=0
PROBE_016_RAN=0
PROBE_021_RAN=0

# Evidence rows accumulated here (one per task, 9 total)
EVIDENCE_ROWS=()

# ─── output helpers ───────────────────────────────────────────────────────────

# status_line: print a formatted probe status line to stdout
# args: task-id, status, command, detail
status_line() {
    local task_id="$1"
    local status="$2"
    local cmd="$3"
    local detail="$4"
    printf '[%s] %-8s  %s\n' "$task_id" "$status" "$detail"
}

# ─── preflight gate ───────────────────────────────────────────────────────────

if [ "$DRY_RUN" -eq 0 ]; then
    PREFLIGHT_SCRIPT="${L6_PROBE_PATH:+${L6_PROBE_PATH}/scripts/l6-preflight.sh}"
    PREFLIGHT_SCRIPT="${PREFLIGHT_SCRIPT:-${REPO_ROOT}/scripts/l6-preflight.sh}"

    # If L6_PROBE_PATH is set and has a stub preflight, use it; otherwise use repo
    if [ -n "${L6_PROBE_PATH:-}" ] && [ -f "${L6_PROBE_PATH}/scripts/l6-preflight.sh" ]; then
        PREFLIGHT_SCRIPT="${L6_PROBE_PATH}/scripts/l6-preflight.sh"
    else
        PREFLIGHT_SCRIPT="${REPO_ROOT}/scripts/l6-preflight.sh"
    fi

    preflight_output="$(bash "$PREFLIGHT_SCRIPT" 2>&1)" && preflight_exit=0 || preflight_exit=$?
    if [ "$preflight_exit" -ne 0 ]; then
        printf 'ERROR: Host is NOT READY for L6 probes.\n' >&2
        printf 'Preflight output:\n%s\n' "$preflight_output" >&2
        printf '\nRun "make l6-preflight" (or "bash scripts/l6-preflight.sh") to diagnose.\n' >&2
        printf 'Fix all FAIL/MISSING checks before running l6-probe.\n' >&2
        exit 1
    fi
fi

# ─── probe runner ─────────────────────────────────────────────────────────────
#
# run_probe: run a single probe (or simulate in dry-run mode)
# args:
#   $1  task_id       — e.g. "014"
#   $2  cmd           — the command to run (for documentation)
#   $3  skip_reason   — if non-empty, skip this probe with this reason
#
# Returns via globals:
#   _LAST_STATUS      — PASS, SKIP, or FAIL
#   _LAST_OUTPUT_LINE — verbatim final output line (or placeholder)
#
run_probe() {
    local task_id="$1"
    local cmd="$2"
    local skip_reason="$3"
    shift 3
    # Remaining args are the actual command argv (unused in dry-run)
    local actual_cmd=("$@")

    if [ -n "$skip_reason" ]; then
        _LAST_STATUS="SKIP"
        _LAST_OUTPUT_LINE="[skipped: ${skip_reason}]"
        status_line "$task_id" "SKIP" "$cmd" "SKIP — ${skip_reason}"
        return 0
    fi

    if [ "$DRY_RUN" -eq 1 ]; then
        _LAST_STATUS="DRY-RUN"
        _LAST_OUTPUT_LINE="[dry-run: not executed]"
        status_line "$task_id" "DRY-RUN" "$cmd" "[dry-run: not executed]"
        return 0
    fi

    # Real run
    local output last_line exit_code
    output="$("${actual_cmd[@]}" 2>&1)" && exit_code=0 || exit_code=$?
    last_line="$(printf '%s' "$output" | tail -1)"
    [ -z "$last_line" ] && last_line="[no output]"

    if [ "$exit_code" -eq 0 ]; then
        _LAST_STATUS="PASS"
        _LAST_OUTPUT_LINE="$last_line"
        status_line "$task_id" "PASS" "$cmd" "$last_line"
    else
        _LAST_STATUS="FAIL"
        _LAST_OUTPUT_LINE="$last_line"
        status_line "$task_id" "FAIL" "$cmd" "exit ${exit_code}: ${last_line}"
    fi
}

# record_evidence: add a row to the evidence array
# args: task_id, cmd, output_line, status, [skip_reason]
record_evidence() {
    local task_id="$1"
    local cmd="$2"
    local output_line="$3"
    local status="$4"
    local skip_reason="${5:-}"

    local row
    if [ -n "$skip_reason" ]; then
        row="TASK-${task_id} | ${cmd} | ${output_line} | ${status} | SKIP-REASON: ${skip_reason}"
    else
        row="TASK-${task_id} | ${cmd} | ${output_line} | ${status}"
    fi
    EVIDENCE_ROWS+=("$row")
}

# ─── seed_live_fixture helper ────────────────────────────────────────────────
#
# seed_live_fixture: create a temp task-root and git worktree for live probes (022/028)
# Outputs: two lines to stdout: task-root path, then worktree path
# Sets shell variables: AGENT_BUILDER_TASK_ROOT, AGENT_BUILDER_WORKTREE
#
# The fixture task-root contains docs/plans/roadmap.md and one docs/tasks/backlog/001-fixture.md.
# The fixture worktree is a real git-init directory with go.mod and a minimal test.
#
# Note: This function uses real system tools (mktemp, mkdir, git) and requires
# PATH to not be restricted to stubs. It's only called when L6_PROBE_PATH is empty.

seed_live_fixture() {
    # Ensure we have access to real tools, not stubs
    local real_path="/usr/bin:/bin:/usr/local/bin:${PATH}"

    local task_root worktree
    task_root="$(PATH="$real_path" mktemp -d)"
    worktree="$(PATH="$real_path" mktemp -d)"

    # Create task-root structure
    PATH="$real_path" mkdir -p "$task_root/docs/plans" "$task_root/docs/tasks/backlog"

    # Write roadmap.md
    cat > "$task_root/docs/plans/roadmap.md" <<'EOF'
# Roadmap
EOF

    # Write a fixture task file (001-fixture.md)
    cat > "$task_root/docs/tasks/backlog/001-fixture.md" <<'EOF'
# Task 001: fixture task

**Status:** ready

## Goal

Minimal fixture task for testing probe 022/028.
EOF

    # Create worktree as a real git repo with minimal Go module
    local old_pwd
    old_pwd="$(pwd)"
    cd "$worktree"
    PATH="$real_path" git init > /dev/null 2>&1

    cat > "$worktree/go.mod" <<'EOF'
module example.com/fixture

go 1.23
EOF

    cat > "$worktree/main.go" <<'EOF'
package main

func main() {}
EOF

    cat > "$worktree/main_test.go" <<'EOF'
package main

import "testing"

func TestFixture(t *testing.T) {
    t.Skip("fixture test — not meant to run")
}
EOF

    PATH="$real_path" git add -A > /dev/null 2>&1
    PATH="$real_path" git commit -m "initial" > /dev/null 2>&1
    cd "$old_pwd"

    # Output paths and set shell variables
    printf '%s\n%s\n' "$task_root" "$worktree"
    AGENT_BUILDER_TASK_ROOT="$task_root"
    AGENT_BUILDER_WORKTREE="$worktree"
    export AGENT_BUILDER_TASK_ROOT AGENT_BUILDER_WORKTREE
}

# ─── detect prerequisite tools ────────────────────────────────────────────────

HAS_PODMAN=0
HAS_PODMAN_ROOTLESS=0
HAS_RUNSC=0
HAS_SRT=0
HAS_CLAUDE=0
HAS_ANTHROPIC_API_KEY=0
HAS_GH=0
HAS_GIT_REMOTE=0

command -v podman > /dev/null 2>&1 && HAS_PODMAN=1 || true

if [ "$HAS_PODMAN" -eq 1 ]; then
    rootless_out="$(podman info --format '{{.Host.Security.Rootless}}' 2>/dev/null)" && rootless_exit=0 || rootless_exit=$?
    if [ "$rootless_exit" -eq 0 ] && [ "$rootless_out" = "true" ]; then
        HAS_PODMAN_ROOTLESS=1
    fi
fi

command -v runsc > /dev/null 2>&1 && HAS_RUNSC=1 || true
command -v srt   > /dev/null 2>&1 && HAS_SRT=1   || true
command -v claude > /dev/null 2>&1 && HAS_CLAUDE=1 || true
[ -n "${ANTHROPIC_API_KEY:-}" ] && [ -n "$(printf '%s' "${ANTHROPIC_API_KEY}" | tr -d ' ')" ] && HAS_ANTHROPIC_API_KEY=1 || true
command -v gh    > /dev/null 2>&1 && HAS_GH=1    || true

if command -v git > /dev/null 2>&1; then
    remote_out="$(git remote -v 2>/dev/null || true)"
    [ -n "$remote_out" ] && HAS_GIT_REMOTE=1 || true
fi

# In dry-run mode, treat all tools as present so ordering/gating logic is testable
# via explicit missing: stubs on L6_PROBE_PATH (which won't be found by command -v).
# Actually: in dry-run, we still gate via command -v so stub absences work correctly.
# The test creates a stub dir where missing tools have no stub binary — so command -v
# returns non-zero for absent tools even in dry-run. No special dry-run bypass needed.

# ─── closing order probes ─────────────────────────────────────────────────────
#
# Order (from checklist "Closing order" section):
#   1. 014 — containment probe
#   2. 015 — egress probe
#   3. 016 — runsc runtime probe
#   4. 021 — sandbox-runtime live harness
#   5. 030 — ledger update (observe 014/015/016/021 green)
#   6. 022 — Claude CLI executor
#   7. 028 — default run wiring
#   8. 033 — gate-in-box probe
#   9. 034 — branch & PR publication
#   10. 032 — phase 0 end-to-end capstone

printf '\n=== L6 probe run (%s) ===\n\n' "$([ "$DRY_RUN" -eq 1 ] && echo "dry-run" || echo "live")"

# ── Probe 014: Podman containment profile ────────────────────────────────────

SKIP_014=""
if [ "$HAS_PODMAN" -eq 0 ] || [ "$HAS_PODMAN_ROOTLESS" -eq 0 ]; then
    SKIP_014="podman/rootless-podman absent"
fi

CMD_014='containment/execution-box/run.sh --gate-tools <gate-tools-dir> --worktree . --probe'
run_probe "014" "$CMD_014" "$SKIP_014" \
    bash containment/execution-box/run.sh --gate-tools "$GATE_TOOLS_DIR" --worktree . --probe

STATUS_014="$_LAST_STATUS"
OUTPUT_014="$_LAST_OUTPUT_LINE"

if [ -z "$SKIP_014" ] && [ "$STATUS_014" != "FAIL" ]; then
    PROBE_014_RAN=1
fi

record_evidence "014" "$CMD_014" "$OUTPUT_014" "$STATUS_014" "$SKIP_014"

# ── Probe 015: Default-deny egress allowlist ──────────────────────────────────

SKIP_015=""
if [ "$HAS_PODMAN" -eq 0 ] || [ "$HAS_PODMAN_ROOTLESS" -eq 0 ]; then
    SKIP_015="podman/rootless-podman absent"
fi

CMD_015='containment/execution-box/run.sh --gate-tools <gate-tools-dir> --worktree . --egress-probe'
run_probe "015" "$CMD_015" "$SKIP_015" \
    bash containment/execution-box/run.sh --gate-tools "$GATE_TOOLS_DIR" --worktree . --egress-probe

STATUS_015="$_LAST_STATUS"
OUTPUT_015="$_LAST_OUTPUT_LINE"

if [ -z "$SKIP_015" ] && [ "$STATUS_015" != "FAIL" ]; then
    PROBE_015_RAN=1
fi

record_evidence "015" "$CMD_015" "$OUTPUT_015" "$STATUS_015" "$SKIP_015"

# ── Probe 016: Tiered OCI runtime seam (runsc) ────────────────────────────────

SKIP_016=""
if [ "$HAS_PODMAN" -eq 0 ] || [ "$HAS_PODMAN_ROOTLESS" -eq 0 ]; then
    SKIP_016="podman/rootless-podman absent"
elif [ "$HAS_RUNSC" -eq 0 ]; then
    SKIP_016="prereq runsc absent"
fi

CMD_016='containment/execution-box/run.sh --gate-tools <gate-tools-dir> --worktree . --runtime runsc --probe'
run_probe "016" "$CMD_016" "$SKIP_016" \
    bash containment/execution-box/run.sh --gate-tools "$GATE_TOOLS_DIR" --worktree . --runtime runsc --probe

STATUS_016="$_LAST_STATUS"
OUTPUT_016="$_LAST_OUTPUT_LINE"

if [ -z "$SKIP_016" ] && [ "$STATUS_016" != "FAIL" ]; then
    PROBE_016_RAN=1
fi

record_evidence "016" "$CMD_016" "$OUTPUT_016" "$STATUS_016" "$SKIP_016"

# ── Probe 021: sandbox-runtime live harness ───────────────────────────────────

SKIP_021=""
if [ "$HAS_SRT" -eq 0 ]; then
    SKIP_021="prereq srt absent"
fi

CMD_021='env AGENT_BUILDER_LIVE_SRT=1 AGENT_BUILDER_LIVE_SRT_ALLOW_HOST=<allow> AGENT_BUILDER_LIVE_SRT_DENY_HOST=<deny> go test -count=1 -v ./tests/sandbox -run TestSandboxRuntimeLiveHarness_TC002_TC003'
run_probe "021" "$CMD_021" "$SKIP_021" \
    env AGENT_BUILDER_LIVE_SRT=1 \
    go test -count=1 -v ./tests/sandbox -run TestSandboxRuntimeLiveHarness_TC002_TC003

STATUS_021="$_LAST_STATUS"
OUTPUT_021="$_LAST_OUTPUT_LINE"

if [ -z "$SKIP_021" ] && [ "$STATUS_021" != "FAIL" ]; then
    PROBE_021_RAN=1
fi

record_evidence "021" "$CMD_021" "$OUTPUT_021" "$STATUS_021" "$SKIP_021"

# ── Probe 030: Runtime isolation evidence (ledger update) ─────────────────────
#
# Task 030 is a ledger update, not a binary probe. It closes when 014/015/016/021
# are all observed green. If any of those are SKIP, 030 is also SKIP.

SKIP_030=""
LEDGER_DETAIL=""

if [ "$PROBE_014_RAN" -eq 0 ] || [ "$PROBE_015_RAN" -eq 0 ] || \
   [ "$PROBE_016_RAN" -eq 0 ] || [ "$PROBE_021_RAN" -eq 0 ]; then
    SKIP_030="one or more of 014/015/016/021 did not complete (014=$STATUS_014 015=$STATUS_015 016=$STATUS_016 021=$STATUS_021)"
else
    LEDGER_DETAIL="014=${STATUS_014} 015=${STATUS_015} 016=${STATUS_016} 021=${STATUS_021} — all green"
fi

CMD_030='[ledger update] observe 014/015/016/021 outputs green and record in coverage-tracker.md'

if [ -n "$SKIP_030" ]; then
    _LAST_STATUS="SKIP"
    _LAST_OUTPUT_LINE="[skipped: ${SKIP_030}]"
    status_line "030" "SKIP" "$CMD_030" "SKIP — ${SKIP_030}"
elif [ "$DRY_RUN" -eq 1 ]; then
    _LAST_STATUS="DRY-RUN"
    _LAST_OUTPUT_LINE="[dry-run: not executed]"
    status_line "030" "DRY-RUN" "$CMD_030" "[dry-run: not executed]"
else
    # Real run: the "probe" is reviewing the outputs of 014/015/016/021
    _LAST_STATUS="PASS"
    _LAST_OUTPUT_LINE="$LEDGER_DETAIL"
    status_line "030" "PASS" "$CMD_030" "$LEDGER_DETAIL"
fi

STATUS_030="$_LAST_STATUS"
OUTPUT_030="$_LAST_OUTPUT_LINE"

record_evidence "030" "$CMD_030" "$OUTPUT_030" "$STATUS_030" "$SKIP_030"

# ── Probe 022: Claude CLI executor ────────────────────────────────────────────

SKIP_022=""
if [ "$HAS_CLAUDE" -eq 0 ]; then
    SKIP_022="prereq claude CLI absent (or not authenticated)"
fi
if [ "$HAS_ANTHROPIC_API_KEY" -eq 0 ] && [ -z "$SKIP_022" ]; then
    SKIP_022="ANTHROPIC_API_KEY unset"
fi

# Seed fixture for 022/028 probes (reused by both) — but only if PATH isn't restricted
# (in test mode with L6_PROBE_PATH, mktemp won't be available, so skip fixture seeding)
FIXTURE_TASK_ROOT=""
FIXTURE_WORKTREE=""
AGENT_BUILDER_RUN_RECORD=""

if [ -z "${L6_PROBE_PATH:-}" ]; then
    fixture_output="$(seed_live_fixture)"
    FIXTURE_TASK_ROOT="$(printf '%s\n' "$fixture_output" | head -1)"
    FIXTURE_WORKTREE="$(printf '%s\n' "$fixture_output" | tail -1)"
    AGENT_BUILDER_RUN_RECORD="$(mktemp)"
    export AGENT_BUILDER_RUN_RECORD
fi

CMD_022='env AGENT_BUILDER_TASK_ROOT=<fixture> AGENT_BUILDER_WORKTREE=<fixture> AGENT_BUILDER_PUBLISH_REMOTE=... AGENT_BUILDER_RUN_TIMEOUT=300s AGENT_BUILDER_MAX_ATTEMPTS=1 AGENT_BUILDER_RUN_RECORD=<tmp> go run ./cmd/agent-builder run'
run_probe "022" "$CMD_022" "$SKIP_022" \
    env AGENT_BUILDER_TASK_ROOT="${FIXTURE_TASK_ROOT}" \
    AGENT_BUILDER_WORKTREE="${FIXTURE_WORKTREE}" \
    AGENT_BUILDER_PUBLISH_REMOTE="${AGENT_BUILDER_PUBLISH_REMOTE:-}" \
    AGENT_BUILDER_RUN_TIMEOUT=300s \
    AGENT_BUILDER_MAX_ATTEMPTS=1 \
    AGENT_BUILDER_RUN_RECORD="${AGENT_BUILDER_RUN_RECORD}" \
    go run ./cmd/agent-builder run

STATUS_022="$_LAST_STATUS"
OUTPUT_022="$_LAST_OUTPUT_LINE"

record_evidence "022" "$CMD_022" "$OUTPUT_022" "$STATUS_022" "$SKIP_022"

# ── Probe 028: Default run wiring ─────────────────────────────────────────────
#
# Bug 3 fix: AGENT_BUILDER_SANDBOX_RUNTIME=srt was removed by ADR 021; passing
# it causes ConfigFromEnv to error with the migration message. Drop it entirely.
# Probe 028 is gated only on claude presence + ANTHROPIC_API_KEY (not srt — srt
# was only needed for the now-removed env var, not for 028's actual workload).
#
# Reuses the fixture seeded by probe 022.

SKIP_028=""
if [ "$HAS_CLAUDE" -eq 0 ]; then
    SKIP_028="prereq claude CLI absent (or not authenticated)"
fi
if [ "$HAS_ANTHROPIC_API_KEY" -eq 0 ] && [ -z "$SKIP_028" ]; then
    SKIP_028="ANTHROPIC_API_KEY unset"
fi

AGENT_BUILDER_RUN_RECORD="$(mktemp)"
export AGENT_BUILDER_RUN_RECORD

CMD_028='env AGENT_BUILDER_TASK_ROOT=<fixture> AGENT_BUILDER_WORKTREE=<fixture> AGENT_BUILDER_PUBLISH_REMOTE=... AGENT_BUILDER_RUN_TIMEOUT=300s AGENT_BUILDER_MAX_ATTEMPTS=1 AGENT_BUILDER_RUN_RECORD=<tmp> go run ./cmd/agent-builder run'
run_probe "028" "$CMD_028" "$SKIP_028" \
    env AGENT_BUILDER_TASK_ROOT="${FIXTURE_TASK_ROOT:-}" \
    AGENT_BUILDER_WORKTREE="${FIXTURE_WORKTREE:-}" \
    AGENT_BUILDER_PUBLISH_REMOTE="${AGENT_BUILDER_PUBLISH_REMOTE:-}" \
    AGENT_BUILDER_RUN_TIMEOUT=300s \
    AGENT_BUILDER_MAX_ATTEMPTS=1 \
    AGENT_BUILDER_RUN_RECORD="$AGENT_BUILDER_RUN_RECORD" \
    go run ./cmd/agent-builder run

STATUS_028="$_LAST_STATUS"
OUTPUT_028="$_LAST_OUTPUT_LINE"

record_evidence "028" "$CMD_028" "$OUTPUT_028" "$STATUS_028" "$SKIP_028"

# ── Probe 033: Execution-box Gate toolchain (gate-in-box) ─────────────────────

SKIP_033=""
if [ "$HAS_PODMAN" -eq 0 ] || [ "$HAS_PODMAN_ROOTLESS" -eq 0 ]; then
    SKIP_033="podman/rootless-podman absent"
fi

CMD_033='containment/execution-box/run.sh --gate-tools <gate-tools-dir> --worktree . --probe'
run_probe "033" "$CMD_033" "$SKIP_033" \
    bash containment/execution-box/run.sh --gate-tools "$GATE_TOOLS_DIR" --worktree . --probe

STATUS_033="$_LAST_STATUS"
OUTPUT_033="$_LAST_OUTPUT_LINE"

record_evidence "033" "$CMD_033" "$OUTPUT_033" "$STATUS_033" "$SKIP_033"

# ── Probe 034: Branch & PR publication (live test) ─────────────────────────────
#
# Task 053: wired to run the live test TestLiveBranchPRPublication_TC034.
# Thread AGENT_BUILDER_PUBLISH_REMOTE from the environment into the actual argv
# (for TC-046-02 regression guard). When unset, skip gracefully.

SKIP_034=""
if [ "$HAS_GH" -eq 0 ]; then
    SKIP_034="prereq gh CLI absent (or not authenticated)"
fi
if [ "$HAS_GIT_REMOTE" -eq 0 ] && [ -z "$SKIP_034" ]; then
    SKIP_034="no git remote configured"
fi
if [ -z "${AGENT_BUILDER_PUBLISH_REMOTE:-}" ] && [ -z "$SKIP_034" ]; then
    SKIP_034="AGENT_BUILDER_PUBLISH_REMOTE unset"
fi

CMD_034='env AGENT_BUILDER_LIVE_PUBLISH=1 AGENT_BUILDER_LIVE_PUBLISH_REMOTE=<remote> AGENT_BUILDER_PUBLISH_REMOTE=<remote> go test -count=1 -v ./tests/publisher -run TestLiveBranchPRPublication_TC034'
run_probe "034" "$CMD_034" "$SKIP_034" \
    env AGENT_BUILDER_LIVE_PUBLISH=1 \
    AGENT_BUILDER_LIVE_PUBLISH_REMOTE="${AGENT_BUILDER_PUBLISH_REMOTE:-}" \
    AGENT_BUILDER_PUBLISH_REMOTE="${AGENT_BUILDER_PUBLISH_REMOTE:-}" \
    go test -count=1 -v ./tests/publisher -run TestLiveBranchPRPublication_TC034

STATUS_034="$_LAST_STATUS"
OUTPUT_034="$_LAST_OUTPUT_LINE"

record_evidence "034" "$CMD_034" "$OUTPUT_034" "$STATUS_034" "$SKIP_034"

# ── Probe 032: Phase 0 end-to-end capstone (live test) ──────────────────────────
#
# Task 054: wired to run the live test TestLivePhase0EndToEndAcceptance_TC032.
# Capstone requires all of: podman + rootless, runsc, claude, gh + git remote,
# and AGENT_BUILDER_PUBLISH_REMOTE.
#
# Keep AGENT_BUILDER_PUBLISH_REMOTE in the env prefix (TC-046-02 regression guard).
# No AGENT_BUILDER_SANDBOX_RUNTIME (ADR 021 removed it; tasks 035/036 use Podman).

SKIP_032=""
SKIP_032_PARTS=()

[ "$HAS_PODMAN" -eq 0 ] || [ "$HAS_PODMAN_ROOTLESS" -eq 0 ] && SKIP_032_PARTS+=("podman/rootless-podman absent")
[ "$HAS_RUNSC" -eq 0 ]  && SKIP_032_PARTS+=("runsc absent")
[ "$HAS_CLAUDE" -eq 0 ] && SKIP_032_PARTS+=("claude absent")
[ "$HAS_GH" -eq 0 ]     && SKIP_032_PARTS+=("gh absent")
[ "$HAS_GIT_REMOTE" -eq 0 ] && SKIP_032_PARTS+=("no git remote")
[ -z "${AGENT_BUILDER_PUBLISH_REMOTE:-}" ] && SKIP_032_PARTS+=("AGENT_BUILDER_PUBLISH_REMOTE unset")

if [ "${#SKIP_032_PARTS[@]}" -gt 0 ]; then
    SKIP_032="$(IFS=', '; printf '%s' "${SKIP_032_PARTS[*]}")"
fi

CMD_032='env AGENT_BUILDER_LIVE_E2E=1 AGENT_BUILDER_LIVE_E2E_REMOTE=<remote> AGENT_BUILDER_PUBLISH_REMOTE=<remote> go test -count=1 -v ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032'
run_probe "032" "$CMD_032" "$SKIP_032" \
    env AGENT_BUILDER_LIVE_E2E=1 \
    AGENT_BUILDER_LIVE_E2E_REMOTE="${AGENT_BUILDER_PUBLISH_REMOTE:-}" \
    AGENT_BUILDER_PUBLISH_REMOTE="${AGENT_BUILDER_PUBLISH_REMOTE:-}" \
    go test -count=1 -v ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032

STATUS_032="$_LAST_STATUS"
OUTPUT_032="$_LAST_OUTPUT_LINE"

record_evidence "032" "$CMD_032" "$OUTPUT_032" "$STATUS_032" "$SKIP_032"

# ─── write evidence file ──────────────────────────────────────────────────────
#
# Use bash built-in printf for timestamp (%(%Y-%m-%dT%H:%M:%SZ)T, bash 4.2+).
# Fall back to a static marker if the built-in is unavailable so the evidence
# file is always written even when PATH is restricted to stub binaries in tests.

_ts=""
# bash 4.2+ supports printf '%()T' — use it to avoid requiring external 'date'
if printf '%(%Y-%m-%dT%H:%M:%SZ)T' -1 > /dev/null 2>&1; then
    printf -v _ts '%(%Y-%m-%dT%H:%M:%SZ)T' -1
fi

{
    printf '# L6 probe evidence — generated %s\n' "$_ts"
    printf '# Mode: %s\n' "$([ "$DRY_RUN" -eq 1 ] && printf 'dry-run' || printf 'live')"
    printf '# Format: TASK-<id> | <command> | <output-line> | <status> [| SKIP-REASON: <reason>]\n'
    printf '# Paste the relevant rows into the Verified-by column of docs/tasks/test-specs/coverage-tracker.md\n'
    printf '#\n'
    for row in "${EVIDENCE_ROWS[@]}"; do
        printf '%s\n' "$row"
    done
} > "$EVIDENCE_FILE"

printf '\nEvidence file written to: %s\n' "$EVIDENCE_FILE"

# ─── summary ──────────────────────────────────────────────────────────────────
#
# Use bash case patterns instead of external grep so the summary works even
# when PATH is restricted to stub binaries (test mode).

PASS_COUNT=0
SKIP_COUNT=0
FAIL_COUNT=0

for row in "${EVIDENCE_ROWS[@]}"; do
    case "$row" in
        *'| PASS'*)    PASS_COUNT=$(( PASS_COUNT + 1 )) ;;
        *'| DRY-RUN'*) PASS_COUNT=$(( PASS_COUNT + 1 )) ;;  # dry-run counts as "ran"
        *'| SKIP'*)    SKIP_COUNT=$(( SKIP_COUNT + 1 )) ;;
        *'| FAIL'*)    FAIL_COUNT=$(( FAIL_COUNT + 1 )) ;;
    esac
done

printf '\n=== Summary: %d run/pass, %d skipped, %d failed ===\n' "$PASS_COUNT" "$SKIP_COUNT" "$FAIL_COUNT"

if [ "$FAIL_COUNT" -gt 0 ]; then
    printf '\nFailed probes:\n'
    for row in "${EVIDENCE_ROWS[@]}"; do
        case "$row" in
            *'| FAIL'*)
                printf '  %s\n' "$row"
                ;;
        esac
    done
    exit 1
fi

exit 0
