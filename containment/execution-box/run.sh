#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat <<'EOF'
Usage:
  containment/execution-box/run.sh [--worktree PATH] [--probe] [--name NAME] [--image IMAGE] [-- COMMAND...]

Runs a target repo worktree inside the rootless Podman execution-box profile.

Options:
  --worktree PATH   host repo worktree to mount at /work (default: current directory)
  --probe           run the runtime containment probe instead of an interactive shell
  --name NAME       container name prefix (default: agent-builder-execution-box)
  --image IMAGE     image tag to build/use (default: localhost/agent-builder/execution-box:014)
EOF
}

die() {
    printf 'execution-box: %s\n' "$*" >&2
    exit 1
}

box_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
worktree="$(pwd)"
probe=false
name="agent-builder-execution-box"
image="${EXEC_BOX_IMAGE:-localhost/agent-builder/execution-box:014}"

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
    cleanup() {
        podman rm -f "$cid" >/dev/null 2>&1 || true
    }
    trap cleanup EXIT

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

if [ "$#" -eq 0 ]; then
    set -- /bin/sh
fi

exec podman run --rm -it "${common_args[@]}" "$image" "$@"
