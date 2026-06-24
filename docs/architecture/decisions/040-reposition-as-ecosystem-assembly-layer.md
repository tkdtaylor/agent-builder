# ADR 040 — Reposition agent-builder as the ecosystem's assembly layer

**Status:** Accepted — the live project identity is now "assembly layer / builder of
purpose-built secure agents." The autonomous coding agent is the first reference build.
**Date:** 2026-06-24
**Supersedes:** the *north-star framing* of ADR 035 ("from *builds the blocks* toward
*built on the blocks*") and the "builds the blocks" / "first concrete consumer"
identity language in ADR 021 and ADR 026. Those ADRs stand as the historical record of
the decisions they made; this ADR updates the **live identity** the current-truth docs
(README, AGENTS.md, overview, SPEC) assert.

## Context

agent-builder was conceived as *the autonomous coding agent that builds the
secure-agent ecosystem blocks* — exec-sandbox, vault, policy-engine, audit-trail. The
roadmap's chicken-and-egg ("need a safe agent to build the blocks, need the blocks for
a safe agent") was to be resolved by **adopt-to-bootstrap, build-to-ship**: run on
rented isolation, make exec-sandbox v0 the first task, then swap onto it.

That bootstrap happened (ADR 021 swapped containment onto the repo-owned execution-box;
ADR 035 made the shipped `exec-sandbox` block the default run backend). But the rest of
the premise did not play out as planned: **the foundational blocks were built repo by
repo, by hand, not produced autonomously by agent-builder.** As of mid-2026 all the
blocks have shipped to at least v1 and are adopted into agent-builder
(exec-sandbox, audit-trail, vault, policy-engine, armor — see the roadmap's
block-adoption table). The "agent that builds the blocks" framing now describes work
that is finished and was largely done elsewhere.

This left the README and the canonical briefing leading with an identity that is no
longer the project's purpose. Two facts make the repositioning clean rather than a
land-grab:

1. **The "blocks working together" validation role is already owned elsewhere.** The
   an internal contract-validation prototype (v0, in-process) and `agent-integration` (v1
   cross-block, cross-language integration harness over the real compiled binaries)
   already validate that the blocks compose. agent-builder taking that role would
   duplicate them.
2. **agent-builder already *is* a working secure agent composed from the blocks.** It
   runs unattended (pick task → sandboxed executor → verification gate → PR) and wires
   exec-sandbox, vault, policy-engine, audit-trail, and armor over their published
   contracts. The composition layer is the asset; the "build the blocks" mission was
   the scaffolding.

## Decision

**Reposition agent-builder as the assembly layer of the Secure Agent Ecosystem — the
front door that composes the foundational blocks into purpose-built, secure autonomous
agents.** The autonomous coding agent is the **first and reference build**, not the
whole of the project.

Concretely:

- **Lead identity (README, AGENTS.md "What this is", overview).** agent-builder
  composes the blocks (exec-sandbox, vault, policy-engine, audit-trail, guarded by
  armor) into secure agents. The coding agent is the working first reference build.
- **North star.** From *this one agent* to *a tool that assembles any purpose-built
  secure agent from the same blocks/seams* (executor, repo-target, containment, gate).
  This is now the **primary forward arc**, no longer gated on block readiness (the
  blocks have shipped).
- **"Builds the blocks" becomes origin, not purpose.** It is preserved as a short
  historical note in the README/AGENTS.md and recorded in this ADR plus ADR 021/026/035
  — it must not be reintroduced as the live mission.
- **The spec stays present-tense.** `docs/spec/SPEC.md` describes what exists today: a
  Go orchestrator that composes the blocks into a secure autonomous coding agent run
  against a target repo. The generalized "builder of any agent" surface is forward work
  and lives in the roadmap, never in the spec.

## Why this framing and not the alternatives

- **Not "validation harness."** Already covered by the internal contract-validation prototype + `agent-integration`;
  adopting it here would duplicate, not add.
- **Not "keep building the blocks."** The blocks have shipped; the mission is complete
  and was carried out repo by repo. Continuing to assert it makes the README inaccurate.
- **"Assembly layer with a reference build"** is the framing that is simultaneously
  accurate to the code today (a composed, working agent) and honest about the
  direction (generalize the composition). It keeps the load-bearing seams — executor,
  repo-target, containment, verification gate — as the reusable substrate.

## Consequences

- **The current-truth docs are rewritten** to lead with the assembly-layer / builder
  identity (see the file list below). The architectural invariants, the run shape, the
  containment model, and the block-adoption status are all unchanged — only the stated
  purpose moves.
- **No code changes.** This is an identity/positioning decision. The orchestrator,
  seams, gate, and block wiring are untouched.
- **The "builder of purpose-built agents" surface is promoted from a deferred
  north-star bullet to the primary forward arc** in the roadmap. Its decomposition into
  tasks is follow-on work, not part of this ADR.
- **Historical records are preserved.** ADR 021/026/035 and completed task files
  (037, 063) keep their original "builds the blocks" language as the record of what was
  decided then; this ADR is the pointer that the live identity has moved on.
- **The authoritative design source (`autonomous-builder.md`, internal planning hub)
  carries the old framing** and should be reconciled there separately — it lives
  outside this repo, so it is noted here as a follow-up, not edited in this change.

## Spec / doc files updated in this change

- `README.md` — lead identity rewritten to the assembly layer; coding agent as first
  reference build; "builds the blocks" demoted to an Origin note.
- `AGENTS.md` — "What this is" and "North star" reframed (canonical briefing all
  harnesses load).
- `docs/architecture/overview.md` — "The problem it solves" rewritten.
- `docs/spec/SPEC.md` — system-summary one-liner made present-tense and block-composition
  framed (no "to build the blocks").
- `docs/plans/roadmap.md` — the "tool to build agents" bullet promoted to the primary
  forward arc.
- `docs/spec/fitness-functions.md` — F-007 why-clause de-references the stale "north-star"
  wording (the "runs on the block it built" property it guards is unchanged).
