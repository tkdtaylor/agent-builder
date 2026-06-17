#!/usr/bin/env bash
# Test harness for scripts/l6-probe.sh
# Covers TC-044-01 through TC-044-05.
# Uses stub binaries on a temp PATH — no live host tooling required.
#
# Usage: bash scripts/tests/l6-probe-test.sh
# Exit 0 on all pass; non-zero on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
PROBE="$REPO_ROOT/scripts/l6-probe.sh"
PREFLIGHT="$REPO_ROOT/scripts/l6-preflight.sh"

# ─── helpers ─────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILURES=()

tc_pass() {
    local name="$1"
    printf 'PASS %s\n' "$name"
    PASS_COUNT=$(( PASS_COUNT + 1 ))
}

tc_fail() {
    local name="$1"
    local reason="$2"
    printf 'FAIL %s: %s\n' "$name" "$reason" >&2
    FAIL_COUNT=$(( FAIL_COUNT + 1 ))
    FAILURES+=("$name: $reason")
}

assert_contains() {
    local tc="$1"
    local haystack="$2"
    local needle="$3"
    if ! printf '%s' "$haystack" | grep -qF "$needle"; then
        tc_fail "$tc" "expected output to contain '${needle}'; got:\n${haystack}"
        return 1
    fi
    return 0
}

assert_not_contains() {
    local tc="$1"
    local haystack="$2"
    local needle="$3"
    if printf '%s' "$haystack" | grep -qF "$needle"; then
        tc_fail "$tc" "output should NOT contain '${needle}'; got:\n${haystack}"
        return 1
    fi
    return 0
}

assert_exit() {
    local tc="$1"
    local expected_exit="$2"
    local actual_exit="$3"
    if [ "$actual_exit" -ne "$expected_exit" ]; then
        tc_fail "$tc" "expected exit $expected_exit, got $actual_exit"
        return 1
    fi
    return 0
}

# ─── stub factory ────────────────────────────────────────────────────────────
#
# make_probe_stub_dir creates a temp dir with stub binaries for l6-probe.sh.
# All stubs exit 0 by default (simulating a fully provisioned host).
# Arguments:
#   "missing:toolname"  — do NOT create a stub for that tool
#   "preflight_ready"   — scripts/l6-preflight.sh stub exits 0 with READY
#   "preflight_not_ready" — scripts/l6-preflight.sh stub exits 1 with NOT READY
#
# The stub dir is also used as L6_PROBE_PATH for the script.

make_probe_stub_dir() {
    local tmpdir
    tmpdir="$(mktemp -d)"

    local skip_tools=()
    local preflight_mode="ready"

    for spec in "$@"; do
        case "$spec" in
            missing:*)          skip_tools+=("${spec#missing:}") ;;
            preflight_ready)    preflight_mode="ready" ;;
            preflight_not_ready) preflight_mode="not_ready" ;;
        esac
    done

    # All tools that probes may need — create stubs for all by default
    local all_tools=(podman runsc srt claude gh git go bwrap)

    for tool in "${all_tools[@]}"; do
        local skip=0
        for s in "${skip_tools[@]}"; do
            [ "$s" = "$tool" ] && skip=1 && break
        done
        [ "$skip" -eq 1 ] && continue

        case "$tool" in
            podman)
                cat > "$tmpdir/podman" <<'PODMAN_STUB'
#!/bin/sh
if [ "$1" = "info" ]; then
    echo "true"
    exit 0
fi
exit 0
PODMAN_STUB
                ;;
            git)
                cat > "$tmpdir/git" <<'GIT_STUB'
#!/bin/sh
if [ "$1" = "remote" ] && [ "$2" = "-v" ]; then
    echo "origin	git@github.com:example/repo.git (fetch)"
    exit 0
fi
exit 0
GIT_STUB
                ;;
            go)
                cat > "$tmpdir/go" <<'GO_STUB'
#!/bin/sh
# Stub go test runner — exit 0 always
exit 0
GO_STUB
                ;;
            *)
                printf '#!/bin/sh\nexit 0\n' > "$tmpdir/$tool"
                ;;
        esac
        chmod +x "$tmpdir/$tool"
    done

    # Create a stub scripts/ subdir to hold l6-preflight.sh
    mkdir -p "$tmpdir/scripts"

    case "$preflight_mode" in
        ready)
            cat > "$tmpdir/scripts/l6-preflight.sh" <<'PREFLIGHT_READY'
#!/bin/sh
echo "READY"
exit 0
PREFLIGHT_READY
            ;;
        not_ready)
            cat > "$tmpdir/scripts/l6-preflight.sh" <<'PREFLIGHT_NOT_READY'
#!/bin/sh
echo "MISSING podman (binary) — install rootless Podman"
echo "NOT READY"
exit 1
PREFLIGHT_NOT_READY
            ;;
    esac
    chmod +x "$tmpdir/scripts/l6-preflight.sh"

    printf '%s' "$tmpdir"
}

# ─── TC-044-01: dry-run emits all 9 rows in the exact closing order ──────────

run_tc044_01() {
    local tc="TC-044-01"
    local tmpdir evidence_file
    tmpdir="$(make_probe_stub_dir preflight_ready)"
    evidence_file="$(mktemp)"

    # Expected closing order: 014 015 016 021 030 022 028 033 034 032
    local expected_order=(014 015 016 021 030 022 028 033 034 032)

    local output exit_code
    output="$(L6_PROBE_PATH="$tmpdir" L6_EVIDENCE_FILE="$evidence_file" bash "$PROBE" --dry-run 2>&1)" && exit_code=$? || exit_code=$?

    rm -rf "$tmpdir"
    rm -f "$evidence_file"

    local ok=1

    # Exit code must be 0
    if [ "$exit_code" -ne 0 ]; then
        tc_fail "$tc" "expected exit 0, got $exit_code; output:\n$output"
        ok=0
    fi

    # Each task ID must appear in output
    for id in "${expected_order[@]}"; do
        if ! printf '%s' "$output" | grep -q "$id"; then
            tc_fail "$tc" "expected task $id in output; got:\n$output"
            ok=0
        fi
    done

    # Assert position: extract task IDs in the order they appear in the output
    # Each row starts with the task ID (e.g. "014")
    local row_order
    row_order="$(printf '%s' "$output" | grep -oE 'PROBE +[0-9]+|ROW +[0-9]+|^ *[0-9]{3}[[:space:]]' | grep -oE '[0-9]{3}' || true)"

    # Alternative: look for rows that contain task IDs in a structured format
    # The script prints rows like: "014  ... DRY-RUN" or similar
    # Extract lines containing 3-digit task IDs (014, 015, etc.) in order
    local task_ids_in_order
    task_ids_in_order="$(printf '%s' "$output" | grep -oE '\b(014|015|016|021|022|028|030|032|033|034)\b' | head -20 || true)"

    if [ -z "$task_ids_in_order" ]; then
        tc_fail "$tc" "could not extract any task IDs from output:\n$output"
        ok=0
    else
        # Build a string of just the IDs in order
        local ids_string
        ids_string="$(printf '%s\n' "$task_ids_in_order" | head -9 | tr '\n' ' ' | sed 's/ $//')"

        # Check that the sequence is correct
        local expected_string="014 015 016 021 030 022 028 033 034 032"
        # Extract first 10 IDs (may repeat, but we need first occurrence of each in order)
        local extracted
        extracted="$(printf '%s' "$task_ids_in_order" | awk '
            /^014$/ && !seen[14]++ { printf "014 " }
            /^015$/ && !seen[15]++ { printf "015 " }
            /^016$/ && !seen[16]++ { printf "016 " }
            /^021$/ && !seen[21]++ { printf "021 " }
            /^030$/ && !seen[30]++ { printf "030 " }
            /^022$/ && !seen[22]++ { printf "022 " }
            /^028$/ && !seen[28]++ { printf "028 " }
            /^033$/ && !seen[33]++ { printf "033 " }
            /^034$/ && !seen[34]++ { printf "034 " }
            /^032$/ && !seen[32]++ { printf "032 " }
        ' | sed 's/ $//')"

        if [ "$extracted" != "$expected_string" ]; then
            tc_fail "$tc" "wrong order: expected '$expected_string', got '$extracted'; output:\n$output"
            ok=0
        fi
    fi

    # Row count validation is done in TC-044-03 via the evidence file (10 rows expected)

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-044-02: SKIP when prerequisite is absent ─────────────────────────────

run_tc044_02() {
    local tc="TC-044-02"
    local ok=1

    # Part A: runsc absent → task 016 SKIP; others unaffected
    local tmpdir_a ev_a
    tmpdir_a="$(make_probe_stub_dir missing:runsc preflight_ready)"
    ev_a="$(mktemp)"

    local output_a exit_a
    output_a="$(L6_PROBE_PATH="$tmpdir_a" L6_EVIDENCE_FILE="$ev_a" bash "$PROBE" --dry-run 2>&1)" && exit_a=$? || exit_a=$?

    rm -rf "$tmpdir_a" "$ev_a"

    # Exit code must be 0 (SKIP is not FAIL)
    if [ "$exit_a" -ne 0 ]; then
        tc_fail "${tc}a" "expected exit 0 when runsc absent; got $exit_a; output:\n$output_a"
        ok=0
    fi

    # Task 016 must be SKIP — match the leading bracket format [016]
    if ! printf '%s' "$output_a" | grep -q "\[016\]"; then
        tc_fail "${tc}a" "task 016 not found in output; got:\n$output_a"
        ok=0
    fi
    if ! printf '%s' "$output_a" | grep "\[016\]" | grep -qi "SKIP"; then
        tc_fail "${tc}a" "task 016 should be SKIP when runsc absent; got:\n$(printf '%s' "$output_a" | grep "\[016\]")"
        ok=0
    fi

    # Tasks that don't require runsc should NOT be SKIP (e.g. 014 should be DRY-RUN)
    if printf '%s' "$output_a" | grep "\[014\]" | grep -qi "SKIP"; then
        tc_fail "${tc}a" "task 014 should NOT be SKIP when only runsc is absent; got:\n$(printf '%s' "$output_a" | grep "\[014\]")"
        ok=0
    fi

    # 032 (capstone) also requires runsc transitively — should be SKIP
    if ! printf '%s' "$output_a" | grep "\[032\]" | grep -qi "SKIP"; then
        tc_fail "${tc}a" "task 032 (capstone) should be SKIP when runsc absent; got:\n$(printf '%s' "$output_a" | grep "\[032\]")"
        ok=0
    fi

    # Part B: srt absent → task 021 SKIP; others unaffected
    local tmpdir_b ev_b
    tmpdir_b="$(make_probe_stub_dir missing:srt preflight_ready)"
    ev_b="$(mktemp)"

    local output_b exit_b
    output_b="$(L6_PROBE_PATH="$tmpdir_b" L6_EVIDENCE_FILE="$ev_b" bash "$PROBE" --dry-run 2>&1)" && exit_b=$? || exit_b=$?

    rm -rf "$tmpdir_b" "$ev_b"

    if [ "$exit_b" -ne 0 ]; then
        tc_fail "${tc}b" "expected exit 0 when srt absent; got $exit_b; output:\n$output_b"
        ok=0
    fi

    if ! printf '%s' "$output_b" | grep "\[021\]" | grep -qi "SKIP"; then
        tc_fail "${tc}b" "task 021 should be SKIP when srt absent; got:\n$(printf '%s' "$output_b" | grep "\[021\]")"
        ok=0
    fi

    # 030 depends on 014/015/016/021 all having RUN — if 021 is SKIP, 030 must also be SKIP
    if ! printf '%s' "$output_b" | grep "\[030\]" | grep -qi "SKIP"; then
        tc_fail "${tc}b" "task 030 should be SKIP when 021 is skipped; got:\n$(printf '%s' "$output_b" | grep "\[030\]")"
        ok=0
    fi

    # 014 should still proceed (not SKIP)
    if printf '%s' "$output_b" | grep "\[014\]" | grep -qi "SKIP"; then
        tc_fail "${tc}b" "task 014 should NOT be SKIP when only srt is absent; got:\n$(printf '%s' "$output_b" | grep "\[014\]")"
        ok=0
    fi

    # Part C: gh absent → task 034 SKIP
    local tmpdir_c ev_c
    tmpdir_c="$(make_probe_stub_dir missing:gh preflight_ready)"
    ev_c="$(mktemp)"

    local output_c exit_c
    output_c="$(L6_PROBE_PATH="$tmpdir_c" L6_EVIDENCE_FILE="$ev_c" bash "$PROBE" --dry-run 2>&1)" && exit_c=$? || exit_c=$?

    rm -rf "$tmpdir_c" "$ev_c"

    if [ "$exit_c" -ne 0 ]; then
        tc_fail "${tc}c" "expected exit 0 when gh absent; got $exit_c; output:\n$output_c"
        ok=0
    fi

    if ! printf '%s' "$output_c" | grep "\[034\]" | grep -qi "SKIP"; then
        tc_fail "${tc}c" "task 034 should be SKIP when gh absent; got:\n$(printf '%s' "$output_c" | grep "\[034\]")"
        ok=0
    fi

    # 014/015 should proceed (not SKIP)
    if printf '%s' "$output_c" | grep "\[014\]" | grep -qi "SKIP"; then
        tc_fail "${tc}c" "task 014 should NOT be SKIP when only gh is absent; got:\n$(printf '%s' "$output_c" | grep "\[014\]")"
        ok=0
    fi

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-044-03: evidence file has 9 rows with correct structure ───────────────

run_tc044_03() {
    local tc="TC-044-03"
    local tmpdir
    tmpdir="$(make_probe_stub_dir preflight_ready)"

    # Use a dedicated evidence file path via env var so we know where to find it
    local evidence_file
    evidence_file="$(mktemp)"

    local output exit_code
    # Provide AGENT_BUILDER_PUBLISH_REMOTE so probes 034/032 are not SKIP-due-to-unset-remote;
    # this keeps the "all 10 rows are DRY-RUN" invariant that the test checks.
    output="$(L6_PROBE_PATH="$tmpdir" L6_EVIDENCE_FILE="$evidence_file" \
        AGENT_BUILDER_PUBLISH_REMOTE="git@github.com:example/repo.git" \
        bash "$PROBE" --dry-run 2>&1)" \
        && exit_code=$? || exit_code=$?

    rm -rf "$tmpdir"

    local ok=1

    if [ "$exit_code" -ne 0 ]; then
        tc_fail "$tc" "expected exit 0; got $exit_code; output:\n$output"
        ok=0
    fi

    # Evidence file must exist
    if [ ! -f "$evidence_file" ]; then
        tc_fail "$tc" "evidence file not found at $evidence_file"
        rm -f "$evidence_file"
        [ "$ok" -eq 1 ] && tc_pass "$tc" || return 0
        return 0
    fi

    local evidence
    evidence="$(cat "$evidence_file")"
    rm -f "$evidence_file"

    # Must have exactly 10 task rows (lines starting with TASK-):
    # 014, 015, 016, 021, 030, 022, 028, 033, 034, 032
    local row_count
    row_count="$(printf '%s\n' "$evidence" | grep -c '^TASK-' 2>/dev/null || echo 0)"

    if [ "$row_count" -ne 10 ]; then
        tc_fail "$tc" "expected 10 rows in evidence file (TASK- lines), got $row_count; evidence:\n$evidence"
        ok=0
    fi

    # Each task ID must appear exactly once (as TASK-NNN in evidence file)
    # 10 tasks: 014, 015, 016, 021, 030 (ledger), 022, 028, 033, 034, 032
    local task_ids=(014 015 016 021 030 022 028 033 034 032)
    for id in "${task_ids[@]}"; do
        if ! printf '%s\n' "$evidence" | grep -q "TASK-${id}"; then
            tc_fail "$tc" "task $id missing from evidence file (expected TASK-${id}); evidence:\n$evidence"
            ok=0
        fi
    done

    # Each TASK- row must have a status field (PASS, SKIP, FAIL, or DRY-RUN)
    local status_count
    status_count="$(printf '%s\n' "$evidence" | grep '^TASK-' | grep -cE '\| ?(PASS|SKIP|FAIL|DRY-RUN)' 2>/dev/null || echo 0)"
    if [ "$status_count" -ne 10 ]; then
        tc_fail "$tc" "expected all 10 task rows to have a status; status_count=$status_count; evidence:\n$evidence"
        ok=0
    fi

    # In dry-run mode with all prerequisites present (including AGENT_BUILDER_PUBLISH_REMOTE),
    # each TASK- row must have the dry-run placeholder (SKIP rows appear only when a
    # prerequisite tool or required env var is absent — that scenario is tested in TC-044-03b
    # and TC-046-02).
    local dryrun_count
    dryrun_count="$(printf '%s\n' "$evidence" | grep '^TASK-' | grep -c "dry-run" 2>/dev/null || echo 0)"
    if [ "$dryrun_count" -ne 10 ]; then
        tc_fail "$tc" "expected all 10 task rows to have dry-run placeholder; got $dryrun_count; evidence:\n$evidence"
        ok=0
    fi

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-044-03b: evidence file SKIP rows include skip reason ──────────────────

run_tc044_03b() {
    local tc="TC-044-03b"
    local tmpdir
    # runsc absent → 016 SKIP; srt absent → 021 SKIP
    tmpdir="$(make_probe_stub_dir missing:runsc missing:srt preflight_ready)"

    local evidence_file
    evidence_file="$(mktemp)"

    local output exit_code
    output="$(L6_PROBE_PATH="$tmpdir" L6_EVIDENCE_FILE="$evidence_file" bash "$PROBE" --dry-run 2>&1)" \
        && exit_code=$? || exit_code=$?

    rm -rf "$tmpdir"

    local ok=1

    if [ "$exit_code" -ne 0 ]; then
        tc_fail "$tc" "expected exit 0; got $exit_code; output:\n$output"
        ok=0
    fi

    if [ ! -f "$evidence_file" ]; then
        tc_fail "$tc" "evidence file not found"
        [ "$ok" -eq 1 ] && tc_pass "$tc" || return 0
        return 0
    fi

    local evidence
    evidence="$(cat "$evidence_file")"
    rm -f "$evidence_file"

    # Must still have 10 task rows (SKIP rows do NOT disappear)
    local row_count
    row_count="$(printf '%s\n' "$evidence" | grep -c '^TASK-' 2>/dev/null || echo 0)"
    if [ "$row_count" -ne 10 ]; then
        tc_fail "$tc" "expected 10 TASK- rows even with skips; got $row_count; evidence:\n$evidence"
        ok=0
    fi

    # Task 016 row must be SKIP and contain a reason (TASK-016 in evidence file)
    local row_016
    row_016="$(printf '%s\n' "$evidence" | grep "TASK-016" || true)"
    if ! printf '%s' "$row_016" | grep -qi "SKIP"; then
        tc_fail "$tc" "task 016 row should be SKIP; got: $row_016"
        ok=0
    fi
    if ! printf '%s' "$row_016" | grep -qi "runsc"; then
        tc_fail "$tc" "task 016 SKIP row should mention 'runsc'; got: $row_016"
        ok=0
    fi

    # Task 021 row must be SKIP and contain a reason (TASK-021 in evidence file)
    local row_021
    row_021="$(printf '%s\n' "$evidence" | grep "TASK-021" || true)"
    if ! printf '%s' "$row_021" | grep -qi "SKIP"; then
        tc_fail "$tc" "task 021 row should be SKIP; got: $row_021"
        ok=0
    fi
    if ! printf '%s' "$row_021" | grep -qi "srt"; then
        tc_fail "$tc" "task 021 SKIP row should mention 'srt'; got: $row_021"
        ok=0
    fi

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-044-04: preflight gate refuses to run when NOT READY ─────────────────

run_tc044_04() {
    local tc="TC-044-04"
    local tmpdir
    # preflight_not_ready simulates an environment where preflight exits 1
    tmpdir="$(make_probe_stub_dir preflight_not_ready)"

    local evidence_file
    evidence_file="$(mktemp)"

    local output exit_code
    output="$(L6_PROBE_PATH="$tmpdir" L6_EVIDENCE_FILE="$evidence_file" bash "$PROBE" 2>&1)" \
        && exit_code=$? || exit_code=$?

    rm -rf "$tmpdir"
    rm -f "$evidence_file"

    local ok=1

    # Must exit non-zero
    if [ "$exit_code" -eq 0 ]; then
        tc_fail "$tc" "expected non-zero exit when preflight NOT READY; got exit 0; output:\n$output"
        ok=0
    fi

    # Must print a message about preflight
    if ! printf '%s' "$output" | grep -qiE "preflight|l6-preflight|NOT READY"; then
        tc_fail "$tc" "expected message about preflight being NOT READY; got:\n$output"
        ok=0
    fi

    # Must reference make l6-preflight or the preflight script
    if ! printf '%s' "$output" | grep -qiE "make l6-preflight|l6-preflight\.sh"; then
        tc_fail "$tc" "expected message to reference 'make l6-preflight' or 'l6-preflight.sh'; got:\n$output"
        ok=0
    fi

    # No probe commands should have been invoked (no probe output)
    # If probes were invoked, we'd see task IDs with probe results
    # In dry-run the output is different; this is the NON-dry-run path
    if printf '%s' "$output" | grep -qE 'PROBE (014|015|016|021|022|028|030|032|033|034)'; then
        tc_fail "$tc" "probe commands were invoked despite NOT READY preflight; output:\n$output"
        ok=0
    fi

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-044-04b: dry-run bypasses the preflight gate ─────────────────────────

run_tc044_04b() {
    local tc="TC-044-04b"
    local tmpdir
    tmpdir="$(make_probe_stub_dir preflight_not_ready)"

    local evidence_file
    evidence_file="$(mktemp)"

    local output exit_code
    output="$(L6_PROBE_PATH="$tmpdir" L6_EVIDENCE_FILE="$evidence_file" bash "$PROBE" --dry-run 2>&1)" \
        && exit_code=$? || exit_code=$?

    rm -rf "$tmpdir"
    rm -f "$evidence_file"

    local ok=1

    # --dry-run may bypass the preflight gate — exit 0 is acceptable
    if [ "$exit_code" -ne 0 ]; then
        tc_fail "$tc" "--dry-run should bypass preflight gate (exit 0); got $exit_code; output:\n$output"
        ok=0
    fi

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-044-05: make l6-probe target exists, is in .PHONY, not in check ───────

run_tc044_05() {
    local tc="TC-044-05"
    local ok=1

    # Dry-run must show the script invocation
    local dryrun_output
    dryrun_output="$(make --dry-run -C "$REPO_ROOT" l6-probe 2>&1)" || true
    if ! printf '%s' "$dryrun_output" | grep -qE "l6-probe\.sh"; then
        tc_fail "$tc" "make --dry-run l6-probe did not show l6-probe.sh invocation; got:\n$dryrun_output"
        ok=0
    fi

    # l6-probe must be in .PHONY
    local makefile_content
    makefile_content="$(cat "$REPO_ROOT/Makefile")"
    if ! printf '%s' "$makefile_content" | grep -qE '\.PHONY.*l6-probe|l6-probe.*\.PHONY'; then
        tc_fail "$tc" "l6-probe is not in .PHONY in Makefile"
        ok=0
    fi

    # l6-probe must NOT be a prerequisite of check or fitness
    local check_prereqs fitness_prereqs
    check_prereqs="$(printf '%s' "$makefile_content" | grep -E '^check:' | head -1)"
    fitness_prereqs="$(printf '%s' "$makefile_content" | grep -E '^fitness:' | head -1)"

    if printf '%s' "$check_prereqs" | grep -q "l6-probe"; then
        tc_fail "$tc" "l6-probe appears as a prerequisite of check: — it must not be"
        ok=0
    fi
    if printf '%s' "$fitness_prereqs" | grep -q "l6-probe"; then
        tc_fail "$tc" "l6-probe appears as a prerequisite of fitness: — it must not be"
        ok=0
    fi

    # Also confirm l6-preflight is not in check or fitness (belt-and-suspenders from TC-043-05)
    if printf '%s' "$check_prereqs" | grep -q "l6-preflight"; then
        tc_fail "$tc" "l6-preflight appears as a prerequisite of check: — it must not be"
        ok=0
    fi
    if printf '%s' "$fitness_prereqs" | grep -q "l6-preflight"; then
        tc_fail "$tc" "l6-preflight appears as a prerequisite of fitness: — it must not be"
        ok=0
    fi

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-046-01: resolved gate-tools argument is non-empty, honors EXEC_BOX_GATE_TOOLS ──

run_tc046_01() {
    local tc="TC-046-01"
    local ok=1

    # Part A: unset EXEC_BOX_GATE_TOOLS — default should resolve to repo's gate-tools path
    local tmpdir_a ev_a
    tmpdir_a="$(make_probe_stub_dir preflight_ready)"
    ev_a="$(mktemp)"

    local output_a exit_a
    output_a="$(L6_PROBE_PATH="$tmpdir_a" L6_EVIDENCE_FILE="$ev_a" \
        env -u EXEC_BOX_GATE_TOOLS bash "$PROBE" --dry-run 2>&1)" && exit_a=$? || exit_a=$?

    rm -rf "$tmpdir_a"
    rm -f "$ev_a"

    if [ "$exit_a" -ne 0 ]; then
        tc_fail "${tc}a" "expected exit 0; got $exit_a; output:\n$output_a"
        ok=0
    fi

    # Must NOT contain --gate-tools "" (empty string arg)
    if printf '%s' "$output_a" | grep -qF -- '--gate-tools ""'; then
        tc_fail "${tc}a" "output contains '--gate-tools \"\"' (empty gate-tools); output:\n$output_a"
        ok=0
    fi

    # Part B: set EXEC_BOX_GATE_TOOLS to a known temp path — that value must appear in argv
    local tmpdir_b ev_b test_gate_dir
    tmpdir_b="$(make_probe_stub_dir preflight_ready)"
    ev_b="$(mktemp)"
    test_gate_dir="/tmp/test-gate-tools-$$"

    local output_b exit_b
    output_b="$(L6_PROBE_PATH="$tmpdir_b" L6_EVIDENCE_FILE="$ev_b" \
        EXEC_BOX_GATE_TOOLS="$test_gate_dir" bash "$PROBE" --dry-run 2>&1)" && exit_b=$? || exit_b=$?

    local ev_content_b
    ev_content_b="$(cat "$ev_b" 2>/dev/null || true)"

    rm -rf "$tmpdir_b"
    rm -f "$ev_b"

    if [ "$exit_b" -ne 0 ]; then
        tc_fail "${tc}b" "expected exit 0 with EXEC_BOX_GATE_TOOLS set; got $exit_b; output:\n$output_b"
        ok=0
    fi

    # The resolved test_gate_dir must appear in the dry-run script state (GATE_TOOLS_DIR)
    # We verify by running with a custom EXEC_BOX_GATE_TOOLS and checking the script
    # accepts it without error. Additionally, verify no empty --gate-tools
    if printf '%s' "$output_b" | grep -qF -- '--gate-tools ""'; then
        tc_fail "${tc}b" "output contains '--gate-tools \"\"' even with EXEC_BOX_GATE_TOOLS set; output:\n$output_b"
        ok=0
    fi

    # Verify that the custom gate-tools value is wired by running with a bash debug trace
    # to see the actual GATE_TOOLS_DIR expansion. Use a subshell with 'set -x' output capture.
    local trace_dir ev_trace
    trace_dir="$(make_probe_stub_dir preflight_ready)"
    ev_trace="$(mktemp)"

    local trace_out
    trace_out="$(L6_PROBE_PATH="$trace_dir" L6_EVIDENCE_FILE="$ev_trace" \
        EXEC_BOX_GATE_TOOLS="$test_gate_dir" bash -x "$PROBE" --dry-run 2>&1)" || true

    rm -rf "$trace_dir"
    rm -f "$ev_trace"

    # The bash -x trace will show the expanded value of GATE_TOOLS_DIR
    if ! printf '%s' "$trace_out" | grep -qF "$test_gate_dir"; then
        tc_fail "${tc}b" "EXEC_BOX_GATE_TOOLS value '$test_gate_dir' not found in script trace; check GATE_TOOLS_DIR expansion; trace:\n$(printf '%s' "$trace_out" | grep -E 'GATE_TOOLS|gate.tools' | head -20)"
        ok=0
    fi

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-046-02: AGENT_BUILDER_PUBLISH_REMOTE threading and SKIP-when-unset ────

run_tc046_02() {
    local tc="TC-046-02"
    local ok=1

    # Part A: AGENT_BUILDER_PUBLISH_REMOTE set — must appear in argv for 034 and 032
    # (only verifiable via bash -x trace since dry-run doesn't exec the argv)
    local tmpdir_a ev_a test_remote
    tmpdir_a="$(make_probe_stub_dir preflight_ready)"
    ev_a="$(mktemp)"
    test_remote="git@github.com:example/repo.git"

    local trace_a
    trace_a="$(L6_PROBE_PATH="$tmpdir_a" L6_EVIDENCE_FILE="$ev_a" \
        AGENT_BUILDER_PUBLISH_REMOTE="$test_remote" bash -x "$PROBE" --dry-run 2>&1)" || true

    local ev_a_content
    ev_a_content="$(cat "$ev_a" 2>/dev/null || true)"

    rm -rf "$tmpdir_a"
    rm -f "$ev_a"

    # The trace must show the remote value in env setup for run_probe 034/032
    if ! printf '%s' "$trace_a" | grep -qF "$test_remote"; then
        tc_fail "${tc}a" "AGENT_BUILDER_PUBLISH_REMOTE value not found in script trace for 034/032; trace excerpt:\n$(printf '%s' "$trace_a" | grep -E 'PUBLISH_REMOTE|034|032' | head -20)"
        ok=0
    fi

    # 034 must NOT be SKIP due to AGENT_BUILDER_PUBLISH_REMOTE (it is set)
    if printf '%s' "$ev_a_content" | grep "TASK-034" | grep -q "AGENT_BUILDER_PUBLISH_REMOTE unset"; then
        tc_fail "${tc}a" "probe 034 shows 'AGENT_BUILDER_PUBLISH_REMOTE unset' skip even though it is set; evidence:\n$ev_a_content"
        ok=0
    fi

    # Part B: AGENT_BUILDER_PUBLISH_REMOTE unset — 034 must be SKIP, exit 0
    local tmpdir_b ev_b
    tmpdir_b="$(make_probe_stub_dir preflight_ready)"
    ev_b="$(mktemp)"

    local output_b exit_b
    output_b="$(L6_PROBE_PATH="$tmpdir_b" L6_EVIDENCE_FILE="$ev_b" \
        env -u AGENT_BUILDER_PUBLISH_REMOTE bash "$PROBE" --dry-run 2>&1)" && exit_b=$? || exit_b=$?

    local ev_b_content
    ev_b_content="$(cat "$ev_b" 2>/dev/null || true)"

    rm -rf "$tmpdir_b"
    rm -f "$ev_b"

    if [ "$exit_b" -ne 0 ]; then
        tc_fail "${tc}b" "expected exit 0 when AGENT_BUILDER_PUBLISH_REMOTE unset; got $exit_b; output:\n$output_b"
        ok=0
    fi

    # 034 must show SKIP in stdout output
    if ! printf '%s' "$output_b" | grep "\[034\]" | grep -qi "SKIP"; then
        tc_fail "${tc}b" "probe 034 should be SKIP when AGENT_BUILDER_PUBLISH_REMOTE unset; stdout 034 line:\n$(printf '%s' "$output_b" | grep "\[034\]")"
        ok=0
    fi

    # Evidence file 034 row must contain AGENT_BUILDER_PUBLISH_REMOTE unset reason
    if ! printf '%s' "$ev_b_content" | grep "TASK-034" | grep -q "AGENT_BUILDER_PUBLISH_REMOTE unset"; then
        tc_fail "${tc}b" "evidence TASK-034 does not show 'AGENT_BUILDER_PUBLISH_REMOTE unset' skip reason; evidence:\n$(printf '%s' "$ev_b_content" | grep "TASK-034")"
        ok=0
    fi

    # SKIP reason must be distinct from 'no git remote configured'
    if printf '%s' "$ev_b_content" | grep "TASK-034" | grep -q "no git remote configured"; then
        tc_fail "${tc}b" "probe 034 skip reason shows 'no git remote configured' instead of 'AGENT_BUILDER_PUBLISH_REMOTE unset'; those are different conditions"
        ok=0
    fi

    # Exit 0 (SKIP is not FAIL) — already checked above
    # 032 must also be SKIP (AGENT_BUILDER_PUBLISH_REMOTE is a capstone prerequisite)
    if ! printf '%s' "$output_b" | grep "\[032\]" | grep -qi "SKIP"; then
        tc_fail "${tc}b" "probe 032 should be SKIP when AGENT_BUILDER_PUBLISH_REMOTE unset; 032 line:\n$(printf '%s' "$output_b" | grep "\[032\]")"
        ok=0
    fi

    if ! printf '%s' "$ev_b_content" | grep "TASK-032" | grep -q "AGENT_BUILDER_PUBLISH_REMOTE unset"; then
        tc_fail "${tc}b" "evidence TASK-032 does not mention 'AGENT_BUILDER_PUBLISH_REMOTE unset'; evidence:\n$(printf '%s' "$ev_b_content" | grep "TASK-032")"
        ok=0
    fi

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-046-03: no probe command contains AGENT_BUILDER_SANDBOX_RUNTIME=srt ───

run_tc046_03() {
    local tc="TC-046-03"
    local ok=1

    # Part A: scan stdout and evidence file for AGENT_BUILDER_SANDBOX_RUNTIME=srt
    local tmpdir ev
    tmpdir="$(make_probe_stub_dir preflight_ready)"
    ev="$(mktemp)"

    local output exit_code
    output="$(L6_PROBE_PATH="$tmpdir" L6_EVIDENCE_FILE="$ev" \
        AGENT_BUILDER_PUBLISH_REMOTE="git@github.com:example/repo.git" \
        bash "$PROBE" --dry-run 2>&1)" && exit_code=$? || exit_code=$?

    local ev_content
    ev_content="$(cat "$ev" 2>/dev/null || true)"

    rm -rf "$tmpdir"
    rm -f "$ev"

    if [ "$exit_code" -ne 0 ]; then
        tc_fail "${tc}a" "expected exit 0; got $exit_code; output:\n$output"
        ok=0
    fi

    # Neither stdout nor evidence file must contain AGENT_BUILDER_SANDBOX_RUNTIME=srt
    if printf '%s' "$output" | grep -qF 'AGENT_BUILDER_SANDBOX_RUNTIME=srt'; then
        tc_fail "${tc}a" "stdout contains 'AGENT_BUILDER_SANDBOX_RUNTIME=srt'; output:\n$output"
        ok=0
    fi

    if printf '%s' "$ev_content" | grep -qF 'AGENT_BUILDER_SANDBOX_RUNTIME=srt'; then
        tc_fail "${tc}a" "evidence file contains 'AGENT_BUILDER_SANDBOX_RUNTIME=srt'; evidence:\n$ev_content"
        ok=0
    fi

    # Part B: probe 028 NOT skipped when srt absent but claude present
    local tmpdir_b ev_b
    tmpdir_b="$(make_probe_stub_dir missing:srt preflight_ready)"
    ev_b="$(mktemp)"

    local output_b exit_b
    output_b="$(L6_PROBE_PATH="$tmpdir_b" L6_EVIDENCE_FILE="$ev_b" \
        AGENT_BUILDER_PUBLISH_REMOTE="git@github.com:example/repo.git" \
        bash "$PROBE" --dry-run 2>&1)" && exit_b=$? || exit_b=$?

    rm -rf "$tmpdir_b"
    rm -f "$ev_b"

    if [ "$exit_b" -ne 0 ]; then
        tc_fail "${tc}b" "expected exit 0 when srt absent; got $exit_b; output:\n$output_b"
        ok=0
    fi

    # 028 must NOT be SKIP when srt is absent (srt is no longer a prerequisite for 028)
    if printf '%s' "$output_b" | grep "\[028\]" | grep -qi "SKIP"; then
        tc_fail "${tc}b" "probe 028 should NOT be SKIP when only srt is absent (srt gating on 028 was stale); got:\n$(printf '%s' "$output_b" | grep "\[028\]")"
        ok=0
    fi

    # Part C: probe 021 must still be SKIP when srt absent (its gate is legitimate)
    if ! printf '%s' "$output_b" | grep "\[021\]" | grep -qi "SKIP"; then
        tc_fail "${tc}c" "probe 021 should still be SKIP when srt absent; got:\n$(printf '%s' "$output_b" | grep "\[021\]")"
        ok=0
    fi

    if ! printf '%s' "$output_b" | grep "\[021\]" | grep -qi "srt"; then
        tc_fail "${tc}c" "probe 021 SKIP reason should mention 'srt'; got:\n$(printf '%s' "$output_b" | grep "\[021\]")"
        ok=0
    fi

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-046-04: regression guard — TC-044-01..05 still green ─────────────────
#
# TC-044-01 through TC-044-05 are already called directly in main below.
# This function is a named marker so the results summary attributes the
# regression guard to TC-046-04. The actual assertions run inside the
# run_tc044_0x functions; any failure there also counts against this TC.

run_tc046_04_marker() {
    # No separate test body — TC-044 functions are the regression guard.
    # This is a placeholder so TC-046-04 appears explicitly in the report.
    :
}

# ─── main ─────────────────────────────────────────────────────────────────────

printf '\n=== l6-probe test harness ===\n\n'

# Confirm the script under test exists
if [ ! -f "$PROBE" ]; then
    printf 'ERROR: %s not found — build the script first\n' "$PROBE" >&2
    exit 1
fi

run_tc044_01
run_tc044_02
run_tc044_03
run_tc044_03b
run_tc044_04
run_tc044_04b
run_tc044_05
run_tc046_01
run_tc046_02
run_tc046_03

printf '\n=== Results: %d passed, %d failed ===\n' "$PASS_COUNT" "$FAIL_COUNT"

if [ "$FAIL_COUNT" -gt 0 ]; then
    printf '\nFailed test cases:\n'
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f"
    done
    exit 1
fi

exit 0
