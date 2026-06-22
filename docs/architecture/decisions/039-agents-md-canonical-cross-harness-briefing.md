# ADR 039 — AGENTS.md as the canonical cross-harness agent briefing

**Status:** Accepted — AGENTS.md is the live canonical briefing (CLAUDE.md imports it, GEMINI.md symlinks it); pilot rolling out to the block repos
**Date:** 2026-06-21
**Supersedes:** the prior arrangement where `CLAUDE.md` was the canonical briefing and `AGENTS.md` was a pointer to it; relocates the retro log introduced as `docs/architecture/agent-rules.md`.

## Context

agent-builder is run by more than one coding-agent harness. Claude Code is the
primary driver, but the repo is also exercised by Codex and is intended to be
portable to other agentic harnesses (e.g. Antigravity / the Gemini family). Each
harness auto-loads a *different* instruction file:

| Harness | Auto-loads | Import support |
|---|---|---|
| Claude Code | `CLAUDE.md` (root + nested) + the `inject-retros.py` SessionStart hook | `@path` imports |
| Codex | `AGENTS.md` (the open *agents.md* standard) | nested-file merge |
| Antigravity / Gemini | `AGENTS.md` and/or `GEMINI.md` | `@path` imports |

Before this ADR, the load-bearing project rules reached **only Claude Code**:

1. **`CLAUDE.md` was canonical** — it carried project orientation, architectural
   invariants, conventions, commit rules, and boundaries. Only Claude Code
   auto-loads it.
2. **`AGENTS.md` was a stub** — four lines telling the reader to "go read
   `CLAUDE.md`." A pointer is not a load: a harness that reads `AGENTS.md` is not
   guaranteed to follow a prose instruction to open another file, and even when it
   does, `CLAUDE.md` is full of Claude-only mechanics (hook profiles, the Skill
   tool, `.claude/agents/` subagents) that are noise to a non-Claude harness.
3. **The retro log was gated behind a Claude-only hook.** `agent-rules.md` — the
   append-only log of project-specific lessons and refused rationalizations — was
   surfaced exclusively by `inject-retros.py`, a Claude Code SessionStart hook.
   Codex and Antigravity never saw a single retro.
4. **The retro log was also misfiled.** It lived under `docs/architecture/`, whose
   every other member (`overview.md`, `diagrams.md`, `tech-stack.md`, `decisions/`)
   describes *the built system*. `agent-rules.md` describes *how the agent should
   behave while building* — it is a sibling of `CLAUDE.md` and `operating.md`
   (process/runbook docs), not a description of the system's architecture.

The net effect: the rules that exist specifically to stop an agent from making a
preventable mistake were invisible to two of the three harnesses that drive the
repo.

### The core design question

*Where must the load-bearing rules live so that every harness — not just Claude
Code — actually loads them?*

The answer follows from one fact: **a referenced file is not guaranteed to be
loaded by every harness; the file each tool auto-loads is.** Therefore the
essentials must be **inline in the file each tool reads**, with a single source of
truth to avoid drift.

## Decision

**Make `AGENTS.md` the canonical, harness-neutral briefing.** It is the one file
all three harness families auto-load (directly, or via a symlink). The other
entrypoints become thin, harness-specific layers over it.

### Roles after the inversion

- **`AGENTS.md` (root) — canonical, neutral.** Carries project orientation, the
  architectural invariants, conventions, the task workflow, commit rules, and the
  **load-bearing process rules inlined** (commit-after-every-milestone, work on a
  task branch never `main`, test-spec-before-code, the 🟡-vs-✅ verification ladder,
  no smoke tests where assertions are asked, producer→consumer trace before "done",
  no `git checkout -- <path>` over uncommitted work). Inlined, not linked — so a
  harness that reads only `AGENTS.md` still gets the essentials. Written neutrally
  ("the agent / the harness"), with no Claude-specific mechanics.

- **`CLAUDE.md` — Claude-specific layer.** Pulls the shared content via an
  `@AGENTS.md` import (single source of truth, zero duplication), then adds only
  what is Claude Code-specific: the Skill tool and user-invocable skills, the
  `.claude/agents/` subagents, `CLAUDE_HOOK_PROFILE` and the hook roster, the
  plan-mode→tasks restructuring hook, and the `inject-retros.py` dynamic-surfacing
  mechanism.

- **`GEMINI.md` — symlink to `AGENTS.md`.** Gives the Gemini family the full inline
  briefing (a symlink delivers content, not a pointer). Codex needs no shim — it
  reads `AGENTS.md` directly.

- **`docs/agent-rules.md` — the full retro appendix.** `agent-rules.md` moves out
  of `docs/architecture/` to the docs root, beside `operating.md` (the other
  cross-cutting process doc). It remains the append-only incident log; its
  *essentials* are promoted into `AGENTS.md` so every harness gets them inline. The
  `inject-retros.py` hook continues to surface the full log dynamically for Claude
  Code, now reading from the new path.

### Why inline-plus-import rather than three copies

Three independently-edited copies drift. One canonical file (`AGENTS.md`) plus an
`@`-import (`CLAUDE.md`) plus a symlink (`GEMINI.md`) yields one source of truth
that every harness still loads in full. The only content that is *not* shared is
the genuinely harness-specific layer, which is small and lives only in `CLAUDE.md`.

## Consequences

- **The rules reach every harness.** Codex (via `AGENTS.md`) and Antigravity/Gemini
  (via `GEMINI.md` → `AGENTS.md`) now load the same load-bearing rules Claude Code
  has always had. The portability gap is closed.
- **Single source of truth.** Shared content lives once, in `AGENTS.md`. `CLAUDE.md`
  imports it; `GEMINI.md` symlinks it. No copy can silently diverge.
- **`CLAUDE.md` shrinks to its Claude-specific essence.** Hook profiles, skills,
  subagents, and the retro-injection mechanism stay; everything neutral moves to
  `AGENTS.md`. Claude Code behavior is unchanged — `@AGENTS.md` resolves to the same
  content it used to carry inline.
- **`agent-rules.md` is correctly filed and reachable.** It sits at
  `docs/agent-rules.md` (process doc beside `operating.md`), its essentials inlined
  into `AGENTS.md`, its full log still injected for Claude via the hook (path
  updated). The hook, `task-executor.md`, and `verify-worktree-isolation.sh` are
  rewired to the new path in the same change.
- **The pattern propagates.** This ADR pilots the arrangement in agent-builder. The
  same inversion is to be applied to the foundational block repos and baked into the
  `create-project` skill template, so newly scaffolded projects inherit the
  cross-harness layout instead of the Claude-only one.

## Rollout

1. **Pilot (this task):** apply the inversion to agent-builder only — author the
   canonical `AGENTS.md`, slim `CLAUDE.md` to an `@AGENTS.md` import plus
   Claude-specifics, add the `GEMINI.md` symlink, move `agent-rules.md`, rewire the
   three references. Verify every cross-reference resolves and the hook finds the
   moved log.
2. **Fan out:** once the pattern is confirmed, apply it to the foundational block
   repos (exec-sandbox, vault, policy-engine, audit-trail, and the rest), where the
   same misplacement and a second issue — generic boilerplate inlined into
   project-specific `overview.md` files instead of referenced — are also corrected.
3. **Root cause:** update the `create-project` skill template so the
   AGENTS.md-canonical layout is the default for every future project.

## Spec / doc files updated in this change

- `AGENTS.md` — rewritten as the canonical neutral briefing (new role).
- `CLAUDE.md` — slimmed to the Claude-specific layer with an `@AGENTS.md` import.
- `GEMINI.md` — new symlink to `AGENTS.md`.
- `docs/agent-rules.md` — relocated from `docs/architecture/agent-rules.md`.
- `.claude/scripts/inject-retros.py`, `.claude/agents/task-executor.md`,
  `scripts/verify-worktree-isolation.sh` — rewired to the new retro-log path.
