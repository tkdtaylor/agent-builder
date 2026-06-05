# Test Spec 022: Claude Code CLI executor adapter

**Linked task:** [`docs/tasks/backlog/022-claude-cli-executor.md`](../backlog/022-claude-cli-executor.md)
**Written:** 2026-06-04
**Status:** complete — implementation must satisfy these cases before the task can move to code-merged

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-005 | ✅ |
| REQ-002 | TC-002, TC-003 | ✅ |
| REQ-003 | TC-004, TC-006 | ✅ |

## Test cases
### TC-001: Run invokes the CLI subprocess against the worktree
- **Requirement:** REQ-001
- **Input:** `executor.ClaudeCLI` configured with a fake CLI binary path, a temporary task worktree, and `supervisor.Task{ID:"022", Repo:"agent-builder", Spec:"docs/tasks/backlog/022-claude-cli-executor.md"}`
- **Expected output:** the fake CLI receives an argv containing the configured prompt flag/input and runs with its working directory set to the configured worktree. The test must assert the fake observed the worktree path, task ID, repo, and task spec path.
- **Edge cases:** blank worktree rejected before subprocess start; blank CLI path rejected before subprocess start.

### TC-002: produced branch captured into Result.Branch
- **Requirement:** REQ-002
- **Input:** fake CLI writes `task/022-claude-cli-executor` to the documented branch-output file.
- **Expected output:** `Result.Branch == "task/022-claude-cli-executor"`.
- **Edge cases:** missing output file and whitespace-only branch output both return an error and do not silently produce an empty branch.

### TC-003: Result.OK reflects subprocess success
- **Requirement:** REQ-002
- **Input:** successful vs failed subprocess run
- **Expected output:** successful fake subprocess plus valid branch returns `supervisor.Result{OK:true}`; non-zero fake subprocess returns `Result.OK == false` and an error that names the failed CLI.
- **Edge cases:** subprocess failure must preserve captured stdout/stderr in the error without leaking secrets.

### TC-004: auth token supplied as revocable credential
- **Requirement:** REQ-003
- **Input:** executor configured with `AuthToken: "test-token-value"`.
- **Expected output:** the fake CLI observes the token only through `ANTHROPIC_API_KEY` in its environment. The token is not included in argv, branch files, stdout/stderr error messages, or logged output.
- **Edge cases:** missing token returns a clear error before subprocess start.

### TC-005: prompt carries task and branch-output contract
- **Requirement:** REQ-001
- **Input:** fake CLI records stdin or argv prompt content and the executor-created branch-output path.
- **Expected output:** the prompt names the task ID, repo, task spec path, working directory, and the file path where the CLI must write the produced branch. The branch-output path is inside an executor-owned temp directory, not the repo.
- **Edge cases:** task with blank ID or blank spec path is rejected before subprocess start.

### TC-006: documented token mechanism matches implementation
- **Requirement:** REQ-003
- **Input:** inspect `docs/spec/configuration.md` and executor tests.
- **Expected output:** the spec documents `ANTHROPIC_API_KEY` as the Claude CLI executor token source, describes revocability, no host-home reads by default, no token logging, and missing-token fail-fast behavior. Tests assert the same environment variable name.
- **Edge cases:** implementation must not read arbitrary credential files from the host home directory by default.

## Notes
Framework: Go `testing`.

Strategy:
- Unit/integration tests live under the executor package and use a fake CLI subprocess script/binary.
- L5 validation uses the fake CLI to exercise the full subprocess path, including environment injection and branch capture.
- L6 is gated on real Claude Code CLI availability and independently revocable auth. If auth is unavailable, record the L6 gap and keep the coverage-tracker status 🟡.

Required marker coverage:
- Every `TC-xxx` marker above must appear in a concrete Go assertion comment in executor tests.
