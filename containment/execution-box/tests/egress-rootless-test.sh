#!/usr/bin/env bash
# Test harness for TC-050: rootless egress sidecar fix (idempotent nftables + writable egress-state).
#
# Verifies REQ-050-01 through REQ-050-04:
#   TC-050-01  egress-sidecar.sh emits idempotent ruleset: empty table decl before flush before populated table.
#   TC-050-02  populated table is otherwise unchanged (set, policy, accepts, reject preserved).
#   TC-050-03  run.sh makes egress-state dir world-writable (0777) at sidecar launch.
#
# TC-050-04 (L6, real host) is documented but not automated; it requires a live rootless podman 5.x.
#
# Usage: bash containment/execution-box/tests/egress-rootless-test.sh
# Exit 0 on all pass; non-zero on any failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
EGRESS_SIDECAR="$REPO_ROOT/containment/execution-box/egress-sidecar.sh"
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
    if ! printf '%s' "$haystack" | grep -qF -- "$needle"; then
        tc_fail "$tc" "expected output to contain '${needle}'; got: ${haystack}"
        return 1
    fi
    return 0
}

# ─── fixture factories ────────────────────────────────────────────────────────

make_stub_nft() {
    local tmpdir capture_file
    tmpdir="$(mktemp -d)"
    capture_file="$(mktemp)"

    cat > "$tmpdir/nft" <<'NFT_STUB'
#!/bin/bash
# Stub nft that captures -f <file> contents to a capture path
if [ "$1" = "-f" ]; then
    capture_file="${STUB_NFT_CAPTURE_FILE:-}"
    if [ -n "$capture_file" ]; then
        cat "$2" > "$capture_file"
    fi
fi
exit 0
NFT_STUB
    chmod +x "$tmpdir/nft"

    printf '%s' "$tmpdir"
}

make_stub_resolved_allowlist() {
    local tmpfile
    tmpfile="$(mktemp)"
    # Resolved format (for egress-sidecar.sh): host port ip (3 columns)
    printf 'api.github.com 443 1.2.3.4\n' > "$tmpfile"
    printf '%s' "$tmpfile"
}

make_stub_raw_allowlist() {
    local tmpfile
    tmpfile="$(mktemp)"
    # Raw allowlist format (for run.sh): host:port # comment
    printf 'api.github.com:443 # GitHub API\n' > "$tmpfile"
    printf '%s' "$tmpfile"
}

# ─── TC-050-01: idempotent ruleset (empty table before flush before populated) ─
# REQ-050-01

run_tc050_01() {
    local tc="TC-050-01"
    local ok=1

    local stub_nft_dir state_dir allowlist capture_file rules_file
    stub_nft_dir="$(make_stub_nft)"
    state_dir="$(mktemp -d)"
    allowlist="$(make_stub_resolved_allowlist)"
    capture_file="$(mktemp)"
    rules_file="$(mktemp)"

    local exit_code=0
    env PATH="$stub_nft_dir:$PATH" \
        EXEC_BOX_RESOLVED_EGRESS_ALLOWLIST="$allowlist" \
        EXEC_BOX_EGRESS_STATE_DIR="$state_dir" \
        EXEC_BOX_EGRESS_RULES="$rules_file" \
        EXEC_BOX_EGRESS_SIDECAR_TEST_EXIT=1 \
        STUB_NFT_CAPTURE_FILE="$capture_file" \
        bash "$EGRESS_SIDECAR" \
        > /dev/null 2>&1 || exit_code=$?

    if [ "$exit_code" -ne 0 ]; then
        tc_fail "${tc}" "sidecar exited non-zero ($exit_code); expected 0"
        ok=0
    fi

    if [ ! -f "$capture_file" ]; then
        tc_fail "${tc}" "nft capture file not created"
        ok=0
    else
        local ruleset
        ruleset="$(cat "$capture_file")"

        # Check that empty table declaration comes first
        local empty_table_line populated_table_line flush_line
        empty_table_line="$(printf '%s' "$ruleset" | grep -n 'table inet agent_builder_egress { }' | head -1 | cut -d: -f1 || true)"
        flush_line="$(printf '%s' "$ruleset" | grep -n 'flush table inet agent_builder_egress' | head -1 | cut -d: -f1 || true)"
        populated_table_line="$(printf '%s' "$ruleset" | grep -n 'table inet agent_builder_egress {' | tail -1 | cut -d: -f1 || true)"

        if [ -z "$empty_table_line" ]; then
            tc_fail "${tc}/empty-table-not-found" "empty 'table inet agent_builder_egress { }' declaration not found in ruleset"
            ok=0
        fi

        if [ -z "$flush_line" ]; then
            tc_fail "${tc}/flush-not-found" "'flush table inet agent_builder_egress' not found in ruleset"
            ok=0
        fi

        if [ -z "$populated_table_line" ]; then
            tc_fail "${tc}/populated-table-not-found" "populated 'table inet agent_builder_egress {' block not found in ruleset"
            ok=0
        fi

        # Check ordering: empty < flush < populated
        if [ -n "$empty_table_line" ] && [ -n "$flush_line" ] && [ -n "$populated_table_line" ]; then
            if [ "$empty_table_line" -gt "$flush_line" ]; then
                tc_fail "${tc}/order-empty-flush" "empty table declaration (line $empty_table_line) must come before flush (line $flush_line)"
                ok=0
            fi
            if [ "$flush_line" -gt "$populated_table_line" ]; then
                tc_fail "${tc}/order-flush-populated" "flush (line $flush_line) must come before populated table (line $populated_table_line)"
                ok=0
            fi
        fi
    fi

    # Check that ready marker was written
    if [ ! -f "$state_dir/ready" ]; then
        tc_fail "${tc}/ready-marker" "ready marker not written to $state_dir/ready"
        ok=0
    fi

    rm -rf "$stub_nft_dir" "$state_dir" "$allowlist" "$capture_file" "$rules_file"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-050-02: allow/deny semantics unchanged ────────────────────────────────
# REQ-050-02

run_tc050_02() {
    local tc="TC-050-02"
    local ok=1

    local stub_nft_dir state_dir allowlist capture_file rules_file
    stub_nft_dir="$(make_stub_nft)"
    state_dir="$(mktemp -d)"
    allowlist="$(make_stub_resolved_allowlist)"
    capture_file="$(mktemp)"
    rules_file="$(mktemp)"

    env PATH="$stub_nft_dir:$PATH" \
        EXEC_BOX_RESOLVED_EGRESS_ALLOWLIST="$allowlist" \
        EXEC_BOX_EGRESS_STATE_DIR="$state_dir" \
        EXEC_BOX_EGRESS_RULES="$rules_file" \
        EXEC_BOX_EGRESS_SIDECAR_TEST_EXIT=1 \
        STUB_NFT_CAPTURE_FILE="$capture_file" \
        bash "$EGRESS_SIDECAR" \
        > /dev/null 2>&1 || true

    if [ ! -f "$capture_file" ]; then
        tc_fail "${tc}" "nft capture file not created"
        ok=0
    else
        local ruleset
        ruleset="$(cat "$capture_file")"

        # Check all required elements in the populated table are present and unchanged
        if ! assert_contains "${tc}/set-allowed-tcp4" "$ruleset" "set allowed_tcp4"; then
            ok=0
        fi

        if ! assert_contains "${tc}/type-ipv4-service" "$ruleset" "type ipv4_addr . inet_service"; then
            ok=0
        fi

        if ! assert_contains "${tc}/flags-interval" "$ruleset" "flags interval"; then
            ok=0
        fi

        if ! assert_contains "${tc}/type-filter-hook-output" "$ruleset" "type filter hook output priority 0; policy drop;"; then
            ok=0
        fi

        if ! assert_contains "${tc}/lo-accept" "$ruleset" "oifname \"lo\" accept"; then
            ok=0
        fi

        if ! assert_contains "${tc}/established-accept" "$ruleset" "ct state established,related accept"; then
            ok=0
        fi

        if ! assert_contains "${tc}/allow-rule" "$ruleset" "ip daddr . tcp dport @allowed_tcp4 accept"; then
            ok=0
        fi

        if ! assert_contains "${tc}/reject" "$ruleset" "reject"; then
            ok=0
        fi
    fi

    rm -rf "$stub_nft_dir" "$state_dir" "$allowlist" "$capture_file" "$rules_file"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── TC-050-03: egress-state dir is world-writable (0777) ──────────────────────
# REQ-050-03

run_tc050_03() {
    local tc="TC-050-03"
    local ok=1

    local stub_dir worktree gate_tools
    stub_dir="$(mktemp -d)"
    worktree="$(mktemp -d)"
    gate_tools="$(mktemp -d)"

    # Create stub podman that captures the mode of the egress-state source dir
    cat > "$stub_dir/podman" <<'PODMAN_STUB'
#!/bin/bash
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
        exit 0
        ;;
    run)
        # This is the sidecar run -d. Capture the egress-state source dir mode.
        if [ "${1:-}" = "-d" ]; then
            for arg in "$@"; do
                case "$arg" in
                    type=bind,source=*,target=/egress-state,*)
                        egress_state_source="${arg#type=bind,source=}"
                        egress_state_source="${egress_state_source%%,target=*}"
                        # Capture the mode (use perl for portability; fallback to ls)
                        if [ -n "${STUB_EGRESS_STATE_MODE_FILE:-}" ]; then
                            # Try perl first, fallback to ls awk
                            mode=$(perl -e "printf '%o', (stat('$egress_state_source'))[2] & 07777" 2>/dev/null || ls -ld "$egress_state_source" | awk '{print substr($1, 2, 3)}')
                            printf '%s\n' "$mode" > "$STUB_EGRESS_STATE_MODE_FILE"
                        fi
                        # Create the ready marker so run.sh proceeds
                        touch "$egress_state_source/ready"
                        break
                        ;;
                esac
            done
        fi
        exit 0
        ;;
    rm)
        exit 0
        ;;
    logs)
        exit 0
        ;;
    *)
        exit 0
        ;;
esac
PODMAN_STUB
    chmod +x "$stub_dir/podman"

    # Create stub gate tools
    for tool in golangci-lint gods code-scanner; do
        printf '#!/bin/sh\nexit 0\n' > "$gate_tools/$tool"
        chmod +x "$gate_tools/$tool"
    done

    # Create stub runsc
    printf '#!/bin/sh\nexit 0\n' > "$stub_dir/runsc"
    chmod +x "$stub_dir/runsc"

    # Create a fake allowlist (raw format for run.sh)
    local allowlist
    allowlist="$(make_stub_raw_allowlist)"

    # Create a mode capture file
    local mode_file
    mode_file="$(mktemp)"

    # Run run.sh --egress-probe with the stub podman
    env PATH="$stub_dir:$PATH" \
        EXEC_BOX_EGRESS_ALLOWLIST="$allowlist" \
        EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0 \
        EXEC_BOX_STORAGE_SIZE="" \
        STUB_EGRESS_STATE_MODE_FILE="$mode_file" \
        bash "$RUN_SH" \
            --egress-probe \
            --worktree "$worktree" \
            --gate-tools "$gate_tools" \
            --image "stub-image:test" \
            --runtime "runsc" \
        > /dev/null 2>&1 || true

    # Check that the mode file was written and contains 777
    if [ ! -f "$mode_file" ]; then
        tc_fail "${tc}" "mode file not created; stub podman may not have been invoked"
        ok=0
    else
        local captured_mode
        captured_mode="$(cat "$mode_file")"
        if [ "$captured_mode" != "777" ]; then
            tc_fail "${tc}/mode-not-0777" "expected egress-state dir mode 777; got $captured_mode"
            ok=0
        fi
    fi

    rm -rf "$stub_dir" "$worktree" "$gate_tools" "$allowlist" "$mode_file"

    [ "$ok" -eq 1 ] && tc_pass "$tc"
}

# ─── main ─────────────────────────────────────────────────────────────────────

printf '\n=== egress-rootless test harness (TC-050) ===\n\n'

if [ ! -f "$EGRESS_SIDECAR" ]; then
    printf 'ERROR: %s not found\n' "$EGRESS_SIDECAR" >&2
    exit 1
fi

if [ ! -f "$RUN_SH" ]; then
    printf 'ERROR: %s not found\n' "$RUN_SH" >&2
    exit 1
fi

run_tc050_01
run_tc050_02
run_tc050_03

# TC-050-04 (L6, real host): verified by operator via:
#   bash containment/execution-box/run.sh --worktree . --egress-probe; echo "exit=$?"
# Expected output (in order):
#   TC-001 PASS: egress sidecar installed nftables default-deny output policy
#   TC-003 PASS: allowlisted connect succeeded: <host>:<port>
#   TC-004 PASS: non-allowlisted connect blocked: <host>:<port>
#   TC-004 PASS: direct IP bypass blocked: <ip>:<port>
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
