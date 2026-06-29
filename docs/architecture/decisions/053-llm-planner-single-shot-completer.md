# ADR 053 — Single-shot model completion for the LLMPlanner (`Completer` seam)

**Status:** Proposed  
**Date:** 2026-06-28  
**Author:** architect  
**Architect review required:** yes — this introduces a new non-agentic model-invocation primitive and wires `AGENT_BUILDER_PLANNER=llm` live  

---

## Context

Task 100 merged the `LLMPlanner` (`internal/orchestrator/planner/llmplanner.go`): the
LLM-backed concrete behind the stable `orchestrator.Planner` seam. It decomposes a
free-form human goal into an ordered `orchestrator.Plan` by routing a decomposition
prompt through a model. It defines two seams and parses the model's structured-line
response into sub-goals:

- `ExecutorResolver.Resolve(ctx, router.RoutingSpec) (registry.RegistryEntry, error)` —
  picks the model (the registry entry the router selected).
- `Invoker func(ctx, registry.RegistryEntry, prompt string) (string, error)` — sends
  one prompt, returns raw model text.

Both are currently satisfied **only by test stubs**. There is no production `Invoker`,
and `internal/cli/orchestrate.go` `plannerFromEnv()` returns `ErrPlannerNotAvailable`
for `AGENT_BUILDER_PLANNER=llm` (a deliberate placeholder left by task 099). So
`AGENT_BUILDER_PLANNER=llm` cannot be selected on the live path.

The decision this ADR resolves: **how does the orchestrate CLI's `Invoker` reach a
model for a single-shot, non-agentic decomposition call?**

### What the existing primitives offer (and why none fits directly)

- `supervisor.Executor` (`internal/supervisor/supervisor.go`) exposes only
  `Run(Task) (Result, error)` — the **full agentic loop**: it creates a worktree,
  drives a tool-using conversation, runs the verification gate, and returns a branch.
  That is the wrong primitive for decomposition. Decomposition is a pure text query:
  no sandbox, no worktree, no tools, no gate, no branch. Forcing it through
  `Executor.Run` would mean spinning a box and a gate to ask a model one question.
- `internal/executor/ollama_native.go` already wraps a single-turn primitive: the
  `Chatter` seam — `Chat(ctx, ollamaclient.ChatRequest) (ollamaclient.ChatResponse, error)` —
  is one `/api/chat` round-trip. The `ollamaclient` package (task 102, ADR 051) provides
  `*ollamaclient.Client` implementing it. `OllamaNative` loops `Chat` for the agentic
  case, but a single call with one user message and no tools is exactly a single-shot
  completion. The operator already has a working local ollama setup (`qwen3:8b` drove
  task 108's L6 run).
- Cloud CLIs (`claude-cli`, `codex-cli`, `gemini-cli`, enumerated in
  `internal/registry/types.go`) currently expose only the agentic `Run` via their
  harness adapters. A single-shot for them would need a **print-mode subprocess**
  (`claude -p`, etc.) — new, per-harness code that does not exist yet.
- `internal/runtime/run.go` already maps a selected `registry.RegistryEntry` to a
  concrete agentic executor in `buildExecutorForEntry(entry, config)`, and builds the
  router catalog in the unexported package-level `var buildCatalog(config)`. These are
  the structural templates the single-shot path should mirror.

### Import-boundary constraints (load-bearing)

Two fitness functions gate this work and must stay green:

- **F-010** (`make fitness-orchestrator-no-executor`) — `internal/orchestrator` has no
  **direct** import of `internal/executor`.
- **F-014** (`make fitness-llm-planner-no-executor`) — `internal/orchestrator/planner`
  has no **direct** import of `internal/executor`.

Both are *direct*-import assertions (a `-deps` transitive check would false-positive on
the legitimate `internal/router` → `internal/executor` path). The planner package only
ever sees the `ExecutorResolver` interface and the `Invoker` func type it defines; the
concrete model path is supplied at the **wiring layer** (`internal/cli`), where an
`internal/executor` import already lives (`orchestrate.go` imports `internal/runtime`,
which imports `internal/executor`; `orchestrate_seams.go` imports executor-adjacent
packages). Anything the planner's `Invoker` calls into must therefore be constructed in
`internal/cli` and handed in as the opaque func — never imported by the planner package.

---

## Decision

### 1. Introduce a narrow `Completer` seam — the non-agentic counterpart to `Executor.Run`

```go
// Completer sends ONE prompt to the model behind a registry entry and returns the
// raw text. No worktree, no tools, no verification gate, no branch — the non-agentic
// counterpart to supervisor.Executor.Run. It is the production backing for the
// LLMPlanner's Invoker seam.
type Completer interface {
    Complete(ctx context.Context, entry registry.RegistryEntry, prompt string) (string, error)
}
```

**Location: `internal/executor`** (alongside the harness adapters, beside
`ollama_native.go`). This is the correct home because:

- The concrete completers *are* harness adapters — an ollama completer wraps
  `ollamaclient.Chat`, a future claude completer shells `claude -p`. They belong with
  their agentic siblings, reusing the same `ollamaclient` / harness-adapter code.
- `internal/executor` is **already** off the planner's and orchestrator's direct-import
  graph (that is exactly what F-010/F-014 assert). Adding the completer there changes
  nothing about those invariants: the planner still never imports `internal/executor`;
  it still only sees the `Invoker` func type. The CLI constructs the completer and
  closes over it to produce the `Invoker`.

A new top-level package was considered and rejected (see Options): it would buy no
isolation the existing boundary doesn't already give, and would fragment the
ollama-client reuse.

The completer is reached through a dispatcher that selects by harness:

```go
// CompleterForEntry returns the single-shot Completer for the entry's harness, or a
// typed "harness X single-shot completion not yet supported" error (fail-closed).
func CompleterForEntry(entry registry.RegistryEntry, config ...) (Completer, error)
```

### 2. Implement `ollama-native` first; cloud CLIs are named fail-closed follow-ons

The **only** completer built now is `ollama-native`, via the existing
`ollamaclient.Chat`: one `ChatRequest` with a single `{Role: "user", Content: prompt}`
message, `Tools: nil`, `Stream: false`; return `resp.Message.Content`. It is:

- locally testable end-to-end on the operator's host (no cloud credential, no quota),
  so it reaches **L5/L6** (validation harness / live binary observation);
- the lowest-cost path — one local round-trip, no sandbox.

`CompleterForEntry` dispatches on `entry.Harness`:

- `HarnessOllamaNative` → the ollama completer (built from `entry.Endpoint` +
  `entry.ModelID`, mirroring `buildExecutorForEntry`'s ollama arm).
- `HarnessClaudeCLI` / `HarnessCodexCLI` / `HarnessGeminiCLI` → a clear, typed error:
  *"harness <X> single-shot completion not yet supported"*. **Fail-closed, never
  silently wrong**: the planner gets an error and decomposition halts, rather than a
  cloud CLI being driven through its agentic `Run` (which would spin a box/gate) or an
  empty string being parsed as a zero-sub-goal plan. The cloud print-mode completers
  (`claude -p` / `gemini` / `codex` print mode) are deferred until there is a concrete
  need; their slots in the dispatcher are reserved by the explicit error arm.

### 3. The orchestrate CLI wires the two planner seams from existing pieces

In `internal/cli` (where the executor import already lives, keeping F-010/F-014 green):

- **`Invoker`** = a thin closure over `CompleterForEntry`:
  `func(ctx, entry, prompt) (string, error) { c, err := executor.CompleterForEntry(entry, …); if err != nil { return "", err }; return c.Complete(ctx, entry, prompt) }`.
- **`ExecutorResolver`** = a thin adapter over `*router.Router.Select`. The planner's
  `Resolve(ctx, spec)` drops the context and calls `r.Select(spec)` (Select takes no
  context and returns `(registry.RegistryEntry, error)` directly — shapes already
  align).
- **Catalog** = built the same way `internal/runtime` builds it. The runtime's
  `buildCatalog` is an **unexported package-level `var`** in `internal/runtime`, so the
  CLI cannot call it directly. Task 109/110 must either (a) lift the catalog-build logic
  into an exported helper the CLI and runtime share, or (b) have the CLI build its own
  `*registry.Catalog` via `registry.LoadFromEnv()` + the synthetic-default fallback.
  Option (b) is the lighter touch and keeps `internal/runtime` unchanged; the task that
  wires this picks based on how much duplication (a) would remove. Either way the router
  is `router.New(catalog)` and the resolver wraps its `Select`.
- `plannerFromEnv()` stops returning `ErrPlannerNotAvailable` for `"llm"` and instead
  constructs `planner.New(resolver, invoker)`.

### Why F-010 and F-014 stay green

- The `Completer` interface and concretes live in `internal/executor`. The
  `internal/orchestrator` and `internal/orchestrator/planner` packages **do not import
  `internal/executor`** — they never name `Completer` at all. The planner sees only the
  `Invoker` func type and `ExecutorResolver` interface it already defines.
- The closure that adapts `CompleterForEntry` to `Invoker`, and the adapter that wraps
  `router.Select` as `ExecutorResolver`, are both constructed in `internal/cli`, the
  blessed wiring layer that already imports executor-adjacent code (identical in form to
  how `*router.Router` satisfies `ExecutorResolver` only at wiring per ADR 046 §6).
- No new edge is added to the orchestrator's or planner's direct-import set, so the
  `go list -f '{{ .Imports }}'` direct-import checks behind F-010/F-014 are unaffected.

---

## Options considered

### Option A — `Completer` seam in `internal/executor`, ollama-first, CLI wires it (recommended)

One-sentence description: add a non-agentic `Completer` interface and an ollama concrete
beside the harness adapters; the CLI closes over a `CompleterForEntry` dispatcher to
produce the planner's `Invoker`.

**Pros**
- Clean separation of the single-shot query from the agentic `Run` — no sandbox/gate is
  ever spun to ask a model one question (Unix "one thing well").
- Reuses `ollamaclient.Chat` and the harness-adapter home; no new package, no duplicated
  client.
- F-010/F-014 untouched — the new type lives where the planner already cannot reach, and
  is wired only at `internal/cli`.
- Locally testable to L5/L6 with the operator's ollama; lowest cost.

**Cons**
- Adds a second model-invocation seam (`Completer` next to `Executor`) — two ways to
  reach a model. Mitigated: they are deliberately different primitives (text query vs.
  agentic loop), and the dispatcher names the split explicitly.
- Cloud-CLI single-shot is left unimplemented (fail-closed error), so `llm` planning is
  ollama-only until a follow-on adds print-mode.

Sketch: `internal/executor/completer.go` defines `Completer` + `CompleterForEntry`
(switch on `entry.Harness`, ollama arm built like `buildExecutorForEntry`'s, other arms
return a typed error). `internal/executor/ollama_completer.go` wraps `ollamaclient.Chat`
for a single message. `internal/cli/orchestrate.go` builds a catalog, wraps
`router.Select` as the resolver, closes over `CompleterForEntry` as the `Invoker`, and
calls `planner.New`.

### Option B — Reuse `supervisor.Executor.Run` for decomposition

One-sentence description: drive the existing agentic executor with the decomposition
prompt as the task spec and read the branch/result back as the response.

**Pros**
- No new seam; one model-invocation path for everything.
- The router/registry/harness wiring already exists end-to-end.

**Cons**
- Category error: `Run` creates a worktree, runs tools, and runs the **verification
  gate** — none of which decomposition needs. The decomposition "answer" is text, not a
  branch; `Result{Branch, OK}` has nowhere to put it.
- Spins a sandbox and a gate for a pure text query — large cost, large blast radius, and
  it drags gate/sandbox dependencies into a path that should have none.
- The planner would need a model path that is the agentic executor, which is exactly the
  unrestricted code-authoring path F-010/F-014 exist to keep out of the planning layer.

Sketch: the CLI's `Invoker` would call `buildExecutorForEntry(entry, config).Run(taskFromPrompt(prompt))`
and scrape text out of the produced branch — awkward, expensive, and boundary-violating.

### Option C — New top-level `internal/completion` package for the seam

One-sentence description: put `Completer` and its concretes in a fresh package separate
from `internal/executor`.

**Pros**
- Maximally explicit that single-shot completion is its own concept.
- A future where completion has no relationship to executors would be cleanly served.

**Cons**
- The concretes wrap the *same* `ollamaclient` and the *same* per-harness logic the
  executor adapters do — splitting them fragments that reuse and invites duplication.
- Buys no isolation the existing boundary doesn't already give: `internal/executor` is
  already off the planner/orchestrator direct-import graph, so F-010/F-014 are satisfied
  either way.
- Premature abstraction — there is one concrete (ollama) today; a new package for one
  implementation violates "defer premature decisions" (AGENTS.md design principles).

Sketch: identical code to Option A, relocated to `internal/completion`, with the
harness-adapter logic either duplicated or re-exported from `internal/executor`.

---

## Recommendation

**Option A.** The deciding factor is **blast radius and reuse**: decomposition is a pure
text query, and Option A is the only one that treats it as such — one local round-trip,
no sandbox, no gate — while reusing the `ollamaclient` and harness-adapter home that
already exists. Option B forces a text query through the agentic gate/sandbox machinery
(wrong primitive, large blast radius, and it would route the planner through the very
code-authoring path F-010/F-014 fence off). Option C adds a package boundary that buys no
isolation the existing one doesn't already provide and fragments the ollama reuse —
premature abstraction for a single concrete. Option A keeps F-010/F-014 green by
construction, reaches L5/L6 locally, and leaves a clean fail-closed slot for cloud
print-mode completers when they are actually needed.

---

## Consequences

**Positive**
- `AGENT_BUILDER_PLANNER=llm` becomes selectable live (ollama-backed), exercising the
  full decompose → plan → gate → dispatch path with a real model.
- A reusable, non-agentic `Completer` primitive exists for any future single-shot model
  need (not just planning) without dragging in the sandbox/gate.
- The single-shot path is verifiable end-to-end on the operator's host, so the LLM
  planner reaches ✅ (L5/L6) rather than stalling at unit-test level.

**Negative / what becomes harder**
- Two model-invocation seams now coexist (`Executor` for agentic work, `Completer` for
  single-shot). Anyone reaching for "call the model" must pick the right one; the
  dispatcher and this ADR are the signposts.
- `llm` planning is **ollama-only** until cloud print-mode completers are added — a cloud
  registry entry fails closed with a clear error. This is a deliberate, surfaced
  limitation, not a silent gap.
- The catalog-build duplication question (lift `buildCatalog` vs. CLI builds its own) is
  deferred to the implementing task; whichever path it picks, `internal/runtime` and
  `internal/cli` must not silently diverge in how they synthesize the default entry.

**Cross-cutting risks to handle in implementation**
- **Timeout/cost on the single-shot call.** `Complete` must take a `context.Context` and
  the call must be bounded — a hung local model must not wedge the orchestrate loop. The
  ollama completer threads the context into `ollamaclient.Chat` (which already honors
  cancellation, per `client_test.go` `TestChatContextCancelled`).
- **Prompt size.** `buildPrompt` embeds the full recipe catalog; for large catalogs the
  prompt could grow. Not a blocker now (the catalog is small), but the completer must not
  assume a bounded prompt size.
- **Fail-closed on unsupported harness.** The dispatcher returns a typed error for cloud
  harnesses; the planner already fails closed on an invoker error (`Plan` wraps it and
  returns the zero `Plan`), so an unsupported-harness selection halts decomposition
  cleanly rather than producing a degenerate plan.
- **SEC-001 (independent, noticed in passing).** `newTransportDispatch` in
  `internal/cli/orchestrate_seams.go` discards the error from two
  `envelope.GenerateKeyPair()` calls (`orchXPub, orchXPriv, _ := …`). This is the task-099
  audit finding and is unrelated to the planner wiring, but it sits in the same file the
  orchestrate path owns; task 111 below carries the fix.

---

## Recommended task decomposition

Three tasks. The task-planner owns writing the task files + test specs; this section is
the decomposition it should follow.

### Task 109 — `Completer` seam + `ollama-native` completer + `CompleterForEntry` dispatcher

Add the `Completer` interface and the `CompleterForEntry(entry, …)` dispatcher in
`internal/executor`, plus the `ollama-native` concrete that wraps `ollamaclient.Chat`
(single user message, no tools, no loop). The dispatcher returns the ollama completer for
`HarnessOllamaNative` and a typed "single-shot completion not yet supported" error for the
three cloud harnesses (fail-closed). This is the **prerequisite** for 110 and has no
dependency itself. **Highest verification level: L6** — the ollama completer is exercisable
against the operator's local ollama (`qwen3:8b`), so a validation-harness/live-binary run
can observe a real prompt-in → text-out round-trip; the dispatcher's fail-closed arms are
L4 unit-asserted.

### Task 110 — Wire `AGENT_BUILDER_PLANNER=llm` into `orchestrate` (depends on 109)

In `internal/cli/orchestrate.go`: build the router catalog (lift `buildCatalog` to an
exported shared helper, or build a catalog in the CLI via `registry.LoadFromEnv()` + the
synthetic-default fallback — pick per duplication cost), wrap `*router.Router.Select` as the
planner's `ExecutorResolver`, close over `CompleterForEntry` (from 109) as the `Invoker`,
construct `planner.New(resolver, invoker)`, and **remove the `ErrPlannerNotAvailable`
placeholder** from `plannerFromEnv()` for the `"llm"` case. Update the orchestrate usage
string (the `"(pending task 100)"` note) and `docs/spec/configuration.md` accordingly.
**Highest verification level: L6** — run the orchestrate binary with `AGENT_BUILDER_PLANNER=llm`
against local ollama and a free-form goal, observing a real decomposed plan; the F-010/F-014
fitness checks must be re-run and shown green in the same task.

### Task 111 — SEC-001 hardening: propagate the discarded `GenerateKeyPair()` error (independent)

In `internal/cli/orchestrate_seams.go` `newTransportDispatch`, stop discarding the error
from the two `envelope.GenerateKeyPair()` calls (`orchXPub, orchXPriv, _ := …` /
`workerXPub, workerXPriv, _ := …`). Propagate it so a keygen failure fails closed at
assembly time rather than producing zero-value seal keys. This is the task-099 security
audit finding SEC-001 — small, independent (no dependency on 109/110), **no ADR needed**.
**Highest verification level: L4** — a unit test injecting/forcing a keygen failure asserts
`newTransportDispatch` (and the `assembleOrchestrate` path that calls it) returns the error
rather than swallowing it; the change is not independently runtime-observable beyond the
assembly path, so L4 is the ceiling.
