# Test Spec 036: Podman adapter swap in default run wiring

**Linked task:** [`docs/tasks/backlog/036-podman-adapter-swap.md`](../backlog/036-podman-adapter-swap.md)
**Written:** 2026-06-16
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-036-01 | TC-036-01 | ✅ |
| REQ-036-02 | TC-036-02 | ✅ |
| REQ-036-03 | TC-036-03 | ✅ |
| REQ-036-04 | TC-036-04 | ✅ |

## Test cases

### TC-036-01: ConfigFromEnv rejects absent AGENT_BUILDER_SANDBOX_RUNTIME but accepts Podman config

- **Requirement:** REQ-036-01
- **Input:** environment with `AGENT_BUILDER_TASK_ROOT`, `AGENT_BUILDER_WORKTREE`, `ANTHROPIC_API_KEY`, `AGENT_BUILDER_RUN_TIMEOUT`, `AGENT_BUILDER_MAX_ATTEMPTS`, and `AGENT_BUILDER_PUBLISH_REMOTE` set; `AGENT_BUILDER_SANDBOX_RUNTIME` absent; `EXEC_BOX_IMAGE` or equivalent Podman adapter config set.
- **Expected output:** `ConfigFromEnv` returns a valid `Config` without error; the returned config names the Podman adapter rather than `srt`.
- **Edge cases:** explicit `AGENT_BUILDER_SANDBOX_RUNTIME` set to a non-empty value must produce an error or be ignored (no silent acceptance of the deprecated variable).

### TC-036-02: runtime.Run wires the Podman adapter as the box backend

- **Requirement:** REQ-036-02
- **Input:** `runtime.Config` with valid values and a fake task source returning one task; the containment box is a fake that records which runner type was used.
- **Expected output:** the box that wraps the Podman adapter is constructed; the `sandboxruntime.Runner` type is not imported or instantiated by `internal/runtime`.
- **Edge cases:** if a build-time import of `sandboxruntime` remains after the swap, the fitness function (see REQ-036-04) catches it.

### TC-036-03: spec documents no longer reference srt as a required runtime dependency

- **Requirement:** REQ-036-03
- **Input:** `docs/spec/configuration.md` and `docs/spec/interfaces.md` after the swap.
- **Expected output:** `AGENT_BUILDER_SANDBOX_RUNTIME` is removed from the required environment variable table; `srt` is removed from the outbound interfaces table; the Podman adapter's configuration surface is recorded instead.
- **Edge cases:** historical references to `srt` in the `sandboxruntime` package docs are acceptable (that package remains for reference); only the default run wiring and spec must be updated.

### TC-036-04: fitness check confirms no srt dependency in the default run pipeline

- **Requirement:** REQ-036-04
- **Input:** `make fitness` or a dedicated fitness step that inspects the `internal/runtime` import graph.
- **Expected output:** `internal/runtime` does not transitively import `internal/sandbox/sandboxruntime`; the fitness step exits zero and prints a PASS line.
- **Edge cases:** the `sandboxruntime` package itself may still exist (for future reference or removal); only its import from the production run pipeline must be absent.

## Notes

Framework: Go `testing` for wiring validation; import-graph inspection via `go list -json ./internal/runtime/...` or a dedicated fitness script. TC-036-03 is a docs-only check verified by the spec-verifier pass; TC-036-04 is executable via `make fitness`.
