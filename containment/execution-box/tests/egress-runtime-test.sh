#!/usr/bin/env bash
# Test harness for TC-051: egress path runs under runc; explicit runsc fails loud; --add-host on pod.
#
# Verifies REQ-051-01 through REQ-051-05:
#   TC-051-01  default (no --runtime) + --egress-probe → workload argv has
#              --runtime runc AND --label agent-builder.runtime=runc (silent resolve from runsc)
#   TC-051-02  --runtime runsc --egress-probe → dies with ADR 030 message, non-zero,
#              no workload podman run issued
#   TC-051-03  --runtime runc --egress-probe → workload argv has --runtime runc; no die
#   TC-051-04  --add-host H:IP entries appear on podman pod create argv and NOT on
#              egress workload podman run argv
#   TC-051-05  plain --probe (non-pod) → container still uses runsc; egress runc override
#              does NOT leak to non-egress paths
#
# TC-051-06 (L6, real host) is documented but not automated; it requires a
# live rootless podman 5.x environment.
#
# Usage: bash containment/execution-box/tests/egress-runtime-test.sh
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
    for tool in golangci-lint dep-scan code-scanner; do
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

make_allowlist_with_entry() {
    local tmpfile
    tmpfile="$(mktemp)"
    printf 'api.github.com:443 # GitHub API\n' > "$tmpfile"
    printf '%s' "$tmpfile"
}

# ─── egress stub: per-subcommand argv recording ─────────────────────────────
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
# Stub podman — per-subcommand argv recording for TC-051.

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
        printf 'stub-container-id-051\n'
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
                printf 'runc\n'
                ;;
            *)
                printf 'runc\n'
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

# ─── probe-path stub (for TC-051-05: non-pod --probe) ────────────────────────
#
# make_probe_stub_dir creates a stub with argv recording for podman create,
# plus all the usual TC-003/TC-016 inspect responses. The non-pod path should
# keep the agent-tier runsc default unchanged.

make_probe_stub_dir() {
    local tmpdir
    tmpdir="$(mktemp -d)"

    cat > "$tmpdir/podman" <<'PODMAN_PROBE_STUB'
#!/bin/bash
# Stub podman for --probe path (TC-051-05).

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
        printf 'stub-container-probe-id-051\n'
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

# run_egress_probe_argv STUB_DIR WORKTREE GATE_TOOLS RUNTIME ALLOWLIST
#   Runs run.sh --egress-probe with per-subcommand argv capture.
#   RUNTIME can be empty (default) or "runsc" or "runc".
#   The caller must export STUB_POD_CREATE_ARGV_FILE, STUB_SIDECAR_RUN_ARGV_FILE,
#   STUB_WORKLOAD_RUN_ARGV_FILE before calling.
#   Sets: _rep_exit, _rep_stdout, _rep_stderr
run_egress_probe_argv() {
    local stub_dir="$1" worktree="$2" gate_tools="$3" runtime="$4" allowlist="$5"

    local stdout_file stderr_file
    stdout_file="$(mktemp)"
    stderr_file="$(mktemp)"

    _rep_exit=0

    local cmd_args=(
        --egress-probe
        --worktree "$worktree"
        --gate-tools "$gate_tools"
        --image "stub-image:test"
        --egress-allowlist "$allowlist"
    )

    if [ -n "$runtime" ]; then
        cmd_args+=(--runtime "$runtime")
    fi

    env PATH="$stub_dir:$PATH" \
        EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 \
        EXEC_BOX_STORAGE_SIZE="" \
        STUB_POD_CREATE_ARGV_FILE="${STUB_POD_CREATE_ARGV_FILE:-}" \
        STUB_SIDECAR_RUN_ARGV_FILE="${STUB_SIDECAR_RUN_ARGV_FILE:-}" \
        STUB_WORKLOAD_RUN_ARGV_FILE="${STUB_WORKLOAD_RUN_ARGV_FILE:-}" \
        bash "$RUN_SH" \
            "${cmd_args[@]}" \
        > "$stdout_file" 2> "$stderr_file" \
        || _rep_exit=$?

    _rep_stdout="$(cat "$stdout_file")"
    _rep_stderr="$(cat "$stderr_file")"

    rm -f "$stdout_file" "$stderr_file"
}

# run_probe_argv STUB_DIR WORKTREE GATE_TOOLS
#   Runs run.sh --probe (non-pod) with argv capture for podman create.
#   The caller must export STUB_PROBE_CREATE_ARGV_FILE before calling.
#   Sets: _rpa_exit, _rpa_stdout, _rpa_stderr
run_probe_argv() {
    local stub_dir="$1" worktree="$2" gate_tools="$3"

    local stdout_file stderr_file
    stdout_file="$(mktemp)"
    stderr_file="$(mktemp)"

    _rpa_exit=0
    env PATH="$stub_dir:$PATH" \
        EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 \
        EXEC_BOX_STORAGE_SIZE="" \
        STUB_PROBE_CREATE_ARGV_FILE="${STUB_PROBE_CREATE_ARGV_FILE:-}" \
        bash "$RUN_SH" \
            --probe \
            --worktree "$worktree" \
            --gate-tools "$gate_tools" \
            --image "stub-image:test" \
        > "$stdout_file" 2> "$stderr_file" \
        || _rpa_exit=$?

    _rpa_stdout="$(cat "$stdout_file")"
    _rpa_stderr="$(cat "$stderr_file")"

    rm -f "$stdout_file" "$stderr_file"
}

# ─── TC-051-01: default (no --runtime) + --egress-probe: workload runs runc ───
# REQ-051-01

run_tc051_01() {
    local tc="TC-051-01"
    local ok=1

    local stub_dir worktree gate_tools allowlist
    local workload_argv_file
    stub_dir="$(make_argv_egress_stub_dir)"
    worktree="$(make_fake_worktree)"
    gate_tools="$(make_fake_gate_tools)"
    allowlist="$(make_allowlist_with_entry)"
    workload_argv_file="$(mktemp)"

    local _rep_exit _rep_stdout _rep_stderr
    STUB_WORKLOAD_RUN_ARGV_FILE="$workload_argv_file" \
    run_egress_probe_argv "$stub_dir" "$worktree" "$gate_tools" "" "$allowlist"

    local workload_argv
    workload_argv="$(cat "$workload_argv_file" 2>/dev/null || true)"

    if [ "$_rep_exit" -ne 0 ]; then
        tc_fail "${tc}" "expected exit 0; got $_rep_exit; stderr: $_rep_stderr"
        ok=0
    fi

    # REQ-051-01: workload must have --runtime runc (not runsc)
    if ! assert_contains "${tc}/workload-has-runtime-runc" "$workload_argv" "--runtime runc"; then
        ok=0
    fi

    # REQ-051-01: workload must have --label agent-builder.runtime=runc
    if ! assert_contains "${tc}/workload-has-label-runc" "$workload_argv" "--label agent-builder.runtime=runc"; then
        ok=0
    fi

    # Ensure it does NOT have --runtime runsc
    if ! assert_not_contains "${tc}/workload-not-runsc" "$workload_argv" "--runtime runsc"; then
        ok=0
    fi

    # REQ-051-01: the stale agent-builder.runtime=runsc label from common_args must NOT
    # survive onto the egress workload (only the substituted runc label is correct — a
    # contradictory runtime label would mislead audit). Guards the two-element --label fix.
    if ! assert_not_contains "${tc}/workload-no-stale-runsc-label" "$workload_argv" "agent-builder.runtime=runsc"; then
        ok=0
    fi

    rm -rf "$stub_dir" "$worktree" "$gate_tools" "$allowlist" "$workload_argv_file"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-051-02: explicit --runtime runsc + --egress-probe: die loudly ────────
# REQ-051-02

run_tc051_02() {
    local tc="TC-051-02"
    local ok=1

    local stub_dir worktree gate_tools allowlist
    local workload_argv_file
    stub_dir="$(make_argv_egress_stub_dir)"
    worktree="$(make_fake_worktree)"
    gate_tools="$(make_fake_gate_tools)"
    allowlist="$(make_allowlist_with_entry)"
    workload_argv_file="$(mktemp)"

    local _rep_exit _rep_stdout _rep_stderr
    STUB_WORKLOAD_RUN_ARGV_FILE="$workload_argv_file" \
    run_egress_probe_argv "$stub_dir" "$worktree" "$gate_tools" "runsc" "$allowlist"

    local workload_argv
    workload_argv="$(cat "$workload_argv_file" 2>/dev/null || true)"

    # REQ-051-02: must exit non-zero
    if [ "$_rep_exit" -eq 0 ]; then
        tc_fail "${tc}" "expected non-zero exit; got $_rep_exit"
        ok=0
    fi

    # REQ-051-02: stderr must mention ADR 030
    if ! assert_contains "${tc}/stderr-names-adr030" "$_rep_stderr" "ADR 030"; then
        ok=0
    fi

    # REQ-051-02: stderr must mention the rootless-pod-userns limitation or gVisor
    if ! printf '%s' "$_rep_stderr" | grep -qiE "(gvisor|gofer|userns|user namespace)"; then
        tc_fail "${tc}/stderr-mentions-limitation" "stderr should mention gVisor/gofer/userns limitation; got: $_rep_stderr"
        ok=0
    fi

    # REQ-051-02: NO workload podman run should have been issued
    if [ -s "$workload_argv_file" ]; then
        tc_fail "${tc}/no-workload-run" "workload argv file should be empty (no run issued); got: $workload_argv"
        ok=0
    fi

    rm -rf "$stub_dir" "$worktree" "$gate_tools" "$allowlist" "$workload_argv_file"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-051-03: explicit --runtime runc + --egress-probe: runs runc, no die ───
# REQ-051-03

run_tc051_03() {
    local tc="TC-051-03"
    local ok=1

    local stub_dir worktree gate_tools allowlist
    local workload_argv_file
    stub_dir="$(make_argv_egress_stub_dir)"
    worktree="$(make_fake_worktree)"
    gate_tools="$(make_fake_gate_tools)"
    allowlist="$(make_allowlist_with_entry)"
    workload_argv_file="$(mktemp)"

    local _rep_exit _rep_stdout _rep_stderr
    STUB_WORKLOAD_RUN_ARGV_FILE="$workload_argv_file" \
    run_egress_probe_argv "$stub_dir" "$worktree" "$gate_tools" "runc" "$allowlist"

    local workload_argv
    workload_argv="$(cat "$workload_argv_file" 2>/dev/null || true)"

    # REQ-051-03: must exit 0
    if [ "$_rep_exit" -ne 0 ]; then
        tc_fail "${tc}" "expected exit 0; got $_rep_exit; stderr: $_rep_stderr"
        ok=0
    fi

    # REQ-051-03: workload must have --runtime runc
    if ! assert_contains "${tc}/workload-has-runtime-runc" "$workload_argv" "--runtime runc"; then
        ok=0
    fi

    rm -rf "$stub_dir" "$worktree" "$gate_tools" "$allowlist" "$workload_argv_file"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-051-04: --add-host on pod create, not on workload member ─────────────
# REQ-051-04

run_tc051_04() {
    local tc="TC-051-04"
    local ok=1

    local stub_dir worktree gate_tools allowlist
    local pod_argv_file workload_argv_file
    stub_dir="$(make_argv_egress_stub_dir)"
    worktree="$(make_fake_worktree)"
    gate_tools="$(make_fake_gate_tools)"
    allowlist="$(make_allowlist_with_entry)"
    pod_argv_file="$(mktemp)"
    workload_argv_file="$(mktemp)"

    local _rep_exit _rep_stdout _rep_stderr
    STUB_POD_CREATE_ARGV_FILE="$pod_argv_file" \
    STUB_WORKLOAD_RUN_ARGV_FILE="$workload_argv_file" \
    run_egress_probe_argv "$stub_dir" "$worktree" "$gate_tools" "" "$allowlist"

    local pod_argv workload_argv
    pod_argv="$(cat "$pod_argv_file" 2>/dev/null || true)"
    workload_argv="$(cat "$workload_argv_file" 2>/dev/null || true)"

    if [ "$_rep_exit" -ne 0 ]; then
        tc_fail "${tc}" "expected exit 0; got $_rep_exit; stderr: $_rep_stderr"
        ok=0
    fi

    # REQ-051-04: pod create argv must have at least one --add-host entry
    if ! printf '%s' "$pod_argv" | grep -q -- '--add-host'; then
        tc_fail "${tc}/pod-has-add-host" "pod create argv should contain '--add-host'; got: $pod_argv"
        ok=0
    fi

    # REQ-051-04: workload argv must NOT have --add-host
    if ! assert_not_contains "${tc}/workload-no-add-host" "$workload_argv" "--add-host"; then
        ok=0
    fi

    rm -rf "$stub_dir" "$worktree" "$gate_tools" "$allowlist" "$pod_argv_file" "$workload_argv_file"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-051-05: non-pod --probe keeps the agent default (runsc) unchanged ────
# REQ-051-05 (regression: egress runc override does NOT leak to non-pod paths)

run_tc051_05() {
    local tc="TC-051-05"
    local ok=1

    local stub_dir worktree gate_tools
    local probe_argv_file
    stub_dir="$(make_probe_stub_dir)"
    worktree="$(make_fake_worktree)"
    gate_tools="$(make_fake_gate_tools)"
    probe_argv_file="$(mktemp)"

    local _rpa_exit _rpa_stdout _rpa_stderr
    STUB_PROBE_CREATE_ARGV_FILE="$probe_argv_file" \
    run_probe_argv "$stub_dir" "$worktree" "$gate_tools"

    local probe_argv
    probe_argv="$(cat "$probe_argv_file" 2>/dev/null || true)"

    if [ "$_rpa_exit" -ne 0 ]; then
        tc_fail "${tc}" "expected exit 0 for --probe; got $_rpa_exit; stderr: $_rpa_stderr"
        ok=0
    fi

    # REQ-051-05: non-pod --probe container create must still have --runtime runsc
    # (the agent-tier default, unchanged by egress runc override)
    if ! assert_contains "${tc}/probe-create-has-runsc" "$probe_argv" "--runtime runsc"; then
        ok=0
    fi

    rm -rf "$stub_dir" "$worktree" "$gate_tools" "$probe_argv_file"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── main ─────────────────────────────────────────────────────────────────────

printf '\n=== egress-runtime test harness (TC-051) ===\n\n'

if [ ! -f "$RUN_SH" ]; then
    printf 'ERROR: %s not found\n' "$RUN_SH" >&2
    exit 1
fi

run_tc051_01
run_tc051_02
run_tc051_03
run_tc051_04
run_tc051_05

# TC-051-06 (L6, real host): verified by operator via:
#   bash containment/execution-box/run.sh --worktree . --egress-probe; echo "exit=$?"
# Expected: default → TC-003 PASS + TC-004 PASS + exit 0
#           --runtime runsc --egress-probe → ADR 030 die + non-zero exit
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
