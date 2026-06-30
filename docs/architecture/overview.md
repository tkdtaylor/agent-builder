# Architecture Overview

**Project:** agent-builder
**Last updated:** 2026-06-30

A narrative tour of agent-builder. The authoritative, decision-level design lives in `autonomous-builder.md`; this is the in-repo orientation.

## What it is

agent-builder is a **security-first general autonomous agent** — the Secure Agent Ecosystem's equivalent of OpenClaw / Hermes: persistent, extensible, self-improving, multi-LLM. It runs a capable existing agent (Claude Code first) as its **reasoning brain inside the security envelope** (exec-sandbox, policy-engine + egress allowlist, vault, audit-trail, armor) and owns the gateway, the multi-LLM router, skills/memory governance, and security — it **composes a brain, it does not reimplement reasoning**. You hand it a goal and it works toward it, returning results and escalations over the channel the request arrived on (CLI now, Telegram next), behind two human gates (plan/action approval + result review).

**Coding is the first reference build, not the definition.** The autonomous coding agent — work a target repo's tasks unattended, one at a time, behind a machine-checkable verification gate — is the proving ground and is what most of today's code realizes. Contributing to a repo or starting a project are *skills*; the general agent has many. Forward work (not yet built): persistent cross-session memory, an always-on daemon, a general self-extending skill system, the composed-brain-as-general-executor (a non-coding execution path for the cloud brains), broader routing, and the secure skill-writing loop.

## The problem the security envelope solves

The Secure Agent Ecosystem ships its security as a stack of standalone blocks (exec-sandbox, vault, policy-engine, audit-trail, armor) rather than one framework. Composing them correctly around a running agent — sandboxed execution, JIT-brokered credentials, policy-gated actions, tamper-evident audit, guarded ingestion, all wired over the blocks' published contracts — is the hard part. agent-builder is the layer that runs the brain *inside* that composed envelope; the coding reference build is the first agent assembled this way.

Historically, agent-builder began as the agent meant to *build* the blocks and bootstrapped exec-sandbox v0 — run on rented isolation, then swapped onto the block it produced (ADR 021/035). The blocks ultimately shipped as independent repos; the "adopt-to-bootstrap, build-to-ship" arc and the repositioning to a composition layer are recorded in ADR 040.

## The shape of a run (coding reference build)

This is the run shape of the coding skill — the one fully built today. The general path (goal → clarify → plan → policy-gated dispatch → result over the channel) wraps the same secure seams; the orchestrate flow in `diagrams.md §5` shows it.

```
supervisor (host, trusted, dumb)
  └─ pick one task from the roadmap
  └─ create ephemeral box (rootless Podman, read-only rootfs, default-deny egress allowlist)
  └─ start the agent loop INSIDE the box:
        read task → route to a brain (Claude · agy · local ollama)
        → executor edits ONE target repo's worktree
        → verification gate (go test + build + golangci-lint + dep-scan/code-scanner)
        → pass: branch + PR   |   fail: retry-N → escalate
  └─ enforce wall-clock kill, collect logs (audit seam), tear box down
```

## Components

- **Supervisor** — outside the box. Dispatches one task at a time, enforces the timeout/escalation kill, collects results, tears down. Deliberately has no dependency on executor/LLM/web code so a hijacked agent can never reach back through it (invariant F-003).
- **Agent loop** — inside the box. The pick→attempt→verify→escalate cycle.
- **Executor seam** — `(harness, model) → result`. The three brains are **Claude Code**, **`agy`/Antigravity** (multi-model; the successor to the `gemini` CLI, deprecated 2026-06-18), and **native Ollama** (local). Cloud CLIs bundle harness + model; local LLMs supply a harness. The capability/cost router (`internal/router`, ADR 043) selects across them by quota + sensitivity + cost. For the coding build the result is a verified branch; the seam itself is general.
- **Verification gate** — the machine-checkable definition of done. Thin: adopts Go tooling + the existing scanners as a blocking step.
- **Containment** — rootless Podman with a tiered runtime (`runc` → gVisor `runsc` → Kata/Firecracker) and a default-deny egress allowlist. `armor` guards the web-ingestion + tool-call path. This outer box is exec-sandbox v0 Tier 1 minus orchestration.
- **exec-sandbox adapter** — the `run()` seam; backed by the repo-owned rootless-Podman execution-box (exec-sandbox v0 Tier 1) since the Phase 1 swap (ADR 021). The rented `@anthropic-ai/sandbox-runtime` (`srt`) backend that bootstrapped Phase 0 has been removed from the run pipeline; its adapter package is retained out-of-graph for reference only.

## Design principles

Unix philosophy / composability over monolith (see AGENTS.md). The blocks are standalone and pluggable; agent-builder composes them through stable seams, never absorbs them. The executor, repo-target, containment, and gate are all adapter seams so alternatives stay swappable.

## Repo topology

agent-builder **runs from** its own repo, **reads** the roadmap + contracts from the internal planning hub (read-mostly), and for the coding skill per task **checks out one target repo** to branch-and-PR. It invokes the existing tool blocks (armor, dep-scan, code-scanner) but never edits them, and never edits its own trusted core unattended (self-improvement is secure skill-writing).
