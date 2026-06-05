# Test Spec 015: Default-deny egress allowlist

**Linked task:** [`docs/tasks/completed/015-egress-allowlist.md`](../completed/015-egress-allowlist.md)
**Written:** 2026-06-04
**Status:** complete

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-003, TC-004 | ✅ |
| REQ-002 | TC-002 | ✅ |
| REQ-003 | TC-004 | ✅ |

## Acceptance criteria
- The execution-box launcher consumes a repo-local plain-text allowlist file and rejects malformed entries before starting Podman.
- The allowlist format and default-deny semantics are documented in `docs/spec/configuration.md`.
- The launcher builds a two-layer egress plan: DNS answers are restricted to allowlisted hostnames, and a network filter defaults to deny with explicit allow rules only for resolved allowlisted destinations.
- The application container remains non-root with all capabilities dropped; any network-administration privilege is isolated to the egress sidecar.
- Runtime validation, when rootless Podman is available, quotes both an allowlisted connection success and a denied non-allowlisted connection failure.
- Static tests reference every TC marker below and assert the launcher, sidecar, probe, allowlist, and docs contain the controls they are meant to exercise.

## Test cases
### TC-001: Two-layer default-deny egress plan is generated
- **Requirement:** REQ-001
- **Input:** Read the launcher and egress sidecar scripts; ask the launcher to print the parsed egress plan for the default allowlist.
- **Expected output:** The launcher emits a plan with only allowlisted host:port entries, prepares DNS-layer allow records, prepares an nftables or equivalent network-filter layer with a default drop/deny policy, and runs the agent workload without `CAP_NET_ADMIN`.
- **Static assertions:** launcher creates a pod or equivalent shared network namespace for an egress sidecar; sidecar applies a default-deny network filter; workload args retain `--cap-drop=all`, `--security-opt=no-new-privileges`, and no workload `--cap-add`; sidecar is the only component with network-admin authority.
- **Edge cases:** empty allowlist produces total deny; repeated entries are de-duplicated; comments and blank lines do not create allow rules.

### TC-002: Allowlist is plain-text and spec-documented
- **Requirement:** REQ-002
- **Input:** Read `containment/execution-box/egress.allowlist`, run the launcher's allowlist parser against valid and malformed fixture files, and cross-check `docs/spec/configuration.md`.
- **Expected output:** The default file is UTF-8/plain text with one `host:port` rule per non-comment line; each rule has an inline justification comment; malformed rules exit non-zero before Podman is invoked; the spec documents format, defaults, reload behavior, empty-file behavior, and malformed-entry behavior.
- **Edge cases:** scheme/path values are rejected; missing or non-numeric ports are rejected; wildcard/IP/CIDR entries are rejected for this bootstrap contract unless a later ADR expands the format.

### TC-003: Allowlisted host is reachable (happy path)
- **Requirement:** REQ-001
- **Input:** Launch the box with `containment/execution-box/run.sh --worktree . --egress-probe --egress-allow-host <allowlisted-host:port>` where the host appears in the allowlist and is expected to be reachable from the operator network.
- **Expected output:** In-box probe prints `TC-003 PASS` and quotes the command/output showing the connection succeeded through the allowlist.
- **Edge cases:** exact hostname matching is case-insensitive after parser normalization; an allowlisted host on a different port is denied unless that port is separately listed.

### TC-004 (NEGATIVE/escape): Non-allowlisted host and direct-IP bypass are NOT reachable
- **Requirement:** REQ-001, REQ-003
- **Input:** Launch the box with `containment/execution-box/run.sh --worktree . --egress-probe --egress-deny-host <non-allowlisted-host:port>` and with a direct IP literal not present in the allowlist.
- **Expected output:** In-box probe prints `TC-004 PASS` and quotes the refusal, timeout, DNS block, or network-filter block; the probe fails if either non-allowlisted hostname or direct IP connection succeeds.
- **Edge cases:** DNS-over-HTTPS and DNS-over-TLS targets not present in the allowlist are denied by the network-filter layer; redirects to non-allowlisted hosts do not bypass because the destination connection is still filtered.

## Notes
Framework: Go static/contract tests under `tests/containment/` plus the runtime harness `containment/execution-box/run.sh --egress-probe` when rootless Podman is available. Static tests keep `make check` meaningful in CI without Podman; L6 requires a real Podman run and must quote both the allowlisted-success and non-allowlisted-block lines. Builds on the task 014 box profile; uses runc unless a runsc result is also recorded (runtime tiering is task 016).
