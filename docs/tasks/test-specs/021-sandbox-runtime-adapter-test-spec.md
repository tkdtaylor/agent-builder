# Test Spec 021: sandbox-runtime backing adapter (bootstrap isolation)

**Linked task:** [`docs/tasks/completed/021-sandbox-runtime-adapter.md`](../completed/021-sandbox-runtime-adapter.md)
**Written:** 2026-06-04
**Status:** complete

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ✅ |
| REQ-002 | TC-002, TC-003 | ✅ |
| REQ-003 | TC-004 | ✅ |

## Test cases
### TC-001: command runs isolated in sandbox-runtime
- **Requirement:** REQ-001
- **Harness:** Go adapter test with a fake `srt` executable that records `--settings`, working directory, and argv while executing the requested command; live opt-in harness with real `srt` when available.
- **Input:** a trivial command (e.g. `sh -c 'printf hello'`) + an isolated worktree.
- **Expected output:** adapter invokes `srt --settings <generated-json> <command...>` with the command working directory set to the requested worktree; exit code `0`, expected stdout, captured stderr, and non-zero command exits surface as exit codes rather than backend errors.
- **Edge cases:** empty command is rejected before invoking `srt`; missing worktree is rejected; missing `srt` binary is a backend error; wall-clock timeout cancels the subprocess.

### TC-002: allowlisted egress permitted
- **Requirement:** REQ-002
- **Harness:** static/config test plus live opt-in `srt` harness.
- **Input:** request limits include egress allowlist entries such as `api.github.com:443` and `proxy.golang.org:443`.
- **Expected output:** generated sandbox-runtime settings contain `network.allowedDomains` with exactly the allowlisted hostnames, normalizing `host:port` entries to hostnames because sandbox-runtime's public config is domain-based. In the live harness, a command reaching an allowlisted host succeeds.
- **Edge cases:** empty allowlist generates an empty `allowedDomains` list and permits no outbound network destinations.

### TC-003: non-allowlisted egress blocked
- **Requirement:** REQ-002
- **Harness:** static/config test plus live opt-in `srt` harness.
- **Input:** request limits omit `example.com` and the live harness attempts to connect to it.
- **Expected output:** generated settings do not include the omitted host, so sandbox-runtime's deny-by-default network allowlist should block it; in the live harness, the command fails with the sandbox-runtime network-deny output.
- **Edge cases:** DNS-only versus full-connect attempts both denied in live runtime evidence.

### TC-004: swap-compatible behind the 020 interface
- **Requirement:** REQ-003
- **Harness:** compile-time interface assertion plus supervisor construction test.
- **Input:** assign the concrete sandbox-runtime adapter to `sandbox.Runner`, then pass it through `supervisor.WithSandboxRunner`.
- **Expected output:** code compiles with no caller-side change versus `sandbox.FakeRunner`; supervisor imports only the task-020 seam package, not the concrete sandbox-runtime backend package.
- **Edge cases:** replacing the fake with the concrete adapter must not require changing the `sandbox.Runner` method signature.

## Notes
Framework: Go `testing` + a fake-`srt` subprocess for deterministic local assertions. Live L5/L6 evidence is opt-in because `@anthropic-ai/sandbox-runtime` and Linux bubblewrap support may be absent locally. Static tests must prove the exact `srt --settings` argv, generated settings, stdout/stderr/exit-code handling, request validation, and interface conformance; live promotion to ✅ requires real `srt` output for one allowlisted connection and one blocked non-allowlisted connection.
