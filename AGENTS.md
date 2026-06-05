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

## Boundaries To Preserve

- Test specs come before implementation.
- One task equals one repo branch or isolated worktree.
- Do not commit directly to `main`.
- Do not mark a task verified until the spec-verifier gate and the required
  runtime or harness evidence exist.
- Do not use destructive git commands or path checkouts over uncommitted work.
- Do not add dependencies or restructure the project without a task-level need.
