#!/usr/bin/env bash
# Test harness for containment/execution-box/run.sh — storage quota + fail-loud behavior.
# Covers TC-045-01 through TC-045-04 (REQ-045-01 through REQ-045-06).
#
# Uses stub binaries on a temp PATH and the EXEC_BOX_STORAGE_QUOTA_SUPPORTED env override.
# No live Podman, real XFS, or real container required.
#
# Usage: bash containment/execution-box/tests/storage-quota-test.sh
# Exit 0 on all pass; non-zero on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
RUN_SH="$REPO_ROOT/containment/execution-box/run.sh"

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
        tc_fail "$tc" "expected output to contain '${needle}'; got: ${haystack}"
        return 1
    fi
    return 0
}

assert_not_contains() {
    local tc="$1"
    local haystack="$2"
    local needle="$3"
    if printf '%s' "$haystack" | grep -qF "$needle"; then
        tc_fail "$tc" "output should NOT contain '${needle}'; got: ${haystack}"
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
# make_box_stub_dir creates a temp directory with stub binaries needed by run.sh --probe.
#
# Arguments (optional keywords):
#   "podman_create_exit=N"  — exit code for podman create (default 0)
#   "podman_start_exit=N"   — exit code for podman start (default 0)
#   "storage_opt_null"      — stub inspect returns null in StorageOpt position
#   "storage_opt_set"       — stub inspect returns {"size":"4G"} in StorageOpt position (default)
#
# The stub podman writes its create-subcommand argv to $STUB_ARGV_FILE when that var is set.
# The stub podman info always returns valid data for the real detection path (though
# we use EXEC_BOX_STORAGE_QUOTA_SUPPORTED to bypass detection in these tests).

make_box_stub_dir() {
    local tmpdir
    tmpdir="$(mktemp -d)"

    local create_exit=0
    local start_exit=0
    local storage_opt='{"size":"4G"}'

    for spec in "$@"; do
        case "$spec" in
            podman_create_exit=*)  create_exit="${spec#podman_create_exit=}" ;;
            podman_start_exit=*)   start_exit="${spec#podman_start_exit=}" ;;
            storage_opt_null)      storage_opt="null" ;;
            storage_opt_set)       storage_opt='{"size":"4G"}' ;;
        esac
    done

    # Write stub podman binary.
    cat > "$tmpdir/podman" <<PODMAN_STUB
#!/bin/bash
# Stub podman for TC-045 tests.
# Subcommands handled: info, build, create, inspect, start, run, rm, pod.

STUB_CREATE_EXIT=${create_exit}
STUB_START_EXIT=${start_exit}
STUB_STORAGE_OPT='${storage_opt}'

subcommand="\$1"
shift || true

case "\$subcommand" in
    info)
        # Minimal response for podman info checks in run.sh
        # (rootless check, OCIRuntimes, etc.)
        if [ "\${1:-}" = "--format" ]; then
            fmt="\$2"
            case "\$fmt" in
                '{{json .Host.OCIRuntimes}}')
                    printf '{"runsc":"/usr/bin/runsc","runc":"/usr/bin/runc"}\n'
                    ;;
                '{{.Store.GraphDriverName}}')
                    printf 'overlay\n'
                    ;;
                '{{.Store.GraphRoot}}')
                    printf '/tmp/containers/storage\n'
                    ;;
                *)
                    printf 'true\n'
                    ;;
            esac
        else
            printf 'true\n'
        fi
        exit 0
        ;;
    build)
        # Pretend image build succeeded.
        exit 0
        ;;
    create)
        # Record argv to STUB_ARGV_FILE if set.
        if [ -n "\${STUB_ARGV_FILE:-}" ]; then
            printf '%s\n' "\$*" > "\$STUB_ARGV_FILE"
        fi
        if [ \$STUB_CREATE_EXIT -ne 0 ]; then
            printf 'Error: stub podman create failed (simulated failure)\n' >&2
            exit \$STUB_CREATE_EXIT
        fi
        # Return a fake container ID.
        printf 'stub-container-id-045\n'
        exit 0
        ;;
    inspect)
        # Return fake inspect output for the probe's TC-003 and TC-016 checks.
        # run.sh calls inspect with two different --format arguments:
        #  1. TC-003: NanoCpus Memory PidsLimit ShmSize StorageOpt
        #  2. TC-016: Runtime
        fmt="\${2:-}"
        case "\$fmt" in
            '{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}} {{.HostConfig.PidsLimit}} {{.HostConfig.ShmSize}} {{json .HostConfig.StorageOpt}}')
                # NanoCpus=200000000 (2 CPUs), Memory=2147483648 (2g), PidsLimit=256, ShmSize=67108864 (64m)
                printf '200000000 2147483648 256 67108864 %s\n' "\$STUB_STORAGE_OPT"
                ;;
            '{{.HostConfig.Runtime}}')
                printf 'runsc\n'
                ;;
            *)
                printf 'runsc\n'
                ;;
        esac
        exit 0
        ;;
    start)
        if [ \$STUB_START_EXIT -ne 0 ]; then
            printf 'Error: stub podman start failed (simulated failure)\n' >&2
            exit \$STUB_START_EXIT
        fi
        exit 0
        ;;
    rm)
        exit 0
        ;;
    pod)
        exit 0
        ;;
    run)
        exit 0
        ;;
    *)
        exit 0
        ;;
esac
PODMAN_STUB
    chmod +x "$tmpdir/podman"

    # Stub runsc — run.sh checks if the runtime is available.
    printf '#!/bin/sh\nexit 0\n' > "$tmpdir/runsc"
    chmod +x "$tmpdir/runsc"

    # Stub id — run.sh calls id -u and id -g to get uid/gid.
    # We forward to the real id binary (it's not under test).
    # Actually we need the real id to be found; we just don't put it in tmpdir.

    # Stub stat — run.sh calls stat -f -c '%T' to detect backing FS.
    # We don't stub it here; the EXEC_BOX_STORAGE_QUOTA_SUPPORTED seam bypasses it.

    printf '%s' "$tmpdir"
}

# cleanup_stub_dir removes a stub dir created by make_box_stub_dir.
cleanup_stub_dir() {
    local dir="$1"
    rm -rf "$dir"
}

# make_fake_worktree creates a minimal worktree dir that run.sh will accept.
make_fake_worktree() {
    local tmpdir
    tmpdir="$(mktemp -d)"
    printf '%s' "$tmpdir"
}

# make_fake_allowlist creates a minimal egress allowlist file run.sh will accept.
make_fake_allowlist() {
    local tmpdir
    tmpdir="$(mktemp)"
    printf 'api.github.com:443 # GitHub API\n' > "$tmpdir"
    printf '%s' "$tmpdir"
}

# make_fake_gate_tools creates a minimal gate-tools dir that run.sh will accept.
make_fake_gate_tools() {
    local tmpdir
    tmpdir="$(mktemp -d)"
    for tool in golangci-lint gods code-scanner; do
        printf '#!/bin/sh\nexit 0\n' > "$tmpdir/$tool"
        chmod +x "$tmpdir/$tool"
    done
    printf '%s' "$tmpdir"
}

# run_probe invokes run.sh --probe with the given stub dir on PATH, capturing
# stdout, stderr, and the exit code.
# Usage: run_probe STUB_DIR WORKTREE ALLOWLIST GATE_TOOLS [env_var=val ...]
# Outputs three lines to stdout: the exit code, then stdout content (base64), then stderr (base64).
# (We use a temp file approach instead.)
run_probe() {
    local stub_dir="$1" worktree="$2" allowlist="$3" gate_tools="$4"
    shift 4

    local stdout_file stderr_file
    stdout_file="$(mktemp)"
    stderr_file="$(mktemp)"

    local exit_code=0
    # Run with only the stub dir prepended to PATH.
    # Pass remaining args as environment overrides.
    env PATH="$stub_dir:$PATH" \
        EXEC_BOX_EGRESS_ALLOWLIST="$allowlist" \
        "$@" \
        bash "$RUN_SH" \
            --probe \
            --worktree "$worktree" \
            --gate-tools "$gate_tools" \
            --image "stub-image:test" \
            --runtime "runsc" \
            > "$stdout_file" 2> "$stderr_file" \
        || exit_code=$?

    printf '%d\n' "$exit_code"
    cat "$stdout_file"
    printf '__STDERR__\n'
    cat "$stderr_file"

    rm -f "$stdout_file" "$stderr_file"
}

# ─── TC-045-01: quota applied when host is enforceable ───────────────────────
# REQ-045-01, REQ-045-04

run_tc045_01() {
    local tc="TC-045-01"
    local ok=1

    local stub_dir worktree allowlist gate_tools argv_file
    stub_dir="$(make_box_stub_dir storage_opt_set)"
    worktree="$(make_fake_worktree)"
    allowlist="$(make_fake_allowlist)"
    gate_tools="$(make_fake_gate_tools)"
    argv_file="$(mktemp)"

    # Part A: EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1 with EXEC_BOX_STORAGE_SIZE=4G
    # → --storage-opt size=4G must appear in podman create argv; no WARNING on stderr.
    local combined_output exit_code stdout_part stderr_part
    combined_output="$(
        env PATH="$stub_dir:$PATH" \
            EXEC_BOX_EGRESS_ALLOWLIST="$allowlist" \
            EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1 \
            EXEC_BOX_STORAGE_SIZE=4G \
            STUB_ARGV_FILE="$argv_file" \
            bash "$RUN_SH" \
                --probe \
                --worktree "$worktree" \
                --gate-tools "$gate_tools" \
                --image "stub-image:test" \
                --runtime "runsc" \
            2>&1
    )" && exit_code=$? || exit_code=$?

    # Check exit 0
    if [ "$exit_code" -ne 0 ]; then
        tc_fail "${tc}a" "expected exit 0 with quota supported; got $exit_code; output: $combined_output"
        ok=0
    fi

    # Check --storage-opt appears in captured podman create argv
    local captured_argv=""
    [ -f "$argv_file" ] && captured_argv="$(cat "$argv_file")" || true
    if ! printf '%s' "$captured_argv" | grep -qF -- '--storage-opt'; then
        tc_fail "${tc}a" "expected --storage-opt in podman create argv; argv: $captured_argv"
        ok=0
    fi
    if ! printf '%s' "$captured_argv" | grep -qF 'size=4G'; then
        tc_fail "${tc}a" "expected size=4G in podman create argv; argv: $captured_argv"
        ok=0
    fi

    # No WARNING on combined output
    if printf '%s' "$combined_output" | grep -q 'WARNING'; then
        tc_fail "${tc}a" "no WARNING expected when quota is supported; got: $combined_output"
        ok=0
    fi

    # TC-003 PASS line must appear
    if ! printf '%s' "$combined_output" | grep -q 'TC-003 PASS'; then
        tc_fail "${tc}a" "TC-003 PASS expected; got: $combined_output"
        ok=0
    fi

    # Part B: EXEC_BOX_STORAGE_SIZE="" with EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1
    # → operator opt-out: no --storage-opt in argv, no WARNING.
    > "$argv_file"
    local combined_b exit_b
    combined_b="$(
        env PATH="$stub_dir:$PATH" \
            EXEC_BOX_EGRESS_ALLOWLIST="$allowlist" \
            EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1 \
            EXEC_BOX_STORAGE_SIZE="" \
            STUB_ARGV_FILE="$argv_file" \
            bash "$RUN_SH" \
                --probe \
                --worktree "$worktree" \
                --gate-tools "$gate_tools" \
                --image "stub-image:test" \
                --runtime "runsc" \
            2>&1
    )" && exit_b=$? || exit_b=$?

    local argv_b=""
    [ -f "$argv_file" ] && argv_b="$(cat "$argv_file")" || true

    if [ "$exit_b" -ne 0 ]; then
        tc_fail "${tc}b" "expected exit 0 with empty EXEC_BOX_STORAGE_SIZE; got $exit_b; output: $combined_b"
        ok=0
    fi
    if printf '%s' "$argv_b" | grep -qF -- '--storage-opt'; then
        tc_fail "${tc}b" "--storage-opt must be absent when EXEC_BOX_STORAGE_SIZE is empty; argv: $argv_b"
        ok=0
    fi
    if printf '%s' "$combined_b" | grep -q 'WARNING'; then
        tc_fail "${tc}b" "no WARNING expected when EXEC_BOX_STORAGE_SIZE is empty (opt-out); got: $combined_b"
        ok=0
    fi

    cleanup_stub_dir "$stub_dir"
    rm -rf "$worktree" "$allowlist" "$gate_tools" "$argv_file"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-045-02: quota skipped + WARNING when host not enforceable ─────────────
# REQ-045-02, REQ-045-04

run_tc045_02() {
    local tc="TC-045-02"
    local ok=1

    local stub_dir worktree allowlist gate_tools argv_file
    stub_dir="$(make_box_stub_dir storage_opt_null)"
    worktree="$(make_fake_worktree)"
    allowlist="$(make_fake_allowlist)"
    gate_tools="$(make_fake_gate_tools)"
    argv_file="$(mktemp)"

    # Part A: EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 AND EXEC_BOX_STORAGE_SIZE=4G
    # → no --storage-opt in argv; WARNING on stderr; exit 0; TC-003 PASS.
    local stdout_part stderr_part exit_code
    stdout_part="$(
        env PATH="$stub_dir:$PATH" \
            EXEC_BOX_EGRESS_ALLOWLIST="$allowlist" \
            EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 \
            EXEC_BOX_STORAGE_SIZE=4G \
            STUB_ARGV_FILE="$argv_file" \
            bash "$RUN_SH" \
                --probe \
                --worktree "$worktree" \
                --gate-tools "$gate_tools" \
                --image "stub-image:test" \
                --runtime "runsc" \
            2>/tmp/tc045_02_stderr
    )" && exit_code=$? || exit_code=$?
    stderr_part="$(cat /tmp/tc045_02_stderr 2>/dev/null || true)"
    rm -f /tmp/tc045_02_stderr

    local captured_argv=""
    [ -f "$argv_file" ] && captured_argv="$(cat "$argv_file")" || true

    if [ "$exit_code" -ne 0 ]; then
        tc_fail "${tc}a" "expected exit 0 with quota not supported; got $exit_code; stdout: $stdout_part; stderr: $stderr_part"
        ok=0
    fi

    # --storage-opt must NOT appear in argv
    if printf '%s' "$captured_argv" | grep -qF -- '--storage-opt'; then
        tc_fail "${tc}a" "--storage-opt must be absent when quota not supported; argv: $captured_argv"
        ok=0
    fi

    # WARNING must appear on stderr
    if ! printf '%s' "$stderr_part" | grep -q 'WARNING'; then
        tc_fail "${tc}a" "WARNING expected on stderr when quota not supported and EXEC_BOX_STORAGE_SIZE set; stderr: $stderr_part"
        ok=0
    fi
    # WARNING must name the degraded control (disk quota / overlay / storage-opt)
    if ! printf '%s' "$stderr_part" | grep -qiE 'disk quota|overlay|storage.opt'; then
        tc_fail "${tc}a" "WARNING must name the degraded control (disk quota/overlay/storage-opt); stderr: $stderr_part"
        ok=0
    fi

    # TC-003 PASS must appear in stdout (graceful degrade path)
    if ! printf '%s' "$stdout_part" | grep -q 'TC-003 PASS'; then
        tc_fail "${tc}a" "TC-003 PASS expected in stdout; got: $stdout_part"
        ok=0
    fi

    # Part B: EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 AND EXEC_BOX_STORAGE_SIZE="" (opt-out)
    # → no --storage-opt, NO WARNING (operator deliberately opted out).
    > "$argv_file"
    local stdout_b stderr_b exit_b
    stdout_b="$(
        env PATH="$stub_dir:$PATH" \
            EXEC_BOX_EGRESS_ALLOWLIST="$allowlist" \
            EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 \
            EXEC_BOX_STORAGE_SIZE="" \
            STUB_ARGV_FILE="$argv_file" \
            bash "$RUN_SH" \
                --probe \
                --worktree "$worktree" \
                --gate-tools "$gate_tools" \
                --image "stub-image:test" \
                --runtime "runsc" \
            2>/tmp/tc045_02b_stderr
    )" && exit_b=$? || exit_b=$?
    stderr_b="$(cat /tmp/tc045_02b_stderr 2>/dev/null || true)"
    rm -f /tmp/tc045_02b_stderr

    local argv_b=""
    [ -f "$argv_file" ] && argv_b="$(cat "$argv_file")" || true

    if [ "$exit_b" -ne 0 ]; then
        tc_fail "${tc}b" "expected exit 0 when storage size empty and quota not supported; got $exit_b"
        ok=0
    fi
    if printf '%s' "$argv_b" | grep -qF -- '--storage-opt'; then
        tc_fail "${tc}b" "--storage-opt must be absent when EXEC_BOX_STORAGE_SIZE is empty; argv: $argv_b"
        ok=0
    fi
    if printf '%s' "$stderr_b" | grep -q 'WARNING'; then
        tc_fail "${tc}b" "no WARNING expected when operator opted out (empty EXEC_BOX_STORAGE_SIZE); stderr: $stderr_b"
        ok=0
    fi

    cleanup_stub_dir "$stub_dir"
    rm -rf "$worktree" "$allowlist" "$gate_tools" "$argv_file"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-045-03: forced podman create failure → non-zero exit, named error ────
# REQ-045-05

run_tc045_03() {
    local tc="TC-045-03"
    local ok=1

    local stub_dir worktree allowlist gate_tools
    stub_dir="$(make_box_stub_dir podman_create_exit=125 storage_opt_set)"
    worktree="$(make_fake_worktree)"
    allowlist="$(make_fake_allowlist)"
    gate_tools="$(make_fake_gate_tools)"

    # Part A: stub podman create exits non-zero → run.sh must exit non-zero
    local combined exit_code
    combined="$(
        env PATH="$stub_dir:$PATH" \
            EXEC_BOX_EGRESS_ALLOWLIST="$allowlist" \
            EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1 \
            EXEC_BOX_STORAGE_SIZE=4G \
            bash "$RUN_SH" \
                --probe \
                --worktree "$worktree" \
                --gate-tools "$gate_tools" \
                --image "stub-image:test" \
                --runtime "runsc" \
            2>&1
    )" && exit_code=$? || exit_code=$?

    if [ "$exit_code" -eq 0 ]; then
        tc_fail "${tc}a" "expected non-zero exit when podman create fails; got exit 0; output: $combined"
        ok=0
    fi

    # stderr must contain a named error referencing the failure
    if ! printf '%s' "$combined" | grep -qiE 'podman create failed|container did not start|failed'; then
        tc_fail "${tc}a" "expected named error about failed podman create; output: $combined"
        ok=0
    fi

    cleanup_stub_dir "$stub_dir"
    rm -rf "$worktree" "$allowlist" "$gate_tools"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-045-04: --probe TC-003 tolerates null StorageOpt on non-XFS host ─────
# REQ-045-06

run_tc045_04() {
    local tc="TC-045-04"
    local ok=1

    local worktree allowlist gate_tools
    worktree="$(make_fake_worktree)"
    allowlist="$(make_fake_allowlist)"
    gate_tools="$(make_fake_gate_tools)"

    # Part A: EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 + stub inspect returns null StorageOpt
    # → TC-003 PASS (no die on null); exit 0.
    local stub_dir_null
    stub_dir_null="$(make_box_stub_dir storage_opt_null)"

    local stdout_a stderr_a exit_a
    stdout_a="$(
        env PATH="$stub_dir_null:$PATH" \
            EXEC_BOX_EGRESS_ALLOWLIST="$allowlist" \
            EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 \
            EXEC_BOX_STORAGE_SIZE=4G \
            bash "$RUN_SH" \
                --probe \
                --worktree "$worktree" \
                --gate-tools "$gate_tools" \
                --image "stub-image:test" \
                --runtime "runsc" \
            2>/tmp/tc045_04a_stderr
    )" && exit_a=$? || exit_a=$?
    stderr_a="$(cat /tmp/tc045_04a_stderr 2>/dev/null || true)"
    rm -f /tmp/tc045_04a_stderr

    if [ "$exit_a" -ne 0 ]; then
        tc_fail "${tc}a" "expected exit 0 with null StorageOpt on non-XFS host; got $exit_a; stdout: $stdout_a; stderr: $stderr_a"
        ok=0
    fi
    if ! printf '%s' "$stdout_a" | grep -q 'TC-003 PASS'; then
        tc_fail "${tc}a" "TC-003 PASS expected in stdout even with null StorageOpt on non-XFS host; stdout: $stdout_a"
        ok=0
    fi
    # Must NOT die (i.e., no TC-003 FAIL line)
    if printf '%s' "$stdout_a$stderr_a" | grep -q 'TC-003 FAIL'; then
        tc_fail "${tc}a" "TC-003 FAIL must NOT appear when StorageOpt is null on non-enforceable host; output: $stdout_a $stderr_a"
        ok=0
    fi

    cleanup_stub_dir "$stub_dir_null"

    # Part B: EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1 + stub inspect returns non-null StorageOpt
    # → TC-003 PASS (quota present as expected); exit 0.
    local stub_dir_set
    stub_dir_set="$(make_box_stub_dir storage_opt_set)"

    local stdout_b stderr_b exit_b
    stdout_b="$(
        env PATH="$stub_dir_set:$PATH" \
            EXEC_BOX_EGRESS_ALLOWLIST="$allowlist" \
            EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1 \
            EXEC_BOX_STORAGE_SIZE=4G \
            bash "$RUN_SH" \
                --probe \
                --worktree "$worktree" \
                --gate-tools "$gate_tools" \
                --image "stub-image:test" \
                --runtime "runsc" \
            2>/tmp/tc045_04b_stderr
    )" && exit_b=$? || exit_b=$?
    stderr_b="$(cat /tmp/tc045_04b_stderr 2>/dev/null || true)"
    rm -f /tmp/tc045_04b_stderr

    if [ "$exit_b" -ne 0 ]; then
        tc_fail "${tc}b" "expected exit 0 with non-null StorageOpt on XFS host; got $exit_b; stdout: $stdout_b; stderr: $stderr_b"
        ok=0
    fi
    if ! printf '%s' "$stdout_b" | grep -q 'TC-003 PASS'; then
        tc_fail "${tc}b" "TC-003 PASS expected on XFS host with non-null StorageOpt; stdout: $stdout_b"
        ok=0
    fi

    cleanup_stub_dir "$stub_dir_set"
    rm -rf "$worktree" "$allowlist" "$gate_tools"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── main ─────────────────────────────────────────────────────────────────────

printf '\n=== storage-quota test harness ===\n\n'

if [ ! -f "$RUN_SH" ]; then
    printf 'ERROR: %s not found\n' "$RUN_SH" >&2
    exit 1
fi

run_tc045_01
run_tc045_02
run_tc045_03
run_tc045_04

printf '\n=== Results: %d passed, %d failed ===\n' "$PASS_COUNT" "$FAIL_COUNT"

if [ "$FAIL_COUNT" -gt 0 ]; then
    printf '\nFailed test cases:\n'
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f"
    done
    exit 1
fi

exit 0
