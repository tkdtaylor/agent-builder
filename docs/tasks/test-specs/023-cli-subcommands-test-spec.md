# Test Spec 023: CLI subcommand surface (run / version / verify)

**Linked task:** [`docs/tasks/backlog/023-cli-subcommands.md`](../backlog/023-cli-subcommands.md)
**Written:** 2026-06-04
**Status:** ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-002, TC-006 | ✅ |
| REQ-002 | TC-003, TC-004, TC-007 | ✅ |
| REQ-003 | TC-005, TC-008 | ✅ |

## Test cases
### TC-001: version prints the version, exits 0
- **Requirement:** REQ-001
- **Input:** `agent-builder version`
- **Expected output:** stdout contains `agent-builder <version>` using the build's `supervisor.Version`; stderr is empty; exit code 0.
- **Assertions:** unit-level dispatcher test and runtime binary test.
- **Edge cases:** `agent-builder version extra` is malformed usage and exits 2.

### TC-002: run dispatches the loop
- **Requirement:** REQ-001
- **Input:** `agent-builder run` (with stubbed supervisor)
- **Expected output:** the supervisor run path is invoked exactly once; exit code 0 when it returns nil.
- **Assertions:** unit-level dispatcher test injects a fake run function and observes the call.
- **Edge cases:** injected run failure is reported on stderr and exits 1; extra positional args exit 2.

### TC-003: verify on a clean (gate-passing) repo exits 0
- **Requirement:** REQ-002
- **Input:** `agent-builder verify <clean-repo>`
- **Expected output:** the real Gate is constructed with the blocking production steps and invoked against `<clean-repo>`; stdout reports the passing gate; stderr is empty; exit code 0.
- **Assertions:** unit-level dispatcher test injects a fake gate verifier to prove parsing and exit-code behavior; runtime binary test invokes the real binary against a fixture repo with tool shims on `PATH`.
- **Edge cases:** `<clean-repo>` may be relative or absolute and is cleaned before invoking the Gate.

### TC-004: verify on a dirty (gate-failing) repo exits non-zero
- **Requirement:** REQ-002
- **Input:** `agent-builder verify <dirty-repo>`
- **Expected output:** the real Gate runs and stops on the failing step; stdout or stderr names the failed step; exit code 1.
- **Assertions:** unit-level dispatcher test injects a failing verifier; runtime binary test invokes the real binary against a failing fixture repo with tool shims on `PATH`.
- **Edge cases:** missing repo path exits 2; more than one repo arg exits 2.

### TC-005: unknown subcommand exits 2
- **Requirement:** REQ-003
- **Input:** `agent-builder bogus`
- **Expected output:** usage error on stderr, exit code 2
- **Assertions:** unit-level dispatcher test and runtime binary test.
- **Edge cases:** no subcommand exits 2.

### TC-006: help documents subcommands and exit codes
- **Requirement:** REQ-001
- **Input:** `agent-builder -h` and `agent-builder help`
- **Expected output:** stdout documents `run`, `version`, `verify <repo>`, and exit codes 0, 1, 2; exit code 0.
- **Assertions:** unit-level dispatcher test.
- **Edge cases:** subcommand-specific `-h` exits 0 and names that command's usage.

### TC-007: verify exposes no gate bypass flag
- **Requirement:** REQ-002
- **Input:** source inspection through `make fitness-gate-blocking` and CLI usage for `agent-builder verify -h`.
- **Expected output:** no `--no-verify`, skip-verify, scanner-skip, or bypass affordance appears in production CLI/Gate source or help output.
- **Assertions:** existing F-002 fitness check plus a CLI help assertion.
- **Edge cases:** no alternate spelling of a bypass flag is accepted.

### TC-008: malformed usage exits 2
- **Requirement:** REQ-003
- **Input:** `agent-builder`, `agent-builder verify`, `agent-builder verify repo extra`, `agent-builder run extra`, `agent-builder version extra`.
- **Expected output:** usage error on stderr, exit code 2.
- **Assertions:** unit-level dispatcher table test.
- **Edge cases:** flag parse errors on subcommands also exit 2.

## Notes
Framework: stdlib `flag` and Go `testing`; no CLI framework or ADR is needed. Unit tests cover parsing/dispatch with injected runner and verifier functions. Runtime-visible tests build `cmd/agent-builder` and invoke the real binary against fixture repos with tool shims for external Gate tools. The real `verify` command must construct the production Gate and must not expose a skip/bypass flag.
