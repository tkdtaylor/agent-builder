# agent-builder

agent-builder is the **assembly layer of the Secure Agent Ecosystem**: it composes the [foundational blocks](#the-building-blocks) into **purpose-built, secure autonomous agents** — sandboxed execution, JIT-brokered credentials, policy-gated actions, and a tamper-evident audit trail, wired together over the blocks' published contracts.

Today it builds one such agent: an **autonomous coding agent**. Give it a task and it works sandboxed — verifying its own output against a machine-checkable gate and opening a PR on success, one task at a time, on its own branch, unattended.

## How it works

- **Supervisor (outside the box):** dispatches one task at a time, enforces the wall-clock/escalation kill, collects results, tears the box down. Deliberately dumb — never reasons over untrusted content.
- **Agent loop (inside the box):** reads a task → routes it to an executor → the executor edits the target repo's worktree → the **verification gate** runs (tests + build + lint + [`dep-scan`](https://github.com/tkdtaylor/dep-scan)/[`code-scanner`](https://github.com/tkdtaylor/code-scanner)) → branch + PR on pass, escalate on fail.
- **Executors** are pluggable behind one seam, `(harness, model) → branch`: Claude Code / Gemini CLIs (bundle harness + model) and local LLMs (supply a harness). Routed by quota + sensitivity + cost.
- **Containment:** rootless Podman, tiered runtime (`runc` → gVisor → Kata/Firecracker), default-deny egress allowlist. [`armor`](https://github.com/tkdtaylor/armor) guards the web-ingestion + tool-call path.

See [docs/architecture/overview.md](docs/architecture/overview.md), the [architecture diagrams](docs/architecture/diagrams.md), and [docs/spec/SPEC.md](docs/spec/SPEC.md).

## The building blocks

The Secure Agent Ecosystem ships its security as small, standalone, independently
usable blocks rather than one framework. agent-builder composes these over their
published contracts; each links to its own repo (the block's `## Scope` section is the
authoritative statement of what it owns).

| Block | What it does | In agent-builder |
|---|---|---|
| [exec-sandbox](https://github.com/tkdtaylor/exec-sandbox) | OS execution isolation — tiered runtime (`runc` → gVisor `runsc` → Kata/Firecracker), enforced resource limits, default-deny egress proxy | ✅ default run backend |
| [vault](https://github.com/tkdtaylor/vault) | JIT zero-knowledge secret store + credential proxy; secrets resolve to single-use handles the agent never sees in plaintext | ✅ git/GitHub token brokering |
| [policy-engine](https://github.com/tkdtaylor/policy-engine) | Out-of-process authorization (OPA/Rego + Cedar behind an AuthZEN seam); risk→tier scoring, `require_approval` gate | ✅ opt-in `decide` gate before dispatch |
| [audit-trail](https://github.com/tkdtaylor/audit-trail) | Tamper-evident, hash-chained log (the spine); RFC 6962-style signed checkpoints, Rekor anchoring | ✅ `emit` + `verify` (signed checkpoints opt-in) |
| [armor](https://github.com/tkdtaylor/armor) | LLM-guard on the web-ingestion + tool-call path — prompt-injection / jailbreak / exfil detection | ✅ fail-closed ingestion guard |
| [dep-scan](https://github.com/tkdtaylor/dep-scan) | Supply-chain CVE scan of dependencies (SARIF / CycloneDX / SPDX / VEX) | ✅ blocking step in the verification gate |
| [code-scanner](https://github.com/tkdtaylor/code-scanner) | Malware / supply-chain scan of code and skills before they run | ✅ blocking step in the verification gate |
| [memory-guard](https://github.com/tkdtaylor/memory-guard) | Memory-I/O gate against poisoning — write-gate plus post-deletion residue verification (OWASP Agentic ASI06) | ◻️ not yet composed (deferred) |
| [agent-mesh](https://github.com/tkdtaylor/agent-mesh) | Secure inter-agent comms — Ed25519-signed envelopes with a replay-prevention window | ◻️ not yet composed (deferred) |

The two ◻️ blocks exist and are usable, but agent-builder doesn't wire them yet:
memory-guard waits on a live agent memory store worth guarding, agent-mesh on a multi-agent substrate that doesn't exist here yet. The roadmap's block-adoption table is the source of truth for exactly what is wired and at what verification level (see the [roadmap](docs/plans/roadmap.md)).

## Develop locally

```bash
go test ./...                 # tests
go build ./...                # compile
make check                    # the verification gate: lint + test + fitness
go run ./cmd/agent-builder version
```

## Usage — running it on a real project

`agent-builder run` dispatches **one ready task**: pick task → sandbox → Claude executor → verification gate → open a PR on pass (escalate on fail). You review and merge the PR, then run again for the next task. It is configured entirely through environment variables.

**Prerequisites** (one-time, see the [operator guide](docs/operating.md) for setup): rootless Podman + a runtime (`runc`/`runsc`); the gate toolchain in `containment/execution-box/gate-tools/` (`golangci-lint`, `dep-scan` ≥ 1.3.1, `code-scanner` — gitignored, populate per host); `git` + `gh` authenticated; and a Claude credential — your **subscription** token `CLAUDE_CODE_OAUTH_TOKEN` (from `claude setup-token`) or an `ANTHROPIC_API_KEY`.

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

Other subcommands: `agent-builder version`; `agent-builder verify <repo>` (run just the gate against a checkout); `agent-builder verify-checkpoint` (verify a signed checkpoint against an Ed25519 public key). Full environment reference and prerequisites: **[docs/operating.md](docs/operating.md)** and [docs/spec/configuration.md](docs/spec/configuration.md).

## Tech stack

Go 1.26. CLI/container orchestration over provider CLIs and rootless Podman — see [docs/architecture/tech-stack.md](docs/architecture/tech-stack.md).

## License

[Apache License 2.0](LICENSE) — consistent with the other blocks in the Secure
Agent Ecosystem. See [NOTICE](NOTICE) for attribution and the trademark and
security disclaimers, and [CONTRIBUTING.md](CONTRIBUTING.md) for the
inbound=outbound / DCO contribution terms.
