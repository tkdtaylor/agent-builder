# agent-builder — Agent briefing (canonical)

This is the **canonical, harness-neutral briefing** for agent-builder. It is the
single source of truth for project context, commands, architectural invariants,
the task workflow, verification expectations, commit rules, and the load-bearing
process rules every agent must follow.

Every coding-agent harness loads this file:

- **Codex** auto-loads `AGENTS.md` (this file). The Codex-specific short commands
  are at the end under *Harness notes — Codex*.
- **Antigravity (`agy`)** loads it via `GEMINI.md` (a symlink to this file; the
  filename is legacy from the deprecated `gemini` CLI).
- **Claude Code** loads `CLAUDE.md`, which imports this file (`@AGENTS.md`) and adds
  the Claude-specific mechanics (skills, subagents, hooks).

Keep this file harness-neutral. Anything that only one harness understands belongs
in that harness's layer (`CLAUDE.md` for Claude Code), not here.

## What this is

agent-builder is a **security-first general autonomous agent** — the Secure Agent
Ecosystem's equivalent of OpenClaw / Hermes: persistent, extensible, self-improving, and
multi-LLM. It runs a **capable existing agent (Claude Code first) as its reasoning brain
inside the security envelope** — exec-sandbox, policy-engine + egress allowlist, vault,
audit-trail, and armor — and itself owns the gateway, the multi-LLM router, skills/memory
governance, and security. It **composes a brain; it does not reimplement reasoning.**

**Coding is one skill among many.** Contributing to a repo and starting a project are
*skills*, not the definition of the agent. The **autonomous coding agent is the first
reference build** — it works a target repo's tasks unattended, one at a time, behind a
machine-checkable verification gate — and most of this repo's code today realizes that
reference build. It is the proving ground for the general agent, not its boundary.

It composes the blocks over their **published contracts** and treats each block's own
README `## Scope` section as the authoritative statement of what that block does and does
not own; it does not restate or reimplement block responsibilities here. The authoritative
design is **`autonomous-builder.md`** in the internal planning hub — read it before
changing architecture (it still carries the older "builds the blocks" framing; reconcile
it against the north star above). The key invariants below derive from it.

**North star:** a general, secure autonomous agent you hand a goal and it works toward it
— composed brain, multi-LLM router, governed skills and memory, results returned over the
channel. The arc runs from the coding reference build outward to **many skills over the
same secure seams**, not toward an agent factory. Results and escalations flow back over
the same channel the request arrived on (**CLI now, Telegram next**), behind two human
gates — **plan/action approval** and **result review** — plus needs-human escalation.
**Self-improvement is secure skill-writing:** the agent authors and refines reviewable,
sandboxed skills; it never autonomously edits its own trusted core, gate, or escalation
path. Apache-2.0 licensed, part of the Secure Agent Ecosystem.

**The three brains** the router selects across are **local (ollama)**, **Claude**, and
**`agy`/Antigravity** — the latter the multi-model successor to the **`gemini` CLI, which
was deprecated 2026-06-18** (`gemini` is not a live brain; `agy` replaced it).

**Known missing / not-yet-built** (the forward path, not present capability): persistent
cross-session memory; an always-on heartbeat/daemon; a general self-extending skill
system; the **composed-brain-as-general-executor** — a non-coding execution path for the
cloud brains (today only the Ollama-native single-shot `Completer` answers without editing
a repo); broader multi-LLM routing; and the secure skill-writing loop.

**Origin (historical — don't reintroduce as the live purpose).** agent-builder began
as the agent meant to *build* the blocks and bootstrapped exec-sandbox (built on rented
isolation, then swapped onto it). The blocks shipped as independent repos; the
composition layer it pioneered is what carries forward. This history lives in ADR 040
(the repositioning) and the early ADRs (021/026/035) — do not re-assert "builds the
blocks" as the current mission.

## Project structure

```
cmd/          ← entrypoints (main packages) — cmd/agent-builder
internal/     ← code outputs (orchestrator packages; not importable externally)
examples/     ← reference clients / liftable examples that consume published contracts (e.g. the Telegram operator CLI, examples/agent-cli); one-way dependency into internal/envelope, never imported by the orchestrator (ADR 062)
artifacts/    ← non-code outputs (rendered diagrams, exports, schemas)
docs/         ← spec + planning + history (the source-of-truth side)
  spec/           authoritative current-state snapshot — SPEC.md, behaviors, architecture, data-model, interfaces, configuration
  architecture/   narrative overview, diagrams.md, ADRs, tech stack
  plans/          roadmap, sprints
  tasks/          active, backlog, completed task files
    test-specs/   TDD specs — always written before implementation
  agent-rules.md  process rules + project retros (the growing log of lessons)
  operating.md    operator runbook (how to run the orchestrator against a target)
```

Idiomatic Go layout (`cmd/` + `internal/`) is used instead of the generic `src/`.
The key distinction: `docs/` is the input side (read before you act, and the
artifact that survives a rewrite), `cmd/` + `internal/` are the output side (what
gets produced).

`docs/spec/` is **dual-natured** — it's the output of every task that changes
externally-visible behavior, *and* the input to onboarding, drift audits, and (in
the limit) regenerating the codebase from scratch. The code is one realization of
the spec. Spec and code that disagree means one of them is wrong; fix it in the same
change.

## Tech stack

Go — see [docs/architecture/tech-stack.md](docs/architecture/tech-stack.md) for the
full stack and rationale.

## Commands

```bash
go test ./...                     # run tests
go build ./...                    # compile everything
go run ./cmd/agent-builder        # run the orchestrator (entrypoint)
golangci-lint run                 # lint
gofmt -w .                        # format
make check                        # lint + test + fitness (the verification gate)
```

## Architectural invariants (from autonomous-builder.md)

These are load-bearing — violating one breaks the security model, not just style.
Reconcile them against the general-agent north star above where `autonomous-builder.md`
still uses the older "builds the blocks" framing:

- **Verification gate is the definition of done.** Unattended, the agent's only
  ground truth is machine-checkable success. No task is "done" without the gate
  passing (tests + build + lint + scanners green). Keep the gate thin — adopt `go
  test`/`golangci-lint` + scanners, don't build a framework.
- **No unattended self-modification of the trusted core.** agent-builder reads from
  its own repo but never autonomously edits its own gate, escalation path, or control
  plane — a bad self-edit can disable its own safety. Self-improvement happens through
  **secure skill-writing** (reviewable, sandboxed skills), not core edits; core/gate
  self-edits are human-reviewed.
- **the internal planning hub is read-mostly.** The roadmap is input the agent consumes, not
  something it rewrites. It may flip task status; the human stays the author.
- **One task = one repo = one branch.** Never sprawl a task across repos. Cross-repo
  needs become separate, sequenced tasks.
- **Substrate is rootless Podman, not Docker** (tiered runtime: `runc` → gVisor
  `runsc` → Kata/Firecracker). Container definitions in this repo are *product*
  artifacts (the execution-box profile), never a generic dev container.
- **Executor seam = `(harness, model) → result`.** Cloud CLIs (Claude, `agy`) bundle
  both; local LLMs supply the harness. (`agy`/Antigravity is the multi-model successor to
  the `gemini` CLI, deprecated 2026-06-18.) Route by quota + sensitivity + cost. For the
  coding reference build the result is a verified branch; the seam itself is general. The
  verify gate is what makes mixing uneven-quality executors safe.
- **Egress allowlist is the load-bearing control for the accepted token-in-box
  risk** — keep it tight; ensure executor tokens are independently revocable + fast
  to rotate. `dep-scan`/`code-scanner` gate code that could read tokens off disk;
  `armor` guards the web-ingestion + tool-call path.

## Design principles

This project follows **Unix philosophy** as its default design approach — favoring
**composability over monolithic design**. Complex behavior should emerge from
combining small, independent components that communicate through standardized
interfaces, not by growing one large one. The full statement lives in
[docs/architecture/overview.md](docs/architecture/overview.md) under *Design
principles*; the short version is four structural properties to design for:

- **Modularity** — independent units that can be built, understood, and changed on
  their own
- **Interface standardization** — stable, well-defined contracts between components
  (typed signatures, versioned APIs, plain-text formats)
- **Maintainability** — changes in one module should not cascade across unrelated
  ones
- **Reusability** — components should be liftable into another project without
  entanglement

Derived working rules:

- **One thing, well** — each module, service, and function has a single clear
  responsibility
- **Small, composable pieces** over large configurable ones
- **Plain text** for configs, intermediate artifacts, and data interchange where
  possible
- **Explicit over implicit** — surface assumptions in code and types, not in
  comments
- **Fail fast, crash loudly** on unexpected state — never silently paper over it
- **Test in isolation** — every component runnable without the whole stack
- **Defer premature decisions** — no abstractions until the second or third concrete
  use case demands them

**Monolithic is a legitimate choice when deliberate** — the Linux kernel itself is
monolithic for good reasons (performance, correctness, tight internal coupling that
plug-ins would undermine). The same can apply to a hot-path runtime core, a state
machine, or a cryptographic primitive. The principle is "prefer composability at
user-facing or cross-module boundaries, and document any deviation with an ADR."
Accidental monolithic drift is not the same as a deliberate monolithic decision.

## Conventions

- Task files are named `NNN-short-name.md` (zero-padded, sequential across all task
  states)
- Every task has a paired test spec; no implementation starts without one
- Tasks follow Unix philosophy — one task, one responsibility; break things smaller
  when in doubt
- ADRs live in `docs/architecture/decisions/` — add one whenever a significant
  design decision is made
- **Spec is updated in the same commit as the code change.** A task that changes
  externally-visible behavior, the data model, an interface, or configuration is not
  done until the matching `docs/spec/` file reflects the new state. Stale spec
  entries are rewritten in place — never appended to. The ADR carries the history;
  the spec carries the current truth.
- **Diagrams update with the code.** When a component boundary moves or a runtime
  flow changes, update `docs/architecture/diagrams.md` in the same commit.

## Working in this project

Every task lives on its own branch (or worktree under concurrent sessions). Working
directly on `main` is blocked by the `no-commit-on-main` hook —
`scripts/start-task.sh` is how you pick the right isolation for the moment.

1. Start each session by reading the relevant task file (including its
   **Verification plan**) and its test spec
2. Check [docs/architecture/overview.md](docs/architecture/overview.md) for system
   context
3. Write the test spec before any implementation code
4. Implement via your harness's task-execution flow. Its Step 0 runs
   `scripts/start-task.sh <NNN> <slug>` to set up either:
   - `BRANCH task/NNN-<slug>` (solo session — the common case), or
   - `WORKTREE .claude/worktrees/NNN-<slug>/` (concurrent session detected; `cd` in)

   Commit at status **🟡 (code merged)** on the task branch.
5. After the executor returns, run the **spec-verifier** role on the task — it
   returns APPROVE or BLOCK based on per-assertion evidence
6. If spec-verifier APPROVEs **and** the verification plan's L5/L6 evidence is
   recorded (validation-harness output or runtime observation), promote the row to
   **✅ (verified)** in `coverage-tracker.md` in a **separate commit** titled
   `verify: confirm task NNN — <evidence>` (still on the task branch)
7. **Merge to main** when ready: `git checkout main && git merge task/NNN-<slug>`.
   The `auto-cleanup-merge` hook then deletes the task branch and removes the
   worktree automatically. If the merge conflicts or you want to keep the branch, the
   hook surfaces a note and leaves it in place.
8. **Commit and push after each milestone** — never start the next task without
   committing the current one first

The separation between the task branch and `main` is the load-bearing rule for
multi-session safety. Two sessions on different `task/*` branches can work in
parallel without stepping on each other; two sessions both editing `main` cannot.

The separation between 🟡 (feat commit) and ✅ (verify commit) is the load-bearing
rule: it makes "merged" and "verified" two distinct artifacts in git history, so
neither can silently substitute for the other. **Never** mark ✅ in the same commit
as the feature work — the verification step must be its own observable event.

## Commit rules

**You must commit and push after every milestone.** Do not batch multiple tasks into
one commit. Do not continue to the next task until the current one is committed and
pushed.

All commits below land on the **task branch** (`task/NNN-<slug>`), never on `main`
directly. The merge to `main` happens after the verify step, in a separate explicit
operation.

| Milestone | What to stage | Message | Branch |
|-----------|--------------|---------|--------|
| ADR written | `docs/architecture/decisions/NNN-*.md`, any superseded spec entries rewritten in `docs/spec/` | `docs: add ADR NNN — <decision title>` | task branch |
| Test spec written | `docs/tasks/test-specs/NNN-*-test-spec.md`, updated `coverage-tracker.md` | `test: add spec for task NNN — <name>` | task branch |
| Task code merged (🟡) | `internal/`/`cmd/` changes, moved task file, `coverage-tracker.md` row set to **🟡**, **and any affected `docs/spec/` files** | `feat: complete task NNN — <name>` | task branch |
| Task verified (✅) | `coverage-tracker.md` row promoted from 🟡 → ✅ with `Verified by` filled (harness command + final assertion, or operator observation) | `verify: confirm task NNN — <evidence>` | task branch |
| Diagram updated | `docs/architecture/diagrams.md` (with date bump at top) | `docs: refresh diagrams — <what changed>` | task branch (or `[allow-main]` for standalone doc fixes) |
| Spec rewritten standalone | `docs/spec/<file>.md` | `spec: <what changed and why now>` | task branch (or `[allow-main]` for standalone doc fixes) |
| Merged into main | (after `git merge task/NNN-<slug>` on `main`) | (default `Merge branch …` message) | `main` |

Do **not** add a `Co-Authored-By` line to commits unless explicitly asked.

## Load-bearing process rules

These are the rules that exist specifically to stop a preventable mistake. The
**full treatment, with the incident that motivated each, lives in
[docs/agent-rules.md](docs/agent-rules.md)** — read it. The essentials, so they
reach you even without that file loaded:

- **Commit after every milestone — now, not "after the next task too."** Batched
  commits are impossible to untangle. One task, one commit.
- **Test spec before implementation — always.** No "this is too small for a spec."
  The spec defines done; without it you're guessing.
- **Never work directly on the default branch.** First action of any task is
  `scripts/start-task.sh <NNN> <slug>`, which puts you on `task/NNN-<slug>` or in a
  worktree. When it prints `WORKTREE <path>`, your **next command must be `cd
  <path>`** — editing the parent repo while believing you're isolated is the silent
  failure.
- **"Done" means operationally verified, not "code merged."** The verification
  ladder: (1) code merged → (2) unit tests pass → (3) `make fitness` passes → (4) CI
  → (5) validation harness exercises the live path → (6) live binary observed.
  Levels 1–4 are 🟡; only 5 or 6 flips a row to ✅. Never claim a level you did not
  reach.
- **Trace producer→consumer before declaring done on cross-module state.** A test
  that sets a field by hand proves the gate works *given* the field; it does not
  prove the field is ever set on the live path. Grep the write site and the read
  site and identify the live path.
- **No smoke tests where the spec asks for assertions.** If the spec says "returns
  `Some(2)`", the test must verify that, not merely that the call doesn't panic. If
  constructing the state is hard, that's a blocker to report — not a license to
  downgrade the test.
- **No new warnings self-justified away.** A change that adds a linter/typecheck
  warning over baseline must fix the root cause or stop and report. "Acceptable
  false positive" is not a label you apply unilaterally — use an explicit suppression
  with a reason, or escalate.
- **Run it when the change is runtime-visible.** Logging, CLI/exit codes, TUI
  output, endpoints, file outputs, side effects — `make check` is not verification.
  Run the binary path and quote the output.
- **Never `git checkout -- <path>` over uncommitted work.** It silently overwrites
  and the reflog cannot recover it. Use `git stash`, `git worktree add <ref>`, or
  `git diff <ref> -- <path>` / `git show <ref>:<path>` instead. A `protect-checkout`
  hook blocks this; the rule stands even if the hook is off.
- **Git status must be clean before declaring a task complete.** `git status` must
  report `nothing to commit, working tree clean`. The common miss: `cp` instead of
  `git mv` when moving a task file leaves the original undeleted.

## Boundaries

### Always
- Write the test spec before any implementation code
- Fill in the **Verification plan** of the task file *before* writing code — the
  highest verification level achievable, the harness command, the runtime
  observation
- Commit and push after every milestone
- Read the task file (including its Verification plan) and test spec before starting
- Create an ADR for significant design decisions
- **Update `docs/spec/` in the same commit** as any code change that alters
  externally-visible behavior, data model, interfaces, or configuration
- **Update `docs/architecture/diagrams.md` in the same commit** as any code change
  that moves a component boundary or alters a diagrammed runtime flow
- **Default new task status to 🟡 on the feat commit; ✅ only after spec-verifier
  APPROVE + recorded L5/L6 evidence, in a separate `verify:` commit**
- **Run `spec-verifier` on every task** before promoting to ✅ — its APPROVE/BLOCK
  verdict is the gate, not the executor's self-judgement
- **Start every task on its own branch via `scripts/start-task.sh <NNN> <slug>`**

### Ask first
- Modifying files in `docs/plans/`, `docs/tasks/`, or
  `docs/architecture/decisions/` — they are planning and historical documents
- Deleting or renaming existing source files
- Adding dependencies not already in the tech stack
- Changing the project structure beyond what a task requires
- Reorganizing `docs/spec/` (splitting files, renaming sections) — the structure is
  a stable contract; restructure deliberately, not opportunistically

### Never
- Create files in `internal/`/`cmd/` without a corresponding task and test spec
- Combine unrelated changes in one task or commit
- Skip the test spec — even for "small" changes
- Force push or rewrite published git history
- Add a `Co-Authored-By` line to commits unless explicitly asked
- Append to spec entries instead of rewriting them (the ADR keeps history; the spec
  is a snapshot)
- Add future-tense statements to the spec (the spec is what *is*; planned work goes
  in `docs/plans/` and `docs/tasks/`)
- Mark a task ✅ on the same commit as the feature work
- Claim a verification level you did not actually reach
- Commit directly to `main` (use `[allow-main]` in the message for genuine main-only
  fixes — standalone doc fixes, hotfixes)
- Run `git checkout -- <path>` over a dirty working tree

## External tools

- **dep-scan** — supply-chain CVE scan of Go modules (and npm/pypi/cargo) the agent
  pulls; a blocking gate alongside code-scanner. Use `gods` for Go. Install:
  `curl -fsSL https://raw.githubusercontent.com/tkdtaylor/dep-scan/main/install.sh | bash`
- **code-scanner** — scan any target repo/package/deps for malware before the agent
  builds on or runs them; wired into the verification gate as a blocking step.
- **armor** — the LLM-guard block; sits on the web-ingestion + tool-call path
  (injection/exfil/tool-call validation). Wire it where the agent ingests web
  research.
- **gh** — clone/inspect target block repos and open PRs.

MCP is not needed — `gh` covers repo ops, web search/fetch cover research, and the
provider CLIs are driven as subprocesses.

## Operating the orchestrator

To run agent-builder against a real target repository, see
[docs/operating.md](docs/operating.md) (the task-oriented runbook) and
[docs/spec/configuration.md](docs/spec/configuration.md) (the exhaustive
configuration reference).

## Harness notes — Codex

Use these short phrases as automatic repo-local workflows. The user should not need
to paste long agent prompts.

- `work task NNN`, `start task NNN`, `continue task NNN`, or `use task-executor on
  task NNN`: locate the matching task file under
  `docs/tasks/{backlog,active,completed}/NNN-*.md` and the paired
  `docs/tasks/test-specs/NNN-*-test-spec.md`; read `.claude/agents/task-executor.md`;
  then follow that workflow. Start with `scripts/start-task.sh <NNN> <slug>` unless
  already on the correct isolated task branch/worktree.
- `verify task NNN`, `spec verify NNN`, or `use spec-verifier on task NNN`: read
  `.claude/agents/spec-verifier.md` and perform its assertion-by-assertion gate
  against the task, test spec, diff, and test output. Do not edit files.
- `review task NNN`, `review current diff`, or `/code-review`: read
  `.claude/agents/code-reviewer.md` and respond in code-review mode, findings first.
- `architect task NNN`, `architecture review`, or `drift audit`: read
  `.claude/agents/architect.md` and apply that role to the requested scope.
- `security audit task NNN` or `security review`: read
  `.claude/agents/security-auditor.md` and apply that role to the requested scope.

When the user explicitly asks to delegate, run parallel agents, or "use" one of the
named executor/reviewer roles as a subagent, spawn subagents using the matching
`.claude/agents/*.md` file as the role prompt. Otherwise execute the workflow locally
in the current session. The `.claude/agents/*.md` files are role prompts — they are
not automatically-available Codex agents; mirror their intent manually.

For multiple tasks with dependencies, act as the parent coordinator: parse the task
list into dependency levels, spawn subagents only for tasks whose prerequisites are
complete and whose write scopes don't overlap, give every code-modifying subagent the
`task-executor.md` workflow plus a fail-fast "prove you are in an isolated
branch/worktree before editing" instruction, keep dependent tasks queued until their
prerequisites finish and are reviewed, and audit isolation + integration after each
level before starting the next.
