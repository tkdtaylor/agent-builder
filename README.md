# agent-builder

agent-builder is the **assembly layer of the Secure Agent Ecosystem**: it composes the [foundational blocks](#the-building-blocks) into **purpose-built, secure autonomous agents** — sandboxed execution, JIT-brokered credentials, policy-gated actions, and a tamper-evident audit trail, wired together over the blocks' published contracts.

Coding is the **first reference build**, not the definition. Today the most fully realized agent is an **autonomous coding agent**: give it a task and it works sandboxed — verifying its own output against a machine-checkable gate and opening a PR on success, one task at a time, on its own branch, unattended. A general conversational front door also ships — `agent-builder orchestrate` (goal → plan → policy-gated dispatch → result over the channel) and `agent-builder ask` (a single-shot, non-coding question) — with the coding loop as the proving ground for the broader agent. See [Talking to it locally](#talking-to-it-locally).

## How it works

- **Supervisor (outside the box):** dispatches one task at a time, enforces the wall-clock/escalation kill, collects results, tears the box down. Deliberately dumb — never reasons over untrusted content.
- **Agent loop (inside the box):** reads a task → routes it to an executor → the executor edits the target repo's worktree → the **verification gate** runs (tests + build + lint + [`dep-scan`](https://github.com/tkdtaylor/dep-scan)/[`code-scanner`](https://github.com/tkdtaylor/code-scanner)) → branch + PR on pass, escalate on fail.
- **Executors** are pluggable behind one seam, `(harness, model) → branch`: Claude Code and `agy`/Antigravity CLIs bundle harness + model (`agy` is the multi-model successor to the `gemini` CLI, deprecated 2026-06-18), and local Ollama LLMs supply a harness. Routed by quota + sensitivity + cost.
- **Containment:** rootless Podman, tiered runtime (`runc` → gVisor → Kata/Firecracker), default-deny egress allowlist. [`armor`](https://github.com/tkdtaylor/armor) guards the web-ingestion + tool-call path.

See [docs/architecture/overview.md](docs/architecture/overview.md), the [architecture diagrams](docs/architecture/diagrams.md), and [docs/spec/SPEC.md](docs/spec/SPEC.md).

## The building blocks

The Secure Agent Ecosystem ships its security as small, standalone, independently usable blocks rather than one framework. agent-builder composes these over their published contracts; each links to its own repo (the block's `## Scope` section is the authoritative statement of what it owns).

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

The two ◻️ blocks exist and are usable, but agent-builder doesn't wire them yet: memory-guard waits on a live agent memory store worth guarding, agent-mesh on a multi-agent substrate that doesn't exist here yet. The roadmap's block-adoption table is the source of truth for exactly what is wired and at what verification level (see the [roadmap](docs/plans/roadmap.md)).

## Develop locally

```bash
go test ./...                 # tests
go build ./...                # compile
make check                    # the verification gate: lint + test + fitness
go run ./cmd/agent-builder version
```

Working on this project runs through a test-spec-first, one-task-one-branch workflow — read [AGENTS.md](AGENTS.md) (the canonical, harness-neutral briefing) and [CONTRIBUTING.md](CONTRIBUTING.md) before starting; tasks and their specs live under [docs/tasks/](docs/tasks/).

## Usage — running it on a real project

`agent-builder run` dispatches **one ready task**: pick task → sandbox → Claude executor → verification gate → open a PR on pass (escalate on fail). You review and merge the PR, then run again for the next task. It is configured entirely through environment variables.

**Prerequisites** (one-time, see the [operator guide](docs/operating.md) for setup): rootless Podman + a runtime (`runc`/`runsc`); the gate toolchain in `containment/execution-box/gate-tools/` (the operator-required executables `golangci-lint`, `dep-scan` ≥ 1.3.1, `code-scanner`, plus the shipped `gods` and `semgrep-rules` helpers — gitignored, populate per host); `git` + `gh` authenticated; and a Claude credential — your **subscription** token `CLAUDE_CODE_OAUTH_TOKEN` (from `claude setup-token`) or an `ANTHROPIC_API_KEY`.

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

Other subcommands: `agent-builder orchestrate` (drive the general goal-intake path — goal → plan → policy-gated dispatch → result; see [Talking to it locally](#talking-to-it-locally)); `agent-builder ask [--entry <id>] <prompt>` (ask a brain a single-shot, non-coding question and print the answer); `agent-builder version`; `agent-builder verify <repo>` (run just the gate against a checkout); `agent-builder verify-checkpoint` (verify a signed checkpoint against an Ed25519 public key). Full environment reference and prerequisites: **[docs/operating.md](docs/operating.md)** and [docs/spec/configuration.md](docs/spec/configuration.md).

## Talking to it locally

The general goal-intake front door is `agent-builder orchestrate`. It reads goals from an inbound channel, decomposes each into a plan, gates the plan and each worker on policy decisions, and dispatches one worker per approved sub-goal — reporting progress and results back over the same channel. A Telegram channel is wired but **not yet tested end-to-end**; the supported path today is the **local stdin channel** (`AGENT_BUILDER_INBOUND` unset or `env`, the default).

**Start it.** `orchestrate` dispatches each approved sub-goal through the same worker path as `run`, so it needs the **same base environment** as the [run example above](#usage--running-it-on-a-real-project) (`AGENT_BUILDER_TASK_ROOT`, `AGENT_BUILDER_WORKTREE`, `AGENT_BUILDER_PUBLISH_REMOTE`, `AGENT_BUILDER_RUN_TIMEOUT`, `AGENT_BUILDER_MAX_ATTEMPTS`, and a Claude credential), plus two orchestrate-specific pieces. First it fails closed at startup without a worker-transport signing key, so generate one (a hex-encoded 64-byte Ed25519 private key):

```bash
mkdir -p .keys
cat > /tmp/genkey.go <<'EOF'
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func main() {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	fmt.Print(hex.EncodeToString(priv))
}
EOF
go run /tmp/genkey.go > .keys/worker.key
```

Then start it with the base run env plus the signing key and (optionally) a policy binary:

```bash
# ... export the same AGENT_BUILDER_* run vars as the `run` example ...
export AGENT_BUILDER_WORKER_SIGNING_KEY="$(pwd)/.keys/worker.key"
export AGENT_BUILDER_POLICY_BIN=policy-engine   # gate spawns; unset ⇒ every spawn is denied (fail-closed)
go run ./cmd/agent-builder orchestrate
```

The process then blocks reading goals from stdin.

**Talk to it.** One message per line on stdin. A bare line is a new goal; the other verbs manage in-flight goals:

```
build a CLI that prints the current git branch
status                     # fleet-wide status
status goal-1              # status of one goal
info goal-1 prefer cobra   # feed extra context to a running goal
cancel goal-1              # cancel a goal
confirm goal-1             # approve a plan awaiting operator approval
```

Replies (plans, status answers, per-goal summaries) print to stdout. A single goal can also be injected once at startup via `AGENT_BUILDER_GOAL_SPEC` (with optional `AGENT_BUILDER_GOAL_ID` / `AGENT_BUILDER_GOAL_REPO`), delivered before stdin is read.

**Stop it.** Close stdin (Ctrl-D) to end the goal loop cleanly, or Ctrl-C to interrupt. On exit the orchestrator stops any policy daemon it started and tears down cleanly. Concurrency is bounded by `AGENT_BUILDER_MAX_WORKERS` (default 4) and `AGENT_BUILDER_MAX_GOALS` (default 8).

For a one-off, non-coding answer without the goal loop, use `agent-builder ask`:

```bash
go run ./cmd/agent-builder ask "summarize what this repo's verification gate checks"
```

`ask` selects a brain from the registry (or `--entry <id>`), runs a single-shot completion, and prints the answer — no worktree, no gate, no branch. Full environment reference: **[docs/operating.md](docs/operating.md)** and [docs/spec/configuration.md](docs/spec/configuration.md).

## Tech stack

Go 1.26. CLI/container orchestration over provider CLIs and rootless Podman — see [docs/architecture/tech-stack.md](docs/architecture/tech-stack.md).

## License

[Apache License 2.0](LICENSE) — consistent with the other blocks in the Secure Agent Ecosystem. See [NOTICE](NOTICE) for attribution and the trademark and security disclaimers, and [CONTRIBUTING.md](CONTRIBUTING.md) for the inbound=outbound / DCO contribution terms.
