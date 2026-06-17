#!/usr/bin/env bash
# Test harness for containment/execution-box/run.sh — storage quota + fail-loud behavior,
# and TC-016 runtime inspect portability (TC-047).
# Covers TC-045-01 through TC-045-04 (REQ-045-01 through REQ-045-06) and
# TC-047-01 through TC-047-02 (REQ-047-01 through REQ-047-02).
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
#   "storage_opt_null"      — accepted for call-site compat; no longer has effect
#   "storage_opt_set"       — accepted for call-site compat; no longer has effect
#
# The stub podman writes its create-subcommand argv to $STUB_ARGV_FILE when that var is set.
# The stub podman info always returns valid data for the real detection path (though
# we use EXEC_BOX_STORAGE_QUOTA_SUPPORTED to bypass detection in these tests).
# Note: run.sh no longer inspects StorageOpt (not portably exposed by podman 5.x on ext4).
# TC-003 storage message is now derived from the launcher's detection flag.

make_box_stub_dir() {
    local tmpdir
    tmpdir="$(mktemp -d)"

    local create_exit=0
    local start_exit=0
    local oci_runtime="runsc"

    for spec in "$@"; do
        case "$spec" in
            podman_create_exit=*)  create_exit="${spec#podman_create_exit=}" ;;
            podman_start_exit=*)   start_exit="${spec#podman_start_exit=}" ;;
            oci_runtime=*)         oci_runtime="${spec#oci_runtime=}" ;;
            storage_opt_null)      : ;;  # accepted for compat, no longer used
            storage_opt_set)       : ;;  # accepted for compat, no longer used
        esac
    done

    # Write stub podman binary.
    cat > "$tmpdir/podman" <<PODMAN_STUB
#!/bin/bash
# Stub podman for TC-045/TC-047 tests.
# Subcommands handled: info, build, create, inspect, start, run, rm, pod.

STUB_CREATE_EXIT=${create_exit}
STUB_START_EXIT=${start_exit}
# TC-047: the runtime name returned by .OCIRuntime (top-level field, podman 5.x)
STUB_OCI_RUNTIME="${oci_runtime}"

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
        #  1. TC-003: NanoCpus Memory PidsLimit ShmSize  (StorageOpt removed — not portably exposed)
        #  2. TC-016: .OCIRuntime (top-level field, portably set by podman 5.x)
        fmt="\${2:-}"
        case "\$fmt" in
            '{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}} {{.HostConfig.PidsLimit}} {{.HostConfig.ShmSize}}')
                # NanoCpus=200000000 (2 CPUs), Memory=2147483648 (2g), PidsLimit=256, ShmSize=67108864 (64m)
                printf '200000000 2147483648 256 67108864\n'
                ;;
            '{{.OCIRuntime}}')
                # TC-047: return the configured runtime name (replaces .HostConfig.Runtime)
                printf '%s\n' "\$STUB_OCI_RUNTIME"
                ;;
            *)
                printf '%s\n' "\$STUB_OCI_RUNTIME"
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

# make_egress_stub_dir creates stub binaries needed by the non-probe (workload /
# egress-probe) paths in run.sh.  The key difference from make_box_stub_dir:
#
#   "podman run -d …" (sidecar)  → always succeeds; writes the ready file
#   "podman run --rm …" (workload/egress-probe) → exits with STUB_RUN_RM_EXIT
#
# Arguments (optional keywords):
#   "run_rm_exit=N"       — exit code for the "podman run --rm" call (default 0)
#   "storage_opt_null"    — accepted for compat; no longer has effect
#   "storage_opt_set"     — accepted for compat; no longer has effect
#
# The stub podman writes the create/run argv to $STUB_ARGV_FILE when set.

make_egress_stub_dir() {
    local tmpdir
    tmpdir="$(mktemp -d)"

    local run_rm_exit=0

    for spec in "$@"; do
        case "$spec" in
            run_rm_exit=*)   run_rm_exit="${spec#run_rm_exit=}" ;;
            storage_opt_null) : ;;  # accepted for compat, no longer used
            storage_opt_set)  : ;;  # accepted for compat, no longer used
        esac
    done

    # Write stub podman binary.
    cat > "$tmpdir/podman" <<PODMAN_EGRESS_STUB
#!/bin/bash
# Stub podman for TC-045 egress-path tests.

STUB_RUN_RM_EXIT=${run_rm_exit}

subcommand="\$1"
shift || true

case "\$subcommand" in
    info)
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
        exit 0
        ;;
    create)
        # Record argv to STUB_ARGV_FILE if set.
        if [ -n "\${STUB_ARGV_FILE:-}" ]; then
            printf '%s\n' "\$*" > "\$STUB_ARGV_FILE"
        fi
        printf 'stub-container-id-045\n'
        exit 0
        ;;
    inspect)
        fmt="\${2:-}"
        case "\$fmt" in
            '{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}} {{.HostConfig.PidsLimit}} {{.HostConfig.ShmSize}}')
                printf '200000000 2147483648 256 67108864\n'
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
        exit 0
        ;;
    rm)
        exit 0
        ;;
    pod)
        # handle pod create and pod rm
        exit 0
        ;;
    logs)
        exit 0
        ;;
    run)
        # Record argv to STUB_ARGV_FILE if set.
        if [ -n "\${STUB_ARGV_FILE:-}" ]; then
            printf '%s\n' "\$*" > "\$STUB_ARGV_FILE"
        fi

        # Detect: is this the background sidecar run (-d flag) or the foreground run?
        # The sidecar is always invoked with "-d" as the first arg.
        if [ "\${1:-}" = "-d" ]; then
            # Sidecar run: must succeed and write the "ready" file so the loop exits.
            # Extract the egress-state path from the --mount argument:
            #   --mount type=bind,source=<path>,target=/egress-state,...
            for arg in "\$@"; do
                case "\$arg" in
                    type=bind,source=*,target=/egress-state,*)
                        _egress_state="\${arg#type=bind,source=}"
                        _egress_state="\${_egress_state%%,target=*}"
                        touch "\$_egress_state/ready"
                        break
                        ;;
                esac
            done
            exit 0
        fi

        # Foreground/probe run (--rm path): use configured exit code.
        if [ \$STUB_RUN_RM_EXIT -ne 0 ]; then
            printf 'Error: stub podman run --rm failed (simulated failure, exit %d)\n' \$STUB_RUN_RM_EXIT >&2
            exit \$STUB_RUN_RM_EXIT
        fi
        exit 0
        ;;
    *)
        exit 0
        ;;
esac
PODMAN_EGRESS_STUB
    chmod +x "$tmpdir/podman"

    # Stub runsc
    printf '#!/bin/sh\nexit 0\n' > "$tmpdir/runsc"
    chmod +x "$tmpdir/runsc"

    printf '%s' "$tmpdir"
}

# run_egress_path invokes run.sh with the egress pod path (no --probe flag) using
# an empty allowlist so no DNS resolution occurs.
# Usage: run_egress_path STUB_DIR WORKTREE GATE_TOOLS EXTRA_ARGS...
# Sets: _rp_exit, _rp_stdout, _rp_stderr (caller's locals must be declared first)
run_egress_path() {
    local stub_dir="$1" worktree="$2" gate_tools="$3"
    shift 3

    local stdout_file stderr_file empty_allowlist
    stdout_file="$(mktemp)"
    stderr_file="$(mktemp)"
    empty_allowlist="$(mktemp)"
    # Empty allowlist — valid (no entries) so no DNS resolution happens.
    printf '' > "$empty_allowlist"

    _rp_exit=0
    env PATH="$stub_dir:$PATH" \
        EXEC_BOX_EGRESS_ALLOWLIST="$empty_allowlist" \
        "$@" \
        bash "$RUN_SH" \
            --worktree "$worktree" \
            --gate-tools "$gate_tools" \
            --image "stub-image:test" \
            --runtime "runsc" \
            -- /bin/true \
        > "$stdout_file" 2> "$stderr_file" \
        || _rp_exit=$?

    _rp_stdout="$(cat "$stdout_file")"
    _rp_stderr="$(cat "$stderr_file")"

    rm -f "$stdout_file" "$stderr_file" "$empty_allowlist"
}

# run_egress_probe_path invokes run.sh with --egress-probe (not --probe).
# Usage: run_egress_probe_path STUB_DIR WORKTREE GATE_TOOLS EXTRA_ARGS...
# Callers may set STUB_ARGV_FILE before calling to capture the podman run argv.
run_egress_probe_path() {
    local stub_dir="$1" worktree="$2" gate_tools="$3"
    shift 3

    local stdout_file stderr_file empty_allowlist
    stdout_file="$(mktemp)"
    stderr_file="$(mktemp)"
    empty_allowlist="$(mktemp)"
    printf '' > "$empty_allowlist"

    # Build optional STUB_ARGV_FILE env assignment so the stub can record run argv.
    local stub_argv_env=()
    if [ -n "${STUB_ARGV_FILE:-}" ]; then
        stub_argv_env=("STUB_ARGV_FILE=${STUB_ARGV_FILE}")
    fi

    _rp_exit=0
    env PATH="$stub_dir:$PATH" \
        EXEC_BOX_EGRESS_ALLOWLIST="$empty_allowlist" \
        "${stub_argv_env[@]}" \
        "$@" \
        bash "$RUN_SH" \
            --egress-probe \
            --worktree "$worktree" \
            --gate-tools "$gate_tools" \
            --image "stub-image:test" \
            --runtime "runsc" \
        > "$stdout_file" 2> "$stderr_file" \
        || _rp_exit=$?

    _rp_stdout="$(cat "$stdout_file")"
    _rp_stderr="$(cat "$stderr_file")"

    rm -f "$stdout_file" "$stderr_file" "$empty_allowlist"
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

# ─── TC-045-04: --probe TC-003 reports correct storage message from detection flag ─
# REQ-045-06

run_tc045_04() {
    local tc="TC-045-04"
    local ok=1

    local worktree allowlist gate_tools
    worktree="$(make_fake_worktree)"
    allowlist="$(make_fake_allowlist)"
    gate_tools="$(make_fake_gate_tools)"

    # Part A: EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 + EXEC_BOX_STORAGE_SIZE=4G
    # → TC-003 PASS with "not enforced" message; exit 0 (graceful degrade).
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
        tc_fail "${tc}a" "expected exit 0 with quota not supported (non-XFS); got $exit_a; stdout: $stdout_a; stderr: $stderr_a"
        ok=0
    fi
    if ! printf '%s' "$stdout_a" | grep -q 'TC-003 PASS'; then
        tc_fail "${tc}a" "TC-003 PASS expected in stdout on non-XFS host (not-enforced message); stdout: $stdout_a"
        ok=0
    fi
    # Must carry the "not enforced" wording (detection-flag path, quota=0)
    if ! printf '%s' "$stdout_a" | grep -q 'storage quota not enforced on this host'; then
        tc_fail "${tc}a" "TC-003 PASS must say 'storage quota not enforced on this host' when quota_supported=0; stdout: $stdout_a"
        ok=0
    fi
    # Must NOT die (i.e., no TC-003 FAIL line)
    if printf '%s' "$stdout_a$stderr_a" | grep -q 'TC-003 FAIL'; then
        tc_fail "${tc}a" "TC-003 FAIL must NOT appear on non-enforceable host; output: $stdout_a $stderr_a"
        ok=0
    fi

    cleanup_stub_dir "$stub_dir_null"

    # Part B: EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1 + EXEC_BOX_STORAGE_SIZE=4G
    # → TC-003 PASS with "storage quota applied (size=4G)" message; exit 0.
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
        tc_fail "${tc}b" "expected exit 0 with quota supported (XFS host); got $exit_b; stdout: $stdout_b; stderr: $stderr_b"
        ok=0
    fi
    if ! printf '%s' "$stdout_b" | grep -q 'TC-003 PASS'; then
        tc_fail "${tc}b" "TC-003 PASS expected on XFS host (quota applied message); stdout: $stdout_b"
        ok=0
    fi
    # Must carry the "storage quota applied" wording (detection-flag path, quota=1)
    if ! printf '%s' "$stdout_b" | grep -q 'storage quota applied'; then
        tc_fail "${tc}b" "TC-003 PASS must say 'storage quota applied' when quota_supported=1 and size set; stdout: $stdout_b"
        ok=0
    fi

    cleanup_stub_dir "$stub_dir_set"
    rm -rf "$worktree" "$allowlist" "$gate_tools"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-045-03b/c/d: podman run launch-failure vs workload exit propagation ───
# REQ-045-05
#
# TC-045-03b: podman run --rm exits 125 (workload path) → run.sh exits non-zero,
#             named error "container did not start" emitted.
# TC-045-03c: podman run --rm exits 1  (workload path) → run.sh exits 1,
#             NO "container did not start" named error (not a launch failure).
# TC-045-03d: podman run --rm exits 125 (egress-probe path) → run.sh exits non-zero,
#             named error "container did not start" emitted.

run_tc045_03b() {
    local tc="TC-045-03b"
    local ok=1

    local stub_dir worktree gate_tools
    stub_dir="$(make_egress_stub_dir run_rm_exit=125 storage_opt_set)"
    worktree="$(make_fake_worktree)"
    gate_tools="$(make_fake_gate_tools)"

    local _rp_exit _rp_stdout _rp_stderr
    run_egress_path "$stub_dir" "$worktree" "$gate_tools" \
        EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1 EXEC_BOX_STORAGE_SIZE=4G

    if [ "$_rp_exit" -eq 0 ]; then
        tc_fail "$tc" "expected non-zero exit when podman run exits 125 (workload path); got exit 0"
        ok=0
    fi
    if ! printf '%s' "$_rp_stderr" | grep -qiE 'container did not start|podman run failed'; then
        tc_fail "$tc" "expected named error about failed podman run; stderr: $_rp_stderr"
        ok=0
    fi

    cleanup_stub_dir "$stub_dir"
    rm -rf "$worktree" "$gate_tools"
    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

run_tc045_03c() {
    local tc="TC-045-03c"
    local ok=1

    local stub_dir worktree gate_tools
    stub_dir="$(make_egress_stub_dir run_rm_exit=1 storage_opt_set)"
    worktree="$(make_fake_worktree)"
    gate_tools="$(make_fake_gate_tools)"

    local _rp_exit _rp_stdout _rp_stderr
    run_egress_path "$stub_dir" "$worktree" "$gate_tools" \
        EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1 EXEC_BOX_STORAGE_SIZE=4G

    # run.sh must propagate exit 1 unchanged
    if [ "$_rp_exit" -ne 1 ]; then
        tc_fail "$tc" "expected exit 1 propagated from workload; got $_rp_exit"
        ok=0
    fi
    # Must NOT emit the named launch-failure error
    if printf '%s' "$_rp_stderr" | grep -qiE 'container did not start'; then
        tc_fail "$tc" "must NOT emit 'container did not start' for normal workload exit 1; stderr: $_rp_stderr"
        ok=0
    fi

    cleanup_stub_dir "$stub_dir"
    rm -rf "$worktree" "$gate_tools"
    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

run_tc045_03d() {
    local tc="TC-045-03d"
    local ok=1

    local stub_dir worktree gate_tools
    stub_dir="$(make_egress_stub_dir run_rm_exit=125 storage_opt_set)"
    worktree="$(make_fake_worktree)"
    gate_tools="$(make_fake_gate_tools)"

    local _rp_exit _rp_stdout _rp_stderr
    run_egress_probe_path "$stub_dir" "$worktree" "$gate_tools" \
        EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1 EXEC_BOX_STORAGE_SIZE=4G

    if [ "$_rp_exit" -eq 0 ]; then
        tc_fail "$tc" "expected non-zero exit when podman run exits 125 (egress-probe path); got exit 0"
        ok=0
    fi
    if ! printf '%s' "$_rp_stderr" | grep -qiE 'container did not start|podman run.*egress.probe.*failed'; then
        tc_fail "$tc" "expected named error about failed egress-probe podman run; stderr: $_rp_stderr"
        ok=0
    fi

    cleanup_stub_dir "$stub_dir"
    rm -rf "$worktree" "$gate_tools"
    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-045-02c: egress-probe path omits --storage-opt on non-XFS host ────────
# REQ-045-02, REQ-045-04
# When EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0, the egress-probe podman run must NOT
# carry --storage-opt (the quota flag is only ever part of common_args/create, not
# the egress-probe argv, so this confirms the quota logic does not bleed through).

run_tc045_02c() {
    local tc="TC-045-02c"
    local ok=1

    local stub_dir worktree gate_tools argv_file
    stub_dir="$(make_egress_stub_dir run_rm_exit=0 storage_opt_null)"
    worktree="$(make_fake_worktree)"
    gate_tools="$(make_fake_gate_tools)"
    argv_file="$(mktemp)"

    local _rp_exit _rp_stdout _rp_stderr
    STUB_ARGV_FILE="$argv_file" \
    run_egress_probe_path "$stub_dir" "$worktree" "$gate_tools" \
        EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 EXEC_BOX_STORAGE_SIZE=4G

    local captured_argv=""
    [ -f "$argv_file" ] && captured_argv="$(cat "$argv_file")" || true

    if [ "$_rp_exit" -ne 0 ]; then
        tc_fail "$tc" "expected exit 0 for egress-probe with non-XFS host; got $_rp_exit; stderr: $_rp_stderr"
        ok=0
    fi
    if printf '%s' "$captured_argv" | grep -qF -- '--storage-opt'; then
        tc_fail "$tc" "--storage-opt must NOT appear in egress-probe podman run argv on non-XFS host; argv: $captured_argv"
        ok=0
    fi

    cleanup_stub_dir "$stub_dir"
    rm -rf "$worktree" "$gate_tools" "$argv_file"
    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-047-01: runtime check reads .OCIRuntime and PASSES when it matches ────
# REQ-047-01
#
# Stub .OCIRuntime = runc, requested --runtime runc.
# Assert: run.sh template references .OCIRuntime (not .HostConfig.Runtime),
# TC-016 HOST: ...runtime=runc is printed, no TC-016 FAIL, exit 0.

run_tc047_01() {
    local tc="TC-047-01"
    local ok=1

    # Verify the fix is in place: run.sh must reference .OCIRuntime, not .HostConfig.Runtime
    if grep -qF '.HostConfig.Runtime' "$RUN_SH"; then
        tc_fail "$tc" "run.sh still references .HostConfig.Runtime — must be changed to .OCIRuntime"
        ok=0
    fi
    if ! grep -qF '.OCIRuntime' "$RUN_SH"; then
        tc_fail "$tc" "run.sh does not reference .OCIRuntime — template fix not applied"
        ok=0
    fi

    local stub_dir worktree allowlist gate_tools
    # oci_runtime=runc matches --runtime runc → should pass
    stub_dir="$(make_box_stub_dir oci_runtime=runc)"
    worktree="$(make_fake_worktree)"
    allowlist="$(make_fake_allowlist)"
    gate_tools="$(make_fake_gate_tools)"

    local combined exit_code
    combined="$(
        env PATH="$stub_dir:$PATH" \
            EXEC_BOX_EGRESS_ALLOWLIST="$allowlist" \
            EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 \
            EXEC_BOX_STORAGE_SIZE="" \
            bash "$RUN_SH" \
                --probe \
                --worktree "$worktree" \
                --gate-tools "$gate_tools" \
                --image "stub-image:test" \
                --runtime "runc" \
            2>&1
    )" && exit_code=$? || exit_code=$?

    # Must exit 0
    if [ "$exit_code" -ne 0 ]; then
        tc_fail "$tc" "expected exit 0 with matching .OCIRuntime=runc --runtime runc; got $exit_code; output: $combined"
        ok=0
    fi

    # TC-016 HOST: line must appear with runtime=runc
    if ! printf '%s' "$combined" | grep -q 'TC-016 HOST:.*runtime=runc'; then
        tc_fail "$tc" "expected 'TC-016 HOST: ...runtime=runc' in output; got: $combined"
        ok=0
    fi

    # TC-016 FAIL must NOT appear
    if printf '%s' "$combined" | grep -q 'TC-016 FAIL'; then
        tc_fail "$tc" "TC-016 FAIL must NOT appear when .OCIRuntime matches requested runtime; got: $combined"
        ok=0
    fi

    cleanup_stub_dir "$stub_dir"
    rm -rf "$worktree" "$allowlist" "$gate_tools"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-047-02: runtime mismatch still FAILS loudly ──────────────────────────
# REQ-047-02
#
# Stub .OCIRuntime = runc but requested --runtime runsc → mismatch.
# Assert: TC-016 FAIL is printed, exit non-zero.

run_tc047_02() {
    local tc="TC-047-02"
    local ok=1

    local stub_dir worktree allowlist gate_tools
    # .OCIRuntime returns runc but we request runsc — deliberate mismatch
    stub_dir="$(make_box_stub_dir oci_runtime=runc)"
    worktree="$(make_fake_worktree)"
    allowlist="$(make_fake_allowlist)"
    gate_tools="$(make_fake_gate_tools)"

    local combined exit_code
    combined="$(
        env PATH="$stub_dir:$PATH" \
            EXEC_BOX_EGRESS_ALLOWLIST="$allowlist" \
            EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 \
            EXEC_BOX_STORAGE_SIZE="" \
            bash "$RUN_SH" \
                --probe \
                --worktree "$worktree" \
                --gate-tools "$gate_tools" \
                --image "stub-image:test" \
                --runtime "runsc" \
            2>&1
    )" && exit_code=$? || exit_code=$?

    # Must exit non-zero
    if [ "$exit_code" -eq 0 ]; then
        tc_fail "$tc" "expected non-zero exit when .OCIRuntime=runc but --runtime=runsc (mismatch); got exit 0; output: $combined"
        ok=0
    fi

    # TC-016 FAIL must appear
    if ! printf '%s' "$combined" | grep -q 'TC-016 FAIL'; then
        tc_fail "$tc" "expected 'TC-016 FAIL' in output on runtime mismatch; got: $combined"
        ok=0
    fi

    cleanup_stub_dir "$stub_dir"
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
run_tc045_03b
run_tc045_03c
run_tc045_03d
run_tc045_02c
run_tc047_01
run_tc047_02
# TC-047-03 (L6, real host): verified by operator via:
#   bash containment/execution-box/run.sh --worktree . --runtime runc --probe
# Expected: TC-016 HOST: workload=agent runtime=runc + exit 0.
# This test case requires a live podman 5.x environment and is not automated here.

printf '\n=== Results: %d passed, %d failed ===\n' "$PASS_COUNT" "$FAIL_COUNT"

if [ "$FAIL_COUNT" -gt 0 ]; then
    printf '\nFailed test cases:\n'
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f"
    done
    exit 1
fi

exit 0
