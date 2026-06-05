# agent-builder

An autonomous coding agent that reviews a roadmap and builds the **secure-agent ecosystem blocks** (exec-sandbox, vault, policy-engine, audit-trail) unattended — working one task at a time on its own branch, gated by a machine-checkable verification step.

It is the first concrete consumer of those blocks, and the bootstrap that resolves their chicken-and-egg: it runs on rented isolation today, and its **first task is to build `exec-sandbox` v0**, after which it swaps the rented isolation for the block it just produced.

**North star:** agent-builder starts as *the agent that builds the blocks* and evolves into *a tool to build agents from the blocks* — the ecosystem's front door.

> **Status:** scaffolding. Not yet runnable. Design is pinned in `autonomous-builder.md`.

## How it works (target architecture)

- **Supervisor (outside the box):** dispatches one task at a time, enforces the wall-clock/escalation kill, collects results, tears the box down. Deliberately dumb — never reasons over untrusted content.
- **Agent loop (inside the box):** reads a task → routes it to an executor → the executor edits the target repo's worktree → the **verification gate** runs (tests + build + lint + `dep-scan`/`code-scanner`) → branch + PR on pass, escalate on fail.
- **Executors** are pluggable behind one seam, `(harness, model) → branch`: Claude Code / Gemini CLIs (bundle harness + model) and local LLMs (supply a harness). Routed by quota + sensitivity + cost.
- **Containment:** rootless Podman, tiered runtime (`runc` → gVisor → Kata/Firecracker), default-deny egress allowlist. `armor` guards the web-ingestion + tool-call path.

See [docs/architecture/overview.md](docs/architecture/overview.md) and [docs/spec/SPEC.md](docs/spec/SPEC.md).

## Build order

`exec-sandbox` → `audit-trail` → `policy-engine` → `vault`. Tracked in [docs/plans/roadmap.md](docs/plans/roadmap.md).

## Run locally

```bash
go test ./...                 # tests
go build ./...                # compile
go run ./cmd/agent-builder    # run the orchestrator (entrypoint — not yet implemented)
make check                    # the verification gate: lint + test + fitness
```

## Tech stack

Go 1.26. CLI/container orchestration over provider CLIs and rootless Podman — see [docs/architecture/tech-stack.md](docs/architecture/tech-stack.md).

## License

Private / unlicensed while in bootstrap. Intended license on going public: **Apache-2.0** (high value-add orchestrator), consistent with the ecosystem's other novel blocks.
