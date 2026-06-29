# Test spec — Task 110: wire `AGENT_BUILDER_PLANNER=llm` into `orchestrate`

**Linked task:** `docs/tasks/backlog/110-wire-llm-planner-orchestrate.md`
**Written:** 2026-06-28
**Status:** ready
**Governing ADRs:** ADR 053 §3 (the orchestrate CLI wires the two planner seams from
existing pieces — catalog, `ExecutorResolver` over `router.Select`, `Invoker` over
`CompleterForEntry`). ADR 046 §6 (`*router.Router` satisfies `ExecutorResolver` only at the
wiring layer). ADR 043 (registry + router).

## Context

Task 100 shipped the `LLMPlanner` and `planner.NewPlannerFromEnv(resolver, invoke)`, but the
orchestrate CLI cannot select it: `internal/cli/orchestrate.go`'s `plannerFromEnv()` returns
`ErrPlannerNotAvailable` for `AGENT_BUILDER_PLANNER=llm` (the deliberate placeholder left by
task 099, "pending task 100"). Task 109 supplied the production single-shot backing
(`executor.CompleterForEntry` + the ollama completer). This task closes the loop: assemble
the two planner seams in `internal/cli` and remove the placeholder so `=llm` is live.

### The two seams to assemble (ADR 053 §3)

- **`Invoker`** = a closure over task 109's `executor.CompleterForEntry`:
  ```go
  invoke := func(ctx context.Context, entry registry.RegistryEntry, prompt string) (string, error) {
      c, err := executor.CompleterForEntry(entry)
      if err != nil {
          return "", err
      }
      return c.Complete(ctx, entry, prompt)
  }
  ```
  This is constructed in `internal/cli` (where the `internal/executor` import already lives),
  keeping F-010/F-014 green — the planner package never imports executor.

- **`ExecutorResolver`** = a thin adapter over `*router.Router.Select`. The planner's
  `Resolve(ctx, spec)` drops the context and calls `r.Select(spec)` — `Select` takes **no
  `ctx`** and returns `(registry.RegistryEntry, error)` directly, so the adapter discards the
  planner's ctx. **This is a documented, deliberate limitation:** the router's selection is
  not context-cancellable today; the adapter comment must say so, and the dropped ctx must
  not be silently swallowed in a way that hides a future cancellation contract.

- **Catalog** — `internal/runtime`'s `buildCatalog` is an **unexported package-level `var`**,
  so the CLI cannot call it. ADR 053 §3 leaves the choice to this task:
  - (a) lift the catalog-build logic into an exported helper shared by `internal/runtime` and
    `internal/cli`, or
  - (b) have the CLI build its own `*registry.Catalog` via `registry.LoadFromEnv()` + the
    synthetic-default fallback (the lighter touch; leaves `internal/runtime` unchanged).

  **This task must make the choice explicit in the task body and the verify commit.** Whichever
  path is chosen, `internal/runtime` and `internal/cli` must NOT silently diverge in how they
  synthesize the default entry (ADR 053 Consequences). The router is `router.New(catalog)` and
  the resolver wraps its `Select`.

### `plannerFromEnv()` stops failing closed for `"llm"`

`plannerFromEnv()` (or its replacement) constructs `planner.NewPlannerFromEnv(resolver,
invoke)` for the `"llm"` case instead of returning `ErrPlannerNotAvailable`. `"structured"`
(and unset) still returns the `StructuredPlanner`; an **unknown** value still returns an error
that drives `ExitUsage` — that fail-fast behavior is unchanged.

### Docs reconciliation

`docs/spec/configuration.md` already (prematurely, in task 100) describes `llm` as live, but
the `orchestrate` **usage string** in `orchestrate.go` still prints `"llm" (pending task 100)`
and `EnvPlanner`'s doc comment + `ErrPlannerNotAvailable` still say "pending task 100". This
task removes those stale "pending"/"placeholder" notes from the code and confirms
`configuration.md` matches the now-live behavior (ollama-backed; cloud entries fail closed per
task 109).

## Requirements coverage

| Req ID      | Description                                                                                                                       | Test cases               |
|-------------|----------------------------------------------------------------------------------------------------------------------------------|--------------------------|
| REQ-110-01  | `AGENT_BUILDER_PLANNER=llm` assembles an `*planner.LLMPlanner` (no `ErrPlannerNotAvailable`); `=structured`/unset → StructuredPlanner | TC-110-01            |
| REQ-110-02  | An unknown `AGENT_BUILDER_PLANNER` value still returns an error driving `ExitUsage` (unchanged fail-fast)                          | TC-110-02                |
| REQ-110-03  | The `ExecutorResolver` adapter wraps `*router.Router.Select` (drops ctx, documented); `Resolve` returns the router-selected entry | TC-110-03                |
| REQ-110-04  | The `Invoker` closure routes through `executor.CompleterForEntry`; an ollama entry resolves a completer, a cloud entry fails closed | TC-110-04             |
| REQ-110-05  | F-010 and F-014 stay green (the wiring adds no direct executor import to orchestrator/planner); existing `run`/`orchestrate` paths unbroken | TC-110-05         |
| REQ-110-06  | Stale "pending task 100" / placeholder notes removed from the usage string + `EnvPlanner` doc; `configuration.md` matches live behavior | TC-110-06            |

---

## Test cases

### TC-110-01 — `=llm` assembles an `*planner.LLMPlanner` (L2)

- **Requirement:** REQ-110-01
- **Level:** L2 (unit on the planner-selection assembler in `internal/cli`)

**Input A — `AGENT_BUILDER_PLANNER=llm`:** Set the env var to `"llm"`; call the CLI's
planner-construction path (the function that replaces `plannerFromEnv`, given the assembled
resolver + invoker). The router catalog is built from a minimal in-test registry (one
`local-ollama` entry, or the synthetic default).

**Expected output:**
- Returns a `nil` error.
- The returned `orchestrator.Planner`'s dynamic type is `*planner.LLMPlanner` (type assertion
  succeeds), NOT `*orchestrator.StructuredPlanner`.
- The returned error is NOT `ErrPlannerNotAvailable` (assert `!errors.Is(err,
  ErrPlannerNotAvailable)` — and, ideally, that the symbol is gone or no longer returned on
  this path).

**Input B — unset / `"structured"`:** Env var unset (and separately `"structured"`).

**Expected output:**
- Returns `*orchestrator.StructuredPlanner`, `nil` error, in both cases.

---

### TC-110-02 — unknown value still drives `ExitUsage` (L2)

- **Requirement:** REQ-110-02
- **Level:** L2 (unit)

**Input:** `AGENT_BUILDER_PLANNER=magic`.

**Expected output:**
- The planner-selection path returns a non-nil error (unknown planner type).
- The error message names the bad value (`"magic"`) and lists the valid values
  (`structured`, `llm`).
- When driven through the `orchestrate` subcommand entrypoint, this error results in the
  `ExitUsage` exit code (the existing usage-error contract is preserved — assert the returned
  exit code equals `ExitUsage`).

---

### TC-110-03 — `ExecutorResolver` adapter wraps `router.Select` and drops ctx (L2)

- **Requirement:** REQ-110-03
- **Level:** L2 (unit)

**Input:** Build a `*registry.Catalog` containing one eligible entry (e.g. `local-ollama`,
`HarnessOllamaNative`, `CapabilityTier 1`). Construct `router.New(catalog)`. Wrap it in the
CLI's resolver adapter. Call `resolver.Resolve(context.Background(), router.RoutingSpec{MinCapability: 1})`.

**Expected output:**
- Returns the `local-ollama` `registry.RegistryEntry` (the entry `router.Select` would
  return for that spec) and a `nil` error — i.e. `Resolve` delegates to `Select` and returns
  its result unchanged.
- With a `RoutingSpec{MinCapability: 99}` (no eligible entry), `Resolve` returns the router's
  `ErrNoEligibleExecutor` (wrapped or via `errors.Is`) — the adapter does not mask routing
  errors.
- A cancelled `ctx` passed to `Resolve` does NOT change the result (the entry is still
  returned) — confirming the adapter drops the ctx as documented (the router does not honor
  it). This is asserted as a *documented limitation*, with a code comment, not as desired
  behavior.

---

### TC-110-04 — `Invoker` closure routes through `CompleterForEntry` (L2)

- **Requirement:** REQ-110-04
- **Level:** L2 (unit)

**Input A — ollama entry:** Build the `Invoker` closure as wired in the CLI. Call it with
`registry.RegistryEntry{Harness: registry.HarnessOllamaNative, Endpoint: "http://localhost:11434", ModelID: "qwen3:8b"}`
and a prompt, using a test seam / stub that lets the underlying `Complete` be exercised
without a live model **OR** (if the closure is hard-wired to the real `CompleterForEntry`)
assert that the closure returns the same `(string, error)` shape and that no error is raised
at completer-construction time for the ollama harness.

**Expected output:**
- For the ollama entry, the closure obtains a non-nil completer (no
  `ErrSingleShotUnsupported`); the call shape is `(string, error)`.

**Input B — cloud entry (fail-closed propagation):** Call the closure with
`registry.RegistryEntry{Harness: registry.HarnessClaudeCLI}`.

**Expected output:**
- The closure returns `("", err)` with `errors.Is(err, executor.ErrSingleShotUnsupported)`
  `== true` — task 109's fail-closed error is propagated through the CLI `Invoker` to the
  planner unchanged. The planner's `Plan` will then fail closed (it wraps invoker errors per
  task 100 TC-100-03 sub-case D), so a cloud-only registry halts decomposition with a clear
  error rather than producing a degenerate plan.

---

### TC-110-05 — F-010/F-014 stay green; `run`/`orchestrate` paths unbroken (L2 + L3)

- **Requirement:** REQ-110-05
- **Level:** L2 (existing suites) + L3 (fitness)

**L3 assertions:**
```
make fitness-orchestrator-no-executor
make fitness-llm-planner-no-executor
```
Expected: `PASS F-010 …` and `PASS F-014 …`. The new wiring lives in `internal/cli` (which
already imports `internal/executor` transitively via `internal/runtime`); no direct
`internal/executor` import is added to `internal/orchestrator` or `internal/orchestrator/planner`.

**L2 assertions:**
```
go test -count=1 ./internal/cli/... ./internal/orchestrator/... ./internal/orchestrator/planner/... ./tests/e2e/...
```
Expected: `ok` for each. The existing `run`-path e2e and the task-099 orchestrate assembly
tests still pass (SEC-003 startup key check, shared ReplayCache, policy fail-closed all
unchanged — this task only swaps the planner construction).

---

### TC-110-06 — stale "pending" notes removed; config doc matches live (L2 + doc review)

- **Requirement:** REQ-110-06
- **Level:** L2 (grep-style assertions on the usage output / source) + documentation review

**Input/assertions:**
- The `orchestrate` usage string (`orchestrateUsage`) no longer contains the substring
  `"pending task 100"`; the `AGENT_BUILDER_PLANNER` line describes `llm` as a live value
  (ollama-backed; cloud harnesses fail closed).
- The `EnvPlanner` doc comment and the `ErrPlannerNotAvailable` usage no longer say "pending
  task 100"/"reserved for task 100" for the `llm` case. If `ErrPlannerNotAvailable` becomes
  unused, it is removed (no dead exported symbol) or repurposed with an accurate comment.
- `docs/spec/configuration.md`'s `AGENT_BUILDER_PLANNER` row reflects that `llm` is live and
  ollama-only (cloud entries fail closed via task 109), consistent with task 109's dispatcher.
- `agent-builder orchestrate -h` output (captured in test or operator run) shows the updated
  planner line.

---

## Verification plan

- **Highest level achievable: L6** — run `agent-builder orchestrate` with
  `AGENT_BUILDER_PLANNER=llm` against a local ollama model (`qwen3:8b`) and a free-form goal,
  observing the model decompose it into sub-goals on the live binary. L2 (assembly unit tests)
  + L3 (F-010/F-014 fitness) are the CI-automatable ceiling; L6 needs the operator's ollama.
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
  registry entry (`http://localhost:11434`, `qwen3:8b`), the SEC-003 worker signing key, and a
  free-form goal via `AGENT_BUILDER_GOAL_SPEC`; run `agent-builder orchestrate`; observe the
  rendered plan with ≥1 decomposed sub-goal sourced from the model (not the rule-based
  StructuredPlanner). Record the model, the goal, and the sub-goal count/lines in the verify
  commit. A cloud-only registry must instead surface the task-109 fail-closed error — capture
  that as the negative confirmation.

## Out of scope

- Building the `Completer` seam / ollama completer (task 109 — this task consumes it).
- Cloud print-mode completers (deferred by ADR 053).
- Making `router.Select` context-cancellable (the dropped-ctx limitation is documented, not
  fixed here).
- The decomposition prompt quality / model evaluation (task 094).
- Any change to the `orchestrator.Planner` interface or `internal/orchestrator`.
- The SEC-001 keypair-error fix (task 111 — independent).
```
