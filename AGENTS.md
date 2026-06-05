# agent-builder Agent Instructions

This repository's fullest agent briefing lives in `CLAUDE.md`. Reuse it as
the source of truth for project context, commands, architecture invariants,
task workflow, verification expectations, and boundaries.

Before changing code or docs:

1. Read `CLAUDE.md`.
2. Read the relevant task file and test spec when working on planned work.
3. Read `docs/architecture/overview.md` for system context.
4. Check `docs/spec/SPEC.md` and update the matching `docs/spec/` files in
   the same change when behavior, interfaces, architecture, configuration, or
   data model change.

## Codex and Cross-Tool Notes

- Prefer the existing `scripts/start-task.sh <NNN> <slug>` flow for task
  branch or worktree isolation.
- Run `make check` before claiming implementation work is complete.
- Use `go test ./...`, `go build ./...`, and `gofmt -w .` for narrower Go
  verification and formatting loops.
- Keep edits scoped. Do not reorganize the Claude-specific hook or agent files
  unless the task explicitly asks for that integration work.
- Treat `.claude/agents/` as implementation guidance for Claude subagents, not
  as automatically available Codex agents. If a workflow depends on one, mirror
  the intent manually in the current task.

## Codex Short Commands

Use these short phrases as automatic repo-local workflows. The user should not
need to paste the long Claude agent prompts.

- `work task NNN`, `start task NNN`, `continue task NNN`, or
  `use task-executor on task NNN`: locate the matching task file under
  `docs/tasks/{backlog,active,completed}/NNN-*.md` and the paired
  `docs/tasks/test-specs/NNN-*-test-spec.md`; read
  `.claude/agents/task-executor.md`; then follow that workflow. Start with
  `scripts/start-task.sh <NNN> <slug>` unless already in the correct isolated
  task branch/worktree.
- `verify task NNN`, `spec verify NNN`, or `use spec-verifier on task NNN`:
  read `.claude/agents/spec-verifier.md` and perform its assertion-by-assertion
  gate against the task, test spec, diff, and test output. Do not edit files.
- `review task NNN`, `review current diff`, or `/code-review`: read
  `.claude/agents/code-reviewer.md` and respond in code-review mode with
  findings first.
- `architect task NNN`, `architecture review`, or `drift audit`: read
  `.claude/agents/architect.md` and apply that role to the requested scope.
- `security audit task NNN` or `security review`: read
  `.claude/agents/security-auditor.md` and apply that role to the requested
  scope.

When the user explicitly asks to delegate, run parallel agents, or "use" one of
the named executor/reviewer agents as a subagent, spawn Codex subagents using
the matching `.claude/agents/*.md` file as the role prompt. Otherwise, execute
the workflow locally in the current Codex session.

For multiple tasks with dependencies, act as the parent coordinator:

- Parse the task list into dependency levels before spawning agents.
- Spawn Codex subagents only for tasks whose prerequisites are already complete
  and whose expected write scopes do not overlap.
- Give every code-modifying subagent the matching `.claude/agents/task-executor.md`
  workflow, its task path, its test-spec path, and a fail-fast instruction to
  prove it is in an isolated branch/worktree before editing.
- Keep dependent tasks queued until their prerequisite agents finish and their
  results are reviewed.
- After parallel code agents finish, audit isolation and integration before
  starting the next dependency level.

## Boundaries To Preserve

- Test specs come before implementation.
- One task equals one repo branch or isolated worktree.
- Do not commit directly to `main`.
- Do not mark a task verified until the spec-verifier gate and the required
  runtime or harness evidence exist.
- Do not use destructive git commands or path checkouts over uncommitted work.
- Do not add dependencies or restructure the project without a task-level need.
