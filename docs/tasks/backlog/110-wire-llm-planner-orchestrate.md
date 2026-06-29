# Task 110: wire `AGENT_BUILDER_PLANNER=llm` into `orchestrate`

**Project:** agent-builder
**Created:** 2026-06-28
**Status:** backlog

## Goal

Make `AGENT_BUILDER_PLANNER=llm` selectable on the live `orchestrate` path: in
`internal/cli/orchestrate.go`, build the router catalog, wrap `*router.Router.Select` as the
planner's `ExecutorResolver`, close over task 109's `executor.CompleterForEntry` as the
`Invoker`, construct `planner.NewPlannerFromEnv(resolver, invoke)`, and remove the
`ErrPlannerNotAvailable` placeholder for the `"llm"` case. Reconcile the now-stale "pending
task 100" notes in the usage string and `docs/spec/configuration.md`.

## Context

ADR 053 (the authoritative design) §3 specifies how the orchestrate CLI assembles the two
planner seams from existing pieces. Today `plannerFromEnv()` in `internal/cli/orchestrate.go`
returns `ErrPlannerNotAvailable` for `"llm"` — the deliberate placeholder left by task 099
("pending task 100"). Task 100 shipped the `LLMPlanner` + `planner.NewPlannerFromEnv`; task
109 shipped the production single-shot backing (`executor.CompleterForEntry` + the ollama
completer). This task is the wiring that turns `=llm` live.

### The three pieces to assemble (ADR 053 §3)

- **`Invoker`** = a closure over `executor.CompleterForEntry` (task 109), constructed in
  `internal/cli` where the `internal/executor` import already lives (transitively, via
  `internal/runtime`), keeping F-010/F-014 green:
  ```go
  invoke := func(ctx context.Context, entry registry.RegistryEntry, prompt string) (string, error) {
      c, err := executor.CompleterForEntry(entry)
      if err != nil { return "", err }
      return c.Complete(ctx, entry, prompt)
  }
  ```

- **`ExecutorResolver`** = a thin adapter over `*router.Router.Select`. The planner's
  `Resolve(ctx, spec)` calls `r.Select(spec)` — `Select` takes **no `ctx`** and returns
  `(registry.RegistryEntry, error)` directly, so the adapter **drops the planner's ctx**. This
  is a deliberate, documented limitation (the router's selection is not context-cancellable
  today); the adapter must carry a comment saying so. The shapes otherwise align — no
  conversion beyond discarding the ctx.

- **Catalog** — `internal/runtime`'s `buildCatalog` is an **unexported package-level `var`**,
  so the CLI cannot call it. **This task must decide and document, in the task body and the
  feat commit, which option it takes:**
  - **(a)** lift the catalog-build logic into an **exported helper** shared by
    `internal/runtime` and `internal/cli` (removes duplication, touches `internal/runtime`), or
  - **(b)** have the CLI **build its own** `*registry.Catalog` via `registry.LoadFromEnv()` +
    the synthetic-default fallback (lighter touch; leaves `internal/runtime` unchanged).

  **Recommended default: (b)** per ADR 053 §3 ("the lighter touch and keeps `internal/runtime`
  unchanged") — but the implementer picks based on how much duplication (a) would actually
  remove. Whichever is chosen, `internal/runtime` and `internal/cli` must NOT silently diverge
  in how they synthesize the default entry (ADR 053 Consequences); if (b), add a regression
  test or shared constant guarding the default-entry shape. The router is `router.New(catalog)`.

### Remove the placeholder and reconcile docs

`plannerFromEnv()` (or its replacement) constructs `planner.NewPlannerFromEnv(resolver,
invoke)` for `"llm"` instead of returning `ErrPlannerNotAvailable`. `"structured"`/unset still
returns `StructuredPlanner`; an unknown value still returns an error that drives `ExitUsage`
(unchanged). The `orchestrate` usage string and the `EnvPlanner` doc comment must drop their
`"pending task 100"` notes; if `ErrPlannerNotAvailable` becomes unused, remove it (no dead
exported symbol). `docs/spec/configuration.md`'s `AGENT_BUILDER_PLANNER` row already (from task
100) describes `llm` as live — confirm it matches the now-live, ollama-only behavior (cloud
entries fail closed via task 109).

### Why F-010/F-014 stay green (ADR 053 §"Why F-010 and F-014 stay green")

The resolver adapter and the `Invoker` closure are constructed in `internal/cli`, the blessed
wiring layer. `internal/orchestrator` and `internal/orchestrator/planner` gain no direct
import of `internal/executor` — they still see only the `Invoker` func type and
`ExecutorResolver` interface. No new edge is added to their direct-import sets.

## Requirements

| Req ID      | Description                                                                                                                       | Priority   |
|-------------|----------------------------------------------------------------------------------------------------------------------------------|------------|
| REQ-110-01  | `AGENT_BUILDER_PLANNER=llm` assembles an `*planner.LLMPlanner` (no `ErrPlannerNotAvailable`); `structured`/unset → StructuredPlanner | must have |
| REQ-110-02  | An unknown `AGENT_BUILDER_PLANNER` value still returns an error driving `ExitUsage`                                               | must have  |
| REQ-110-03  | An `ExecutorResolver` adapter wraps `*router.Router.Select` (drops + documents ctx); `Resolve` returns the router-selected entry  | must have  |
| REQ-110-04  | The `Invoker` closure routes through `executor.CompleterForEntry`; ollama resolves a completer, cloud propagates the fail-closed error | must have |
| REQ-110-05  | F-010 + F-014 stay green; existing `run`/`orchestrate` (task 099) paths unbroken                                                  | must have  |
| REQ-110-06  | Stale "pending task 100"/placeholder notes removed from usage + `EnvPlanner` doc; `configuration.md` matches live behavior        | must have  |

## Readiness gate

- [ ] Task 109 merged (`executor.Completer`, ollama completer, `CompleterForEntry` dispatcher + `ErrSingleShotUnsupported`)
- [x] Task 100 merged (`LLMPlanner` + `planner.NewPlannerFromEnv(resolver, invoke)`)
- [x] Task 099 merged (`orchestrate` subcommand + `plannerFromEnv` + `EnvPlanner` constant + `ExitUsage` contract)
- [x] Task 092/095 merged (router + `router.New` + `router.Router.Select` + `RoutingSpec`)
- [x] ADR 053 §3 read and the catalog-build option chosen + documented in this task's feat commit

## Acceptance criteria

- [ ] [REQ-110-01] TC-110-01: `=llm` → `*planner.LLMPlanner`, `nil` err, `!errors.Is(err, ErrPlannerNotAvailable)`; unset/`structured` → `*orchestrator.StructuredPlanner`
- [ ] [REQ-110-02] TC-110-02: `=magic` → error naming the value + valid options; entrypoint returns `ExitUsage`
- [ ] [REQ-110-03] TC-110-03: resolver `Resolve(ctx, spec)` returns the entry `router.Select` returns; no-eligible → `ErrNoEligibleExecutor`; cancelled ctx does not change result (documented dropped-ctx)
- [ ] [REQ-110-04] TC-110-04: ollama entry → non-nil completer via closure; cloud entry → `errors.Is(err, executor.ErrSingleShotUnsupported)` propagated through the `Invoker`
- [ ] [REQ-110-05] TC-110-05: `make fitness-orchestrator-no-executor`/`-llm-planner-no-executor` → PASS; `go test ./internal/cli/... ./internal/orchestrator/... ./internal/orchestrator/planner/... ./tests/e2e/...` → `ok`
- [ ] [REQ-110-06] TC-110-06: usage string has no "pending task 100"; `EnvPlanner` doc updated; `ErrPlannerNotAvailable` removed-if-unused; `configuration.md` matches live

## Verification plan

- **Highest level achievable: L6** — run `agent-builder orchestrate` with
  `AGENT_BUILDER_PLANNER=llm` against a local ollama model (`qwen3:8b`, same as task 108's L6)
  and a free-form goal, observing a real decomposed plan on the live binary. L2 (assembly unit
  tests) + L3 (F-010/F-014 fitness, re-run and shown green in this task per ADR 053 §"Task
  110") are the CI-automatable ceiling.
- **L2 harness commands:**
  ```
  go test -count=1 ./internal/cli/... ./internal/orchestrator/... ./internal/orchestrator/planner/... ./tests/e2e/...
  ```
  Expected: `ok` each.
- **L3 fitness commands:**
  ```
  make fitness-orchestrator-no-executor
  make fitness-llm-planner-no-executor
  make check
  ```
  Expected: `PASS F-010 …`; `PASS F-014 …`; `All checks passed.`
- **L6 (operator-run, dev host):** export `AGENT_BUILDER_PLANNER=llm`, a `local-ollama`
  registry entry, the SEC-003 worker signing key, and `AGENT_BUILDER_GOAL_SPEC=<free-form
  goal>`; run `agent-builder orchestrate`; observe the rendered plan with ≥1 model-sourced
  sub-goal (distinguishable from the StructuredPlanner's rule-based split). A cloud-only
  registry must surface the task-109 fail-closed error — capture that as the negative
  confirmation. Record model, goal, and sub-goal lines in the verify commit.

## Modules touched

- `internal/cli` (the `orchestrate.go` planner assembly — catalog, resolver adapter, invoker
  closure, `plannerFromEnv` replacement, usage string; and `orchestrate_seams.go` only if the
  resolver/invoker helpers land there).
- `docs/spec/configuration.md` (confirm/adjust the `AGENT_BUILDER_PLANNER` row to the live
  ollama-only behavior).
- *(If catalog-build option (a) is chosen)* `internal/runtime` (export the shared catalog
  helper) — this would make the task touch two code modules (`internal/cli` + `internal/runtime`);
  option (b) keeps it to one code module + the spec doc. **Prefer (b)** to stay within the
  at-most-two-modules rule with margin.

## Out of scope

- Building the `Completer` seam / ollama completer (task 109).
- Cloud print-mode completers (deferred by ADR 053).
- Making `router.Select` context-cancellable (the dropped-ctx limitation is documented, not
  fixed).
- The decomposition prompt quality / model evaluation (task 094).
- Any change to the `orchestrator.Planner` interface or `internal/orchestrator`.
- The SEC-001 keypair-error fix (task 111 — independent, no dependency either way).

## Dependencies

- **Task 109 (single-shot `Completer` seam + ollama completer + `CompleterForEntry`) — HARD
  dependency.** This task closes over `executor.CompleterForEntry` and propagates
  `executor.ErrSingleShotUnsupported`; both come from 109. **109 → 110.**
- Task 100 (LLMPlanner + `NewPlannerFromEnv`) — merged.
- Task 099 (orchestrate wiring + `EnvPlanner` + `ExitUsage`) — merged.
- Task 092/095 (router) — merged.
```
