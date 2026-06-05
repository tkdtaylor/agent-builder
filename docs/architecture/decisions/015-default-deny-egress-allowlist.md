# ADR 015: Default-Deny Execution-Box Egress Allowlist

**Status:** accepted
**Date:** 2026-06-05
**Task:** 015 - Default-deny egress allowlist

## Context

The accepted bootstrap risk is that executor auth tokens may exist inside the execution box. The load-bearing compensating control is egress: a stolen token should not be exfiltratable to arbitrary attacker infrastructure. The allowlist must be plain text so the reviewed contract is obvious in git, and a deny path must be enforced by the runtime rather than by advisory proxy environment variables.

OpenSandbox OSEP-0001 uses a two-layer egress shape: a DNS/domain layer decides which destinations are intended, and a packet-filter layer blocks direct-IP bypasses. The execution-box profile already runs on rootless Podman with the workload non-root and capability-free, so network administration cannot be granted to the workload container.

## Decision

Add `containment/execution-box/egress.allowlist` as the execution-box egress contract. Each non-comment line is one exact hostname plus explicit TCP port and an inline justification comment.

The launcher validates the allowlist before requiring Podman. Malformed entries, schemes, paths, wildcards, IP literals, CIDR blocks, missing ports, and out-of-range ports fail closed before any workload starts. Empty allowlist is valid and means total egress deny.

For normal and `--egress-probe` runs, the launcher creates a temporary Podman pod network namespace and starts a trusted egress sidecar before the workload. The sidecar is the only container granted `CAP_NET_ADMIN`; the workload keeps `--cap-drop=all`, `--security-opt=no-new-privileges`, non-root uid/gid, and disabled workload DNS. The launcher resolves allowlisted hostnames, injects only those host records into the workload container, and passes resolved IP-and-port pairs to the sidecar. The sidecar installs nftables rules with a default-drop output policy and explicit allow rules only for those resolved pairs, then writes a readiness marker. The workload starts only after that marker exists.

This is the bootstrap execution-box form of the OSEP-0001 two-layer design:

1. Hostname layer: the workload receives host records only for allowlisted hostnames; arbitrary DNS is disabled.
2. Network-filter layer: nftables defaults egress to deny and allows only the resolved allowlisted IP-and-port pairs.

Runtime verification is `containment/execution-box/run.sh --worktree . --egress-probe`. It must quote an allowlisted connection success and a non-allowlisted/direct-IP failure. Static tests can prove the contract in environments without Podman, but cannot promote the task beyond code-merged status.

## Rationale

Environment proxy variables alone are advisory: a hostile workload can unset them or open raw sockets. A sidecar-owned packet filter in the shared pod network namespace is an enforcement boundary for every process in the workload container.

Keeping the allowlist exact and port-specific avoids wildcard sprawl during bootstrap. IP literals and CIDR are rejected because they weaken the human-reviewable host contract and make the token-exfiltration risk harder to reason about. Future tasks may expand the format only with a new ADR and deny-path evidence.

The sidecar is trusted infrastructure, not agent-generated workload. Isolating `CAP_NET_ADMIN` there preserves the task 014 workload invariants while still giving the runtime enough authority to install a real deny policy.

## Consequences

Rootless Podman plus nftables support is required for runtime proof. If either is unavailable, the launcher fails closed and the coverage tracker must stay at code-merged status until an operator records L6 evidence.

Allowlisted domains that resolve to shared infrastructure still allow traffic to the resolved IP-and-port pair. This matches the bootstrap DNS-plus-filter model, but it is not a credential-injecting HTTP proxy. A future vault or exec-sandbox task can strengthen the model with request-time host/SNI validation and credential injection at the proxy boundary.

The launcher now owns temporary pod cleanup for egress-enforced runs. Future runtime-tier work must preserve the ordering: sidecar applies deny policy and readiness before workload start.
