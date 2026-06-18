# agent-builder

An autonomous coding agent that reviews a roadmap and builds the **secure-agent ecosystem blocks** (exec-sandbox, vault, policy-engine, audit-trail) unattended — working one task at a time on its own branch, gated by a machine-checkable verification step.

It is the first concrete consumer of those blocks, and the bootstrap that resolves their chicken-and-egg: it runs on rented isolation today, and its **first task is to build `exec-sandbox` v0**, after which it swaps the rented isolation for the block it just produced.

**North star:** agent-builder starts as *the agent that builds the blocks* and evolves into *a tool to build agents from the blocks* — the ecosystem's front door.

> **Status:** Phase 0 working — verified live (L6) end-to-end on a real Claude subscription against rootless Podman + gVisor: pick task → sandboxed Claude executor → full verification gate (build/vet/test/gofmt/golangci-lint/dep-scan/code-scanner) → real PR. Supervised (human reviews/merges each PR). Design is pinned in `autonomous-builder.md`.

## How it works (target architecture)

- **Supervisor (outside the box):** dispatches one task at a time, enforces the wall-clock/escalation kill, collects results, tears the box down. Deliberately dumb — never reasons over untrusted content.
- **Agent loop (inside the box):** reads a task → routes it to an executor → the executor edits the target repo's worktree → the **verification gate** runs (tests + build + lint + `dep-scan`/`code-scanner`) → branch + PR on pass, escalate on fail.
- **Executors** are pluggable behind one seam, `(harness, model) → branch`: Claude Code / Gemini CLIs (bundle harness + model) and local LLMs (supply a harness). Routed by quota + sensitivity + cost.
- **Containment:** rootless Podman, tiered runtime (`runc` → gVisor → Kata/Firecracker), default-deny egress allowlist. `armor` guards the web-ingestion + tool-call path.

See [docs/architecture/overview.md](docs/architecture/overview.md) and [docs/spec/SPEC.md](docs/spec/SPEC.md).

## Build order

`exec-sandbox` → `audit-trail` → `policy-engine` → `vault`. Tracked in [docs/plans/roadmap.md](docs/plans/roadmap.md).

## Develop locally

```bash
go test ./...                 # tests
go build ./...                # compile
make check                    # the verification gate: lint + test + fitness
go run ./cmd/agent-builder version
```

## Usage — running it on a real project

`agent-builder run` dispatches **one ready task**: pick task → sandbox → Claude executor → verification gate → open a PR on pass (escalate on fail). You review and merge the PR, then run again for the next task. It is configured entirely through environment variables.

**Prerequisites** (one-time, see the [operator guide](docs/operating.md) for setup): rootless Podman + a runtime (`runc`/`runsc`); the gate toolchain in `containment/execution-box/gate-tools/` (`golangci-lint`, `dep-scan` ≥ 1.3.1, `code-scanner`); `git` + `gh` authenticated; and a Claude credential — your **subscription** token `CLAUDE_CODE_OAUTH_TOKEN` (from `claude setup-token`) or an `ANTHROPIC_API_KEY`.

**Run one task against a target repo** (the target carries its own `docs/plans/roadmap.md` + `docs/tasks/backlog/NNN-*.md`):

```bash
cd /path/to/agent-builder
export CLAUDE_CODE_OAUTH_TOKEN=...           # subscription credential
TARGET=/path/to/target-repo-clone           # has a git remote (e.g. origin)

env AGENT_BUILDER_TASK_ROOT="$TARGET" \
    AGENT_BUILDER_WORKTREE="$TARGET" \
    AGENT_BUILDER_PUBLISH_REMOTE=origin \
    AGENT_BUILDER_RUN_TIMEOUT=900s \
    AGENT_BUILDER_MAX_ATTEMPTS=2 \
    AGENT_BUILDER_RUN_RECORD=/tmp/run.ndjson \
    AGENT_BUILDER_EXEC_BOX_LAUNCHER="$(pwd)/containment/execution-box/run.sh" \
    go run ./cmd/agent-builder run
```

Other subcommands: `agent-builder version`; `agent-builder verify <repo>` (run just the gate against a checkout). Full environment reference and prerequisites: **[docs/operating.md](docs/operating.md)** and [docs/spec/configuration.md](docs/spec/configuration.md).

## Tech stack

Go 1.26. CLI/container orchestration over provider CLIs and rootless Podman — see [docs/architecture/tech-stack.md](docs/architecture/tech-stack.md).

## License

Private / unlicensed while in bootstrap. Intended license on going public: **Apache-2.0** (high value-add orchestrator), consistent with the ecosystem's other novel blocks.
