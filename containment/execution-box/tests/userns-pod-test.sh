#!/usr/bin/env bash
# Test harness for TC-049: egress pod --userns/--pod conflict (rootless podman).
#
# Verifies REQ-049-01 through REQ-049-04:
#   TC-049-01  podman pod create has --userns=keep-id; sidecar run -d and egress
#              workload run --pod do NOT have --userns.
#   TC-049-02  non-pod --probe path still carries --userns=keep-id on the container.
#   TC-049-03  pod members (sidecar + egress workload) still carry --user <uid>:<gid>.
#
# TC-049-04 (L6, real host) is documented here but not automated; it requires a
# live rootless podman 5.x environment.
#
# Usage: bash containment/execution-box/tests/userns-pod-test.sh
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
    # Use grep -F -- to prevent leading dashes in needle from being parsed as grep flags.
    if ! printf '%s' "$haystack" | grep -qF -- "$needle"; then
        tc_fail "$tc" "expected output to contain '${needle}'; got: ${haystack}"
        return 1
    fi
    return 0
}

assert_not_contains() {
    local tc="$1"
    local haystack="$2"
    local needle="$3"
    # Use grep -F -- to prevent leading dashes in needle from being parsed as grep flags.
    if printf '%s' "$haystack" | grep -qF -- "$needle"; then
        tc_fail "$tc" "output should NOT contain '${needle}'; got: ${haystack}"
        return 1
    fi
    return 0
}

# ─── fixture factories ────────────────────────────────────────────────────────

make_fake_worktree() {
    local tmpdir
    tmpdir="$(mktemp -d)"
    printf '%s' "$tmpdir"
}

make_fake_gate_tools() {
    local tmpdir
    tmpdir="$(mktemp -d)"
    for tool in golangci-lint gods code-scanner; do
        printf '#!/bin/sh\nexit 0\n' > "$tmpdir/$tool"
        chmod +x "$tmpdir/$tool"
    done
    printf '%s' "$tmpdir"
}

make_fake_allowlist() {
    local tmpfile
    tmpfile="$(mktemp)"
    printf '' > "$tmpfile"
    printf '%s' "$tmpfile"
}

make_fake_probe_allowlist() {
    local tmpfile
    tmpfile="$(mktemp)"
    printf 'api.github.com:443 # GitHub API\n' > "$tmpfile"
    printf '%s' "$tmpfile"
}

# ─── per-subcommand-recording egress stub ────────────────────────────────────
#
# make_argv_egress_stub_dir creates a stub podman that writes per-subcommand
# argv to separate files when the environment variables are set:
#
#   STUB_POD_CREATE_ARGV_FILE   — written when: subcommand=pod, first arg=create
#   STUB_SIDECAR_RUN_ARGV_FILE  — written when: subcommand=run, first arg=-d
#   STUB_WORKLOAD_RUN_ARGV_FILE — written when: subcommand=run, first arg=--rm (or --rm -it)
#
# The sidecar run writes the egress-state/ready file so run.sh continues.
# The workload run exits 0.

make_argv_egress_stub_dir() {
    local tmpdir
    tmpdir="$(mktemp -d)"

    cat > "$tmpdir/podman" <<'PODMAN_ARGV_STUB'
#!/bin/bash
# Stub podman — per-subcommand argv recording for TC-049.

subcommand="$1"
shift || true

case "$subcommand" in
    info)
        if [ "${1:-}" = "--format" ]; then
            fmt="$2"
            case "$fmt" in
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
    pod)
        # subcommand="pod", next arg is the pod action (create, rm, ...)
        pod_action="${1:-}"
        shift || true
        if [ "$pod_action" = "create" ]; then
            # Record pod create argv to STUB_POD_CREATE_ARGV_FILE if set.
            if [ -n "${STUB_POD_CREATE_ARGV_FILE:-}" ]; then
                printf '%s\n' "$*" > "$STUB_POD_CREATE_ARGV_FILE"
            fi
        fi
        # pod rm and others: just succeed
        exit 0
        ;;
    run)
        # Detect whether this is the sidecar (-d flag) or workload (--rm flag).
        if [ "${1:-}" = "-d" ]; then
            # Sidecar run: record full argv to STUB_SIDECAR_RUN_ARGV_FILE if set.
            if [ -n "${STUB_SIDECAR_RUN_ARGV_FILE:-}" ]; then
                printf '%s\n' "$*" > "$STUB_SIDECAR_RUN_ARGV_FILE"
            fi
            # Write the egress-state/ready file so run.sh proceeds.
            for arg in "$@"; do
                case "$arg" in
                    type=bind,source=*,target=/egress-state,*)
                        _egress_state="${arg#type=bind,source=}"
                        _egress_state="${_egress_state%%,target=*}"
                        touch "$_egress_state/ready"
                        break
                        ;;
                esac
            done
            exit 0
        fi
        # Workload / egress-probe run (--rm path): record argv.
        if [ -n "${STUB_WORKLOAD_RUN_ARGV_FILE:-}" ]; then
            printf '%s\n' "$*" > "$STUB_WORKLOAD_RUN_ARGV_FILE"
        fi
        exit 0
        ;;
    rm)
        exit 0
        ;;
    logs)
        exit 0
        ;;
    create)
        printf 'stub-container-id-049\n'
        exit 0
        ;;
    inspect)
        # Provide valid TC-003/TC-016 responses for --probe path.
        fmt="${2:-}"
        case "$fmt" in
            '{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}} {{.HostConfig.PidsLimit}} {{.HostConfig.ShmSize}}')
                printf '200000000 2147483648 256 67108864\n'
                ;;
            '{{.OCIRuntime}}')
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
    *)
        exit 0
        ;;
esac
PODMAN_ARGV_STUB
    chmod +x "$tmpdir/podman"

    # Stub runsc
    printf '#!/bin/sh\nexit 0\n' > "$tmpdir/runsc"
    chmod +x "$tmpdir/runsc"

    printf '%s' "$tmpdir"
}

# ─── probe-path stub (for TC-049-02: non-pod --probe) ────────────────────────
#
# make_probe_stub_dir creates a stub with argv recording for podman create,
# plus all the usual TC-003/TC-016 inspect responses.

make_probe_stub_dir() {
    local tmpdir
    tmpdir="$(mktemp -d)"

    cat > "$tmpdir/podman" <<'PODMAN_PROBE_STUB'
#!/bin/bash
# Stub podman for --probe path (TC-049-02).

subcommand="$1"
shift || true

case "$subcommand" in
    info)
        if [ "${1:-}" = "--format" ]; then
            fmt="$2"
            case "$fmt" in
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
        # Record create argv (the non-pod path uses podman create for --probe).
        if [ -n "${STUB_PROBE_CREATE_ARGV_FILE:-}" ]; then
            printf '%s\n' "$*" > "$STUB_PROBE_CREATE_ARGV_FILE"
        fi
        printf 'stub-container-probe-id\n'
        exit 0
        ;;
    inspect)
        fmt="${2:-}"
        case "$fmt" in
            '{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}} {{.HostConfig.PidsLimit}} {{.HostConfig.ShmSize}}')
                printf '200000000 2147483648 256 67108864\n'
                ;;
            '{{.OCIRuntime}}')
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
        exit 0
        ;;
    run)
        exit 0
        ;;
    *)
        exit 0
        ;;
esac
PODMAN_PROBE_STUB
    chmod +x "$tmpdir/podman"

    printf '#!/bin/sh\nexit 0\n' > "$tmpdir/runsc"
    chmod +x "$tmpdir/runsc"

    printf '%s' "$tmpdir"
}

# ─── runner helpers ───────────────────────────────────────────────────────────

# run_egress_probe_argv STUB_DIR WORKTREE GATE_TOOLS
#   Runs run.sh --egress-probe with per-subcommand argv capture.
#   The caller must export STUB_POD_CREATE_ARGV_FILE, STUB_SIDECAR_RUN_ARGV_FILE,
#   STUB_WORKLOAD_RUN_ARGV_FILE before calling.
#   Sets: _rep_exit, _rep_stdout, _rep_stderr
run_egress_probe_argv() {
    local stub_dir="$1" worktree="$2" gate_tools="$3"

    local stdout_file stderr_file empty_allowlist
    stdout_file="$(mktemp)"
    stderr_file="$(mktemp)"
    empty_allowlist="$(make_fake_allowlist)"

    _rep_exit=0
    env PATH="$stub_dir:$PATH" \
        EXEC_BOX_EGRESS_ALLOWLIST="$empty_allowlist" \
        EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 \
        EXEC_BOX_STORAGE_SIZE="" \
        STUB_POD_CREATE_ARGV_FILE="${STUB_POD_CREATE_ARGV_FILE:-}" \
        STUB_SIDECAR_RUN_ARGV_FILE="${STUB_SIDECAR_RUN_ARGV_FILE:-}" \
        STUB_WORKLOAD_RUN_ARGV_FILE="${STUB_WORKLOAD_RUN_ARGV_FILE:-}" \
        bash "$RUN_SH" \
            --egress-probe \
            --worktree "$worktree" \
            --gate-tools "$gate_tools" \
            --image "stub-image:test" \
            --runtime "runsc" \
        > "$stdout_file" 2> "$stderr_file" \
        || _rep_exit=$?

    _rep_stdout="$(cat "$stdout_file")"
    _rep_stderr="$(cat "$stderr_file")"

    rm -f "$stdout_file" "$stderr_file" "$empty_allowlist"
}

# run_probe_argv STUB_DIR WORKTREE GATE_TOOLS ALLOWLIST
#   Runs run.sh --probe with argv capture for podman create.
#   The caller must export STUB_PROBE_CREATE_ARGV_FILE before calling.
#   Sets: _rpa_exit, _rpa_stdout, _rpa_stderr
run_probe_argv() {
    local stub_dir="$1" worktree="$2" gate_tools="$3" allowlist="$4"

    local stdout_file stderr_file
    stdout_file="$(mktemp)"
    stderr_file="$(mktemp)"

    _rpa_exit=0
    env PATH="$stub_dir:$PATH" \
        EXEC_BOX_EGRESS_ALLOWLIST="$allowlist" \
        EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 \
        EXEC_BOX_STORAGE_SIZE="" \
        STUB_PROBE_CREATE_ARGV_FILE="${STUB_PROBE_CREATE_ARGV_FILE:-}" \
        bash "$RUN_SH" \
            --probe \
            --worktree "$worktree" \
            --gate-tools "$gate_tools" \
            --image "stub-image:test" \
            --runtime "runsc" \
        > "$stdout_file" 2> "$stderr_file" \
        || _rpa_exit=$?

    _rpa_stdout="$(cat "$stdout_file")"
    _rpa_stderr="$(cat "$stderr_file")"

    rm -f "$stdout_file" "$stderr_file"
}

# ─── TC-049-01: pod create owns userns; pod members do NOT set --userns ──────
# REQ-049-01, REQ-049-02, REQ-049-03

run_tc049_01() {
    local tc="TC-049-01"
    local ok=1

    local stub_dir worktree gate_tools
    local pod_argv_file sidecar_argv_file workload_argv_file
    stub_dir="$(make_argv_egress_stub_dir)"
    worktree="$(make_fake_worktree)"
    gate_tools="$(make_fake_gate_tools)"
    pod_argv_file="$(mktemp)"
    sidecar_argv_file="$(mktemp)"
    workload_argv_file="$(mktemp)"

    local _rep_exit _rep_stdout _rep_stderr
    STUB_POD_CREATE_ARGV_FILE="$pod_argv_file" \
    STUB_SIDECAR_RUN_ARGV_FILE="$sidecar_argv_file" \
    STUB_WORKLOAD_RUN_ARGV_FILE="$workload_argv_file" \
    run_egress_probe_argv "$stub_dir" "$worktree" "$gate_tools"

    local pod_argv sidecar_argv workload_argv
    pod_argv="$(cat "$pod_argv_file" 2>/dev/null || true)"
    sidecar_argv="$(cat "$sidecar_argv_file" 2>/dev/null || true)"
    workload_argv="$(cat "$workload_argv_file" 2>/dev/null || true)"

    if [ "$_rep_exit" -ne 0 ]; then
        tc_fail "${tc}" "expected exit 0; got $_rep_exit; stderr: $_rep_stderr"
        ok=0
    fi

    # REQ-049-01: pod create must have --userns=keep-id
    if ! assert_contains "${tc}/pod-create-has-userns" "$pod_argv" "--userns=keep-id"; then
        ok=0
    fi

    # REQ-049-02: sidecar run -d must NOT have --userns
    if ! assert_not_contains "${tc}/sidecar-no-userns" "$sidecar_argv" "--userns"; then
        ok=0
    fi

    # REQ-049-02: sidecar still has its security posture args
    if ! assert_contains "${tc}/sidecar-has-cap-net-admin" "$sidecar_argv" "--cap-add=NET_ADMIN"; then
        ok=0
    fi
    if ! assert_contains "${tc}/sidecar-has-read-only" "$sidecar_argv" "--read-only"; then
        ok=0
    fi
    if ! assert_contains "${tc}/sidecar-has-cap-drop" "$sidecar_argv" "--cap-drop=all"; then
        ok=0
    fi
    if ! assert_contains "${tc}/sidecar-has-no-new-privs" "$sidecar_argv" "--security-opt=no-new-privileges"; then
        ok=0
    fi

    # REQ-049-03: egress workload run --pod must NOT have --userns
    if ! assert_not_contains "${tc}/workload-no-userns" "$workload_argv" "--userns"; then
        ok=0
    fi

    # REQ-049-03: egress workload must have --pod
    if ! assert_contains "${tc}/workload-has-pod" "$workload_argv" "--pod"; then
        ok=0
    fi

    rm -rf "$stub_dir" "$worktree" "$gate_tools" "$pod_argv_file" "$sidecar_argv_file" "$workload_argv_file"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-049-02: non-pod --probe path keeps --userns=keep-id on the container ─
# REQ-049-03 (non-pod paths unchanged)

run_tc049_02() {
    local tc="TC-049-02"
    local ok=1

    local stub_dir worktree gate_tools allowlist probe_argv_file
    stub_dir="$(make_probe_stub_dir)"
    worktree="$(make_fake_worktree)"
    gate_tools="$(make_fake_gate_tools)"
    allowlist="$(make_fake_probe_allowlist)"
    probe_argv_file="$(mktemp)"

    local _rpa_exit _rpa_stdout _rpa_stderr
    STUB_PROBE_CREATE_ARGV_FILE="$probe_argv_file" \
    run_probe_argv "$stub_dir" "$worktree" "$gate_tools" "$allowlist"

    local probe_argv
    probe_argv="$(cat "$probe_argv_file" 2>/dev/null || true)"

    if [ "$_rpa_exit" -ne 0 ]; then
        tc_fail "${tc}" "expected exit 0 for --probe; got $_rpa_exit; stderr: $_rpa_stderr"
        ok=0
    fi

    # Non-pod --probe path: podman create must still have --userns=keep-id
    if ! assert_contains "${tc}/probe-create-has-userns" "$probe_argv" "--userns=keep-id"; then
        ok=0
    fi

    rm -rf "$stub_dir" "$worktree" "$gate_tools" "$allowlist" "$probe_argv_file"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-049-03: pod members still carry --user <uid>:<gid> ───────────────────
# REQ-049-04 (keep-id uid mapping preserved on pod members)

run_tc049_03() {
    local tc="TC-049-03"
    local ok=1

    local stub_dir worktree gate_tools
    local pod_argv_file sidecar_argv_file workload_argv_file
    stub_dir="$(make_argv_egress_stub_dir)"
    worktree="$(make_fake_worktree)"
    gate_tools="$(make_fake_gate_tools)"
    pod_argv_file="$(mktemp)"
    sidecar_argv_file="$(mktemp)"
    workload_argv_file="$(mktemp)"

    local _rep_exit _rep_stdout _rep_stderr
    STUB_POD_CREATE_ARGV_FILE="$pod_argv_file" \
    STUB_SIDECAR_RUN_ARGV_FILE="$sidecar_argv_file" \
    STUB_WORKLOAD_RUN_ARGV_FILE="$workload_argv_file" \
    run_egress_probe_argv "$stub_dir" "$worktree" "$gate_tools"

    local sidecar_argv workload_argv
    sidecar_argv="$(cat "$sidecar_argv_file" 2>/dev/null || true)"
    workload_argv="$(cat "$workload_argv_file" 2>/dev/null || true)"

    if [ "$_rep_exit" -ne 0 ]; then
        tc_fail "${tc}" "expected exit 0; got $_rep_exit; stderr: $_rep_stderr"
        ok=0
    fi

    # Sidecar must have --user 0:0 (explicitly set on the sidecar run).
    if ! assert_contains "${tc}/sidecar-has-user" "$sidecar_argv" "--user 0:0"; then
        ok=0
    fi

    # Workload must have --user <uid>:<gid> (carried from common_args).
    # We accept any --user N:N pattern (the real uid:gid on this host).
    if ! printf '%s' "$workload_argv" | grep -qE -- '--user [0-9]+:[0-9]+'; then
        tc_fail "${tc}/workload-has-user" "workload argv must contain '--user <uid>:<gid>'; got: $workload_argv"
        ok=0
    fi

    rm -rf "$stub_dir" "$worktree" "$gate_tools" "$pod_argv_file" "$sidecar_argv_file" "$workload_argv_file"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── main ─────────────────────────────────────────────────────────────────────

printf '\n=== userns-pod test harness (TC-049) ===\n\n'

if [ ! -f "$RUN_SH" ]; then
    printf 'ERROR: %s not found\n' "$RUN_SH" >&2
    exit 1
fi

run_tc049_01
run_tc049_02
run_tc049_03

# TC-049-04 (L6, real host): verified by operator via:
#   bash containment/execution-box/run.sh --worktree . --egress-probe; echo "exit=$?"
# Expected: gets PAST pod create + sidecar run -d without "--userns and --pod cannot be set together".
# This test case requires a live rootless podman 5.x environment and is not automated here.

printf '\n=== Results: %d passed, %d failed ===\n' "$PASS_COUNT" "$FAIL_COUNT"

if [ "$FAIL_COUNT" -gt 0 ]; then
    printf '\nFailed test cases:\n'
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f"
    done
    exit 1
fi

exit 0
