# agent-builder — Claude Code layer

The canonical, harness-neutral briefing for this repo is **`AGENTS.md`**. Read it
first — it holds project context, commands, architectural invariants, the task
workflow, commit rules, boundaries, and the load-bearing process rules. This file
adds only what is **specific to Claude Code** (skills, subagents, hooks, plan mode).

@AGENTS.md

---

Everything below is Claude Code-specific and supplements `AGENTS.md`.

## Subagents

Use the **task-executor** agent to work through tasks one at a time. Each agent call
is ephemeral — it reads the task file, does the work, commits, and reports back
without bloating the main conversation.

```
use task-executor — task: docs/tasks/backlog/NNN-name.md, spec: docs/tasks/test-specs/NNN-name-test-spec.md
```

The workflow (test spec first, `scripts/start-task.sh` for isolation, 🟡 feat commit,
spec-verifier, 🟡→✅ verify commit, merge) is defined in `AGENTS.md`. The named roles
map to subagents under `.claude/agents/` (`task-executor`, `spec-verifier`,
`code-reviewer`, `architect`, `security-auditor`, `qa`, `task-planner`,
`docs-writer`, `dependency-auditor`).

When dispatching parallel agents in one message, run
`scripts/verify-worktree-isolation.sh <agent-id> [<agent-id> ...]` after they
complete to confirm none bypassed the worktree flag.

## Plan mode

When you exit plan mode, a hook automatically restructures the plan:
- Each step becomes a task file in `docs/tasks/backlog/`
- Test spec stubs are created for each task
- The plan is replaced with a lightweight skeleton to save context tokens
- The full plan is backed up to `docs/plans/`

### End handoffs with a resume command

When a response completes a logical milestone that leaves follow-on work (a task
planned but not executed, an ADR drafted awaiting implementation, a handoff to
another session or agent), end the response with a **fenced code block** containing
the exact resume command. Not inline backticks, not a prose description, not a vague
pointer — a fenced code block is what renders the copy button in the VSCode chat UI.
Inline code does not get that affordance.

**Verify the path exists before writing the resume block.** Glob
`docs/tasks/backlog/NNN-*.md` (and the matching
`docs/tasks/test-specs/NNN-*-test-spec.md`) and copy the real filenames into the
block. Do NOT infer filenames from the plan or from a prior message — the plan-mode
hook may rename task files as it writes them out, and a wrong path wastes a whole
task-executor round trip when the user or future session blindly pastes it.

If there is genuinely nothing to resume (the work is fully shipped, nothing follows),
skip the block. This is a rule for real handoffs, not a ritual at the end of every
message.

## Agent rules and retros

Process-level rules, common rationalizations, and project-specific retros live in
[docs/agent-rules.md](docs/agent-rules.md) (their essentials are also inlined in
`AGENTS.md` so every harness sees them). The `inject-retros.py` SessionStart hook
reads that file and surfaces relevant entries at the start of every session, so
adding an entry there is how a one-time mistake becomes a permanent guard. The file
ships with rules covering parallel-dispatch worktree isolation, the `git checkout --
<path>` hazard, smoke-test rationalization, dead-code delegates, and a "Common
rationalizations" table.

## Hook profiles

Hooks run automatically and are gated by profile level. Control via environment
variables:

```bash
export CLAUDE_HOOK_PROFILE=minimal    # Safety hooks only (secret protection, block-no-verify, config-protection, protect-checkout)
export CLAUDE_HOOK_PROFILE=standard   # + workflow hooks (plan restructuring, compaction, checkpoints) — default
export CLAUDE_HOOK_PROFILE=strict     # + formatting, fitness, notifications (batch-format-typecheck, edit-tracker, check-fitness, desktop-notify)
export CLAUDE_DISABLED_HOOKS=desktop-notify,batch-format-typecheck  # Disable specific hooks
```

Already wired via `.claude/settings.json` (standard profile): `no-commit-on-main`,
`protect-secrets`, `block-no-verify`, plan→tasks restructuring, compaction guards,
spec-coverage-check.

## Skills

- **code-scanner** — scan any target repo/package/deps before the agent builds on or
  runs them. Trigger: "scan this repo for malware"
- **code-review** — review the agent's diffs before merge. Trigger: `/code-review`
- **deep-research** — when designing a new block, survey prior art / build-vs-adopt.
  Trigger: "deep research on <X>"
