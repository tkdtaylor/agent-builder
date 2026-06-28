# ADR 047 — agent-builder worker: generated-output format and delivery mechanism

**Status:** Accepted (2026-06-28) — design-only. Resolves the two open questions in
task 082's stub test spec (generated-recipe output **format**, and the **delivery**
mechanism) so the stub can be expanded into real assertions and implemented. No code,
spec, or diagram changes land with this ADR.
**Date:** 2026-06-28
**Extends:** ADR 041 (agent-recipe seam), ADR 042 (secure two-tier orchestrator),
ADR 044 (recipe-seam binding correction — config-taking factories). Does not contradict
any of them.
**Motivated by:** task 082 (`docs/tasks/backlog/082-agent-builder-worker-recipe.md`) and
its test spec, whose "Open questions" defer two decisions the gate-existence assertion
(REQ-082-03) and the audit-provenance assertion (REQ-082-05) both depend on.

## Context

ADR 042 states "code-authoring is a worker task, not an orchestrator capability," and
names the **agent-builder worker** as the first-party recipe that authors a *new agent
definition (a new Go recipe)* and hands it back. The worker inherits the full worker
safety model: code-scanner + dep-scan on generated code, the runtime gate-existence
assertion on the generated recipe output (task 078), containment, policy, human approval
before any generated agent is dispatched, and audit-trail provenance.

Task 082's test spec leaves two questions open:

1. **Output format** — is the "generated recipe" a `.go` source file that registers a
   recipe, or an in-memory struct literal / data artifact?
2. **Delivery** — does the worker produce a PR (like the coding-agent recipe), or deliver
   the recipe back to the orchestrator over agent-mesh ("hand it back")?

Two facts from the current codebase shape the answer:

- **Recipes are Go.** Since ADR 044, a recipe is a Go value produced by a config-taking
  factory registered in `internal/recipe`; `recipe.SelectRecipe(name)` returns it and the
  runtime assembles a worker from it. There is no data-only recipe representation.
- **The gate-existence assertion (task 078) inspects a gate binding.** It checks that a
  recipe binds a non-nil, non-skippable gate. For it to fire on *generated* output, that
  output must be a recipe definition the assertion can inspect — i.e. Go source that
  declares a recipe with a `GateFactory`.

## Decision

### 1. Output format — **a `.go` source file that registers a recipe via the seam**

The agent-builder worker authors a new agent definition as **Go source** (a `.go` file)
that declares a recipe in the `internal/recipe` shape: a config-taking factory binding a
non-skippable gate (per ADR 044). This is the only representation the recipe seam and the
task-078 gate-existence assertion already understand; a struct literal or bespoke data
format would require a second recipe representation and a second gate-existence code path,
which ADR 041/042's "no special code path" rule forbids.

The gate-existence assertion (REQ-082-03) therefore inspects the generated `.go` output
for a bound gate: a generated recipe with no gate binding is a **gate failure**; one with a
valid gate passes (modulo the other checks). The code-scanner + dep-scan steps (REQ-082-02)
run on the generated `.go` file and its declared dependencies.

### 2. Delivery — **the standard worker publish path (branch/PR), identical to any other worker; agent-mesh hand-back is deferred to task 083**

For v1 the agent-builder worker delivers its generated recipe as a **branch/PR artifact**
over the same publish path every worker uses — it does **not** get a bespoke return
channel. This satisfies REQ-082-06 ("no special code path in `internal/runtime` or
`internal/orchestrator`") by construction: the worker is dispatched, runs in exec-sandbox,
produces a branch, and publishes exactly like the coding-agent recipe.

"Hand it back" over agent-mesh (the orchestrator receiving the generated recipe as a signed
envelope) is **task 083's** concern and is explicitly out of task 082's scope. The
orchestrator dispatching the *generated* agent — only after human approval (REQ-082-04) — is
task 081's concern. Task 082 ends at "a gated, audited, contained worker that produces a
valid new-recipe `.go` artifact."

### 3. Audit provenance (REQ-082-05) follows from (1)

Because the output is a concrete `.go` file, the audit event records the **generated file
path + content hash** in its `EventDetail`, and the goal that prompted the generation in
`Refs` (or the nearest equivalent in the current `audit.AuditEvent` shape). This is a
direct consequence of the file-based output format and needs no separate mechanism.

## Consequences

- **Design-only.** No change to `internal/`, `cmd/`, `docs/spec/`, or diagrams lands with
  this ADR. The worker recipe enters the spec when task 082 ships.
- **Task 082 is unblocked.** Its stub spec can be expanded: TC-082-02/03 use `.go` fixtures
  (a known-bad pattern; a generated recipe with/without a gate binding); TC-082-04 uses an
  orchestrator **stub** returning `require_approval` (the real orchestrator is task 081);
  TC-082-05 asserts the generated file path + content hash in a `FakeSink` event; TC-082-06
  diffs `internal/runtime`/`internal/orchestrator` to prove no special path.
- **No second recipe representation** is introduced — the worker authors the same Go-recipe
  shape the seam already defines, keeping one gate-existence code path.
- **agent-mesh hand-back stays a clean follow-on** (task 083): when the signed-envelope
  transport lands, the orchestrator can receive the generated recipe as data; until then the
  branch/PR artifact is the deliverable, and nothing about the worker changes when 083 adds
  the transport (the worker still produces the same artifact).
- **All ADR 042 invariants hold:** the worker authors code (it is a *worker*, the thing ADR
  042 says may author code), never the orchestrator; it targets a branch, not
  agent-builder's own repo; it runs gated + contained + audited; and the generated agent is
  dispatched only after human approval.
