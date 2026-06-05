#!/bin/sh
set -eu

allowlist="${EXEC_BOX_RESOLVED_EGRESS_ALLOWLIST:-/etc/agent-builder/egress.resolved}"
state_dir="${EXEC_BOX_EGRESS_STATE_DIR:-/egress-state}"
rules="/scratch/agent-builder-egress.nft"

fail() {
    printf 'TC-001 FAIL: %s\n' "$*" >&2
    mkdir -p "$state_dir"
    printf '%s\n' "$*" > "$state_dir/fail" 2>/dev/null || true
    exit 1
}

need() {
    command -v "$1" >/dev/null 2>&1 || fail "required egress tool unavailable: $1"
}

need nft

[ -f "$allowlist" ] || fail "resolved allowlist missing: $allowlist"
mkdir -p "$state_dir"

{
    printf 'flush table inet agent_builder_egress\n'
    printf 'table inet agent_builder_egress {\n'
    printf '  set allowed_tcp4 {\n'
    printf '    type ipv4_addr . inet_service\n'
    printf '    flags interval\n'
    printf '    elements = { '
    first=true
    while read -r host port ip; do
        [ -n "${host:-}" ] || continue
        [ -n "${port:-}" ] || fail "resolved allowlist row missing port"
        [ -n "${ip:-}" ] || fail "resolved allowlist row missing IP"
        case "$ip" in
            *:*) fail "IPv6 is fail-closed in the bootstrap egress filter: $ip" ;;
            *.*.*.*) ;;
            *) fail "resolved allowlist row has invalid IPv4 address: $ip" ;;
        esac
        if [ "$first" = true ]; then
            first=false
        else
            printf ', '
        fi
        printf '%s . %s' "$ip" "$port"
    done < "$allowlist"
    printf ' }\n'
    printf '  }\n'
    printf '  chain output {\n'
    printf '    type filter hook output priority 0; policy drop;\n'
    printf '    oifname "lo" accept\n'
    printf '    ct state established,related accept\n'
    printf '    ip daddr . tcp dport @allowed_tcp4 accept\n'
    printf '    reject\n'
    printf '  }\n'
    printf '}\n'
} > "$rules"

nft -f "$rules" || fail 'nftables default-deny egress rules failed to apply'

printf 'TC-001 PASS: egress sidecar installed nftables default-deny output policy\n'
printf 'ready\n' > "$state_dir/ready"

while :; do
    sleep 3600
done
