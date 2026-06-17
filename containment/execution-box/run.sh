#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat <<'EOF'
Usage:
  containment/execution-box/run.sh [--worktree PATH] [--workload agent|dev] [--runtime runc|runsc|kata] [--gate-tools PATH] [--probe] [--egress-probe] [--egress-allowlist PATH] [--print-runtime-plan] [--print-egress-plan] [--print-toolchain-plan] [--name NAME] [--image IMAGE] [-- COMMAND...]

Runs a target repo worktree inside the rootless Podman execution-box profile.

Options:
  --worktree PATH          host repo worktree to mount at /work (default: current directory)
  --workload agent|dev     workload tier for default runtime mapping (default: agent)
  --runtime NAME           OCI runtime passed to Podman --runtime (overrides workload default)
  --gate-tools PATH        directory containing golangci-lint, gods, and code-scanner (default: containment/execution-box/gate-tools)
  --probe                  run the runtime containment probe instead of an interactive shell
  --egress-probe           run the runtime egress allowlist probe
  --egress-allowlist PATH  plain-text host:port allowlist (default: containment/execution-box/egress.allowlist)
  --egress-allow-host H:P  allowlisted host:port expected to succeed during --egress-probe
  --egress-deny-host H:P   non-allowlisted host:port expected to fail during --egress-probe
  --egress-deny-ip H:P     direct IP literal host:port expected to fail during --egress-probe
  --print-runtime-plan     print selected runtime without requiring Podman
  --print-egress-plan      parse and print the allowlist without requiring Podman
  --print-toolchain-plan   validate and print the Gate toolchain plan without requiring Podman
  --name NAME              container name prefix (default: agent-builder-execution-box)
  --image IMAGE            image tag to build/use (default: localhost/agent-builder/execution-box:033)

Environment (storage quota):
  EXEC_BOX_STORAGE_SIZE    size string (default: 4G); empty string disables the quota silently (operator opt-out)
  EXEC_BOX_STORAGE_QUOTA_SUPPORTED  testability seam: set to "1" to force quota enforcement (XFS path),
                           set to "0" to force the graceful-degrade path (non-XFS), or leave unset to
                           run real detection via 'podman info'. Never set this in production.
EOF
}

die() {
    printf 'execution-box: %s\n' "$*" >&2
    exit 1
}

box_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
worktree="$(pwd)"
probe=false
egress_probe=false
print_runtime_plan=false
print_egress_plan=false
print_toolchain_plan=false
name="agent-builder-execution-box"
image="${EXEC_BOX_IMAGE:-localhost/agent-builder/execution-box:033}"
workload="${EXEC_BOX_WORKLOAD:-agent}"
runtime_override="${EXEC_BOX_RUNTIME:-}"
runtime_source="default"
[ -n "$runtime_override" ] && runtime_source="env"
egress_allowlist="${EXEC_BOX_EGRESS_ALLOWLIST:-$box_dir/egress.allowlist}"
gate_tools="${EXEC_BOX_GATE_TOOLS:-$box_dir/gate-tools}"
gate_tool_mount="/opt/agent-builder/gate-tools"
gate_tool_path="$gate_tool_mount:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
egress_allow_host="${EXEC_BOX_EGRESS_PROBE_ALLOW_HOST:-api.github.com:443}"
egress_deny_host="${EXEC_BOX_EGRESS_PROBE_DENY_HOST:-example.com:443}"
egress_deny_ip="${EXEC_BOX_EGRESS_PROBE_DENY_IP:-1.1.1.1:443}"

while [ "$#" -gt 0 ]; do
    case "$1" in
        --worktree)
            [ "$#" -ge 2 ] || die '--worktree requires a path'
            worktree="$2"
            shift 2
            ;;
        --workload)
            [ "$#" -ge 2 ] || die '--workload requires agent or dev'
            workload="$2"
            shift 2
            ;;
        --runtime)
            [ "$#" -ge 2 ] || die '--runtime requires a value'
            runtime_override="$2"
            runtime_source="flag"
            shift 2
            ;;
        --gate-tools)
            [ "$#" -ge 2 ] || die '--gate-tools requires a path'
            gate_tools="$2"
            shift 2
            ;;
        --probe)
            probe=true
            shift
            ;;
        --egress-probe)
            egress_probe=true
            shift
            ;;
        --egress-allowlist)
            [ "$#" -ge 2 ] || die '--egress-allowlist requires a path'
            egress_allowlist="$2"
            shift 2
            ;;
        --egress-allow-host)
            [ "$#" -ge 2 ] || die '--egress-allow-host requires a host:port value'
            egress_allow_host="$2"
            shift 2
            ;;
        --egress-deny-host)
            [ "$#" -ge 2 ] || die '--egress-deny-host requires a host:port value'
            egress_deny_host="$2"
            shift 2
            ;;
        --egress-deny-ip)
            [ "$#" -ge 2 ] || die '--egress-deny-ip requires a host:port value'
            egress_deny_ip="$2"
            shift 2
            ;;
        --print-runtime-plan)
            print_runtime_plan=true
            shift
            ;;
        --print-egress-plan)
            print_egress_plan=true
            shift
            ;;
        --print-toolchain-plan)
            print_toolchain_plan=true
            shift
            ;;
        --name)
            [ "$#" -ge 2 ] || die '--name requires a value'
            name="$2"
            shift 2
            ;;
        --image)
            [ "$#" -ge 2 ] || die '--image requires a value'
            image="$2"
            shift 2
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        --)
            shift
            break
            ;;
        -*)
            die "unknown option: $1"
            ;;
        *)
            break
            ;;
    esac
done

default_runtime_for_workload() {
    case "$1" in
        agent)
            printf 'runsc'
            ;;
        dev)
            printf 'runc'
            ;;
        *)
            die "unknown workload tier: $1"
            ;;
    esac
}

validate_runtime_name() {
    case "$1" in
        runc|runsc|kata)
            ;;
        *)
            die "unknown OCI runtime: $1"
            ;;
    esac
}

resolve_runtime() {
    local selected

    case "$workload" in
        agent|dev)
            ;;
        *)
            die "unknown workload tier: $workload"
            ;;
    esac

    if [ -n "$runtime_override" ]; then
        selected="$runtime_override"
    else
        selected="$(default_runtime_for_workload "$workload")"
    fi
    validate_runtime_name "$selected"
    printf '%s' "$selected"
}

print_runtime_plan_file() {
    printf 'TC-016 PLAN: workload=%s runtime=%s source=%s\n' "$workload" "$runtime" "$runtime_source"
}

required_mounted_gate_tools() {
    printf '%s\n' golangci-lint gods code-scanner
}

resolve_gate_tools() {
    [ -d "$gate_tools" ] || die "Gate toolchain directory does not exist: $gate_tools"
    gate_tools="$(cd "$gate_tools" && pwd)"

    local tool
    while read -r tool; do
        [ -n "$tool" ] || continue
        [ -x "$gate_tools/$tool" ] || die "missing Gate tool $tool in $gate_tools"
    done <<EOF
$(required_mounted_gate_tools)
EOF
}

tool_version_line() {
    local tool="$1"
    local path="$2"
    local version

    version="$("$path" --version 2>&1 | sed -n '1p' || true)"
    [ -n "$version" ] || version="path-only"
    printf 'TC-002 PLAN: %s version=%s\n' "$tool" "$version"
}

print_toolchain_plan_file() {
    local tool

    printf 'TC-001 PLAN: base-image go on PATH\n'
    printf 'TC-001 PLAN: base-image gofmt on PATH\n'
    while read -r tool; do
        [ -n "$tool" ] || continue
        printf 'TC-001 PLAN: mount %s=%s\n' "$tool" "$gate_tools/$tool"
        tool_version_line "$tool" "$gate_tools/$tool"
    done <<EOF
$(required_mounted_gate_tools)
EOF
    printf 'TC-002 PLAN: mounted Gate tools are read-only at %s and require no in-box network fetch\n' "$gate_tool_mount"
}

validate_runtime_available() {
    local runtime_name="$1"
    local known_runtimes

    known_runtimes="$(podman info --format '{{json .Host.OCIRuntimes}}' 2>/dev/null || true)"
    if [ -n "$known_runtimes" ]; then
        case "$known_runtimes" in
            *\""$runtime_name"\"*)
                return 0
                ;;
        esac
    fi

    if command -v "$runtime_name" >/dev/null 2>&1; then
        return 0
    fi

    die "OCI runtime unavailable to Podman or PATH: $runtime_name"
}

# detect_storage_quota_supported: returns 0 (supported) or 1 (not supported).
# Checks whether the rootless overlay container store's backing filesystem
# can enforce per-container size quotas via --storage-opt size=...
#
# Testability seam: if EXEC_BOX_STORAGE_QUOTA_SUPPORTED is set to "1" or "0",
# that value short-circuits the real detection. Never set this in production.
detect_storage_quota_supported() {
    # Testability seam: override real detection if the env var is set.
    case "${EXEC_BOX_STORAGE_QUOTA_SUPPORTED:-}" in
        1)
            return 0
            ;;
        0)
            return 1
            ;;
    esac

    # Real detection: parse the graph-driver name and backing FS from podman info.
    # Podman's overlay driver only supports --storage-opt size= on XFS backing stores.
    local graph_driver graph_root backing_fs
    graph_driver="$(podman info --format '{{.Store.GraphDriverName}}' 2>/dev/null || true)"
    graph_root="$(podman info --format '{{.Store.GraphRoot}}' 2>/dev/null || true)"

    # If we can't determine the driver, err on the side of omitting the flag.
    [ -n "$graph_driver" ] || return 1
    [ "$graph_driver" = "overlay" ] || return 1

    # Determine the backing filesystem of the graph root.
    if [ -n "$graph_root" ]; then
        backing_fs="$(stat -f -c '%T' "$graph_root" 2>/dev/null || true)"
        case "$backing_fs" in
            xfs)
                return 0
                ;;
        esac
    fi

    return 1
}

runtime="$(resolve_runtime)"

if [ "$print_runtime_plan" = true ]; then
    print_runtime_plan_file
    exit 0
fi

trim() {
    local value="$1"
    value="${value#"${value%%[![:space:]]*}"}"
    value="${value%"${value##*[![:space:]]}"}"
    printf '%s' "$value"
}

validate_host_port() {
    local value="$1"
    local label="$2"
    local host port

    case "$value" in
        *://*|*/*|*\\*|*\**|*/*|*%*|*' '*|*$'\t'*)
            die "$label must be plain host:port, got: $value"
            ;;
        *:*)
            host="${value%:*}"
            port="${value##*:}"
            ;;
        *)
            die "$label must include an explicit port: $value"
            ;;
    esac

    [ -n "$host" ] || die "$label has an empty host"
    [ -n "$port" ] || die "$label has an empty port"
    if [[ ! "$host" =~ ^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$ ]]; then
        die "$label has an invalid hostname: $host"
    fi
    case "$host" in
        [0-9]*.[0-9]*.[0-9]*.[0-9]*)
            die "$label does not accept IP literals: $host"
            ;;
    esac
    case "$port" in
        ''|*[!0-9]*)
            die "$label has a non-numeric port: $port"
            ;;
    esac
    [ "$port" -ge 1 ] && [ "$port" -le 65535 ] || die "$label port out of range: $port"

    printf '%s %s\n' "$(printf '%s' "$host" | tr '[:upper:]' '[:lower:]')" "$port"
}

validate_probe_target() {
    local value="$1"
    local label="$2"
    local host port

    case "$value" in
        *:*)
            host="${value%:*}"
            port="${value##*:}"
            ;;
        *)
            die "$label must include an explicit port: $value"
            ;;
    esac
    [ -n "$host" ] || die "$label has an empty host"
    [ -n "$port" ] || die "$label has an empty port"
    case "$port" in
        ''|*[!0-9]*)
            die "$label has a non-numeric port: $port"
            ;;
    esac
    [ "$port" -ge 1 ] && [ "$port" -le 65535 ] || die "$label port out of range: $port"
}

parse_allowlist() {
    local source_file="$1"
    local target_file="$2"
    local line_no=0

    [ -f "$source_file" ] || die "egress allowlist does not exist: $source_file"
    : > "$target_file"

    while IFS= read -r raw_line || [ -n "$raw_line" ]; do
        line_no=$((line_no + 1))
        line="$(trim "${raw_line%%#*}")"
        [ -z "$line" ] && continue
        case "$raw_line" in
            *'#'*) ;;
            *) die "malformed egress allowlist entry at $source_file:$line_no: missing justification comment" ;;
        esac
        validate_host_port "$line" "malformed egress allowlist entry at $source_file:$line_no" >> "$target_file"
    done < "$source_file"

    sort -u "$target_file" -o "$target_file"
}

print_egress_plan_file() {
    local parsed_file="$1"

    printf 'TC-001 PLAN: defaultAction=deny enforcement=dns-hosts+nftables\n'
    if [ ! -s "$parsed_file" ]; then
        printf 'TC-001 PLAN: empty allowlist; total egress deny\n'
        return 0
    fi
    while read -r host port; do
        printf 'TC-001 PLAN: allow %s:%s\n' "$host" "$port"
    done < "$parsed_file"
}

resolve_egress_plan() {
    local parsed_file="$1"
    local resolved_file="$2"
    local add_hosts_file="$3"
    local host port ips ip

    : > "$resolved_file"
    : > "$add_hosts_file"
    while read -r host port; do
        ips="$(getent ahostsv4 "$host" | awk '/STREAM/ { print $1 }' | sort -u || true)"
        [ -n "$ips" ] || die "egress allowlist host did not resolve to IPv4: $host"
        while read -r ip; do
            [ -n "$ip" ] || continue
            printf '%s %s %s\n' "$host" "$port" "$ip" >> "$resolved_file"
            printf '%s:%s\n' "$host" "$ip" >> "$add_hosts_file"
        done <<EOF
$ips
EOF
    done < "$parsed_file"
    sort -u "$resolved_file" -o "$resolved_file"
    sort -u "$add_hosts_file" -o "$add_hosts_file"
}

parsed_allowlist="$(mktemp)"
cleanup_tmp() {
    rm -f "$parsed_allowlist"
}
trap cleanup_tmp EXIT
parse_allowlist "$egress_allowlist" "$parsed_allowlist"
validate_probe_target "$egress_allow_host" '--egress-allow-host'
validate_probe_target "$egress_deny_host" '--egress-deny-host'
validate_probe_target "$egress_deny_ip" '--egress-deny-ip'

if [ "$print_egress_plan" = true ]; then
    print_egress_plan_file "$parsed_allowlist"
    exit 0
fi

if [ "$print_toolchain_plan" = true ]; then
    resolve_gate_tools
    print_toolchain_plan_file
    exit 0
fi

if [ "$probe" = true ] && [ "$egress_probe" = true ]; then
    die '--probe and --egress-probe are mutually exclusive'
fi

resolve_gate_tools

if [ "$(id -u)" -eq 0 ]; then
    die 'refusing to run as root; use rootless Podman as an unprivileged user'
fi

command -v podman >/dev/null 2>&1 || die 'podman unavailable on PATH'
podman info >/dev/null 2>&1 || die 'podman info failed; rootless Podman is unavailable for this user'
validate_runtime_available "$runtime"

[ -d "$worktree" ] || die "worktree does not exist: $worktree"
worktree="$(cd "$worktree" && pwd)"

cpus="${EXEC_BOX_CPUS:-2}"
memory="${EXEC_BOX_MEMORY:-2g}"
pids_limit="${EXEC_BOX_PIDS_LIMIT:-256}"
scratch_size="${EXEC_BOX_SCRATCH_SIZE:-512m}"
shm_size="${EXEC_BOX_SHM_SIZE:-64m}"
storage_size="${EXEC_BOX_STORAGE_SIZE-4G}"
host_uid="$(id -u)"
host_gid="$(id -g)"

# Determine whether the per-container overlay size quota is enforceable on this host.
# See ADR 027: the quota is a secondary anti-DoS control; it degrades gracefully on
# non-XFS backing stores rather than preventing the box from starting.
storage_quota_supported=false
if detect_storage_quota_supported; then
    storage_quota_supported=true
fi

podman build \
    --tag "$image" \
    --file "$box_dir/Containerfile" \
    "$box_dir" >/dev/null

container_name="${name}-$(date +%s)-$$"
common_args=(
    --name "$container_name"
    --label agent-builder.profile=execution-box
    --label "agent-builder.workload=$workload"
    --label "agent-builder.runtime=$runtime"
    --runtime "$runtime"
    --userns=keep-id
    --user "$host_uid:$host_gid"
    --workdir /work
    --read-only
    --cap-drop=all
    --security-opt=no-new-privileges
    --cpus "$cpus"
    --memory "$memory"
    --pids-limit "$pids_limit"
    --shm-size "$shm_size"
    --env HOME=/scratch/home
    --env TMPDIR=/scratch
    --env XDG_CACHE_HOME=/scratch/cache
    --env "EXEC_BOX_WORKLOAD=$workload"
    --env "EXEC_BOX_RUNTIME=$runtime"
    --env "PATH=$gate_tool_path"
    --mount "type=bind,source=$worktree,target=/work,rw,relabel=private"
    --mount "type=bind,source=$gate_tools,target=$gate_tool_mount,ro,relabel=private"
    --tmpfs "/scratch:rw,noexec,nosuid,nodev,mode=1777,size=$scratch_size"
)

# Conditionally add the per-container writable-layer disk quota (ADR 027).
# - Enforceable host AND non-empty EXEC_BOX_STORAGE_SIZE: apply the quota exactly.
# - Non-enforceable host AND non-empty EXEC_BOX_STORAGE_SIZE: omit the flag,
#   emit a WARNING naming the degraded control, continue (box still launches).
# - Empty EXEC_BOX_STORAGE_SIZE (operator opt-out): omit silently, no WARNING.
if [ "$storage_quota_supported" = true ] && [ -n "$storage_size" ]; then
    common_args+=(--storage-opt "size=$storage_size")
elif [ "$storage_quota_supported" = false ] && [ -n "$storage_size" ]; then
    printf 'execution-box: WARNING: per-container writable-layer disk quota (--storage-opt size) unavailable on this host (backing filesystem does not support overlay size enforcement); running without disk quota.\n' >&2
fi

if [ "$probe" = true ]; then
    cid="$(podman create "${common_args[@]}" "$image" /usr/local/bin/execution-box-probe)" \
        || die "podman create failed: container did not start (exit $?)"
    cleanup_probe() {
        podman rm -f "$cid" >/dev/null 2>&1 || true
        cleanup_tmp
    }
    trap cleanup_probe EXIT

    inspect="$(podman inspect --format '{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}} {{.HostConfig.PidsLimit}} {{.HostConfig.ShmSize}}' "$cid")"
    printf 'TC-003 HOST: %s\n' "$inspect"
    # Validate the load-bearing numeric limits (NanoCpus, Memory, PidsLimit, ShmSize).
    # These must be set (non-zero) and PidsLimit must not be -1 (unlimited).
    # StorageOpt is NOT inspected here: podman 5.x does not expose .HostConfig.StorageOpt
    # portably (field absent in InspectContainerHostConfig on ext4 hosts — ADR 027).
    # Storage-quota state is reported from the launcher's enforceability detection instead.
    _ncpus="$(printf '%s' "$inspect" | awk '{print $1}')"
    _mem="$(printf '%s' "$inspect" | awk '{print $2}')"
    _pids="$(printf '%s' "$inspect" | awk '{print $3}')"
    _shm="$(printf '%s' "$inspect" | awk '{print $4}')"

    [ "$_ncpus" != "0" ] || die "TC-003 FAIL: host inspect NanoCpus is 0 (CPU quota not set): $inspect"
    [ "$_mem"   != "0" ] || die "TC-003 FAIL: host inspect Memory is 0 (memory quota not set): $inspect"
    [ "$_pids"  != "-1" ] || die "TC-003 FAIL: host inspect PidsLimit is -1 (no PID limit): $inspect"
    [ "$_shm"   != "0" ] || die "TC-003 FAIL: host inspect ShmSize is 0 (shm size not set): $inspect"

    if [ "$storage_quota_supported" = true ] && [ -n "$storage_size" ]; then
        # Quota was applied by this launcher (storage_quota_supported=true AND size non-empty).
        printf 'TC-003 PASS: host inspect shows explicit cpu/memory/pids/shm limits; storage quota applied (size=%s)\n' "$storage_size"
    else
        # Quota not enforced on this host (non-XFS or operator opt-out) — graceful degrade.
        printf 'TC-003 PASS: host inspect shows explicit cpu/memory/pids/shm limits (storage quota not enforced on this host)\n'
    fi
    runtime_inspect="$(podman inspect --format '{{.OCIRuntime}}' "$cid")"
    printf 'TC-016 HOST: workload=%s runtime=%s\n' "$workload" "$runtime_inspect"
    [ "$runtime_inspect" = "$runtime" ] || die "TC-016 FAIL: host inspect runtime=$runtime_inspect expected $runtime"
    podman start --attach "$cid" || die "podman start failed: container did not run (exit $?)"
    exit 0
fi

egress_state="$(mktemp -d)"
resolved_allowlist="$egress_state/resolved-egress.allowlist"
add_hosts_file="$egress_state/add-hosts"
resolve_egress_plan "$parsed_allowlist" "$resolved_allowlist" "$add_hosts_file"

pod_name="${container_name}-pod"
sidecar_name="${container_name}-egress"
cleanup_egress() {
    podman pod rm -f "$pod_name" >/dev/null 2>&1 || true
    rm -rf "$egress_state"
    cleanup_tmp
}
trap cleanup_egress EXIT

podman pod create \
    --name "$pod_name" \
    --label agent-builder.profile=execution-box \
    --label agent-builder.egress=default-deny >/dev/null \
    || die "podman pod create failed: egress pod did not start (exit $?)"

podman run -d \
    --name "$sidecar_name" \
    --pod "$pod_name" \
    --label agent-builder.profile=execution-box-egress \
    --userns=keep-id \
    --user 0:0 \
    --read-only \
    --cap-drop=all \
    --cap-add=NET_ADMIN \
    --security-opt=no-new-privileges \
    --mount "type=bind,source=$resolved_allowlist,target=/etc/agent-builder/egress.resolved,ro,relabel=private" \
    --mount "type=bind,source=$egress_state,target=/egress-state,rw,relabel=private" \
    --tmpfs "/scratch:rw,noexec,nosuid,nodev,mode=1777,size=16m" \
    "$image" /usr/local/bin/execution-box-egress-sidecar >/dev/null \
    || die "podman run failed: egress sidecar did not start (exit $?)"

for _ in $(seq 1 100); do
    if [ -f "$egress_state/ready" ]; then
        break
    fi
    if [ -f "$egress_state/fail" ]; then
        podman logs "$sidecar_name" >&2 || true
        die 'egress sidecar failed before workload start'
    fi
    sleep 0.1
done

[ -f "$egress_state/ready" ] || {
    podman logs "$sidecar_name" >&2 || true
    die 'egress sidecar did not become ready'
}

workload_args=(
    "${common_args[@]}"
    --pod "$pod_name"
    --dns none
    --mount "type=bind,source=$egress_state,target=/egress-state,ro,relabel=private"
)

while read -r add_host; do
    [ -n "$add_host" ] || continue
    workload_args+=(--add-host "$add_host")
done < "$add_hosts_file"

if [ "$egress_probe" = true ]; then
    _egress_probe_rc=0
    podman run --rm \
        "${workload_args[@]}" \
        --env "EXEC_BOX_EGRESS_PROBE_ALLOW_HOST=$egress_allow_host" \
        --env "EXEC_BOX_EGRESS_PROBE_DENY_HOST=$egress_deny_host" \
        --env "EXEC_BOX_EGRESS_PROBE_DENY_IP=$egress_deny_ip" \
        "$image" /usr/local/bin/execution-box-egress-probe \
        || _egress_probe_rc=$?
    if [ "$_egress_probe_rc" -eq 125 ]; then
        die "podman run (egress-probe) failed: container did not start (exit 125)"
    fi
    exit "$_egress_probe_rc"
fi

if [ "$#" -eq 0 ]; then
    set -- /bin/sh
fi

_workload_rc=0
podman run --rm -it "${workload_args[@]}" "$image" "$@" || _workload_rc=$?
if [ "$_workload_rc" -eq 125 ]; then
    die "podman run failed: container did not start (exit 125)"
fi
exit "$_workload_rc"
