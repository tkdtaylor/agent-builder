# ADR 041 — The agent-recipe seam

**Status:** Accepted — design-only. Defines the seam that makes the ADR 040
generalization real; no code, spec, or diagram changes in this ADR.
**Date:** 2026-06-27
**Motivated by:** ADR 040 (reposition agent-builder as the ecosystem's assembly layer),
which promoted "builder of purpose-built agents" from a deferred north-star bullet to
the primary forward arc and explicitly left its decomposition into tasks as follow-on.
This ADR is the first step of that follow-on.

## Context

ADR 040 repositioned agent-builder as the **assembly layer** — the front door that
composes the secure-agent blocks into purpose-built secure agents — with the autonomous
coding agent as the first reference build. It made no code changes; it pointed at the
load-bearing seams (executor, repo-target, containment, gate) as the reusable substrate
and deferred the question of *how* a second purpose-built agent gets assembled over them.

Reading the run pipeline as it stands today (`internal/runtime/run.go`) settles the open
question with a sharp finding: **the secure block seams are already purpose-neutral and
parametric.** vault token brokering, the policy-engine decide gate, the audit-trail sink,
signed checkpoints, and rootless-Podman/exec-sandbox containment are all opt-in,
config-driven, and indifferent to *what* the agent is doing inside the box. None of them
encode "coding." They are exactly the cross-cutting controls *any* secure agent needs.

What is hardwired to the coding purpose is a small, enumerable set of **IO seams** — the
four places the pipeline touches the outside world with purpose-specific intent:

1. **Goal source** — `internal/tasksource` reads a target repo's roadmap/task metadata
   and picks the next ready task. Generalized: a pluggable "what should the agent do"
   provider.
2. **Executor + prompt** — `internal/executor` is the Claude CLI invoked with a *coding*
   framing in a task worktree. Generalized: a harness plus a purpose system-prompt /
   contract. The `(harness, model) → branch` seam shape is already abstract; the coding
   framing is the part that is fixed.
3. **Verification gate** — `internal/gate` runs `go build`/`go vet`/`go test`/`gofmt`/
   `golangci-lint`/`dep-scan`/`code-scanner`. Generalized: a purpose-specific,
   machine-checkable success predicate. This seam is the one that carries the project's
   most load-bearing invariant, so it gets special treatment below.
4. **Output / publication** — `internal/publisher` pushes a verified branch and records a
   GitHub PR artifact. Generalized: a pluggable result sink.

All four are already Go interfaces with fake backends (`supervisor.Executor`,
`supervisor.Gate`, the `tasksource`/`publisher` seams). The blocker to a second agent is
not the seam shapes — it is that `runtime.Config` hardwires the *coding* concretes
(`tasksource.New(...)`, `executor.NewClaudeCLI(...)`, `newProductionGate()`,
`branchpub.NewGitHubCLI(...)`) and grows one flat env-var struct that conflates
purpose-specific IO with purpose-neutral block wiring.

## Decision

Introduce an **agent recipe**: a declarative profile that names one purpose-built secure
agent by binding the four IO seams (goal source, executor+prompt, gate, result sink) plus
the existing block-wiring config into a single named unit. `runtime.Config` is assembled
**from a recipe** rather than from coding-specific defaults baked into `runtime.Run`.

Concretely the seam is:

- A **recipe** selects one concrete implementation for each of the four IO seams and
  carries (or references) the block-wiring config (vault / policy / audit / checkpoint /
  containment backend / limits). The four IO seams stay leaf packages behind their
  existing small interfaces; the recipe is the *binding*, not a new god-package. `runtime`
  becomes a thin assembler that reads a recipe and constructs the supervisor — it does not
  absorb the seams' logic.
- The existing autonomous coding agent is re-expressed as **recipe #1** ("autonomous
  coding agent") with **zero behavior change**: it binds `tasksource` + Claude-CLI-coding
  + the Go production gate + git/GitHub publisher, and the same block-wiring it has today.
  The current env-var surface remains the way recipe #1 is configured.
- Because the project defers abstractions until the 2nd/3rd concrete use case demands them
  (design principles), this ADR requires a **second, deliberately-trivial recipe** to
  prove the seam is genuine rather than a coding agent in a costume. The named candidate
  is an **autonomous docs-fix agent**: goal source = a list of doc lint findings (or a
  single "fix this file" goal), executor = the same harness with a docs-editing prompt,
  gate = a non-Go predicate (e.g. a markdown linter + link-checker + the existing
  code-scanner), result sink = the same branch/PR publisher. It shares containment and
  blocks with recipe #1 and differs *only* in the four IO seams — exactly the surface this
  ADR claims is the variable part. If a docs-fix recipe cannot be expressed without
  touching `runtime` internals, the seam is wrong and this ADR has failed its own test.

### Recipe form: Go-typed, constructed in-process (not a runtime config file)

A recipe is a **Go value** — a typed struct (or a small constructor function) that returns
the four seam implementations plus the block-wiring config — selected by name at process
start. It is **not** a declarative plain-text file parsed at runtime into a dispatch table.

This is a deliberate departure from the project's "plain text for configs" lean, and the
reasoning is the "explicit over implicit" and "fail fast" rules winning over the plain-text
default:

- A recipe binds **executable behavior** (which executor harness, which gate steps run,
  which result sink), not data. A plain-text recipe file would have to name code to run —
  which is either a fixed enum of in-process implementations (in which case the file adds
  an indirection layer over a Go switch with no new power) or a path to an external binary
  (which is the plugin-dispatch architecture this ADR explicitly defers as premature).
- A Go-typed recipe is checked at compile time: a recipe that omits a gate **cannot
  compile**, which is the strongest possible enforcement of the gate-is-the-definition-of-
  done invariant. A parsed config file pushes that check to runtime and to a validator we
  would have to write and keep correct.
- "Plain text for configs" still holds for the *block-wiring* layer — the vault/policy/
  audit/containment knobs stay env-driven exactly as they are today. The recipe selects
  and binds; the per-deployment tuning stays plain-text env.

The trade-off: adding a recipe requires a code change and a rebuild, not an ops-time
config edit. That is acceptable — and arguably correct — while recipes are first-party and
few. The day a third party needs to define a recipe without forking, revisit this with a
follow-on ADR (the seam shape here does not preclude a later declarative loader over the
same typed recipe interface).

### The four seams stay leaves; `runtime` stays an assembler

Each IO seam remains a small interface in its own leaf package (they already are). The
recipe references those interfaces; it does not pull their implementations into one
package. `runtime.Run` shrinks from "construct the coding concretes inline" to "ask the
selected recipe for its four seam implementations + wiring, then assemble the supervisor."
This keeps modularity (each seam built and tested on its own), avoids coupling (a recipe
depends on seam interfaces, not on sibling seams), and preserves reusability (a seam
implementation is liftable without dragging the recipe with it).

## Why this framing and not the alternatives

- **Not "keep piling env-var flags onto the flat `Config`."** Every new purpose would add
  another cluster of `AGENT_BUILDER_*` vars to one struct that already conflates IO seams
  with block wiring, and the *selection* between coding and non-coding behavior would have
  to live as conditionals inside `runtime.Run`. That is accidental monolithic drift: one
  package growing to know about every purpose, with no compile-time guarantee that a given
  combination is coherent (e.g. nothing stops a config with no gate). The recipe makes the
  unit of "a purpose-built agent" a first-class, type-checked thing instead of an implicit
  combination of env vars.
- **Not a full plugin / external-binary dispatch architecture now.** Defining recipes as
  external binaries or dynamically-loaded plugins would add a process/ABI boundary, a
  discovery mechanism, and a trust boundary (an external recipe binary is attacker surface
  the gate and scanners would now have to vet) — all to support a generality (third-party
  recipes) that has zero concrete demand today. Per "defer premature decisions," this is
  exactly the abstraction to *not* build until a real second-party need exists. The
  in-process typed recipe is reversible into a plugin loader later; the reverse is not
  cheap.

## Consequences

- **This ADR is design-only.** No change to `internal/`, `cmd/`, `docs/spec/`, or
  `docs/architecture/diagrams.md` lands with it. It defines the seam; implementation is
  follow-on tasks.
- **The spec stays present-tense describing the coding agent.** Per ADR 040, `docs/spec/`
  describes what *is* — a Go orchestrator composing the blocks into a secure autonomous
  *coding* agent. The recipe surface enters the spec only when it actually ships
  (the recipe type + loader + at least the two recipes). Until then the spec must not be
  rewritten to speak of recipes in the present tense.
- **All load-bearing invariants survive unchanged, and a recipe cannot weaken any of
  them:**
  - *Verification gate is the definition of done.* A recipe **must** supply a real gate;
    "no gate" is not a legal recipe. With a Go-typed recipe this is enforced at compile
    time. The gate predicate may be purpose-specific, but its existence and its blocking,
    no-skip character (F-002) are non-negotiable across every recipe.
  - *No unattended self-modification.* Recipes select goal sources and result sinks; none
    may target agent-builder's own repo for autonomous edits. The recipe seam does not
    introduce a self-edit path.
  - *One task = one repo = one branch.* The recipe's goal source yields one unit of work
    at a time; the result sink publishes one branch. The seam preserves the
    no-cross-repo-sprawl rule.
  - *Containment = rootless Podman + tiered runtime + default-deny egress allowlist.*
    Containment is block-wiring, not an IO seam; it is shared across recipes and a recipe
    cannot opt out of it. The egress allowlist stays the load-bearing token-in-box control.
  - *Executor seam = `(harness, model) → branch`.* The recipe binds the harness/model and
    the purpose prompt; the seam shape is unchanged and the gate still makes mixing
    uneven-quality executors safe.
  - *Secrets brokering.* vault/policy/audit/checkpoint wiring is purpose-neutral and
    shared; a recipe selects it via the existing opt-in env surface and cannot route around
    it.
- **Decomposition into tasks is the immediate follow-on** (each its own task + test spec):
  (1) the recipe type + the in-process selector/loader; (2) making each of the four IO
  seams selectable from a recipe rather than constructed inline in `runtime.Run`;
  (3) re-expressing the autonomous coding agent as recipe #1 with a behavior-preservation
  test proving zero drift; (4) the second proof recipe (docs-fix) with its own non-Go gate.
- **What becomes harder:** `runtime` gains an indirection — reading the pipeline now means
  reading the selected recipe first, not a single linear `Run`. Adding a recipe is a code
  change, not a config edit. These are the deliberate costs of making "a purpose-built
  agent" a first-class unit; they are accepted in exchange for the compile-time gate
  guarantee and the clean per-recipe boundary.
- **The authoritative design source (`autonomous-builder.md`, internal planning hub)**
  still frames agent-builder around the single coding agent; the recipe seam should be
  reconciled there separately, as ADR 040 already noted for the broader repositioning.
