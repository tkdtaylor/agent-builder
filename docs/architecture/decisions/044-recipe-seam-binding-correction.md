# ADR 044 — Recipe-seam binding correction

**Status:** Accepted — design-only. Corrects the recipe seam's binding shape so the
real coding-agent IO seams can be wired through it; no code lands with this ADR (an
executor implements it on the task 077 branch).
**Date:** 2026-06-27
**Motivated by:** task 077 (re-execute the recipe seam wiring the REAL coding-agent
seams). 077 hit a BLOCK at TC-077-03 that exposed a design gap in task 076 / ADR 041:
the seam shape merged in 076 cannot carry the real goal source or result sink, because
those need runtime config the recipe never receives, and their real interfaces are not
reachable from the leaf `internal/recipe` package.
**Amends:** ADR 041's recipe seam shape. ADR 041 (as built in task 076) made
`GoalSource` and `ResultSink` **config-free instance fields** set at registration
(`init()`) time, with interfaces `FetchGoal() (string, error)` / `WriteResult(string)`.
This ADR replaces those instance fields with **config-taking factories**, replaces the
two invented interfaces with ones matching the real contracts, and relocates the
goal-source/result-sink seam interfaces to a leaf-safe package. ADR 041's leaf-purity
rule (TC-076-02), its Go-typed in-process recipe form, and ADR 043's `RoutingSpec`
(no `ExecutorFactory`) are all preserved unchanged.

## Context

Task 076 merged the `Recipe` type, the registry, and the `RoutingSpec` value type
(`internal/recipe/recipe.go`). It bound the executor seam correctly via the ADR 043
`RoutingSpec` (resolved to a concrete executor by the runtime's `stubResolveExecutor`).
But it modelled the other three IO seams as **instance fields constructed at
registration time**:

```go
type Recipe struct {
    GoalSource  GoalSource   // instance, set in the recipe factory
    GateFactory GateFactory  // func() supervisor.Gate
    ResultSink  ResultSink   // instance, set in the recipe factory
    ...
}
```

with hand-written interfaces `GoalSource.FetchGoal() (string, error)` and
`ResultSink.WriteResult(string) error`. Task 077 then tried to bind the *real*
concretes and surfaced three concrete gaps:

1. **Config can't reach the seams.** The recipe factory runs at process start
   (`Register` is called from an `init()`/`RegisterBuiltins`), but the real goal source
   (`tasksource.New`, which needs `config.TaskRoot` → `os.DirFS`, the roadmap path, the
   task dirs) and the real result sink (`publisher.NewGitHubCLI`, which needs the
   remote, the git/gh tokens, and the CLI paths) require **runtime config that does not
   exist at registration time**. The executor seam already solves this correctly —
   `stubResolveExecutor(spec, config)` takes config at assembly time. The other seams
   need the same treatment, and the instance-field shape forecloses it.

2. **Seam interfaces don't match reality.** The real goal source is
   `tasksource.Source.Next() (supervisor.Task, bool, error)` — it returns a typed
   `supervisor.Task` and an "is there one" boolean, not a `string`. The real result
   sink is `publisher.Publisher.Publish(ctx, Request) (Result, error)` — a context-aware
   call over a typed `Request`/`Result`, not `WriteResult(string)`. The 076 interfaces
   were plausible guesses that no real concrete satisfies.

3. **Leaf-purity blocks the real interfaces.** `internal/recipe` must import no
   concretes (the load-bearing TC-076-02 rule). The executor/gate seams wire fine
   because `supervisor.Executor`/`supervisor.Gate`/`supervisor.Task` live in
   `internal/supervisor`, which the leaf already imports. But the `Publisher` interface
   lives in the **concrete** `internal/publisher` package, and the goal source has no
   leaf-safe interface at all — so `internal/recipe` cannot reference either real seam
   type without importing a concrete and breaking leaf-purity.

These three are one design gap with three faces: the seam binding shape merged in 076
is too narrow to carry the real seams. ADR 041's *intent* — a recipe binds the four IO
seams, runtime is a thin assembler — is correct; the *binding mechanism* needs
correcting before 077 can wire reality.

## Decision

Make all four IO seams **config-taking factories** binding to **leaf-safe interfaces**,
with the goal-source and result-sink interfaces **relocated to `internal/supervisor`**
(the seam package the leaf already imports). Config flows to the factories via a
**narrow, leaf-defined config-accessor interface** that `runtime.Config` satisfies.

### 1. Config-flow mechanism: a narrow leaf-defined accessor interface (option b)

The factory parameter type must not force `internal/recipe` to import
`internal/runtime` (that would invert the dependency and break leaf-purity). Three
options were considered:

- **(a) factory takes `any`, runtime-registered factory type-asserts to
  `runtime.Config`.** Leaf-pure (the leaf names no runtime type) but throws away
  compile-time type-safety and pushes a `config.(runtime.Config)` assertion + runtime
  panic into every factory — directly against the project's "explicit over implicit" /
  "fail fast at compile time" lean and the same reasoning ADR 041 used to choose a
  Go-typed recipe over a parsed file.
- **(b) factory takes a narrow leaf-defined config-accessor interface that
  `runtime.Config` satisfies.** The leaf declares a small interface (e.g.
  `SeamConfig`) exposing only the accessors the seams need (`TaskRoot()`,
  `PublishRemote()`, `GitToken()`, …). `runtime.Config` satisfies it (add the trivial
  accessor methods). Leaf-pure (the interface is defined in the leaf; `runtime` depends
  on `recipe`, never the reverse) **and** type-safe (a factory receives a typed value,
  no assertion).
- **(c) config flows via the existing `BlockWiring map[string]interface{}`.** Stuffing
  `TaskRoot`/tokens into the opaque map is the loosest option — stringly-typed keys, no
  compile-time check that a required key is present, and it conflates per-deployment IO
  config with the block-wiring it was designed for.

**Chosen: (b).** It is the only option that keeps both leaf-purity and the type-safety
the project prefers. The cost is one small interface to maintain and a handful of
one-line accessor methods on `runtime.Config` — cheap, and the accessor surface is
itself documentation of exactly what the seams are allowed to read from config.
Crucially, because the coding-agent recipe is **registered from `internal/runtime`**,
the factory *functions* can freely use `runtime` types (they close over or downcast as
the runtime author sees fit); only the factory *type signatures stored on the leaf
`Recipe`* must speak the leaf-defined accessor interface.

### 2. Relocate the goal-source and result-sink seam interfaces to `internal/supervisor`

Mirror the existing `Executor`/`Gate` pattern: the seam **interfaces** live in
`internal/supervisor` (already a leaf-safe import for `internal/recipe`); the
**concretes** stay where they are.

- **`GoalSource`** moves to `internal/supervisor`, redefined to match
  `tasksource.Source.Next`:
  ```go
  // in internal/supervisor
  type GoalSource interface {
      Next() (task Task, ok bool, err error)
  }
  ```
  `*tasksource.Source` already satisfies this exactly — no adapter needed.

- **`ResultSink`** moves to `internal/supervisor`, redefined to match
  `publisher.Publisher`:
  ```go
  // in internal/supervisor
  type ResultSink interface {
      Publish(ctx context.Context, req PublishRequest) (PublishResult, error)
  }
  ```
  The `Publisher` *interface* is **mirrored** into `internal/supervisor` as
  `ResultSink`; the concrete `*publisher.GitHubCLI` stays in `internal/publisher`. To
  avoid `internal/supervisor` importing `internal/publisher` (which would create a cycle
  and pull a concrete into the supervisor's graph, risking F-003), the `Request`/`Result`
  payloads are mirrored as `supervisor.PublishRequest`/`supervisor.PublishResult` and a
  **thin one-method adapter** in `internal/runtime` (or `internal/publisher`, since it
  already imports supervisor) wraps `*publisher.GitHubCLI` to satisfy
  `supervisor.ResultSink`. Mirroring the interface is acceptable and consistent with how
  `supervisor.Executor`/`supervisor.Gate` already live apart from their concretes; the
  concrete is not moved, only the seam contract is named in a leaf-safe place.

`internal/recipe` then imports only `internal/supervisor` (+ stdlib) for all four seam
types — leaf-purity (TC-076-02) holds, with the same import set 076 already had.

### 3. The corrected `Recipe` type — all four seams are factories

```go
// in internal/recipe — imports only internal/supervisor + stdlib

// SeamConfig is the narrow, leaf-defined accessor the seam factories receive at
// assembly time. runtime.Config satisfies it; the leaf names no runtime type.
type SeamConfig interface {
    TaskRoot() string
    PublishRemote() string
    GitToken() string
    GitHubToken() string
    GitCLI() string
    GitHubCLI() string
    Worktree() string
    // (extend only as a real seam needs a field — keep this surface minimal)
}

type GoalSourceFactory func(cfg SeamConfig) (supervisor.GoalSource, error)
type GateFactory       func() supervisor.Gate            // unchanged — see §4
type ResultSinkFactory func(cfg SeamConfig) (supervisor.ResultSink, error)

type Recipe struct {
    Name              string
    GoalSourceFactory GoalSourceFactory
    RoutingSpec       RoutingSpec        // ADR 043 — unchanged
    GateFactory       GateFactory
    ResultSinkFactory ResultSinkFactory
    BlockWiring       map[string]interface{}
}
```

`recipe.New(...)` and the nil-validation are updated to take the factory trio; the
`GateFactory`-non-nil panic (TC-076-01) is retained and a non-nil
`GoalSourceFactory`/`ResultSinkFactory` check is added in the same spirit (a recipe with
no goal source or no result sink cannot do useful work — fail fast at construction).

### 4. `GateFactory` stays no-arg `func() supervisor.Gate`

The gate seam takes **no runtime config** in the coding-agent recipe — the production
gate (`newProductionGate()`) is constructed from compiled-in tool defaults, not from
`runtime.Config`. Keeping `GateFactory` no-arg is therefore correct: making it take
`SeamConfig` for uniformity alone would add an unused parameter to every gate factory
(against "explicit over implicit" — the signature would imply a config dependency that
does not exist). If a future recipe's gate needs config, it widens `GateFactory` to
`func(SeamConfig) supervisor.Gate` then, when there is a concrete second use case
(deferring premature decisions). The asymmetry is deliberate and documented here: gate
= no-arg because it has no runtime config; goal/sink = config-taking because they do.

### Invariants preserved

- **Leaf-purity of `internal/recipe` (TC-076-02).** Still imports only
  `internal/supervisor` + stdlib. All four seam types resolve through `supervisor`; the
  `SeamConfig` interface is leaf-defined; no concrete, no `runtime`, no registry import.
- **F-003 supervisor isolation.** `internal/supervisor` gains two interface
  declarations and two payload structs — pure type declarations, no executor/LLM/web
  import, no import of `internal/publisher` or `internal/tasksource`. The publisher
  adapter lives on the runtime/publisher side of the injection boundary, exactly where
  the concrete already sits. `make fitness-supervisor-isolation` must be re-run to
  confirm.
- **ADR 043 `RoutingSpec`.** Unchanged. The executor seam stays a `RoutingSpec` the
  `stubResolveExecutor(spec, config)` resolver maps to Claude; this ADR does not touch
  it.
- **Zero-drift (TC-077-02 / TC-077-07).** The corrected shape carries the *same*
  concretes with the *same* config the inline construction used today — the e2e
  Phase-0/Phase-1 acceptance tests must still pass with no test-file changes.

## Why this framing and not the alternatives

- **Not "keep instance fields and construct seams in `init()`."** That is the 076 shape
  that BLOCKED. The real seams need `config.TaskRoot` and the publish tokens, which do
  not exist at registration. Instance fields force either (i) a global config read at
  `init()` (implicit, untestable, against "explicit over implicit") or (ii) re-binding
  the recipe after config is known (a mutation the registry's "construct on demand"
  shape does not support). Factories take config at the one moment it exists — assembly
  time — which is exactly how the executor seam already works. Making all four uniform
  removes the special case rather than adding one.
- **Not factory-takes-`any` (option a).** It preserves leaf-purity but forfeits the
  compile-time type-safety that is the whole reason ADR 041 chose a Go-typed recipe over
  a parsed file. A `config.(runtime.Config)` assertion that panics at runtime is the
  failure mode the Go-typed recipe exists to avoid. The narrow accessor interface gets
  the same leaf-purity *with* compile-time checking.
- **Not config-via-`BlockWiring` (option c).** Stringly-typed keys in an opaque map are
  the loosest binding available; nothing checks at compile time that `"task_root"` is
  present and a string. `BlockWiring` is for the purpose-neutral block knobs it was
  named for; conflating per-deployment IO config into it muddies both.
- **Not moving the concretes into `internal/supervisor`.** That would drag
  `tasksource` and `publisher` (and the publisher's `os/exec`, `gh` shelling) into the
  supervisor's import graph — a direct F-003 violation. Mirroring only the *interface*
  (as `Executor`/`Gate` already are) keeps the concrete on the executor/publisher side
  of the injection boundary and the supervisor free of LLM/IO concretes.

## Consequences

This ADR is **design-only**; ADR 041's seam shape is amended in the header. The
spec stays present-tense describing the coding agent — the recipe surface enters
`docs/spec/` only when the full recipe path ships, per ADR 040/041. Below is the
precise rework an executor performs **on the task 077 branch** (076 is already merged;
these changes land on 077, and the 076 `coverage-tracker.md` row keeps its ✅ — the
076 functionality is preserved, only the type evolves).

### (a) `internal/recipe` — Recipe type + factory types

- Replace the `GoalSource`/`ResultSink` **instance fields** with
  `GoalSourceFactory`/`ResultSinkFactory` **factory fields** (signatures in §3).
- Delete the leaf-local `GoalSource`/`ResultSink` *interface* declarations (lines ~45–60
  of `recipe.go`) — they move to `internal/supervisor` (step b).
- Add the leaf-defined `SeamConfig` accessor interface (§3).
- Keep `GateFactory func() supervisor.Gate` as-is (§4).
- Update `recipe.New(...)` to take the factory trio; retain the nil-`GateFactory` panic
  (TC-076-01) and add nil-`GoalSourceFactory`/nil-`ResultSinkFactory` panics with the
  same loud message style.
- `RoutingSpec`, `Sensitivity`, `Register`, `SelectRecipe`, `ListRecipes`, and the
  `Name`-stamping are untouched.

### (b) Seam-interface relocation — `internal/supervisor`

- Add `GoalSource` (`Next() (Task, bool, error)`) — `*tasksource.Source` satisfies it
  as-is.
- Add `ResultSink` (`Publish(ctx, PublishRequest) (PublishResult, error)`) plus the
  mirrored `PublishRequest`/`PublishResult` payload structs (fields mirror
  `publisher.Request`/`publisher.Result`: `Task`, `Worktree`, `Branch`, `Remote` /
  `Branch`, `PRURL`, `PRID`). No import of `internal/publisher` or `internal/tasksource`
  — pure type declarations only, so F-003 holds.
- Add a thin adapter (in `internal/runtime`, or `internal/publisher` since it already
  imports `supervisor`) wrapping `*publisher.GitHubCLI` to satisfy
  `supervisor.ResultSink` (translate `supervisor.PublishRequest` ↔ `publisher.Request`).

### (c) `internal/recipe` test updates

- `recipe_test.go` constructs recipes with the **old instance fields** and fakes
  (`TestFakeGoalSource.FetchGoal`, `TestFakeResultSink.WriteResult`). These must change:
  the fakes implement the new `supervisor.GoalSource.Next` / `supervisor.ResultSink.Publish`,
  and the test recipes are built from factory funcs returning those fakes.
- Update the `recipe.New` call sites in tests to the new factory-trio signature.
- Keep TC-076-01 (nil `GateFactory` panics); add coverage that nil
  `GoalSourceFactory`/`ResultSinkFactory` panic.
- Keep TC-076-02 (leaf-purity) — the import set is unchanged, so the existing assertion
  should still pass; re-run it.

### (d) `internal/runtime` — 077 wiring

- Register `"coding-agent"` with factory funcs (not instances): the
  `GoalSourceFactory` calls `tasksource.New(os.DirFS(cfg.TaskRoot()), DefaultRoadmapPath,
  DefaultTaskDirs...)`; the `ResultSinkFactory` calls `publisher.NewGitHubCLI(...)` from
  `cfg.PublishRemote()`/tokens/CLI paths and wraps it in the adapter; the `GateFactory`
  calls `newProductionGate()`; the recipe carries `RoutingSpec{MinCapability: 1}`.
- Add the `SeamConfig` accessor methods to `runtime.Config` (`TaskRoot()`,
  `PublishRemote()`, `GitToken()`, `GitHubToken()`, `GitCLI()`, `GitHubCLI()`,
  `Worktree()`) — one-line getters over the existing fields.
- Refactor `Run` so it builds **all four** seams from the recipe's factories
  (`recipe.SelectRecipe(config.RecipeName)` → call `r.GoalSourceFactory(config)`,
  `r.GateFactory()`, `r.ResultSinkFactory(config)`, and `stubResolveExecutor(r.RoutingSpec,
  config)`). **No inline `tasksource.New(...)` / `branchpub.NewGitHubCLI(...)` /
  `newProductionGate()` survive in the `Run` body** (TC-077-03). The
  `stubResolveExecutor` for `RoutingSpec` stays (its `// task 095` comment intact,
  TC-077-07). Note: the status-writer construction (`tasksource.NewStatusWriter`) is a
  separate concern from the goal-source seam; 077's scope is the four IO seams — leave
  the status-writer wiring as-is unless 077's spec says otherwise.
- `internal/runtime` continues to import `tasksource`/`executor`/`gate`/`publisher`
  (TC-077-03 rationale: the change is the construction *site*, not the import set).

### What becomes harder

- One more indirection: a seam is now `recipe → factory → concrete(cfg)` rather than an
  inline constructor. Reading a dispatch means reading the recipe's factories first.
- `runtime.Config` carries a small accessor-method surface (the `SeamConfig`
  satisfaction) in addition to its fields — two ways to read the same data, kept in sync.
- `supervisor.PublishRequest`/`PublishResult` mirror `publisher.Request`/`Result`; a new
  publisher field must be added in both places (or the adapter updated). This is the
  accepted cost of keeping the concrete out of the supervisor's import graph for F-003.

These costs are accepted in exchange for: a uniform config-taking factory across all
four IO seams, compile-time type-safety on the config the seams read, leaf-purity of
`internal/recipe` preserved, and the real coding-agent seams finally bindable through
the recipe — unblocking task 077.
