#!/bin/sh
set -eu

fail() {
    printf '%s FAIL: %s\n' "$1" "$2" >&2
    exit 1
}

pass() {
    printf '%s PASS: %s\n' "$1" "$2"
}

mkdir -p /work/.execution-box-probe/nested /scratch/probe /scratch/home /scratch/cache

printf 'worktree\n' > /work/.execution-box-probe/nested/write.txt ||
    fail TC-001 'worktree mount is not writable'
printf 'scratch\n' > /scratch/probe/write.txt ||
    fail TC-001 'scratch tmpfs is not writable'

if touch /.execution-box-root-write 2>/scratch/root-write.err; then
    fail TC-001 'root filesystem accepted a write'
fi
pass TC-001 'worktree and scratch writable; root filesystem write denied'

for path in / /usr /etc; do
    candidate="${path%/}/.execution-box-denied"
    if touch "$candidate" 2>/scratch/negative-write.err; then
        fail TC-004 "write unexpectedly succeeded at $path"
    fi
done
pass TC-004 'writes to rootfs paths are denied'

uid="$(id -u)"
gid="$(id -g)"
if [ "$uid" = "0" ] || [ "$gid" = "0" ]; then
    fail TC-002 "expected non-root uid/gid, got $uid:$gid"
fi

cap_eff="$(awk '/^CapEff:/ { print $2 }' /proc/self/status)"
if [ "$cap_eff" != "0000000000000000" ]; then
    fail TC-002 "expected empty effective capability set, got $cap_eff"
fi

if [ "${HOME:-}" != "/scratch/home" ]; then
    fail TC-002 "HOME must point at scratch, got ${HOME:-unset}"
fi

if mount | awk '$3 != "/work" && ($1 ~ /^\/home\// || $1 ~ /^\/root/) { found = 1 } END { exit found ? 0 : 1 }'; then
    fail TC-002 'host home-like mount found outside /work'
fi
pass TC-002 "non-root $uid:$gid, no effective capabilities, scratch HOME"

socket_hits="$(find /run /var/run /tmp -maxdepth 4 \( -name '*podman*.sock' -o -name '*docker*.sock' \) 2>/dev/null || true)"
if [ -n "$socket_hits" ]; then
    fail TC-005 "container-engine socket visible: $socket_hits"
fi
if env | grep -Eq '^(CONTAINER_HOST|DOCKER_HOST)='; then
    fail TC-005 'container-engine socket environment variable is present'
fi
pass TC-005 'no container-engine socket path or environment variable is reachable'

cgroup_root="/sys/fs/cgroup"
pids_limited="unknown"
memory_limited="unknown"
cpu_limited="unknown"

if [ -r "$cgroup_root/pids.max" ]; then
    pids_limit="$(cat "$cgroup_root/pids.max")"
    [ "$pids_limit" != "max" ] && pids_limited="yes"
elif [ -r "$cgroup_root/pids/pids.max" ]; then
    pids_limit="$(cat "$cgroup_root/pids/pids.max")"
    [ "$pids_limit" != "max" ] && pids_limited="yes"
fi

if [ -r "$cgroup_root/memory.max" ]; then
    memory_limit="$(cat "$cgroup_root/memory.max")"
    [ "$memory_limit" != "max" ] && memory_limited="yes"
elif [ -r "$cgroup_root/memory/memory.limit_in_bytes" ]; then
    memory_limit="$(cat "$cgroup_root/memory/memory.limit_in_bytes")"
    [ "$memory_limit" -gt 0 ] && memory_limited="yes"
fi

if [ -r "$cgroup_root/cpu.max" ]; then
    cpu_limit="$(cat "$cgroup_root/cpu.max")"
    case "$cpu_limit" in
        max*) ;;
        *) cpu_limited="yes" ;;
    esac
elif [ -r "$cgroup_root/cpu/cpu.cfs_quota_us" ]; then
    cpu_quota="$(cat "$cgroup_root/cpu/cpu.cfs_quota_us")"
    [ "$cpu_quota" -gt 0 ] && cpu_limited="yes"
fi

if [ "$pids_limited" != "yes" ] || [ "$memory_limited" != "yes" ] || [ "$cpu_limited" != "yes" ]; then
    fail TC-003 "expected cgroup limits, got cpu=$cpu_limited memory=$memory_limited pids=$pids_limited"
fi
pass TC-003 'cpu, memory, and pids cgroup limits are visible in-box'
