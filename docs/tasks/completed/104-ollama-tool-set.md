# Task 104 — Worktree-confined Ollama tool set

**Status:** completed
**ID:** 104
**Slug:** ollama-tool-set
**Priority:** must-have
**Dependencies:** task 102 (type definitions from ollamaclient used in schemas)
**Depends on tasks:** 102
**Blocks tasks:** 103 (the loop needs the tool set interface), 105

**Spec:** `docs/tasks/test-specs/104-ollama-tool-set-test-spec.md`
**ADR:** `docs/architecture/decisions/051-ollama-native-executor-harness.md`
**Security review:** REQUIRED (security-auditor must review before merge — see below)

---

## Goal

Produce `internal/executor/ollamatoolset/` — the concrete tool set the Ollama
agentic loop dispatches to. It implements the `ToolDispatcher` interface defined
in task 103.

**Security is load-bearing.** This package contains `run_command`, a real subprocess
invocation surface. Every path-accepting tool is confined to the worktree. This task
MUST receive a security-auditor review before merging.

---

## Background

ADR 051 §3 defines the tool set and its security model:
- Path confinement via `filepath.Abs` + symlink resolution + prefix check.
- `run_command` command allowlist: `{"git", "go", "gofmt", "golangci-lint"}`.
- `finish_branch` as the mechanism for the model to declare the produced branch name.
- The outer exec-sandbox box (nftables egress allowlist + rootless Podman) remains
  the enforcement perimeter; these tool-layer controls are defense-in-depth.

---

## Requirements

### REQ-104-01 — write_file and read_file operate correctly within the worktree

`write_file` MUST create or overwrite a file at the given path (relative to the
worktree), creating parent directories as needed, and return a non-empty success
string. `read_file` MUST return the exact byte content of the file. Both MUST return
a non-nil error on OS failure (file not found, permission denied) containing the
underlying error text.

### REQ-104-02 — All path-accepting tools confine paths to the worktree (security)

Every tool that accepts a path (`write_file`, `read_file`, `list_dir`) MUST:
1. Resolve the path to an absolute path relative to the worktree root.
2. Resolve symlinks via `filepath.EvalSymlinks` before the prefix check.
3. Reject any path that does not have the worktree absolute path as a prefix.
4. Return a non-nil error with a description containing `"outside"`, `"confined"`,
   or `"worktree"` — NOT silently allow or silently ignore.

`..` traversal, absolute paths outside the worktree, and symlink-based escapes MUST
all be rejected.

### REQ-104-03 — run_command enforces a hard allowlist and runs in the worktree

`run_command` MUST:
1. Check the command name against the allowlist `{"git", "go", "gofmt", "golangci-lint"}`
   **before** any path resolution or subprocess construction.
2. Return a non-nil error containing `"not allowed"` or `"allowlist"` for any
   command not on the allowlist — no subprocess is launched.
3. Set `cmd.Dir` to the worktree absolute path.
4. Return the combined stdout+stderr output of the subprocess as the result string.
5. Return a non-nil error on non-zero subprocess exit, containing the exit code and
   stderr text.

### REQ-104-04 — list_dir and finish_branch operate correctly

`list_dir` MUST return the names of entries in the given worktree-relative directory,
subject to the same path-confinement rules as `write_file`/`read_file`.

`finish_branch(branch)` MUST write the branch name to a reserved file in the
worktree (e.g. `.agent-branch`) and return a non-error result. `ExtractBranch()` MUST
read and return that same file, or `("", false)` if it does not exist.

### REQ-104-05 — ToolSchemas returns valid schemas for all five tools

`ToolSchemas()` MUST return exactly five `ollamaclient.Tool` entries with names
`"write_file"`, `"read_file"`, `"list_dir"`, `"run_command"`, and `"finish_branch"`.
Each schema MUST serialize to valid JSON without error.

---

## Types and API

```go
package ollamatoolset

// ToolSet is the concrete tool dispatcher for the Ollama agentic loop.
type ToolSet struct { /* unexported */ }

func NewToolSet(worktree string) (*ToolSet, error)

// Dispatch routes a tool call by name to the correct handler.
// argsJSON is the raw JSON string from ToolCallFunction.Arguments.
// Returns a non-empty result string on success, or a non-nil error.
func (s *ToolSet) Dispatch(toolName string, argsJSON string) (string, error)

// ToolSchemas returns the JSON Schema descriptors for all five tools.
func (s *ToolSet) ToolSchemas() []ollamaclient.Tool

// AllowedCommands returns the set of commands permitted by run_command.
// Exported for the allowlist enumeration test (TC-104-07).
func AllowedCommands() map[string]struct{}

// ExtractBranch reads the reserved branch file from the worktree.
// Returns ("", false) if the file does not exist.
func (s *ToolSet) ExtractBranch() (string, bool)
```

---

## Security model (load-bearing)

The following invariants MUST hold and MUST be verified by the security-auditor:

1. **Allowlist check is the first statement in `run_command`** — before any variable
   reads, path resolution, or subprocess construction. A future maintainer adding a
   case before the check would re-introduce the vulnerability.

2. **Path confinement uses `filepath.EvalSymlinks` before `strings.HasPrefix`**:
   ```
   abs := filepath.Join(s.worktreeAbs, path)
   resolved, err := filepath.EvalSymlinks(abs)
   if err != nil { return error }  // fail closed
   if !strings.HasPrefix(resolved, s.worktreeAbs+string(filepath.Separator)) {
       return confinementError
   }
   ```
   The `+string(filepath.Separator)` suffix prevents a prefix attack where
   `/tmp/wt` falsely matches `/tmp/wt-escaped`.

3. **`run_command` sets `cmd.Dir` to `s.worktreeAbs`** — not a relative path, not
   the caller process CWD.

4. **`run_command` sets `cmd.Env` to an explicit minimal environment** — prevents
   subprocess from accessing orchestrator secrets or registry credentials. Environment
   includes only: `PATH`, `HOME` (if set), `GOCACHE`, `GOPATH` (Go-specific, if set),
   and hardened git variables `GIT_CONFIG_NOSYSTEM=1` + `GIT_CONFIG_GLOBAL=/dev/null`
   (prevents hook execution and secret leakage). The outer exec-sandbox box remains
   the enforcement perimeter.

---

## Acceptance criteria

- [ ] **AC-104-01:** TC-104-01 passes: `write_file` creates `subdir/hello.txt` with exact content `"hello world"`.
- [ ] **AC-104-02:** TC-104-02 passes: `read_file` returns `"read me"` for an existing file; returns non-nil error for a missing file.
- [ ] **AC-104-03:** TC-104-03 passes (security): `write_file` with `path="../../escape-target.txt"` returns an error containing `"outside"`/`"confined"`; file NOT created.
- [ ] **AC-104-04:** TC-104-04 passes (security): `read_file` with `path="/etc/passwd"` returns an error containing `"outside"`/`"confined"`; `/etc/passwd` NOT read.
- [ ] **AC-104-05:** TC-104-05 passes: `run_command` with `git status` in an initialized repo returns non-empty output containing git output.
- [ ] **AC-104-06:** TC-104-06 passes (security): `run_command` with `curl` or `/bin/sh` returns an error containing `"not allowed"`/`"allowlist"`; no subprocess launched.
- [ ] **AC-104-07:** TC-104-07 passes: `AllowedCommands()` returns exactly `{"git","go","gofmt","golangci-lint"}` (4 entries, no more, no less).
- [ ] **AC-104-08:** TC-104-08 passes: `list_dir` returns names containing both `"a.go"` and `"b.go"` for a seeded directory; returns an error for an out-of-worktree path.
- [ ] **AC-104-09:** TC-104-09 passes: `finish_branch("task/104-my-branch")` writes the branch file; `ExtractBranch()` returns `"task/104-my-branch"`.
- [ ] **AC-104-10:** TC-104-10 passes: `ToolSchemas()` returns exactly 5 entries with the correct names; each serializes to valid JSON.
- [ ] **Security-auditor APPROVE** before merge.
- [ ] **AC-104-11:** `make check` passes without any new warnings.

---

## Verification plan

- **Highest level achievable in CI:** L2 — real temp worktrees; TC-104-05 invokes
  a real `git` subprocess (assumed on CI PATH).
- **L2 command:**
  ```
  go test -count=1 ./internal/executor/ollamatoolset/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/executor/ollamatoolset`
- **Full gate:**
  ```
  make check
  ```
- **Security-auditor review** — flag with: "verify allowlist-first ordering, symlink
  resolution before prefix check, cmd.Dir set to worktreeAbs, cmd.Env not overridden."
- **L6 (deferred, operator-run):** exercise `ToolSet` via a live `OllamaNative` run
  with a real Ollama instance. Confirm real model tool calls are dispatched and
  confined. Hardware-specific; deferred per tasks 094/101.

---

## Out of scope

- The Ollama HTTP client (task 102).
- The agentic loop that calls this tool set (task 103).
- Registry wiring (task 105).
- Network egress from `run_command` — enforced by exec-sandbox box egress allowlist.
- Streaming or concurrent tool dispatch (all tool calls in a single turn are
  dispatched sequentially in this v1 implementation).
