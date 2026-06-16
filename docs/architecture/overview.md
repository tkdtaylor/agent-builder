# Architecture Overview

**Project:** agent-builder
**Last updated:** 2026-06-04

A narrative tour of agent-builder. The authoritative, decision-level design lives in `autonomous-builder.md`; this is the in-repo orientation.

## The problem it solves

The secure-agent ecosystem (exec-sandbox, vault, policy-engine, audit-trail) is a stack of blocks that need building. agent-builder is the autonomous coding agent that builds them — reviewing a roadmap and working tasks unattended — and is itself the first concrete consumer of those blocks. The chicken-and-egg ("need a safe agent to build the blocks, need the blocks for a safe agent") is resolved by **adopt-to-bootstrap, build-to-ship**: run on rented isolation, make exec-sandbox v0 the first task, then swap.

## The shape of a run

```
supervisor (host, trusted, dumb)
  └─ pick one task from the roadmap
  └─ create ephemeral box (rootless Podman, read-only rootfs, default-deny egress allowlist)
  └─ start the agent loop INSIDE the box:
        read task → route to executor → executor edits ONE target repo's worktree
        → verification gate (go test + build + golangci-lint + dep-scan/code-scanner)
        → pass: branch + PR   |   fail: retry-N → escalate
  └─ enforce wall-clock kill, collect logs (audit seam), tear box down
```

## Components

- **Supervisor** — outside the box. Dispatches one task at a time, enforces the timeout/escalation kill, collects results, tears down. Deliberately has no dependency on executor/LLM/web code so a hijacked agent can never reach back through it (invariant F-003).
- **Agent loop** — inside the box. The pick→attempt→verify→escalate cycle.
- **Executor seam** — `(harness, model) → branch`. Cloud CLIs (Claude Code, Gemini) bundle harness + model; local LLMs supply a harness. Routed by quota + sensitivity + cost. Bootstrap uses a single executor; the router is a deferred v1 feature designed against this seam now.
- **Verification gate** — the machine-checkable definition of done. Thin: adopts Go tooling + the existing scanners as a blocking step.
- **Containment** — rootless Podman with a tiered runtime (`runc` → gVisor `runsc` → Kata/Firecracker) and a default-deny egress allowlist. `armor` guards the web-ingestion + tool-call path. This outer box is exec-sandbox v0 Tier 1 minus orchestration.
- **exec-sandbox adapter** — the `run()` seam; backed by the repo-owned rootless-Podman execution-box (exec-sandbox v0 Tier 1) since the Phase 1 swap (ADR 021). The rented `@anthropic-ai/sandbox-runtime` (`srt`) backend that bootstrapped Phase 0 has been removed from the run pipeline; its adapter package is retained out-of-graph for reference only.

## Design principles

Unix philosophy / composability over monolith (see CLAUDE.md). The blocks are standalone and pluggable; agent-builder composes them through stable seams, never absorbs them. The executor, repo-target, containment, and gate are all adapter seams so alternatives stay swappable.

## Repo topology

agent-builder **runs from** its own repo, **reads** the roadmap + contracts from the internal planning hub (read-mostly), and per task **checks out one target block repo** (exec-sandbox, vault, …) to branch-and-PR. It invokes the existing tool blocks (armor, dep-scan, code-scanner) but never edits them, and never edits itself unattended.
