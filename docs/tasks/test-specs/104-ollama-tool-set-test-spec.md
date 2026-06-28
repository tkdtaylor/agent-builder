# Test spec — Task 104: Worktree-confined Ollama tool set

**Linked task:** `docs/tasks/backlog/104-ollama-tool-set.md`
**Written:** 2026-06-28
**Status:** ready

## Context

The Ollama native executor harness (task 103) dispatches tool calls it receives from
the model. This task implements the concrete tool set those calls are dispatched to.

The tool set lives in `internal/executor/ollamatoolset/` and exposes:

| Tool | Description |
|------|-------------|
| `write_file` | Write bytes to a file path relative to the worktree |
| `read_file` | Read bytes from a file path relative to the worktree |
| `list_dir` | List entries in a directory path relative to the worktree |
| `run_command` | Run an allowed command in the worktree (allowlisted: `git`, `go`, `gofmt`, `golangci-lint`) |
| `finish_branch` | Record the produced branch name for extraction by the loop (writes a reserved branch file) |

**Security is load-bearing.** Every tool that accepts a path MUST confine it to the
worktree. `run_command` MUST enforce a command allowlist. This task is **flagged for
security-auditor review** before merge.

The tool set is injected into `OllamaNative` via an interface seam, so task 103 tests
and task 104 tests are independent.

## Requirements coverage

| Req ID     | Test cases                     | Covered? |
|------------|--------------------------------|----------|
| REQ-104-01 | TC-104-01, TC-104-02           | yes      |
| REQ-104-02 | TC-104-03, TC-104-04           | yes      |
| REQ-104-03 | TC-104-05, TC-104-06, TC-104-07 | yes     |
| REQ-104-04 | TC-104-08, TC-104-09           | yes      |
| REQ-104-05 | TC-104-10                      | yes      |

---

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-104-01 — write_file creates a file with exact content in the worktree

- **Requirement:** REQ-104-01
- **Level:** L2 (unit test with real temp worktree)
- **Test file:** `internal/executor/ollamatoolset/toolset_test.go`

**Setup:** Create a temp directory as `worktree`. Construct `NewToolSet(worktree)`.

**Input:** `toolset.Dispatch("write_file", `{"path":"subdir/hello.txt","content":"hello world"}`)`.

**Expected output:**
- Returns a non-error result string (e.g. `"ok"` or `"wrote 11 bytes"` — exact
  text implementation-defined, but must be non-empty).
- The file `<worktree>/subdir/hello.txt` exists.
- `os.ReadFile("<worktree>/subdir/hello.txt")` returns exactly `[]byte("hello world")`.
- Missing parent directories are created automatically (no error for `subdir/`).

---

### TC-104-02 — read_file returns exact content of a file in the worktree

- **Requirement:** REQ-104-01
- **Level:** L2 (unit test with real temp worktree)
- **Test file:** `internal/executor/ollamatoolset/toolset_test.go`

**Setup:** Create `<worktree>/data.txt` containing `"read me"`.

**Input:** `toolset.Dispatch("read_file", `{"path":"data.txt"}`)`.

**Expected output:**
- Returns a result string equal to `"read me"` (exact content, not just non-empty).

**Input (missing file):** `toolset.Dispatch("read_file", `{"path":"nonexistent.txt"}`)`.

**Expected output:**
- Returns a non-nil error containing `"not found"` or `"no such file"` (the tool
  must surface a structured error, not panic).

---

### TC-104-03 — Path-escape via `..` is rejected for write_file

- **Requirement:** REQ-104-02
- **Level:** L2 (unit test — security)
- **Test file:** `internal/executor/ollamatoolset/toolset_test.go`

**Setup:** Worktree is at `/tmp/wt-XXXXXX`. Ensure `/tmp/escape-target.txt` does not
exist before the test.

**Input:** `toolset.Dispatch("write_file", `{"path":"../../escape-target.txt","content":"bad"}`)`.

**Expected output:**
- Returns a non-nil error containing `"outside"` or `"confined"` or `"worktree"`.
- The file `/tmp/escape-target.txt` does NOT exist after the call.
- The worktree directory is unchanged.

**Rationale:** Path traversal is the primary tool-layer escape vector. The exact
error text must name the worktree confinement failure clearly enough that the model
can understand the rejection.

---

### TC-104-04 — Absolute path outside worktree is rejected for read_file

- **Requirement:** REQ-104-02
- **Level:** L2 (unit test — security)
- **Test file:** `internal/executor/ollamatoolset/toolset_test.go`

**Input:** `toolset.Dispatch("read_file", `{"path":"/etc/passwd"}`)`.

**Expected output:**
- Returns a non-nil error containing `"outside"` or `"confined"` or `"worktree"`.
- The call does NOT read `/etc/passwd` — confirmed by verifying the error is returned
  before `os.ReadFile` is called (assert via either a test spy or by using a
  non-readable path that would surface a permissions error if reached, and asserting
  the error text does NOT contain `"permission"` or `"denied"`).

**Rationale:** Absolute paths are a second path-escape vector. The confinement check
must fire before any OS call.

---

### TC-104-05 — run_command executes an allowlisted command in the worktree

- **Requirement:** REQ-104-03
- **Level:** L2 (unit test with real temp worktree, real subprocess)
- **Test file:** `internal/executor/ollamatoolset/toolset_test.go`

**Setup:** Create a temp directory as `worktree`. Initialize a git repo inside it
(`git init --initial-branch=main .` and `git commit --allow-empty -m init`).

**Input:** `toolset.Dispatch("run_command", `{"command":"git","args":["status"]}`)`.

**Expected output:**
- Returns a non-nil, non-error result string containing `"branch"` or `"main"`
  (output of `git status` in an initialized repo — exact text is environment-dependent
  but must be non-empty and contain recognizable git output).
- The subprocess runs with `CWD == worktree` (verified by asserting the output is
  consistent with the initialized repo, not `/tmp` or the test process CWD).

---

### TC-104-06 — run_command rejects a non-allowlisted command

- **Requirement:** REQ-104-03
- **Level:** L2 (unit test — security)
- **Test file:** `internal/executor/ollamatoolset/toolset_test.go`

**Input A:** `toolset.Dispatch("run_command", `{"command":"curl","args":["http://example.com"]}`)`.

**Expected output A:**
- Returns a non-nil error containing `"not allowed"` or `"allowlist"` or `"denied"`.
- No subprocess is launched (confirmed: `curl` is not on the allowlist; the check
  fires before `exec.Command` is called).

**Input B:** `toolset.Dispatch("run_command", `{"command":"/bin/sh","args":["-c","id"]}`)`.

**Expected output B:**
- Returns a non-nil error containing `"not allowed"` or `"allowlist"` or `"denied"`.
- No subprocess is launched.

**Rationale:** Shell injection and arbitrary binary execution are the highest-risk
surfaces of `run_command`. The allowlist check must be the FIRST thing checked,
before any path resolution or subprocess construction.

---

### TC-104-07 — run_command allowlist covers exactly the four expected commands

- **Requirement:** REQ-104-03
- **Level:** L2 (unit test — security, compile-time + runtime)
- **Test file:** `internal/executor/ollamatoolset/toolset_test.go`

**Input:** Call `toolset.AllowedCommands()` (or equivalent exported function/constant
that enumerates the allowed commands at the package level).

**Expected output:**
- The returned set is exactly `{"git", "go", "gofmt", "golangci-lint"}` — no more,
  no less.
- Assert `len(allowed) == 4`.
- Assert each of the four names is present in the set.

**Rationale:** Enumerating the allowlist in a test catches accidental additions to
the set during future development. If `run_command` is extended, this test must be
updated deliberately — the test is a speed bump on the review path for allowlist
changes.

---

### TC-104-08 — list_dir returns the names of files in a worktree subdirectory

- **Requirement:** REQ-104-04
- **Level:** L2 (unit test with real temp worktree)
- **Test file:** `internal/executor/ollamatoolset/toolset_test.go`

**Setup:** Create `<worktree>/src/a.go` and `<worktree>/src/b.go`.

**Input:** `toolset.Dispatch("list_dir", `{"path":"src"}`)`.

**Expected output:**
- Returns a non-error result string.
- The result contains both `"a.go"` and `"b.go"` (exact filenames present in the
  output, regardless of format/ordering).

**Input (path outside worktree):** `toolset.Dispatch("list_dir", `{"path":"../etc"}`)`.

**Expected output:**
- Returns a non-nil error containing `"outside"` or `"confined"`.

---

### TC-104-09 — finish_branch writes the branch name to the reserved branch file

- **Requirement:** REQ-104-04
- **Level:** L2 (unit test with real temp worktree)
- **Test file:** `internal/executor/ollamatoolset/toolset_test.go`

**Input:** `toolset.Dispatch("finish_branch", `{"branch":"task/104-my-branch"}`)`.

**Expected output:**
- Returns a non-error result.
- The reserved branch file (e.g. `<worktree>/.agent-branch` or the same
  producer-written path that `OllamaNative.Run` reads) contains exactly
  `"task/104-my-branch"` (trimmed, no trailing newline in the stored value or the
  value after trim).
- `toolset.ExtractBranch()` (or the loop's branch-extraction call) returns
  `"task/104-my-branch"`.

**Rationale:** This proves the producer (finish_branch) and consumer (OllamaNative loop)
are wired to the same file path. The loop must read the same file the tool writes.

---

### TC-104-10 — Tool schema is valid JSON and names all five tools

- **Requirement:** REQ-104-05
- **Level:** L2 (unit test)
- **Test file:** `internal/executor/ollamatoolset/toolset_test.go`

**Input:** Call `toolset.ToolSchemas()` (or the exported function that returns the
`[]ollamaclient.Tool` slice the loop sends to Ollama in the first request).

**Expected output:**
- `len(schemas) == 5` (write_file, read_file, list_dir, run_command, finish_branch).
- Each schema has a non-empty `Function.Name`.
- The set of `Function.Name` values is exactly
  `{"write_file", "read_file", "list_dir", "run_command", "finish_branch"}`.
- Each schema serializes to valid JSON without error
  (`json.Marshal(schema)` returns nil error for each entry).

**Rationale:** The schema is what Ollama uses to select and invoke tools. A missing
or misspelled tool name causes the model to emit tool calls the loop cannot dispatch.

---

## Verification plan

- **Highest level achievable in CI:** L2 — unit tests on real temp worktrees.
  TC-104-05 invokes a real `git` subprocess (git is on the CI PATH).
- **L2 harness command:**
  ```
  go test -count=1 ./internal/executor/ollamatoolset/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/executor/ollamatoolset`
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **Security-auditor review required** before merge (REQ-104-03, `run_command`
  allowlist and path-confinement logic).
- **L6 (deferred, operator-run):** exercise the full `OllamaNative` loop against a
  live Ollama + `qwen3:8b` instance and confirm the tool set handles real model
  tool calls correctly (paths confined, command allowlist enforced, branch produced).

## Security review flags

This task introduces `run_command`, the highest-risk surface in the tool set. The
security-auditor must verify:

1. **Allowlist check is the first operation** in `run_command` — before any path
   resolution or subprocess construction (TC-104-06 asserts this, but the auditor
   must confirm in the diff that the ordering is correct).
2. **Path confinement uses `filepath.Abs` + `strings.HasPrefix`** (or equivalent) with
   the worktree absolute path, and includes symlink resolution via
   `filepath.EvalSymlinks` before the prefix check (TC-104-03 and TC-104-04 assert
   the visible behavior, but the auditor must verify the implementation resolves symlinks).
3. **`run_command` sets `cmd.Dir` to the worktree absolute path** — not a relative
   path, not the caller's process CWD.
4. **`run_command` does NOT set `cmd.Env`** in a way that bypasses the exec-sandbox
   box's environment (it should inherit the process environment unchanged).

## Out of scope

- The Ollama HTTP client (task 102).
- The agentic loop (task 103).
- Registry wiring (task 105).
- Prompt construction.
- Network egress from `run_command` (enforced by the outer exec-sandbox box nftables
  allowlist, not the tool set).
