#!/usr/bin/env bash
# Test harness for scripts/l6-preflight.sh
# Covers TC-043-01 through TC-043-04 (plus TC-043-05 via make dry-run).
# Uses stub binaries on a temp PATH — no live host tooling required.
#
# Usage: bash scripts/tests/l6-preflight-test.sh
# Exit 0 on all pass; non-zero on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
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

# make_stub_dir creates a temp dir with stub binaries and returns the path.
# Arguments after the tmpdir are tool specs:
#   "present:toolname"   — create a stub that exits 0, prints nothing
#   "missing:toolname"   — do NOT create a stub (simulates absent tool)
#   "podman_rootless_ok" — create a podman stub where `podman info` prints "true"
#   "podman_rootless_fail" — podman stub where `podman info` prints "false"
#   "podman_rootless_error" — podman stub where `podman info` exits non-zero
#   "srt_snap"           — srt exits non-zero with snap-confine error string
#   "srt_generic_fail"   — srt exits non-zero with a different error string
#   "git_remote_ok"      — git remote -v prints a non-empty remote listing
#   "git_remote_empty"   — git remote -v prints nothing (no remote configured)
#   "git_missing"        — git is not present at all
#   "make_ok"            — make exits 0 with expected success output

make_stub_dir() {
    local tmpdir
    tmpdir="$(mktemp -d)"

    # Stubs for tools we always control in tests (podman / git / make handled by specs below)
    local present_tools=()
    local skip_tools=()
    local podman_mode="rootless_ok"
    local srt_mode="ok"
    local git_mode="remote_ok"
    local make_mode="ok"

    for spec in "$@"; do
        case "$spec" in
            present:*)      present_tools+=("${spec#present:}") ;;
            missing:*)      skip_tools+=("${spec#missing:}") ;;
            podman_rootless_ok)     podman_mode="rootless_ok" ;;
            podman_rootless_fail)   podman_mode="rootless_fail" ;;
            podman_rootless_error)  podman_mode="rootless_error" ;;
            srt_snap)               srt_mode="snap" ;;
            srt_generic_fail)       srt_mode="generic_fail" ;;
            git_remote_ok)          git_mode="remote_ok" ;;
            git_remote_empty)       git_mode="remote_empty" ;;
            git_missing)            git_mode="missing" ;;
            make_ok)                make_mode="ok" ;;
        esac
    done

    # Write each present tool as a no-op stub
    for tool in "${present_tools[@]}"; do
        # skip if this tool is in the skip list
        local skip=0
        for s in "${skip_tools[@]}"; do
            [ "$s" = "$tool" ] && skip=1 && break
        done
        [ "$skip" -eq 1 ] && continue
        printf '#!/bin/sh\nexit 0\n' > "$tmpdir/$tool"
        chmod +x "$tmpdir/$tool"
    done

    # Podman stub
    local is_skipped_podman=0
    for s in "${skip_tools[@]}"; do [ "$s" = "podman" ] && is_skipped_podman=1 && break; done
    if [ "$is_skipped_podman" -eq 0 ]; then
        case "$podman_mode" in
            rootless_ok)
                cat > "$tmpdir/podman" <<'PODMAN_OK'
#!/bin/sh
if [ "$1" = "info" ]; then
    echo "true"
    exit 0
fi
exit 0
PODMAN_OK
                ;;
            rootless_fail)
                cat > "$tmpdir/podman" <<'PODMAN_FAIL'
#!/bin/sh
if [ "$1" = "info" ]; then
    echo "false"
    exit 0
fi
exit 0
PODMAN_FAIL
                ;;
            rootless_error)
                cat > "$tmpdir/podman" <<'PODMAN_ERR'
#!/bin/sh
if [ "$1" = "info" ]; then
    echo "cannot connect to Podman socket" >&2
    exit 1
fi
exit 0
PODMAN_ERR
                ;;
        esac
        chmod +x "$tmpdir/podman"
    fi

    # srt stub
    local is_skipped_srt=0
    for s in "${skip_tools[@]}"; do [ "$s" = "srt" ] && is_skipped_srt=1 && break; done
    if [ "$is_skipped_srt" -eq 0 ]; then
        case "$srt_mode" in
            ok)
                printf '#!/bin/sh\nexit 0\n' > "$tmpdir/srt"
                ;;
            snap)
                cat > "$tmpdir/srt" <<'SRT_SNAP'
#!/bin/sh
echo "snap-confine has elevated permissions and is not confined"
exit 1
SRT_SNAP
                ;;
            generic_fail)
                cat > "$tmpdir/srt" <<'SRT_GENERIC'
#!/bin/sh
echo "some other error occurred"
exit 1
SRT_GENERIC
                ;;
        esac
        chmod +x "$tmpdir/srt"
    fi

    # git stub
    if [ "$git_mode" != "missing" ]; then
        case "$git_mode" in
            remote_ok)
                cat > "$tmpdir/git" <<'GIT_REMOTE_OK'
#!/bin/sh
if [ "$1" = "remote" ] && [ "$2" = "-v" ]; then
    echo "origin	git@github.com:example/repo.git (fetch)"
    echo "origin	git@github.com:example/repo.git (push)"
    exit 0
fi
exit 0
GIT_REMOTE_OK
                ;;
            remote_empty)
                cat > "$tmpdir/git" <<'GIT_REMOTE_EMPTY'
#!/bin/sh
if [ "$1" = "remote" ] && [ "$2" = "-v" ]; then
    exit 0
fi
exit 0
GIT_REMOTE_EMPTY
                ;;
        esac
        chmod +x "$tmpdir/git"
    fi

    # make stub (for make check / make fitness)
    local is_skipped_make=0
    for s in "${skip_tools[@]}"; do [ "$s" = "make" ] && is_skipped_make=1 && break; done
    if [ "$is_skipped_make" -eq 0 ]; then
        case "$make_mode" in
            ok)
                cat > "$tmpdir/make" <<'MAKE_OK'
#!/bin/sh
# Stub make that exits 0 for any target
# Emit the expected success lines for check and fitness
case "$1" in
    check)   echo "All checks passed." ;;
    fitness) echo "Fitness checks passed." ;;
    *)       echo "make stub: target=$1" ;;
esac
exit 0
MAKE_OK
                ;;
        esac
        chmod +x "$tmpdir/make"
    fi

    printf '%s' "$tmpdir"
}

# ─── TC-043-01: all prerequisites present — READY, exit 0 ────────────────────

run_tc043_01() {
    local tc="TC-043-01"
    local tmpdir
    tmpdir="$(make_stub_dir \
        present:runsc \
        present:bwrap \
        present:claude \
        present:gh \
        podman_rootless_ok \
        git_remote_ok \
        make_ok)"

    local output exit_code
    output="$(L6_PREFLIGHT_PATH="$tmpdir" bash "$PREFLIGHT" 2>&1)" && exit_code=$? || exit_code=$?

    rm -rf "$tmpdir"

    local ok=1

    # All prerequisite rows should show PASS
    for tool in podman runsc bwrap srt claude gh; do
        if ! printf '%s' "$output" | grep -qE "PASS.*$tool|$tool.*PASS"; then
            tc_fail "$tc" "expected PASS for $tool; got:\n$output"
            ok=0
        fi
    done

    # git remote row should PASS
    if ! printf '%s' "$output" | grep -qiE "PASS.*(git.remote|remote)|(git.remote|remote).*PASS"; then
        tc_fail "$tc" "expected PASS for git-remote check; got:\n$output"
        ok=0
    fi

    # podman rootless row should PASS (covered by podman PASS above or a separate row)

    # Final line must be READY
    if ! printf '%s' "$output" | grep -qE "^READY$"; then
        tc_fail "$tc" "expected final READY line; got:\n$output"
        ok=0
    fi

    # Exit code must be 0
    if [ "$exit_code" -ne 0 ]; then
        tc_fail "$tc" "expected exit 0, got $exit_code; output:\n$output"
        ok=0
    fi

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-043-02: single missing tool — MISSING row, NOT READY, exit non-zero ──

run_tc043_02() {
    local tc="TC-043-02"
    local tmpdir
    # runsc is absent; all others present
    tmpdir="$(make_stub_dir \
        present:bwrap \
        present:claude \
        present:gh \
        missing:runsc \
        podman_rootless_ok \
        git_remote_ok \
        make_ok)"

    local output exit_code
    output="$(L6_PREFLIGHT_PATH="$tmpdir" bash "$PREFLIGHT" 2>&1)" && exit_code=$? || exit_code=$?

    rm -rf "$tmpdir"

    local ok=1

    # runsc row must be MISSING
    if ! printf '%s' "$output" | grep -qiE "MISSING.*runsc|runsc.*MISSING"; then
        tc_fail "$tc" "expected MISSING for runsc; got:\n$output"
        ok=0
    fi

    # Final line must be NOT READY
    if ! printf '%s' "$output" | grep -qE "NOT READY"; then
        tc_fail "$tc" "expected NOT READY verdict; got:\n$output"
        ok=0
    fi

    # Exit code must be non-zero
    if [ "$exit_code" -eq 0 ]; then
        tc_fail "$tc" "expected non-zero exit, got 0; output:\n$output"
        ok=0
    fi

    # Other tools should still PASS (verify bwrap and claude at least)
    for tool in bwrap claude gh; do
        if ! printf '%s' "$output" | grep -qE "PASS.*$tool|$tool.*PASS"; then
            tc_fail "$tc" "expected PASS for $tool (unaffected by missing runsc); got:\n$output"
            ok=0
        fi
    done

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-043-03: snap-confine srt — FAIL with snap-specific hint ───────────────

run_tc043_03() {
    local tc="TC-043-03"

    # Part A: snap-confine srt → FAIL with snap-specific hint
    local tmpdir_a
    tmpdir_a="$(make_stub_dir \
        present:runsc \
        present:bwrap \
        present:claude \
        present:gh \
        srt_snap \
        podman_rootless_ok \
        git_remote_ok \
        make_ok)"

    local output_a exit_code_a
    output_a="$(L6_PREFLIGHT_PATH="$tmpdir_a" bash "$PREFLIGHT" 2>&1)" && exit_code_a=$? || exit_code_a=$?
    rm -rf "$tmpdir_a"

    local ok=1

    # srt row must be FAIL (not MISSING — binary is present but unusable)
    if ! printf '%s' "$output_a" | grep -qiE "FAIL.*srt|srt.*FAIL"; then
        tc_fail "$tc" "Part A: expected FAIL for srt (snap-confine); got:\n$output_a"
        ok=0
    fi
    # Must NOT be MISSING (binary is present)
    if printf '%s' "$output_a" | grep -qiE "MISSING.*srt|srt.*MISSING"; then
        tc_fail "$tc" "Part A: srt row should be FAIL not MISSING; got:\n$output_a"
        ok=0
    fi

    # The hint must mention snap
    if ! printf '%s' "$output_a" | grep -qiE "(snap-confine|snap)"; then
        tc_fail "$tc" "Part A: expected snap-specific remediation hint; got:\n$output_a"
        ok=0
    fi

    # NOT READY and non-zero exit
    if ! printf '%s' "$output_a" | grep -qE "NOT READY"; then
        tc_fail "$tc" "Part A: expected NOT READY verdict; got:\n$output_a"
        ok=0
    fi
    if [ "$exit_code_a" -eq 0 ]; then
        tc_fail "$tc" "Part A: expected non-zero exit; got 0; output:\n$output_a"
        ok=0
    fi

    # Part B: generic-fail srt → FAIL but NOT snap-specific hint
    local tmpdir_b
    tmpdir_b="$(make_stub_dir \
        present:runsc \
        present:bwrap \
        present:claude \
        present:gh \
        srt_generic_fail \
        podman_rootless_ok \
        git_remote_ok \
        make_ok)"

    local output_b exit_code_b
    output_b="$(L6_PREFLIGHT_PATH="$tmpdir_b" bash "$PREFLIGHT" 2>&1)" && exit_code_b=$? || exit_code_b=$?
    rm -rf "$tmpdir_b"

    # srt row should still be FAIL, but the snap-confine hint should NOT appear
    if ! printf '%s' "$output_b" | grep -qiE "FAIL.*srt|srt.*FAIL"; then
        tc_fail "$tc" "Part B: expected FAIL for srt (generic); got:\n$output_b"
        ok=0
    fi
    # Snap-specific hint should NOT appear for generic errors
    # Note: we check for "snap-confine" specifically (the key phrase) not just "snap"
    if printf '%s' "$output_b" | grep -qi "snap-confine"; then
        tc_fail "$tc" "Part B: snap-confine hint should NOT appear for generic srt error; got:\n$output_b"
        ok=0
    fi

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-043-04: rootless Podman check fails ────────────────────────────────────

run_tc043_04() {
    local tc="TC-043-04"

    # Part A: podman info prints "false"
    local tmpdir_a
    tmpdir_a="$(make_stub_dir \
        present:runsc \
        present:bwrap \
        present:claude \
        present:gh \
        podman_rootless_fail \
        git_remote_ok \
        make_ok)"

    local output_a exit_code_a
    output_a="$(L6_PREFLIGHT_PATH="$tmpdir_a" bash "$PREFLIGHT" 2>&1)" && exit_code_a=$? || exit_code_a=$?
    rm -rf "$tmpdir_a"

    local ok=1

    # The podman rootless row must be FAIL
    if ! printf '%s' "$output_a" | grep -qiE "FAIL.*(podman|rootless)|(podman|rootless).*FAIL"; then
        tc_fail "$tc" "Part A: expected FAIL for rootless podman check; got:\n$output_a"
        ok=0
    fi

    # Hint must reference rootless or podman info
    if ! printf '%s' "$output_a" | grep -qiE "(rootless|podman info)"; then
        tc_fail "$tc" "Part A: expected rootless remediation hint; got:\n$output_a"
        ok=0
    fi

    # NOT READY and non-zero exit
    if ! printf '%s' "$output_a" | grep -qE "NOT READY"; then
        tc_fail "$tc" "Part A: expected NOT READY verdict; got:\n$output_a"
        ok=0
    fi
    if [ "$exit_code_a" -eq 0 ]; then
        tc_fail "$tc" "Part A: expected non-zero exit; got 0; output:\n$output_a"
        ok=0
    fi

    # Part B: podman info exits non-zero
    local tmpdir_b
    tmpdir_b="$(make_stub_dir \
        present:runsc \
        present:bwrap \
        present:claude \
        present:gh \
        podman_rootless_error \
        git_remote_ok \
        make_ok)"

    local output_b exit_code_b
    output_b="$(L6_PREFLIGHT_PATH="$tmpdir_b" bash "$PREFLIGHT" 2>&1)" && exit_code_b=$? || exit_code_b=$?
    rm -rf "$tmpdir_b"

    # Same expectation — FAIL in rootless row
    if ! printf '%s' "$output_b" | grep -qiE "FAIL.*(podman|rootless)|(podman|rootless).*FAIL"; then
        tc_fail "$tc" "Part B: expected FAIL for rootless podman check (info exits non-zero); got:\n$output_b"
        ok=0
    fi
    if [ "$exit_code_b" -eq 0 ]; then
        tc_fail "$tc" "Part B: expected non-zero exit; got 0; output:\n$output_b"
        ok=0
    fi

    # Other tools remain PASS
    for tool in runsc bwrap claude gh; do
        if ! printf '%s' "$output_a" | grep -qE "PASS.*$tool|$tool.*PASS"; then
            tc_fail "$tc" "expected PASS for $tool (unaffected by rootless fail); got:\n$output_a"
            ok=0
        fi
    done

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-043-05: make l6-preflight dry-run ─────────────────────────────────────

run_tc043_05() {
    local tc="TC-043-05"
    local ok=1

    # Dry-run must show the script invocation
    local dryrun_output
    dryrun_output="$(make --dry-run -C "$REPO_ROOT" l6-preflight 2>&1)" || true
    if ! printf '%s' "$dryrun_output" | grep -qE "l6-preflight\.sh"; then
        tc_fail "$tc" "make --dry-run l6-preflight did not show l6-preflight.sh invocation; got:\n$dryrun_output"
        ok=0
    fi

    # l6-preflight must be in .PHONY
    local makefile_content
    makefile_content="$(cat "$REPO_ROOT/Makefile")"
    if ! printf '%s' "$makefile_content" | grep -qE '\.PHONY.*l6-preflight|l6-preflight.*\.PHONY'; then
        tc_fail "$tc" "l6-preflight is not in .PHONY in Makefile"
        ok=0
    fi

    # l6-preflight must NOT be a prerequisite of check or fitness
    # Extract the check: and fitness: target lines and their prerequisites
    local check_prereqs fitness_prereqs
    check_prereqs="$(printf '%s' "$makefile_content" | grep -E '^check:' | head -1)"
    fitness_prereqs="$(printf '%s' "$makefile_content" | grep -E '^fitness:' | head -1)"

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

# ─── main ─────────────────────────────────────────────────────────────────────

printf '\n=== l6-preflight test harness ===\n\n'

# Confirm the script under test exists
if [ ! -f "$PREFLIGHT" ]; then
    printf 'ERROR: %s not found — build the script first\n' "$PREFLIGHT" >&2
    exit 1
fi

run_tc043_01
run_tc043_02
run_tc043_03
run_tc043_04
run_tc043_05

printf '\n=== Results: %d passed, %d failed ===\n' "$PASS_COUNT" "$FAIL_COUNT"

if [ "$FAIL_COUNT" -gt 0 ]; then
    printf '\nFailed test cases:\n'
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f"
    done
    exit 1
fi

exit 0
