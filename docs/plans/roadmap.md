# Roadmap

**Project:** agent-builder
**Last updated:** 2026-06-04

Derived from `autonomous-builder.md` §2, §8. This roadmap doubles as the agent's own work queue once it runs — but during bootstrap it is built by hand (supervised).

## Phase 0 — Bootstrap the loop (supervised, human-in-the-loop)

Goal: a runnable orchestrator that can take one task, route it to one executor, run the verification gate, and branch+PR — all inside rented isolation. This is the minimum to "flip the switch."

| # | Deliverable | Notes |
|---|-------------|-------|
| 0.1 | **Verification gate** (thin) | `go test` + `golangci-lint` + `dep-scan`/`code-scanner` as a blocking step. The one genuinely-missing necessary piece. |
| 0.2 | **Agent loop + escalation** | pick task → attempt → verify → retry-N → escalate → next. Stop condition is mandatory. |
| 0.3 | **Podman containment profile + egress allowlist** | rootless, read-only rootfs, no socket, default-deny egress. The outer box = exec-sandbox v0 Tier 1 minus orchestration. |
| 0.4 | **Supervisor** | dispatch one task, wall-clock kill, collect logs, teardown. Dumb by design. |
| 0.5 | **exec-sandbox adapter seam** | `run()` contract; backed by `@anthropic-ai/sandbox-runtime` today (rented). |
| 0.6 | **Single executor** (Claude Code CLI) | one executor first; router is later. |

## Phase 1 — First block: exec-sandbox v0

The agent's **first real task**. Build exec-sandbox v0 behind the adapter seam, then swap the rented isolation for it. Resolves the chicken-and-egg.

## Phase 2+ — Remaining blocks (self-leveraging order)

`audit-trail` → `policy-engine` → `vault`. Each removes a specific human checkpoint and improves the agent that builds the next.

## Deferred (not bootstrap-critical)

- **Multi-provider router** (Claude + Gemini + local LLMs, quota/sensitivity/cost routing) — design the seam now, build as v1.
- **memory-guard / agent-mesh** — multi-agent / long-horizon memory, out of scope.
- **The "tool to build agents" product surface** — the north-star evolution, after the blocks are usable.

## Sequencing note

The agent is built against block *interfaces* (specced + contract-validated in the internal design hub), and its first task *implements* exec-sandbox behind that interface — foundations-before-agent is preserved, not skipped.
