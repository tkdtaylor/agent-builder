# ADR 051 — Native Ollama executor harness

**Status:** proposed
**Date:** 2026-06-28
**Supersedes:** none
**Related:** ADR 043 (executor registry/router), ADR 040 (ecosystem assembly layer), tasks 091/092/095/101 (local-model stack predecessors), task 094 (live investigation that motivated this)

---

## Context

During the 2026-06-28 live investigation of task 094, the LiteLLM + Claude Code CLI
route to a local Ollama model was proven to work **mechanically** (auth, routing, proxy
`200 OK`) but fail **semantically**: the Claude Code CLI never executes tool calls produced
by local models on the LiteLLM `openai/<model>` (Ollama OpenAI-compat `/v1`) path,
because LiteLLM returns tool calls as **plain-text JSON** rather than structured
`tool_calls`. The Claude Code CLI never interprets these as real tool invocations,
so no files are written and no branch is produced.

The workaround — routing through LiteLLM `ollama_chat/<model>` (Ollama native
`/api/chat`) — preserves structured `tool_calls` in the Anthropic proxy response, but
it adds a second layer of impedance (LiteLLM + the Claude Code CLI are still both in
the loop). The fundamental issue is that the Claude Code CLI's agentic loop was built
for Anthropic's own infrastructure, not as a general harness for local model inference.

**Root cause (committed to `docs/operating.md` and `docs/agent-rules.md`, 2026-06-28):**
The executor seam needs *structured* tool calls. The LiteLLM `openai/<model>` provider
never delivers them for local models. Depending on LiteLLM's `ollama_chat/<model>`
path is a brittle workaround — it ties the seam to LiteLLM's internal routing logic
and depends on the Claude Code CLI interpreting `tool_use` blocks that originated
from a non-Anthropic model, which is untested and model-version-sensitive.

## Decision

Add a **native Ollama executor harness** that calls Ollama's `/api/chat` endpoint
directly and executes tool calls itself, removing LiteLLM and the Claude Code CLI
from the local-model execution path entirely.

The new harness, `internal/executor/ollama_native.go`, implements `supervisor.Executor`
and satisfies the existing `(harness, model) → branch` contract without any proxy.

## Architecture shape

### 1. Thin Ollama native client (`internal/executor/ollamaclient/`)

A minimal HTTP client for Ollama's `/api/chat` endpoint:

```
POST <base_url>/api/chat
{
  "model": "<model>",
  "messages": [...],
  "tools": [...],
  "stream": false
}
```

Returns structured `message.tool_calls` (for models that support them) or
`message.content` (for terminal responses). No LiteLLM. No Claude Code CLI.

**Model requirement:** Models must return structured `tool_calls` via Ollama's
`/api/chat`. As of Ollama 0.17.7 on 2026-06-28:
- `qwen3:8b` returns parseable `tool_calls` (verified live).
- `qwen2.5-coder:7b` does NOT — it emits bare JSON without the `<tool_call>` wrapper,
  so Ollama never parses `tool_calls`. This is a known per-model limitation.

This is documented as an operator requirement: use `qwen3:8b` or equivalent.

### 2. Agentic tool-execution loop

The harness drives a stateful in-process loop:

1. Build the initial prompt from `supervisor.Task` (spec text, repo path, worktree path).
2. Append the tool schema to the first request.
3. Send the request to `/api/chat`.
4. If the response contains `tool_calls`: dispatch each to the tool set, append
   results as `role:"tool"` messages, go to step 3.
5. If no `tool_calls` (terminal response or explicit completion signal from the model):
   read the produced branch from the worktree, return `Result{Branch, OK}`.
6. If the hard iteration cap (`MaxIterations`, default 30) is reached: return
   `Result{OK: false}` — the verify gate's normal escalation path applies.

### 3. Worktree-confined tool set

Minimum viable tool set for task execution:

| Tool | Description | Confinement |
|------|-------------|-------------|
| `write_file` | Write content to a file | Path must be under the worktree |
| `read_file` | Read file content | Path must be under the worktree |
| `list_dir` | List directory entries | Path must be under the worktree |
| `run_command` | Run a command in the worktree | CWD = worktree; explicit allowlist of safe commands |

**Security (load-bearing):** every tool operation that takes a path MUST:
1. Resolve the path against the worktree root.
2. Reject any path that resolves outside the worktree (absolute escape, `..` traversal,
   symlink escape). Return a structured error to the model; do NOT panic or silently
   allow.

`run_command` is the highest-risk surface. It is initially scoped to the git workflow
and the verification gate commands the task executor needs (`git`, `go`, `golangci-lint`,
`gofmt`). The outer exec-sandbox box + nftables egress allowlist remain the enforcement
perimeter; `run_command` restriction is defense-in-depth at the tool layer.

This task is **flagged for security-auditor review** before merge.

### 4. Registry driver value + runtime wiring

A new `HarnessDriver` constant `HarnessOllamaNative = "ollama-native"` is added to
`internal/registry/types.go`. The `String()` case is added. The `buildExecutorForEntry`
switch in `internal/runtime/run.go` routes `HarnessOllamaNative` to the new
`OllamaNative` constructor.

The entry's existing fields carry all needed configuration:
- `entry.Endpoint` — Ollama base URL (e.g. `http://localhost:11434`)
- `entry.ModelID` — model name (e.g. `qwen3:8b`)

No new env vars are required beyond what is already part of the registry entry
env-var contract (`AGENT_BUILDER_REGISTRY_<ID>_ENDPOINT`,
`AGENT_BUILDER_REGISTRY_<ID>_MODEL`).

## Why not the LiteLLM + Claude Code CLI path?

| Path | Structured tool_calls? | Dependencies | Risk |
|------|----------------------|-------------|------|
| LiteLLM `openai/<model>` | **No** — returns bare JSON | LiteLLM, claude CLI | Tool calls never execute |
| LiteLLM `ollama_chat/<model>` | Yes — but brittle | LiteLLM, claude CLI, LiteLLM routing internals | Model+version-specific; two extra layers |
| **Native `/api/chat` (this ADR)** | **Yes** — directly parsed | Ollama only | Clean; one less layer; tool execution is in-process |

The native path removes LiteLLM and the Claude Code CLI from the hot path, making
tool-call dispatch reliable and model-agnostic within Ollama's supported model set.

## Termination policy

| Condition | Action |
|-----------|--------|
| Model returns no `tool_calls` | Extract branch from worktree, return `Result{Branch, OK:true}` |
| Hard cap (`MaxIterations`, default 30) reached | Return `Result{OK: false}` — normal escalation |
| Tool error (path escape, run_command denied) | Append structured error to messages; continue loop |
| Context cancellation | Return `ctx.Err()` — supervisor timeout kills the box |

The verify gate remains the definition of done. A completed harness loop that does not
produce a valid, gate-passing branch is treated as a failed attempt (escalation).

## Security model

- **Path confinement** is the load-bearing tool-layer control: no `write_file`,
  `read_file`, or `list_dir` call may escape the worktree. Implemented via
  `filepath.Abs` + `strings.HasPrefix(resolved, worktreeAbs)`. Symlink traversal
  is rejected by resolving the path with `filepath.EvalSymlinks` before the prefix
  check.
- **`run_command` allowlist** is defense-in-depth. The outer exec-sandbox box with
  its nftables egress allowlist remains the enforcement perimeter.
- **No auth secrets** flow through this harness. Ollama runs on localhost; no token
  is needed. `entry.SecretRef` is empty for local entries (same as translation-proxy
  entries, per ADR 043).
- **No executor ingestion harness** is needed. This harness does not invoke the Claude
  Code CLI, so the `ClaudeIngestionPolicy` / armor web-guard path does not apply.
- **Security-auditor review required** before merge (task 104, which contains
  `run_command`).

## Fitness functions

No new fitness functions are needed. The existing F-003 (supervisor isolation) and
F-010 (orchestrator authors no code) remain the guards. The new package
`internal/executor/ollamaclient` must not be imported by `internal/supervisor` — this
is already enforced by F-003.

## Verification

- **L2:** unit tests with a stubbed Ollama HTTP server returning canned `tool_calls`
  responses. Assert: loop executes tools against a temp worktree, produces a branch,
  terminates on the iteration cap, confines paths, and returns `Result{Branch}`.
- **L5/L6 (deferred, operator-run):** live run against real Ollama + `qwen3:8b`
  producing a branch + gate passing. Hardware-specific; not CI-automatable. Mark
  deferred in the same style as tasks 094 and 101.

## Consequences

- `HarnessOllamaNative` becomes a first-class harness driver value in the registry.
- The router selects it on the same capability/cost axes as other entries; no
  special-casing needed.
- LiteLLM is no longer required for local-model execution (it remains supported via
  the existing translation-proxy path for operators who prefer it).
- The `run_command` tool surface requires a security-auditor review on every change
  to its allowlist or invocation logic.
- `qwen2.5-coder:7b` is documented as incompatible with this harness (no structured
  `tool_calls`). Operators must use `qwen3:8b` or a model confirmed to produce
  `tool_calls` via `/api/chat`.
