# Architecture — C4 Element Catalog

**Project:** agent-builder
**Last updated:** 2026-06-05

The structured catalog of architectural elements that the diagrams in [`../architecture/diagrams.md`](../architecture/diagrams.md) render. Tables here are the **machine-readable spec** for the system's structure — they survive a Mermaid rewrite and are what a drift audit checks the code against.

## How this file relates to the diagrams

| File | Form | Use when |
|------|------|----------|
| [`../architecture/diagrams.md`](../architecture/diagrams.md) | Visual (Mermaid C4 + sequence) | You want to *see* the structure |
| `architecture.md` (this file) | Tabular (rows + columns) | You want to *check, query, or regenerate* the structure |

When the structure changes, both files update in the same commit. The tables here are the source of truth for *what exists*; the diagrams are the source of truth for *how it's drawn*. If you can't reconcile a diagram to a row in this file, one of them is wrong — fix it before the change ships.

---

## 1. Persons (actors)

> Who uses the system. Includes humans (end users, operators, admins) and external automated systems acting as clients. One row per distinct role.

| Name | Description | Goals |
|------|-------------|-------|
| | | |

---

## 2. Systems

> The system itself, plus every external system it integrates with. The "system in scope" gets one row; each integration gets its own.

| Name | Type | Description | Owner |
|------|------|-------------|-------|
| agent-builder | In-scope | | This team |
| Claude Code CLI | External CLI | Cloud executor harness/model subprocess invoked against a task worktree | Anthropic |
| @anthropic-ai/sandbox-runtime | External CLI | Rented bootstrap isolation runtime invoked through `srt --settings` behind the exec-sandbox run adapter seam | Anthropic experimental |
| armor | External CLI/service | LLM guard invoked behind the ingestion boundary to classify content and tool-call candidates | External tool |
| code-scanner | External CLI | Malware/backdoor/credential-harvest scanner invoked as a blocking gate step | Tooling environment |

---

## 3. Containers

> Independently deployable / runnable units inside the system: services, processes, databases, queues, scheduled jobs. Each container has a technology choice and a single responsibility.

| Name | Technology | Responsibility | Source path | Depends on |
|------|------------|----------------|-------------|------------|
| agent-builder CLI | Go | Entrypoint process for the autonomous builder scaffold | `cmd/agent-builder` | |
| execution-box profile | Rootless Podman / OCI image with selectable OCI runtime | Product containment artifact for running one target repo worktree with read-only rootfs, scratch tmpfs, non-root execution, dropped workload capabilities, resource quotas, default-deny egress, and workload-tier runtime defaults (`agent` -> `runsc`, `dev` -> `runc`) | `containment/execution-box` | |
| execution-box egress sidecar | Rootless Podman / nftables sidecar | Trusted per-run network filter that installs default-deny egress rules for the execution-box pod namespace before the workload starts | `containment/execution-box` | execution-box profile |

**Invariants for this table**
- Every container listed has a corresponding directory or deployable artifact under `src/` (or equivalent). The drift-audit mode of the `architect` agent checks this against the actual repo layout.
- Every `Depends on` entry must resolve to another row in this table (Container) or a row in Section 2 (Systems).

---

## 4. Components

> Modules / packages inside containers that are worth naming at the architecture level — typically the ones with stable interfaces between them. Not every file in the codebase belongs here; only the load-bearing ones.

| Container | Component | Source path | Responsibility | Depends on |
|-----------|-----------|-------------|----------------|------------|
| agent-builder CLI | Supervisor | `internal/supervisor` | Trusted outside-the-box dispatcher that creates one containment box, starts one in-box loop, streams run output to a durable run-record when configured, logs lifecycle events, and tears down deterministically | Verification Gate model; exec-sandbox Run Adapter |
| agent-builder CLI | Agent Loop | `internal/loop` | Drives one inside-the-box pick -> attempt -> verify cycle and applies the bounded retry/escalation policy around that policy-free outcome | Supervisor; Task Source; Task Status Writer; Verification Gate |
| agent-builder CLI | Ingestion Boundary | `internal/ingestion` | Defines typed web-content and tool-call candidates plus the guard/broker seam that releases only allowed candidates to the executor path | |
| agent-builder CLI | Armor Guard Adapter | `internal/armor` | Adapts an external armor-compatible process/service to the ingestion guard decision model without vendoring armor source | Ingestion Boundary; armor |
| agent-builder CLI | Executor Ingestion Harness | `internal/executorharness` | Converts executor-facing web-content and tool-call events into ingestion candidates, routes them through the broker, and exposes only broker-reviewed release values to continuations/executors; constructs armor-backed harness wiring when configured | Ingestion Boundary; Armor Guard Adapter |
| agent-builder CLI | Claude CLI Executor | `internal/executor` | Concrete `supervisor.Executor` adapter that invokes Claude Code CLI in a task worktree and captures the produced branch | Supervisor; Claude Code CLI |
| agent-builder CLI | exec-sandbox Run Adapter | `internal/sandbox` | Typed contained-command run seam plus deterministic fake backend | |
| agent-builder CLI | sandbox-runtime Adapter | `internal/sandbox/sandboxruntime` | Concrete bootstrap backend that generates sandbox-runtime settings from `sandbox.Request` and invokes `srt --settings` without changing callers of the task-020 seam | exec-sandbox Run Adapter; @anthropic-ai/sandbox-runtime |
| agent-builder CLI | Verification Gate | `internal/gate` | Runs ordered blocking verification Steps and returns structured Verdicts | code-scanner |
| agent-builder CLI | Task Source | `internal/tasksource` | Reads roadmap/task metadata and selects the next ready task without writing task state | Supervisor Task model |
| agent-builder CLI | Task Status Writer | `internal/tasksource` | Writes constrained task status markers such as `needs-human` without changing task prose | |

---

## 5. Cross-cutting decisions

> Architectural choices that span multiple containers or components and don't naturally fit in any single row above — auth approach, observability strategy, error-handling convention, retry policy, transaction boundaries. Each entry should link to an ADR.

- ADR 002: Gate orchestrator shape — ordered Step interface, structured Verdict model, first-failure short-circuit, no skip path.
- ADR 012: Agent loop state machine shape — explicit pick/attempt/verify/advance states, done/idle/fail outcomes, and policy-free failure reporting.
- ADR 013: Retry escalation policy — non-negative attempt bound, mandatory stop condition, needs-human status write, and substitutable escalation hook.
- ADR 014: Rootless Podman execution-box profile — product containment artifact under `containment/execution-box` with read-only rootfs, writable worktree and scratch only, non-root/drop-all-caps execution, no host home or container-engine socket mount, and explicit resource quotas.
- ADR 015: Default-deny execution-box egress allowlist — plain-text exact host:port allowlist, launcher-validated fail-closed parsing, sidecar-owned network administration, and nftables default-drop filtering before workload start.
- ADR 016: Tiered runtime selection seam — execution-box launcher maps workload tiers to Podman OCI runtimes (`agent` -> `runsc`, `dev` -> `runc`), exposes explicit `--runtime`, and records `runsc` Go-toolchain compatibility through the containment probe.
- ADR 020: exec-sandbox run adapter seam — typed command/worktree/limits request, result plus exit code plus error response, fake backend for tests.
- Task 021: sandbox-runtime backing adapter — concrete rented-isolation backend behind ADR 020 that invokes `srt --settings`, maps egress allowlist entries into sandbox-runtime `network.allowedDomains`, and preserves `sandbox.Runner` swap compatibility.
- ADR 024: armor ingestion and tool-call boundary — repo-owned in-box boundary for attacker-reachable content and tool-call candidates, with fail-closed guard decisions before executor release.
- Task 025: armor guard adapter — external process/service invocation seam maps armor allow/findings/failure output into ingestion allow/block/quarantine decisions without editing armor source.
- Task 017: Supervisor dispatch lifecycle — one task per `Run()`, create -> run-inside -> teardown ordering, teardown-on-error, and recovered-panic teardown.
- Task 019: RunRecord collection — host-side NDJSON run record captures command/stdout/stderr stream events during `RunInside`, writes terminal outcomes, and closes before teardown.

---

## Maintenance

- **Update in the same commit as `../architecture/diagrams.md`** when the structure changes. The catalog and the diagrams are two views of the same model — they drift together or not at all.
- **Supersede in place. Never append.** When a container is renamed or a dependency edge moves, rewrite the row. The ADR carries the history of *why* something changed; this file carries *what* exists now.
- **Don't list every file.** Components in Section 4 are the load-bearing modules with stable interfaces. If listing a component does not change how someone reasons about the system, leave it out.
- The drift-audit mode of the `architect` agent uses this catalog as its primary check against the import graph and the deployable artifact list. Run it periodically — drift accumulates silently.
