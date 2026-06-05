#!/bin/sh
set -eu

fail() {
    printf '%s FAIL: %s\n' "$1" "$2" >&2
    exit 1
}

pass() {
    printf '%s PASS: %s\n' "$1" "$2"
}

split_host_port() {
    value="$1"
    label="$2"
    case "$value" in
        *:*)
            host="${value%:*}"
            port="${value##*:}"
            ;;
        *)
            fail "$label" "target must be host:port, got $value"
            ;;
    esac
    [ -n "$host" ] || fail "$label" "target host is empty"
    [ -n "$port" ] || fail "$label" "target port is empty"
    case "$port" in
        *[!0-9]*)
            fail "$label" "target port is not numeric: $port"
            ;;
    esac
    printf '%s %s\n' "$host" "$port"
}

try_connect() {
    host="$1"
    port="$2"
    nc -vz -w 5 "$host" "$port" 2>&1
}

[ -f /egress-state/ready ] || fail TC-003 'egress sidecar readiness marker is missing'

set -- $(split_host_port "${EXEC_BOX_EGRESS_PROBE_ALLOW_HOST:-api.github.com:443}" TC-003)
allow_host="$1"
allow_port="$2"
if allow_output="$(try_connect "$allow_host" "$allow_port")"; then
    pass TC-003 "allowlisted connect succeeded: $allow_host:$allow_port :: $allow_output"
else
    fail TC-003 "allowlisted connect failed: $allow_host:$allow_port :: $allow_output"
fi

set -- $(split_host_port "${EXEC_BOX_EGRESS_PROBE_DENY_HOST:-example.com:443}" TC-004)
deny_host="$1"
deny_port="$2"
if deny_output="$(try_connect "$deny_host" "$deny_port")"; then
    fail TC-004 "non-allowlisted connect unexpectedly succeeded: $deny_host:$deny_port :: $deny_output"
else
    pass TC-004 "non-allowlisted connect blocked: $deny_host:$deny_port :: $deny_output"
fi

set -- $(split_host_port "${EXEC_BOX_EGRESS_PROBE_DENY_IP:-1.1.1.1:443}" TC-004)
deny_ip="$1"
deny_ip_port="$2"
if deny_ip_output="$(try_connect "$deny_ip" "$deny_ip_port")"; then
    fail TC-004 "direct IP bypass unexpectedly succeeded: $deny_ip:$deny_ip_port :: $deny_ip_output"
else
    pass TC-004 "direct IP bypass blocked: $deny_ip:$deny_ip_port :: $deny_ip_output"
fi
