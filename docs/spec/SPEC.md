# agent-builder — Authoritative Spec

**Project:** agent-builder
**Last updated:** 2026-06-30 (goal-alignment — general-agent framing; agy as 3rd brain, gemini deprecated)
**Status:** Phase 1 implementation snapshot. The core Gate, loop, supervisor lifecycle, CLI, containment launcher contracts, the rootless **Podman** execution-box `sandbox.Runner` adapter, ingestion/armor harness seams, and branch/PR artifact publication are implemented as described here. The Phase 1 Podman containment swap (ADR 021) **removed** the rented `@anthropic-ai/sandbox-runtime` (`srt`) backend from the `agent-builder run` pipeline; `srt` is historical for the run path, not a pending runtime, and `AGENT_BUILDER_SANDBOX_RUNTIME` now errors loudly when set. Phase 0 and Phase 1 end-to-end acceptance are recorded at fake-provider L5 by the Task 032 and Task 037 harnesses; live Podman, the `runsc` runtime, real Claude, and real PR publication remain pending L6/operator evidence where local tooling is unavailable.

> Authoritative design source: `autonomous-builder.md`. This SPEC is the in-repo snapshot; where they disagree, reconcile in the same change.

## System summary

agent-builder is a **security-first general autonomous agent** (the Secure Agent Ecosystem's OpenClaw/Hermes equivalent): it runs a composed reasoning brain inside the security envelope (exec-sandbox, vault, policy-engine, audit-trail, armor), routes across multiple LLMs, and returns results over the channel. Its **first reference build — and what the current implementation realizes — is a secure autonomous coding agent**, run unattended against a target repository. In that Phase 1 implementation it can read ready task metadata, route one task through the pluggable `(harness, model) → branch` executor seam (Claude CLI, Antigravity/`agy`, and native Ollama harnesses, selected by the capability/cost router — ADR 043), run the attempt through the repo-owned rootless Podman execution-box-backed containment wiring (no longer the rented `srt` backend), verify the result with the machine-checkable Gate, publish the verified non-empty branch as a PR artifact through the git/GitHub CLI publisher seam, record run evidence, and escalate exhausted attempts through the constrained task-status writer. A general conversational goal-intake front door (`orchestrate`) is also implemented (clarify → plan → approval → policy-gated dispatch → result over the channel).

## Spec index

| Doc | Covers |
|-----|--------|
| [behaviors.md](behaviors.md) | What the system does — the loop, routing, escalation |
| [architecture.md](architecture.md) | Components: supervisor, agent loop, executor seam, containment, gate |
| [data-model.md](data-model.md) | Task, executor, verdict, run-record shapes |
| [interfaces.md](interfaces.md) | CLI surface; executor `(harness, model) → branch` contract; exec-sandbox `run()` seam |
| [configuration.md](configuration.md) | Egress allowlist, executor auth/token handling, resource limits |
| [fitness-functions.md](fitness-functions.md) | Executable invariants (see below) |

## Invariants (load-bearing)

1. **Verification gate is the definition of done.** No task completes unattended without the gate (tests + build + lint + scanners) passing. The gate is the only ground truth.
2. **No unattended self-modification of the trusted core.** The agent reads its own repo but never autonomously edits its own gate, escalation path, or control plane. (Self-improvement, when built, is reviewable sandboxed skill-writing — not core edits.)
3. **the internal planning hub is read-mostly.** Roadmap is input; the agent may flip task status, never author/reprioritize.
4. **One task = one repo = one branch.** No cross-repo sprawl within a task.
5. **Containment is rootless Podman + tiered runtime + default-deny egress allowlist.** The allowlist is the load-bearing control for the accepted token-in-box risk.
6. **Executor seam is `(harness, model) → branch`.** Pluggable across the three brains — **Claude**, **Antigravity/`agy`** (multi-model; successor to the `gemini` CLI deprecated 2026-06-18), and **native Ollama** (local); mixing uneven-quality executors is made safe by the gate (fail → escalate to a stronger executor). Every executor harness is coding-shaped (edits a worktree, returns a gate-verified branch); the only non-coding execution path is the single-shot `Completer` (ADR 053), which is **Ollama-native only** — cloud harnesses fail closed with `ErrSingleShotUnsupported`.
7. **Secrets:** git/GitHub publication tokens are brokered through vault in proxy mode when vault is enabled (`AGENT_BUILDER_VAULT_BIN` set) — they reach `api.github.com` only via the egress proxy as `Authorization: Bearer` and are never present in the box environment (ADR 036, task 066). The executor provider auth token (`ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN`) still lives in the box (accepted risk — flat-rate/no-overage + tight allowlist + revocability + scanners); brokering it through the proxy is deferred pending the feasibility probe (TC-066-07). When vault is disabled, all tokens follow the prior env-forwarding path. vault is the broker for publication tokens and *task* secrets, not (yet) for executor auth.

## Fitness functions

- **F-003 — supervisor has no LLM/untrusted-content dependency:** implemented by `make fitness-supervisor-isolation`; the supervisor package import graph contains no executor/LLM/web-fetch packages.
- **F-001 — no Docker dev-environment references:** implemented by `make fitness-no-docker`; working-tree files contain no `docker`/`docker-compose`/`Dockerfile` dev-environment references outside the allowed product-container path.
- **F-002 — gate is blocking:** implemented by `make fitness-gate-blocking`; production gate and CLI source expose no `--no-verify`/skip route around `dep-scan`/`code-scanner`.
- **F-010 — orchestrator authors no code (no direct executor import):** implemented by `make fitness-orchestrator-no-executor`; `internal/orchestrator`'s own direct imports contain no `internal/executor` (the executor is reached only transitively, through `internal/runtime`, for the dispatched worker — ADR 042/046).
- **F-011 — worker-transport adapter is a leaf:** implemented by `make fitness-worker-transport-isolation`; `internal/channel/worker`'s own direct imports contain no `agent-builder/internal/` package other than `internal/envelope`, `internal/supervisor`, and `internal/audit` (a direct-import check, as `internal/supervisor` legitimately drags in `internal/gate`/`internal/sandbox` transitively — ADR 048 §3).
- **F-012 — memoryguard IPC adapter is a leaf:** implemented by `make fitness-memoryguard-isolation`; `internal/memoryguard`'s transitive dependency graph contains no `agent-builder/internal/` path other than itself (only stdlib — ADR 049 §1).
- **F-013 — no recipe targets the agent-builder own-repo as a result sink:** implemented by `make fitness-no-self-repo-sink`; no registered recipe source declares `github.com/tkdtaylor/agent-builder` as a result sink / publish target (the static half of the self-repo bright line — ADR 050 §2 / ADR 042; the runtime half is the orchestrator's `spawn-worker` deny).
- **F-014 — LLM planner authors no code (no direct executor import):** implemented by `make fitness-llm-planner-no-executor`; `internal/orchestrator/planner`'s own direct imports contain no `internal/executor` (the model is reached only through the router/registry path — `internal/router` imports `internal/executor` transitively — via the narrow `ExecutorResolver`/`Invoker` seams; ADR 046 §6). The same direct-import bright line as F-010, extended to the LLM-backed `Planner` concrete.

See [fitness-functions.md](fitness-functions.md) for executable rule definitions and commands.
