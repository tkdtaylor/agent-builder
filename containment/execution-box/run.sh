#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat <<'EOF'
Usage:
  containment/execution-box/run.sh [--worktree PATH] [--probe] [--egress-probe] [--egress-allowlist PATH] [--print-egress-plan] [--name NAME] [--image IMAGE] [-- COMMAND...]

Runs a target repo worktree inside the rootless Podman execution-box profile.

Options:
  --worktree PATH          host repo worktree to mount at /work (default: current directory)
  --probe                  run the runtime containment probe instead of an interactive shell
  --egress-probe           run the runtime egress allowlist probe
  --egress-allowlist PATH  plain-text host:port allowlist (default: containment/execution-box/egress.allowlist)
  --egress-allow-host H:P  allowlisted host:port expected to succeed during --egress-probe
  --egress-deny-host H:P   non-allowlisted host:port expected to fail during --egress-probe
  --egress-deny-ip H:P     direct IP literal host:port expected to fail during --egress-probe
  --print-egress-plan      parse and print the allowlist without requiring Podman
  --name NAME              container name prefix (default: agent-builder-execution-box)
  --image IMAGE            image tag to build/use (default: localhost/agent-builder/execution-box:014)
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
print_egress_plan=false
name="agent-builder-execution-box"
image="${EXEC_BOX_IMAGE:-localhost/agent-builder/execution-box:014}"
egress_allowlist="${EXEC_BOX_EGRESS_ALLOWLIST:-$box_dir/egress.allowlist}"
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
        --print-egress-plan)
            print_egress_plan=true
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

if [ "$probe" = true ] && [ "$egress_probe" = true ]; then
    die '--probe and --egress-probe are mutually exclusive'
fi

if [ "$(id -u)" -eq 0 ]; then
    die 'refusing to run as root; use rootless Podman as an unprivileged user'
fi

command -v podman >/dev/null 2>&1 || die 'podman unavailable on PATH'
podman info >/dev/null 2>&1 || die 'podman info failed; rootless Podman is unavailable for this user'

[ -d "$worktree" ] || die "worktree does not exist: $worktree"
worktree="$(cd "$worktree" && pwd)"

cpus="${EXEC_BOX_CPUS:-2}"
memory="${EXEC_BOX_MEMORY:-2g}"
pids_limit="${EXEC_BOX_PIDS_LIMIT:-256}"
scratch_size="${EXEC_BOX_SCRATCH_SIZE:-512m}"
shm_size="${EXEC_BOX_SHM_SIZE:-64m}"
storage_size="${EXEC_BOX_STORAGE_SIZE:-4G}"
host_uid="$(id -u)"
host_gid="$(id -g)"

podman build \
    --tag "$image" \
    --file "$box_dir/Containerfile" \
    "$box_dir" >/dev/null

container_name="${name}-$(date +%s)-$$"
common_args=(
    --name "$container_name"
    --label agent-builder.profile=execution-box
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
    --storage-opt "size=$storage_size"
    --env HOME=/scratch/home
    --env TMPDIR=/scratch
    --env XDG_CACHE_HOME=/scratch/cache
    --mount "type=bind,source=$worktree,target=/work,rw,relabel=private"
    --tmpfs "/scratch:rw,noexec,nosuid,nodev,mode=1777,size=$scratch_size"
)

if [ "$probe" = true ]; then
    cid="$(podman create "${common_args[@]}" "$image" /usr/local/bin/execution-box-probe)"
    cleanup_probe() {
        podman rm -f "$cid" >/dev/null 2>&1 || true
        cleanup_tmp
    }
    trap cleanup_probe EXIT

    inspect="$(podman inspect --format '{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}} {{.HostConfig.PidsLimit}} {{.HostConfig.ShmSize}} {{json .HostConfig.StorageOpt}}' "$cid")"
    printf 'TC-003 HOST: %s\n' "$inspect"
    case "$inspect" in
        0\ *|*\ 0\ *|*\ -1\ *|*\ null*)
            die "TC-003 FAIL: host inspect does not show explicit limits: $inspect"
            ;;
    esac
    printf 'TC-003 PASS: host inspect shows explicit cpu/memory/pids/shm/storage limits\n'
    podman start --attach "$cid"
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
    --label agent-builder.egress=default-deny >/dev/null

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
    "$image" /usr/local/bin/execution-box-egress-sidecar >/dev/null

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
    podman run --rm \
        "${workload_args[@]}" \
        --env "EXEC_BOX_EGRESS_PROBE_ALLOW_HOST=$egress_allow_host" \
        --env "EXEC_BOX_EGRESS_PROBE_DENY_HOST=$egress_deny_host" \
        --env "EXEC_BOX_EGRESS_PROBE_DENY_IP=$egress_deny_ip" \
        "$image" /usr/local/bin/execution-box-egress-probe
    exit $?
fi

if [ "$#" -eq 0 ]; then
    set -- /bin/sh
fi

podman run --rm -it "${workload_args[@]}" "$image" "$@"
exit $?
