# Operating agent-builder

How to run the orchestrator against a real target repository. This is the task-oriented
runbook; the exhaustive configuration reference is [spec/configuration.md](spec/configuration.md),
and the host-provisioning specifics (rootless runsc wrapper, gate-tools population, sandbox remote)
live in [plans/l6-operator-runbook.md](plans/l6-operator-runbook.md).

## What `agent-builder run` does

One invocation dispatches **exactly one ready task** through the Phase-0 loop:

```
pick next ready task  →  launch sandbox box (rootless Podman)  →  Claude executor edits the
worktree  →  verification gate (build · vet · test · gofmt · golangci-lint · dep-scan ·
code-scanner)  →  on PASS: push branch + open PR;  on FAIL: retry up to MAX_ATTEMPTS, then escalate
```

It is **supervised**: you review and merge each PR, then run again for the next task. There is no
multi-task autopilot in Phase 0 — the verification gate is the safety net, the human is the merge
gate. One task = one repo = one branch.

The agent never edits its **own** repo autonomously (`agent-builder` builds *blocks*, not itself).

## Prerequisites (one-time per host)

| Need | Detail |
|------|--------|
| Rootless Podman + runtime | `podman` rootless; a registered OCI runtime (`runc`, or `runsc`/gVisor for the agent tier). See the [L6 runbook §1a](plans/l6-operator-runbook.md) for the rootless `runsc` cgroup wrapper. |
| Gate toolchain | `containment/execution-box/gate-tools/` must contain executables `golangci-lint`, `dep-scan` (**≥ 1.3.1** — earlier versions false-block every Go dependency), and `code-scanner`. These are gitignored; populate them per host. `go`/`gofmt` come from the box image. |
| Git + GitHub | `git` and `gh`, authenticated (`gh auth status`). Publication uses `gh pr create`. |
| Claude credential | **Subscription (preferred):** `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`. **Or** a metered `ANTHROPIC_API_KEY`. Supply exactly one; if both are set the OAuth token wins (ADR 033). Keep it in a gitignored `.env`. |

## The target repository

The target is the block you're building (e.g. a clone of `exec-sandbox`). It must:

- be a **git clone with a remote** (the branch the agent produces descends from the remote's default
  branch so `gh pr create` can resolve a base);
- carry its own **roadmap and tasks**: `docs/plans/roadmap.md` plus ready tasks at
  `docs/tasks/backlog/NNN-*.md` with a clear goal and acceptance criteria. The agent hands the task
  spec to Claude verbatim — task quality is the single biggest lever on output quality.

For a real project, **task-root and worktree are the same clone** (`AGENT_BUILDER_TASK_ROOT` =
`AGENT_BUILDER_WORKTREE`): the repo holds its own tasks and is also the code being modified.

Scaffold a new target with the `create-project` workflow, then write the first task + spec (or use
the `task-planner` agent).

## Environment contract

Run from inside the `agent-builder` repo (the launcher and gate-tools live here). Required:

| Variable | Meaning |
|----------|---------|
| `AGENT_BUILDER_TASK_ROOT` | Dir containing `docs/plans/roadmap.md` + `docs/tasks/backlog/`. |
| `AGENT_BUILDER_WORKTREE` | Clone of the target repo the executor edits (usually = task root). |
| `AGENT_BUILDER_PUBLISH_REMOTE` | Git remote name in the worktree to push + open the PR against. |
| `AGENT_BUILDER_RUN_TIMEOUT` | Wall-clock bound for the in-box loop, a Go duration (e.g. `900s`). |
| `AGENT_BUILDER_MAX_ATTEMPTS` | Attempts before escalation (e.g. `2`). |
| `CLAUDE_CODE_OAUTH_TOKEN` *or* `ANTHROPIC_API_KEY` | The executor credential. |

Common optional:

| Variable | Default | Meaning |
|----------|---------|---------|
| `AGENT_BUILDER_EXEC_BOX_LAUNCHER` | repo default | Path to `containment/execution-box/run.sh`. |
| `AGENT_BUILDER_RUN_RECORD` | none | NDJSON run-log path (recommended — your observability into the run). |
| `AGENT_BUILDER_CLAUDE_CLI` | `claude` | Claude Code CLI binary. |
| `AGENT_BUILDER_GIT_CLI` / `AGENT_BUILDER_GH_CLI` | `git` / `gh` | Tool overrides. |
| `AGENT_BUILDER_AUDIT_RECORD` / `AGENT_BUILDER_AUDIT_BIN` | none | Audit-trail sink wiring. |

> Do **not** set `AGENT_BUILDER_SANDBOX_RUNTIME` — the rented `srt` backend was removed (ADR 021)
> and a stale value fails loudly.

## Run one task

```bash
cd /path/to/agent-builder
export PATH="$HOME/.nvm/versions/node/v24.14.0/bin:$HOME/go/bin:$HOME/.local/bin:/path/to/code-scanner/cli:$PATH"
set -a; . ./.env; set +a                       # CLAUDE_CODE_OAUTH_TOKEN
TARGET=/path/to/target-repo-clone

env AGENT_BUILDER_TASK_ROOT="$TARGET" \
    AGENT_BUILDER_WORKTREE="$TARGET" \
    AGENT_BUILDER_PUBLISH_REMOTE=origin \
    AGENT_BUILDER_RUN_TIMEOUT=900s \
    AGENT_BUILDER_MAX_ATTEMPTS=2 \
    AGENT_BUILDER_RUN_RECORD=/tmp/run.ndjson \
    AGENT_BUILDER_EXEC_BOX_LAUNCHER="$(pwd)/containment/execution-box/run.sh" \
    go run ./cmd/agent-builder run
```

On success the run prints `run completed: task NNN` and the run record's `run_finished` event
carries `outcome=completed` plus the PR URL. Then: review the PR, merge, and run again for the next
ready task.

## Reading the outcome

- **stdout**: `task NNN selected` → `executor attempt completed: branch=…` → `gate passed: …` →
  `publication recorded: … pr=<url>` → `run completed: task NNN`.
- **Run record** (`AGENT_BUILDER_RUN_RECORD`): one JSON object per line. The `gate passed:` /
  `gate failed:` line names each step's PASS/FAIL — the fastest way to see *where* a run stopped.
- **Escalation**: `task NNN escalated after N attempts` means every attempt failed the gate (or the
  executor). The record's `stderr` carries the failing gate step and its output.

## Other subcommands

- `agent-builder version` — print the version.
- `agent-builder verify <repo>` — run only the verification gate against a checkout (no executor, no
  publish). Useful to check a worktree by hand.

## Troubleshooting

| Symptom | Cause / fix |
|---------|-------------|
| `missing Gate tool <x> in …/gate-tools` | The toolchain dir is missing a binary (often in a fresh clone/worktree). Populate `golangci-lint`, `dep-scan`, `code-scanner`. |
| `supervisor: create box: sandbox: create probe exited N` | Box failed to launch — check rootless Podman + the runtime registration (L6 runbook §1a). |
| `Not logged in` / `Credit balance is too low` | Auth: ensure `CLAUDE_CODE_OAUTH_TOKEN` is set (subscription) and `ANTHROPIC_API_KEY` is unset/funded. The executor isolates Claude in a temp HOME, so it authenticates by the env credential alone. |
| `gate failed: … FAIL dep-scan … sumdb signature verification failed` | dep-scan < 1.3.1 false-blocks Go deps. Upgrade dep-scan to ≥ 1.3.1 in gate-tools. |
| Gate step needs the network and is blocked | The box enforces a default-deny egress allowlist (`containment/execution-box/egress.allowlist`). Add the required host:port with a justification comment. |
| `run config: AGENT_BUILDER_SANDBOX_RUNTIME …` | Unset it — the `srt` backend was removed (ADR 021). |

For the full host bring-up and per-probe verification, see
[plans/l6-operator-runbook.md](plans/l6-operator-runbook.md).
