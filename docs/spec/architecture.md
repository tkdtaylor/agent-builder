# Architecture — C4 Element Catalog

**Project:** agent-builder
**Last updated:** 2026-06-20

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
| rootless Podman | External runtime | Execution-box containment substrate invoked through `containment/execution-box/run.sh` behind the exec-sandbox run adapter seam; selectable OCI runtime (`runsc`/`runc`) | Tooling environment |
| armor | External CLI/service | LLM guard invoked behind the ingestion boundary to classify content and tool-call candidates | External tool |
| code-scanner | External CLI | Malware/backdoor/credential-harvest scanner invoked as a blocking gate step | Tooling environment |
| git | External CLI | Version-control CLI used to push verified executor branches to the configured remote | Tooling environment |
| GitHub CLI | External CLI | `gh pr` CLI used to look up or create PR artifacts for verified branches | GitHub |
| audit-trail block | External CLI | Hash-chained, append-only forensic log block (`github.com/tkdtaylor/audit-trail`); invoked as `audit-trail emit` and `audit-trail verify` CLI subprocesses by `audit.BlockSink`; owns the JSONL chain format, SHA-256 chaining, RFC 8785 canonicalization, genesis sentinel, and verifier — agent-builder owns only the typed-event→argv mapping. Governing ADR: 026. | tkdtaylor |
| exec-sandbox block | External CLI | Tiered-isolation contained-command runner block; invoked as a CLI subprocess by `internal/sandbox/execsandbox` behind the `sandbox.Runner` seam. The default run backend when `AGENT_BUILDER_EXEC_SANDBOX_BIN` is set; owns the box lifecycle, OCI-runtime tier selection, and secret injection at spawn. agent-builder owns only the typed `sandbox.Request`→argv mapping. Governing ADR: 035. | tkdtaylor |
| vault block | External CLI/daemon | Token-brokering block (`internal/vault` client); started as a Unix-socket daemon by `internal/runtime` when `AGENT_BUILDER_VAULT_BIN` is set, holds publication-token plaintext exactly once via `put`, and returns opaque handles via `resolve`. The block owns the newline-delimited-JSON socket protocol and the in-sandbox `inject` verb; agent-builder owns only the three-verb client. Governing ADR: 036. | tkdtaylor |
| policy-engine block | External CLI/daemon | AuthZEN authorization block (`internal/policy` client); started as a Unix-socket daemon (`policy-engine serve --socket --allow`) by `internal/runtime` when `AGENT_BUILDER_POLICY_BIN` is set, and queried with one `decide` call per run. The block owns the policy evaluation and obligation set; agent-builder owns only the fail-closed `decide` client and obligation application. Governing ADR: 038. | tkdtaylor |

---

## 3. Containers

> Independently deployable / runnable units inside the system: services, processes, databases, queues, scheduled jobs. Each container has a technology choice and a single responsibility.

| Name | Technology | Responsibility | Source path | Depends on |
|------|------------|----------------|-------------|------------|
| agent-builder CLI | Go | Entrypoint process for the autonomous builder scaffold | `cmd/agent-builder` | |
| execution-box profile | Rootless Podman / OCI image with selectable OCI runtime | Product containment artifact for running one target repo worktree with read-only rootfs, scratch tmpfs, non-root execution, dropped workload capabilities, resource quotas, default-deny egress, workload-tier runtime defaults (`agent` -> `runsc`, `dev` -> `runc`), and read-only mounted Gate scanner/linter tools on the in-box `PATH`. On the rootless egress (networked pod) path, the workload runtime resolves to `runc` regardless of the agent-tier default, because gVisor's gofer cannot join a rootless pod userns (ADR 030); non-networked paths keep the selected runtime unchanged. | `containment/execution-box` | |
| execution-box egress sidecar | Rootless Podman / nftables sidecar | Trusted per-run network filter that installs default-deny egress rules for the execution-box pod namespace before the workload starts. Egress allowlist entries are declared on the pod (infra container) so the pod resolves allowlisted destinations while the workload runs with `--dns none` (ADR 030). | `containment/execution-box` | execution-box profile |

**Invariants for this table**
- Every container listed has a corresponding directory or deployable artifact under `src/` (or equivalent). The drift-audit mode of the `architect` agent checks this against the actual repo layout.
- Every `Depends on` entry must resolve to another row in this table (Container) or a row in Section 2 (Systems).

---

## 4. Components

> Modules / packages inside containers that are worth naming at the architecture level — typically the ones with stable interfaces between them. Not every file in the codebase belongs here; only the load-bearing ones.

| Container | Component | Source path | Responsibility | Depends on |
|-----------|-----------|-------------|----------------|------------|
| agent-builder CLI | Supervisor | `internal/supervisor` | Trusted outside-the-box dispatcher that creates one containment box, starts one in-box loop, streams run output to a durable run-record when configured, exposes an optional typed `audit.Sink` to the loop and Seals it before teardown on success and failure, logs lifecycle events, and tears down deterministically | Verification Gate model; exec-sandbox Run Adapter; Audit Sink Seam |
| agent-builder CLI | Default Run Wiring | `internal/runtime` | Host-side CLI bootstrap that parses explicit run configuration, selects one ready task, composes the concrete run adapters (including the optional `audit.BlockSink` behind `AGENT_BUILDER_AUDIT_RECORD`, resolved fail-fast before dispatch), projects each action-class lifecycle event through the Sink alongside the raw run-record stream, and hands one configured task to the Supervisor | Supervisor; Task Source; Task Status Writer; Agent Loop; Claude CLI Executor; Verification Gate; Podman Adapter; Branch Publisher; Audit Sink Seam |
| agent-builder CLI | Agent Loop | `internal/loop` | Drives one inside-the-box pick -> attempt -> verify cycle and applies the bounded retry/escalation policy around that policy-free outcome | Supervisor; Task Source; Task Status Writer; Verification Gate |
| agent-builder CLI | Ingestion Boundary | `internal/ingestion` | Defines typed web-content and tool-call candidates plus the guard/broker seam that releases only allowed candidates to the executor path | |
| agent-builder CLI | Armor Guard Adapter | `internal/armor` | Adapts an external armor-compatible process/service to the ingestion guard decision model without vendoring armor source | Ingestion Boundary; armor |
| agent-builder CLI | Executor Ingestion Harness | `internal/executorharness` | Converts executor-facing web-content and tool-call events into ingestion candidates, routes them through the broker, and exposes only broker-reviewed release values to continuations/executors; constructs armor-backed harness wiring when configured | Ingestion Boundary; Armor Guard Adapter |
| agent-builder CLI | Claude CLI Executor | `internal/executor` | Concrete `supervisor.Executor` adapter that invokes Claude Code CLI in a task worktree, captures the produced branch, and declares fail-closed/reviewed policy for Claude-facing web/tool routes | Supervisor; Claude Code CLI; Executor Ingestion Harness |
| agent-builder CLI | Audit Sink Seam | `internal/audit` | Typed closed-enum `AuditAction` taxonomy, `AuditEvent` value type, `Sink` interface, event validation helper (`Validate`), in-process `FakeSink`, and `BlockSink` production CLI adapter; strict leaf package with no executor/LLM/web imports (enforced by F-005 fitness check, task 042) | audit-trail block |
| agent-builder CLI | exec-sandbox Run Adapter | `internal/sandbox` | Typed contained-command run seam plus deterministic fake backend | |
| agent-builder CLI | Podman Adapter | `internal/sandbox/podman` | Concrete run backend that invokes `containment/execution-box/run.sh` with the worktree and typed limits (egress allowlist, CPU/memory/PID quotas, wall-clock timeout) without changing callers of the task-020 seam | exec-sandbox Run Adapter; rootless Podman |
| agent-builder CLI | exec-sandbox Backend Adapter | `internal/sandbox/execsandbox` | Concrete run backend that wraps the shipped exec-sandbox block binary behind the same `sandbox.Runner` seam as the Podman Adapter (typed `sandbox.Request`→argv, result/exit/error out). `internal/runtime` constructs it as the default backend when `AGENT_BUILDER_EXEC_SANDBOX_BIN` is set, falling back to the Podman Adapter otherwise (ADR 035). The north-star milestone run backend. | exec-sandbox Run Adapter; exec-sandbox block |
| agent-builder CLI | Secret Source | `internal/secrets` | Stdlib-only leaf defining the `SecretSource` seam for provider/publication token retrieval, an env-backed default, and the `VaultSecretSource` (`vault_source.go`) that brokers publication tokens through vault and exposes opaque handles. The seam vault plugs into; one-way dependency `executor`/`runtime` → `secrets`. | Vault Client |
| agent-builder CLI | Vault Client | `internal/vault` | Stdlib-only leaf client and daemon-lifecycle manager for the vault block's newline-delimited-JSON Unix socket protocol (`ping`/`put`/`resolve`). Opt-in token brokering: `internal/runtime` starts the daemon and registers git/GitHub token plaintext exactly once when `AGENT_BUILDER_VAULT_BIN` is set, then retains only opaque handles passed through `sandbox.RunWiring` with `InjectionMode=proxy` (ADR 036). One-way dependency `secrets`/`runtime` → `vault`. | vault block |
| agent-builder CLI | Policy Gate | `internal/policy` | Host-side, out-of-process authorization gate (ADR 038). Stdlib-only leaf client (`PolicyClient`, fail-closed `Decide`) plus `PolicyDaemon` lifecycle (execs `policy-engine serve --socket --allow`, ping-waits, stops). `runtime.Run` calls `decide` **after** vault handle resolution and **before** `sandboxBox.Create`: `deny`/`require_approval` writes needs-human and the box never starts; `allow` applies the `tier_select` (→ `sandbox.Request.Tier`) and `vault_injection_floor` (→ raise-only `RunWiring.InjectionMode`) obligations. Opt-in via `AGENT_BUILDER_POLICY_BIN`; unset = no gate. | policy-engine block |
| agent-builder CLI | Verification Gate | `internal/gate` | Runs ordered blocking verification Steps and returns structured Verdicts | code-scanner |
| agent-builder CLI | Task Source | `internal/tasksource` | Reads roadmap/task metadata and selects the next ready task without writing task state | Supervisor Task model |
| agent-builder CLI | Task Status Writer | `internal/tasksource` | Writes constrained task status markers such as `needs-human` without changing task prose | |
| agent-builder CLI | Branch Publisher | `internal/publisher` | Pushes Gate-verified non-empty executor branches and records an existing or newly-created PR artifact with token redaction | Supervisor Task model; git; GitHub CLI |

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
- Task 035: Podman backing adapter — concrete repo-owned containment backend behind ADR 020 that invokes `containment/execution-box/run.sh`, maps typed `sandbox.Limits` (egress allowlist, CPU/memory/PID quotas, wall-clock timeout) onto launcher flags and `EXEC_BOX_*` env, and preserves `sandbox.Runner` swap compatibility.
- ADR 021: Podman default-run containment swap — `internal/runtime` constructs the Podman adapter (task 036) instead of the rented `@anthropic-ai/sandbox-runtime` (`srt`) backend, which is removed from the run pipeline and enforced absent by the `fitness-no-srt` check; the deprecated `AGENT_BUILDER_SANDBOX_RUNTIME` errors loudly when set, the launcher path is overridable via `AGENT_BUILDER_EXEC_BOX_LAUNCHER`, and the run record carries `containment=podman` evidence. The `internal/sandbox/sandboxruntime` package is retained out-of-graph for reference only.
- ADR 024: armor ingestion and tool-call boundary — repo-owned in-box boundary for attacker-reachable content and tool-call candidates, with fail-closed guard decisions before executor release.
- Task 025: armor guard adapter — external process/service invocation seam maps armor allow/findings/failure output into ingestion allow/block/quarantine decisions without editing armor source.
- Task 017: Supervisor dispatch lifecycle — one task per `Run()`, create -> run-inside -> teardown ordering, teardown-on-error, and recovered-panic teardown.
- Task 019: RunRecord collection — host-side NDJSON run record captures command/stdout/stderr stream events during `RunInside`, writes terminal outcomes, and closes before teardown.
- Task 028: Default run wiring — `agent-builder run` composes the concrete task source, Executor, Gate, containment-box adapter, retrying loop, timeout, and optional RunRecord path from explicit environment configuration while preserving supervisor isolation. (Containment backend swapped from the rented srt adapter to the Podman adapter by ADR 021 / task 036.)
- Task 037: Phase 1 end-to-end acceptance — fake-Podman end-to-end harness drives the real `agent-builder run` pipeline through Podman containment with zero `srt` invocation and a clean run record; accepted at fake-provider L5 (live `runsc` + provisioned Gate-toolchain L6 pending).
- Task 034: Branch and PR publication — default run wiring publishes only Gate-verified non-empty branches through the fakeable publisher seam and records PR artifact evidence without logging configured publication tokens.
- ADR 026 / Task 039: audit-trail block consumption — agent-builder reaches the shipped `audit-trail` block over a CLI subprocess seam (`audit.BlockSink`); the block owns the chain format, hash, and verifier; agent-builder owns only the typed `AuditEvent`→argv mapping. `context`/`refs` fields are deferred to the IPC-socket upgrade (ADR 026 Option B). `internal/audit` stays a strict stdlib-only leaf (no block Go import; enforced by F-005).
- ADR 038 / Task 072: policy-engine decide gate — `internal/runtime` starts the `policy-engine` block as a host-side out-of-process daemon (`policy-engine serve --socket <path> --allow <egress-csv>`, fed from `sandbox.Limits.EgressAllowlist`) and calls AuthZEN `decide` (subject `agent/agent-builder`, action `run-task`, resource `task/<id>`+egress hosts, static `context.risk` from `AGENT_BUILDER_POLICY_RISK`) **after** vault handle resolution and **before** `sandboxBox.Create`. The placement is load-bearing: in-process or post-box-start decide cannot stop a compromised agent from self-granting or from running before denial. `deny`/`require_approval` writes a needs-human status and returns without dispatch (box never starts); `allow` applies the `tier_select` and raise-only `vault_injection_floor` obligations to the box request. The decide path is fail-closed (any error → deny). Opt-in via `AGENT_BUILDER_POLICY_BIN`; unset = unchanged behavior. `internal/policy` is a stdlib-only leaf (no executor/LLM/web import; one-way dependency `runtime → policy`). `require_approval` distinct routing and the `audit_emit` obligation are task 073.
- ADR 026 / Task 041: audit supervisor wiring — the Supervisor exposes an optional `audit.Sink` to the in-box loop via `RunStreams.Audit` and Seals it before containment teardown on both the success and failure paths (the audit-chain analogue of the RunRecord close-before-teardown durability rule). The Default Run Wiring projects each action-class lifecycle event (containment, pick, attempt, verify+verdict, publish, escalate, finish+outcome) through the Sink **alongside** the unchanged 019 RunRecord raw stream — raw stdout/stderr stay in the RunRecord, never the Sink. The chain is produced by the block via `BlockSink` behind `AGENT_BUILDER_AUDIT_RECORD` (binary resolved and path checked writable before dispatch; never silently skipped) and a produced run's chain verifies via task 040's `VerifyChain`. The Supervisor depends only on the `audit.Sink` interface, so F-003 supervisor isolation holds (`internal/audit` is a leaf; no executor/LLM/web package enters the supervisor graph).

---

## Maintenance

- **Update in the same commit as `../architecture/diagrams.md`** when the structure changes. The catalog and the diagrams are two views of the same model — they drift together or not at all.
- **Supersede in place. Never append.** When a container is renamed or a dependency edge moves, rewrite the row. The ADR carries the history of *why* something changed; this file carries *what* exists now.
- **Don't list every file.** Components in Section 4 are the load-bearing modules with stable interfaces. If listing a component does not change how someone reasons about the system, leave it out.
- The drift-audit mode of the `architect` agent uses this catalog as its primary check against the import graph and the deployable artifact list. Run it periodically — drift accumulates silently.
