# Configuration

**Project:** agent-builder
**Last updated:** 2026-06-29 (task 133 — Antigravity subscription/OAuth harness added; task 132 — Gemini subscription/OAuth auth mode added; entry can authenticate via cached `gemini` CLI login instead of API key)

Every knob the system exposes — env vars, config files, runtime parameters, deployment settings. Each entry is a public contract: changes to defaults or accepted values are observable.

Not in this file:
- What gets configured (the behaviors live in [behaviors.md](behaviors.md))
- How values get into the process (the parsing is in code; the *contract* is here)

---

## Configuration files

### File: `containment/execution-box/Containerfile`

- **Location:** `containment/execution-box/Containerfile`
- **Format:** OCI image build file consumed by Podman
- **Required vs optional:** required for the execution-box launcher
- **Reload behavior:** read when `containment/execution-box/run.sh` builds the local execution-box image

The image supplies the Go toolchain, `/work`, `/scratch`, and the in-box probe binary. Runtime security and quota settings live in the launcher, not in the image file, because they are host/container run arguments.

### File: `containment/execution-box/gate-toolchain.manifest`

- **Location:** `containment/execution-box/gate-toolchain.manifest`
- **Format:** plain UTF-8 key/value manifest
- **Required vs optional:** required documentation for the execution-box Gate toolchain contract
- **Reload behavior:** read by contributors/operators as the version/source contract; runtime validation is performed by `containment/execution-box/run.sh`

The manifest records that `go` and `gofmt` are supplied by the execution-box base image. The production Gate scanner/linter tools, `golangci-lint`, `dep-scan`, and `code-scanner`, are supplied by a read-only artifact directory mounted at `/opt/agent-builder/gate-tools`. Those mounted artifacts are version-reported rather than fetched during task execution: `containment/execution-box/run.sh --print-toolchain-plan` validates the host-side directory and prints each mounted executable path plus the first `--version` line when available, and `--probe` repeats path/version reporting from inside the box.

The default host-side artifact directory is `containment/execution-box/gate-tools`; override it with `EXEC_BOX_GATE_TOOLS` or `--gate-tools`. The directory must contain executable files named exactly `golangci-lint`, `dep-scan`, and `code-scanner`. Missing directories or missing executables fail closed before Podman starts. The workload container mounts the directory read-only and prepends it to `PATH`, so the Gate does not need broad network egress to fetch tools during task execution.

### File: `containment/execution-box/egress.allowlist`

- **Location:** `containment/execution-box/egress.allowlist`
- **Format:** plain UTF-8 text; one exact hostname plus explicit TCP port per non-comment line, followed by an inline `#` justification comment
- **Required vs optional:** required by the execution-box launcher; override with `EXEC_BOX_EGRESS_ALLOWLIST` or `--egress-allowlist`
- **Reload behavior:** read and validated on every `containment/execution-box/run.sh` invocation before Podman is required

Example:

```text
api.github.com:443 # GitHub API for branch and PR automation
```

Rules:

- Blank lines and lines beginning with `#` are ignored.
- Hostnames are exact matches after lowercase normalization. Wildcards, IP literals, CIDR blocks, URL schemes, paths, and query strings are not accepted by this bootstrap contract.
- Ports are mandatory decimal TCP ports from `1` through `65535`.
- Duplicate `host:port` entries are de-duplicated after parsing.
- Empty allowlist means total egress deny.
- Malformed entries fail closed before Podman starts; the launcher exits non-zero and names the bad line.

The launcher resolves allowlisted hostnames to IPv4 addresses before the workload starts, adds only those host records to the workload container, starts an egress sidecar, waits for the sidecar readiness marker, and only then starts the workload. The sidecar installs an nftables output policy with default drop and explicit allow rules for the resolved allowlisted IP-and-port pairs. The workload container keeps `--cap-drop=all`, `--security-opt=no-new-privileges`, and no `CAP_NET_ADMIN`; network-administration authority is isolated to the sidecar.

---

## Environment variables

| Variable | Type | Default | Required | Effect |
|----------|------|---------|----------|--------|
| `EXEC_BOX_IMAGE` | string | `localhost/agent-builder/execution-box:033` | no | Image tag built and run by the execution-box launcher |
| `EXEC_BOX_WORKLOAD` | enum: `agent`, `dev` | `agent` | no | Workload tier used to choose the default OCI runtime: `agent` -> `runsc`, `dev` -> `runc` |
| `EXEC_BOX_RUNTIME` | enum: `runc`, `runsc`, `kata` | workload default | no | OCI runtime passed to Podman `--runtime`; overrides `EXEC_BOX_WORKLOAD` default mapping |
| `EXEC_BOX_GATE_TOOLS` | path | `containment/execution-box/gate-tools` | no | Host artifact directory containing executable `golangci-lint`, `dep-scan`, and `code-scanner`; mounted read-only into the execution-box at `/opt/agent-builder/gate-tools` |
| `EXEC_BOX_CPUS` | number/string accepted by Podman | `2` | no | CPU quota passed as `--cpus` |
| `EXEC_BOX_MEMORY` | size string | `2g` | no | Memory quota passed as `--memory` |
| `EXEC_BOX_PIDS_LIMIT` | integer | `256` | no | PID quota passed as `--pids-limit` |
| `EXEC_BOX_SCRATCH_SIZE` | size string | `512m` | no | Size of tmpfs mounted at `/scratch` |
| `EXEC_BOX_SHM_SIZE` | size string | `64m` | no | Shared-memory size passed as `--shm-size` |
| `EXEC_BOX_STORAGE_SIZE` | size string | `4G` | no | Per-container writable-layer disk quota passed as `--storage-opt size=...` when the host backing filesystem supports overlay size enforcement (XFS). On non-XFS hosts the flag is omitted and a `WARNING` is emitted to stderr naming the degraded control. An empty string disables the quota without a warning (operator opt-out). Detection may be overridden for tests via `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0|1`. |
| `EXEC_BOX_STORAGE_QUOTA_SUPPORTED` | `0` or `1` | unset (auto-detect) | no | Testability seam: when set to `1`, forces the enforcement path (`--storage-opt size=...` is applied); when set to `0`, forces the graceful-degrade path (flag omitted, WARNING emitted if `EXEC_BOX_STORAGE_SIZE` is non-empty). When unset, the launcher detects enforceability via `podman info` and `stat`. Never set in production. |
| `EXEC_BOX_EGRESS_ALLOWLIST` | path | `containment/execution-box/egress.allowlist` | no | Plain-text egress allowlist consumed by the execution-box launcher |
| `EXEC_BOX_EGRESS_PROBE_ALLOW_HOST` | `host:port` | `api.github.com:443` | no | Allowlisted probe target expected to connect during `--egress-probe` |
| `EXEC_BOX_EGRESS_PROBE_DENY_HOST` | `host:port` | `example.com:443` | no | Non-allowlisted probe target expected to be blocked during `--egress-probe` |
| `EXEC_BOX_EGRESS_PROBE_DENY_IP` | `host:port` IP literal | `1.1.1.1:443` | no | Direct-IP probe target expected to be blocked during `--egress-probe` |
| `ANTHROPIC_API_KEY` | secret string | none | no (one of two for cloud entries) | Independently revocable Claude Code CLI credential injected into the subprocess environment for cloud entries. The executor accepts this OR `CLAUDE_CODE_OAUTH_TOKEN` (OAuth token preferred when both are set). NOT injected for local entries — local entries use `ANTHROPIC_AUTH_TOKEN` instead (see below). The executor fails before subprocess start when both this and `CLAUDE_CODE_OAUTH_TOKEN` are absent for cloud entries. |
| `CLAUDE_CODE_OAUTH_TOKEN` | secret string | none | no (one of two for cloud entries) | Subscription OAuth token alternative to `ANTHROPIC_API_KEY`. When set, it is preferred over the API key and injected into the subprocess environment for cloud entries. Minted by `claude setup-token` on a Claude Pro/Max subscription. Never injected for local entries. The executor fails before subprocess start when both this and `ANTHROPIC_API_KEY` are absent for cloud entries. |
| `ANTHROPIC_AUTH_TOKEN` | secret string | none | no (local entries only) | Gateway/proxy bearer-token passed to Claude Code CLI in local/translation-proxy mode (when `ANTHROPIC_BASE_URL` is set). The CLI does not validate this as a real Anthropic credential; it is passed straight through to the custom endpoint. For local entries, the executor injects a fixed placeholder sentinel `executor.LocalProxyAuthPlaceholder` (value: `"local-proxy-no-auth"`) — NOT the operator's real key — and the translation proxy ignores the value. Only set for local entries; omitted for cloud entries. |
| `ANTHROPIC_BASE_URL` | URL string | none | no (local entries only) | Redirects the Claude Code CLI to a custom endpoint. Set by `executor.ClaudeCLI` to `entry.Endpoint` when the entry has an empty `SecretRef` (local/translation-proxy mode). The translation proxy (LiteLLM, claude-code-router) presents an Anthropic-compatible endpoint over a local OpenAI-API inference server. When set, `ANTHROPIC_AUTH_TOKEN` is injected as the placeholder sentinel (see `ANTHROPIC_AUTH_TOKEN` row for details), `ANTHROPIC_API_KEY` is omitted, and `CLAUDE_CODE_OAUTH_TOKEN` is omitted. |
| `AGENT_BUILDER_REQUIRE_APPROVAL` | boolean string | `true` | no | **`orchestrate` subcommand only** (task 129, ADR 056). Determines whether the orchestrator forces an `AwaitingApproval` checkpoint pause on policy-allowed (`DecisionAllow`) plans. When true (default), allowed plans pause for human approval. When false (lenient false: `"false"`, `"0"`, `"no"`), the orchestrator auto-dispatches the plan immediately on policy allowance. Deny decisions are unaffected. |
| `AGENT_BUILDER_TASK_ROOT` | path | none | yes for `agent-builder run` | Root containing `docs/plans/roadmap.md` and `docs/tasks/{backlog,active,completed}` for task selection and constrained status writes |
| `AGENT_BUILDER_WORKTREE` | path | none | yes for `agent-builder run` | Target repo worktree passed to the Claude CLI Executor, Gate, and Podman execution-box containment probe |
| `AGENT_BUILDER_CLAUDE_CLI` | path/name | `claude` | no | Claude Code CLI executable used by default run wiring |
| `AGENT_BUILDER_EXEC_BOX_LAUNCHER` | path/name | `containment/execution-box/run.sh` | no | Podman execution-box launcher invoked by the default run containment adapter (`podman.Runner`). Tests may point this at a fake launcher. |
| `AGENT_BUILDER_RUN_RECORD` | path | disabled | no | Host-side RunRecord NDJSON path for `agent-builder run`; blank disables record writing without disabling dispatch |
| `AGENT_BUILDER_AUDIT_RECORD` | path | disabled | no | Hash-chained audit-trail chain logfile for `agent-builder run`. When set, the supervisor projects each action-class lifecycle event (containment, pick, attempt, verify, publish, escalate, finish) through `audit.BlockSink` into this file (one `audit-trail emit` per event) and Seals it before containment teardown. Blank/absent disables the audit chain without disabling dispatch (mirrors `AGENT_BUILDER_RUN_RECORD`). When set, the `audit-trail` binary must resolve and the path must be writable **before dispatch** — an unresolvable binary or unwritable path is a fail-fast configuration error; auditing is never silently skipped. |
| `AGENT_BUILDER_AUDIT_BIN` | path/name | `audit-trail` on `$PATH` | no | The `audit-trail` block binary used to produce the audit chain when `AGENT_BUILDER_AUDIT_RECORD` is set. Falls back to resolving `audit-trail` on `$PATH`. Tests may point this at a prebuilt binary. |
| `AGENT_BUILDER_RUN_TIMEOUT` | duration string | none | yes for `agent-builder run` | Explicit supervisor wall-clock timeout and sandbox request timeout for one default run |
| `AGENT_BUILDER_MAX_ATTEMPTS` | non-negative integer | none | yes for `agent-builder run`; optional for `agent-builder orchestrate` | Explicit bounded retry attempt count for the selected task (`agent-builder run`) or for blocked-action reevaluation before human escalation (`agent-builder orchestrate`, ADR 055 seam 4, task 123; note: on the orchestrate path this escalation is Reporter-backed, not task-file-backed) |
| `AGENT_BUILDER_PUBLISH_REMOTE` | string | none | yes for `agent-builder run` | Git remote name or URL used by the branch publisher after Gate success |
| `AGENT_BUILDER_RECIPE` | enum: `coding-agent`, `docs-fix`, `agent-builder-worker` | `coding-agent` | no | Pluggable recipe selector: determines the goal source, gate, and result sink for the run (ADR 041, tasks 076–079, 082). `coding-agent` is the default (solves coding tasks via the Claude CLI, gates via Go tooling); `docs-fix` is the second proof recipe (documentation fixes, gates via markdown linter + code-scanner); `agent-builder-worker` is the code-authoring worker recipe (authors a new Go recipe, gates via code-scanner + dep-scan + generated-gate-existence assertion, requires human approval before generated agent dispatch, emits audit provenance — ADR 042, ADR 047). Unknown recipe names return an error before sandbox creation. |
| `AGENT_BUILDER_GIT_CLI` | path/name | `git` | no | Git executable used by branch publication |
| `AGENT_BUILDER_GH_CLI` | path/name | `gh` | no | GitHub CLI executable used to find or create the PR artifact |
| `AGENT_BUILDER_GIT_TOKEN` | secret string | none | no | Optional token exposed to the publication subprocess as `GIT_TOKEN` and redacted from publisher errors/run records |
| `AGENT_BUILDER_GITHUB_TOKEN` | secret string | none | no | Optional token exposed to the publication subprocess as `GH_TOKEN` and `GITHUB_TOKEN` and redacted from publisher errors/run records |
| `AGENT_BUILDER_EXEC_SANDBOX_BIN` | path/name | unset | no | Path to the exec-sandbox block binary. When set, `execsandbox.Runner` becomes the default containment backend (preferred over the Podman launcher). Tests and local runs may point this at a custom binary; unset disables the block backend entirely. |
| `AGENT_BUILDER_EXEC_SANDBOX_GOROOT` | path | `go env GOROOT` | no | Go toolchain root directory to be forwarded into the block via `FileRead` capability. When unset, discovered via `go env GOROOT`; if neither is available, toolchain forwarding is skipped (base system PATH applies). |
| `AGENT_BUILDER_GATE_TOOLS` | path | `containment/execution-box/gate-tools` | no | Gate toolchain artifacts directory (`golangci-lint`, `dep-scan`, `code-scanner`) to be forwarded into the block via `FileRead` capability and prepended to `PATH`. When unset, defaults to the bundled directory; if that doesn't exist, forwarding is skipped. |
| `AGENT_BUILDER_VAULT_BIN` | path/name | unset | no | Path to the vault block binary. **When set, vault token brokering is enabled** (ADR 036, task 066): a vault daemon is started for the run, the git/GitHub tokens are registered and resolved to opaque handles, and the handles + socket + `injection_mode="proxy"` are passed to exec-sandbox via `Request.Wiring`. When unset, vault is disabled and the git/GitHub tokens are forwarded via the existing env path (no behavior change). Vault absence is not a startup error. |
| `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY` | path | unset | required when checkpoint enabled | Path to the PEM-encoded Ed25519 private signing key used to produce the checkpoint attestation. **When set, checkpoint creation is enabled** (ADR 037, task 068): after the run succeeds and the audit chain is sealed, `audit-trail checkpoint create` is called with this key to produce a cryptographic attestation of the sealed chain. When unset, checkpoint creation is disabled and the run proceeds unchanged. The file must exist before dispatch — absence is a fail-fast configuration error. Never logged. |
| `AGENT_BUILDER_AUDIT_CHECKPOINT_LOG_ID` | string | unset | required when checkpoint enabled | Stable log identifier passed as `--log-id` to `audit-trail checkpoint create`. Uniquely identifies the audit log being checkpointed (e.g. `prod-agent-builder-<run-id>`). Only consulted when `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY` is set. |
| `AGENT_BUILDER_AUDIT_CHECKPOINT_OUT` | path | unset (stdout) | no | Filesystem path where the checkpoint JSON is written (`--out`). When unset, the checkpoint JSON goes to the `audit-trail` subprocess stdout (not persisted). When set, the parent directory must exist and be writable **before dispatch** — an unwritable directory is a fail-fast configuration error. Only consulted when `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY` is set. |
| `AGENT_BUILDER_AUDIT_CHECKPOINT_PUBLIC_KEY` | path | unset | no | Path to the PEM-encoded Ed25519 public key corresponding to `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY`. Stored in `Config` for use by the `agent-builder verify-checkpoint` subcommand (task 069). Not used by checkpoint creation itself. Only consulted when `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY` is set. |
| `AGENT_BUILDER_VAULT_SOCKET` | path | `${TMPDIR}/agent-builder-vault-<pid>.sock` | no | Unix socket path the vault daemon listens on and that exec-sandbox connects to at injection time. Only consulted when `AGENT_BUILDER_VAULT_BIN` is set. |
| `AGENT_BUILDER_VAULT_STORE_PATH` | path | unset (in-memory) | no | Optional path for vault's persistent encrypted store, passed as `--store-path`. When unset, vault runs in-memory only (secrets do not survive a daemon restart). Only consulted when `AGENT_BUILDER_VAULT_BIN` is set. |
| `AGENT_BUILDER_POLICY_BIN` | path/name | unset | no | Path to the `policy-engine` block binary. **When set, the host-side policy decide gate is enabled** (ADR 038, task 072): a `policy-engine serve --socket <path> --allow <egress-csv>` daemon is started for the run, and `runtime.Run` calls AuthZEN `decide` (subject `agent/agent-builder`, action `run-task`, resource `task/<id>` with `properties.egress_hosts`, `context.risk`) **before** `sandboxBox.Create`. `deny` (and, in task 072, `require_approval`) writes a needs-human status and returns without dispatching — the box never starts. `allow` applies obligations (`tier_select`, `vault_injection_floor`) and proceeds. **Fail-closed:** any decide error (dial, timeout, malformed response, unknown decision) maps to deny. When unset, the gate is skipped entirely and the run proceeds exactly as before (zero regression). A set-but-unresolvable binary is a fail-fast configuration error before dispatch. |
| `AGENT_BUILDER_POLICY_SOCKET` | path | `${TMPDIR}/agent-builder-policy-<pid>.sock` | no | Unix socket path the policy daemon listens on (`--socket`) and that agent-builder connects to for the decide call. Only consulted when `AGENT_BUILDER_POLICY_BIN` is set. |
| `AGENT_BUILDER_POLICY_RISK` | string | `low` | no | Static value sent as `context.risk` in the AuthZEN decide request. Dynamic risk scoring is deferred (ADR 038); this is a fixed per-deployment value. Only consulted when `AGENT_BUILDER_POLICY_BIN` is set. |
| `AGENT_BUILDER_POLICY_ALLOW` | comma-separated strings | unset | no | **`orchestrate` subcommand only** (ADR 055 seam 1, task 122). The optional **deployment base allow** that **narrows** the plan-derived authorization set. On orchestrate the policy daemon is **not** started at assembly time; instead, per admitted plan, the orchestrator derives the plan's resource set — `Plan.AllowedResources()` = `{plan.GoalID} ∪ {recipe names} ∪ {sub-goal task IDs}` (the `spawn-plan` / `spawn-worker` / `run-task` resources) — and launches the daemon with `--allow` set to **`effectiveAllow(plan, base)`**: the plan-derived set when this var is unset/whitespace-only, else the **intersection** of the plan-derived set with this base. The deployment can therefore only **narrow** (drop resources the plan declared), never **widen** (it cannot add a resource the plan did not declare). This is the daemon side of plan-derived authorization: it makes the independent policy engine **allow** exactly the in-plan resources, fixing the prior empty-allowlist denial where every orchestrate spawn returned `Plan denied`. **Fail-closed:** a base disjoint from the plan yields an empty effective set → the daemon is launched with an empty `--allow` → it denies every spawn for that plan. Before any plan is configured there is no daemon and `Decide` is deny. The orchestrator-side plan-derived gate (task 118) and the self-repo bright line remain in force regardless of this var. Only consulted when `AGENT_BUILDER_POLICY_BIN` is set. |
| `AGENT_BUILDER_WORKER_SIGNING_KEY` | path | none | required when the orchestrator↔worker transport is constructed via `worker.NewWorkItemSenderFromEnv` / `worker.LoadSigningKey` | Path to the file holding the orchestrator's Ed25519 signing key (hex-encoded 64-byte `ed25519.PrivateKeySize` private key) for the orchestrator↔worker transport (ADR 048, task 083). The transport signs every dispatched work-item with this key so the worker can verify provenance, and replay-checks every inbound envelope. **Fail-closed at startup (REQ-083-05):** when the variable is unset, or names an absent/unreadable/malformed key file, the transport constructor returns an error satisfying `errors.Is(err, worker.ErrMissingSigningKey)` that names this variable, **before any work-item is dispatched or received** — never at first message receipt. The key bytes are never logged. |
| `AGENT_BUILDER_MEMORY_GUARD_BIN` | path/name | unset | no | Path to the `memory-guard` block binary. **When set, the memory-guard write-gate + delete-verify are enabled** (ADR 049, task 084): orchestrator plan/fleet state writes go through `validate_write` (fail-closed on allow=false) and deletes go through `verify_delete` (tamper=halt on confirmed=false or residue_detected=true). When unset, the orchestrator degrades gracefully to the in-memory `MemoryPlanStore` (v1) and emits a structured warning log naming this variable. The absence of this variable is never a startup error — existing e2e tests pass unchanged when it is unset. A set-but-unresolvable binary surfaces as an error on the first IPC call (not at startup). |
| `AGENT_BUILDER_PLANNER` | enum: `structured`, `llm` | `structured` | no | **`orchestrate` subcommand only** (task 099/100/110, ADR 053). Selects the `Planner` the orchestrator assembles. `structured` (default) wires the rule-based `StructuredPlanner` (no LLM, no executor import). `llm` (live since task 110) wires the LLM-backed `LLMPlanner` (`internal/orchestrator/planner`): the CLI builds a router catalog from `AGENT_BUILDER_REGISTRY_*` env vars (synthetic-default Claude entry when none are configured), wraps `*router.Router.Select` as the `ExecutorResolver`, and closes over `executor.CompleterForEntry` as the `Invoker`. **Ollama-native entries only** — cloud harnesses (`claude-cli`, `codex-cli`, `gemini-cli`) fail closed with `ErrSingleShotUnsupported` (ADR 053 §2); a cloud-only registry halts decomposition with a clear error rather than producing a degenerate plan. The `ExecutorResolver` adapter drops the planner's ctx (router.Select is not context-cancellable today — documented limitation). `internal/orchestrator/planner` never imports `internal/executor` directly (F-014). Any other value is a fail-fast configuration error (the subcommand exits `ExitUsage`). |
| `AGENT_BUILDER_CLARIFIER` | enum: `heuristic`, `llm` | `heuristic` | no | **`orchestrate` subcommand only** (task 131). Selects the `Clarifier` the orchestrator assembles. `heuristic` (default) wires the rule-based `HeuristicClarifier` (static regexes). `llm` wires the LLM-backed `LLMClarifier` (`internal/orchestrator/planner`) which uses the same executor catalog and LLM seams (resolver/invoker) as the `LLMPlanner` to analyze specification readiness. Any other value is a fail-fast configuration error. |
| `AGENT_BUILDER_GOAL_SPEC` | string | unset | no | **`orchestrate` subcommand only** (task 099, generalized in task 113 / ADR 054 §2). When set, the env/stdin `MessageSource` delivers it as the **first** inbound message — a single `MsgNewGoal` whose `Goal.Spec` is this text — before reading any stdin line. The spec text is decomposed by the planner into sub-goals (one `"<recipe>: <text>"` line per sub-goal, or a single free-form line on the default recipe). When unset and stdin carries no input, the subcommand idles (no dispatch). |
| `AGENT_BUILDER_GOAL_ID` | string | `goal` | no | **`orchestrate` subcommand only** (task 099). The `Task.ID`/`GoalID` used as the plan-state and registry key for the `AGENT_BUILDER_GOAL_SPEC` goal. (Bare stdin goal lines auto-assign collision-free IDs `goal-1`, `goal-2`, … — see the stdin command grammar below.) |
| `AGENT_BUILDER_GOAL_REPO` | string | empty | no | **`orchestrate` subcommand only** (task 099). The `Task.Repo` carried into the `AGENT_BUILDER_GOAL_SPEC` goal. |
| `AGENT_BUILDER_MAX_WORKERS` | positive integer | `4` | no | **`orchestrate` subcommand only** (task 112, ADR 054 §1). The **fleet-wide** cap on total live sub-goal workers across all concurrent goals — the load-bearing bound on sandbox/box pressure. Enforced by a shared weighted semaphore acquired **inside** `dispatchPlan`'s per-sub-goal goroutine (`Acquire(1)` before the worker dispatch, deferred `Release(1)` after), so the total number of in-flight workers — summed over M concurrent goals × N sub-goals each — never exceeds this value. A non-integer, empty, or `< 1` value falls back to the default (the bound is a tuning knob, not a security gate, so a malformed value never fails the subcommand). |
| `AGENT_BUILDER_MAX_GOALS` | positive integer | `8` | no | **`orchestrate` subcommand only** (task 112, ADR 054 §1). The **goal-admission cap**: the maximum number of goal-actor goroutines that may be in a non-`Queued`, non-terminal lifecycle state at once. Enforced at the control loop (not the orchestrator core); excess `new-goal`s are registered with `Queued` status and park until a live goal reaches a terminal state and frees a slot. This is back-pressure on planning state, looser than `AGENT_BUILDER_MAX_WORKERS`. A non-integer, empty, or `< 1` value falls back to the default. |
| `AGENT_BUILDER_INBOUND` | enum: `""`, `env`, `telegram` | `""` (same as `env`) | no | **`orchestrate` subcommand only** (task 117, ADR 054 §2). Selects the inbound `MessageSource` (and, for `telegram`, the outbound `Reporter`) for the async control plane. `""` or `"env"` (default) uses the env/stdin line-oriented `MessageSource` — the local-first testing seam. `"telegram"` wires `telegram.Adapter` as the `MessageSource` and `telegram.ReplyAdapter` as the `Reporter`; the full set of `AGENT_BUILDER_TELEGRAM_*` env vars must be set (missing any is a fail-fast `ExitUsage` error at assembly time, never a nil-adapter panic at first `Next()` call). Any other value is a fail-fast configuration error. |
| `AGENT_BUILDER_TELEGRAM_BOT_TOKEN` | secret string | none | required when `AGENT_BUILDER_INBOUND=telegram` | Telegram Bot API token. Never logged. |
| `AGENT_BUILDER_TELEGRAM_BASE_URL` | URL | `https://api.telegram.org` | no | Telegram Bot API base URL. Useful for local stubs in integration tests. |
| `AGENT_BUILDER_TELEGRAM_SIGNING_KEY` | hex string (32 bytes) | none | required when `AGENT_BUILDER_INBOUND=telegram` | Operator Ed25519 public key (hex-encoded, 32 bytes) used by the inbound `telegram.Adapter` to verify the envelope signature on every incoming message. |
| `AGENT_BUILDER_TELEGRAM_X25519_PUB` | hex string (32 bytes) | none | required when `AGENT_BUILDER_INBOUND=telegram` | Operator X25519 public key (hex-encoded, 32 bytes) used as the inbound envelope sender's public key for AEAD decryption. |
| `AGENT_BUILDER_TELEGRAM_ORCH_PRIV` | secret hex string (32 bytes) | none | required when `AGENT_BUILDER_INBOUND=telegram` | Orchestrator X25519 private key (hex-encoded, 32 bytes) used as the inbound envelope recipient's private key for AEAD decryption. Never logged. |
| `AGENT_BUILDER_TELEGRAM_ORCH_ED_PRIV` | secret hex string (64 bytes) | none | required when `AGENT_BUILDER_INBOUND=telegram` | Orchestrator Ed25519 private key (hex-encoded, 64 bytes) used by `telegram.ReplyAdapter` to sign outbound reply envelopes. Never logged. |
| `AGENT_BUILDER_TELEGRAM_OP_X25519_PUB` | hex string (32 bytes) | none | required when `AGENT_BUILDER_INBOUND=telegram` | Operator X25519 public key (hex-encoded, 32 bytes) used by `telegram.ReplyAdapter` to seal outbound reply envelopes for the operator. |
| `AGENT_BUILDER_TELEGRAM_CHAT_ID` | string | none | required when `AGENT_BUILDER_INBOUND=telegram` | Telegram chat ID that `telegram.ReplyAdapter` uses as the destination for all outbound replies (acks, status, results). |
| `VAULT_MASTER_KEY` | secret string (hex) | none | required when vault is enabled and `VAULT_MASTER_KEY_FILE` is unset | 32-byte hex-encoded master key for the vault daemon's encryption. Validated to decode to exactly 32 bytes; an invalid or short key is a fail-fast error. **Never auto-generated** — absence when vault is enabled fails loud (a silent ephemeral key would lose secrets across restarts). Never logged. |
| `VAULT_MASTER_KEY_FILE` | path | none | alternative to `VAULT_MASTER_KEY` | Path to a file containing the hex master key. **Takes precedence over `VAULT_MASTER_KEY`** when both are set. The file contents are read and validated identically to `VAULT_MASTER_KEY`; the key value is never logged. |

### stdin command grammar (`orchestrate`)

When no inbound channel is configured, the `orchestrate` subcommand's inbound seam is
the env/stdin line-oriented `MessageSource` (ADR 054 §2, task 113). It is the
local-first testing seam the operator uses to drive the async control plane without
Telegram. `AGENT_BUILDER_GOAL_SPEC` (if set) is delivered first as a single new goal;
then **one message is parsed per non-blank stdin line** by the following grammar:

| Input line | Message kind | `GoalID` | Payload |
|------------|--------------|----------|---------|
| `<any bare line>` (no known verb) | `MsgNewGoal` | auto-assigned `goal-1`, `goal-2`, … | `Goal.Spec` = the line |
| `status` | `MsgStatus` | `""` (fleet-wide) | — |
| `status <goalID>` | `MsgStatus` | `<goalID>` | — |
| `info <goalID> <text…>` | `MsgInfo` | `<goalID>` | `Text` = the remaining text |
| `cancel <goalID>` | `MsgCancel` | `<goalID>` | — |
| `confirm <goalID>` | `MsgConfirm` | `<goalID>` | — |

- **EOF / no-more-input** → the source returns `ok=false`; the control plane drains all
  in-flight goal actors and the subcommand exits `0`.
- **Malformed control line** (a `cancel`/`confirm` with no goalID, or `info <goalID>` with no
  text) → a parse error wrapping `ErrMalformedInput`; the control loop reports it over
  the Reporter and **continues** reading. A malformed control line is **never** silently
  accepted as a new goal.
- **Routing** (ADR 054 §2): `new-goal` spawns a goal actor (register-then-start: the
  goal's command mailbox and registry entry are created before the actor starts);
  `status` is answered from the live registry (handler body is task 114); `info`/`cancel`/`confirm`
  are delivered to the addressed goal's per-goal command mailbox. An `info`/`cancel` for
  an **unknown goalID** yields a graceful "no such goal" report — never a panic, and no
  mailbox is auto-created for the unknown goal.

### Executor Registry Configuration

The executor registry is configured via well-known env-var prefixes per entry ID, allowing per-deployment tuning of which executor entries are enabled and their endpoints. Each entry supports the following variables (where `<ID>` is the entry ID in `SCREAMING_SNAKE_CASE`, e.g., `CLAUDE_OAUTH` for `claude-oauth`):

| Variable | Type | Default | Required (if enabled) | Effect |
|----------|------|---------|------------|--------|
| `AGENT_BUILDER_REGISTRY_<ID>_ENABLED` | enum: `true`, `false` | (not set = disabled) | no | When `true`, the entry is loaded and registered into the catalog; when `false` or unset, the entry is skipped. |
| `AGENT_BUILDER_REGISTRY_<ID>_ENDPOINT` | URL string | none | yes (if enabled) | Base URL the harness points at (cloud API, or a local model translation proxy). Blank values fail with a descriptive error. |
| `AGENT_BUILDER_REGISTRY_<ID>_SECRET_REF` | string | none | yes for cloud entries; **optional (empty) for local entries** | Vault secret name to resolve at dispatch time (never the secret itself). For cloud entries, blank values fail with a descriptive error. For local entries (`local-qwen`, `local`, `gemini`, `antigravity`), the field is intentionally empty — no cloud API key is needed. Local entries use their own authentication patterns: Claude/Qwen entries inject `ANTHROPIC_AUTH_TOKEN=<placeholder>` and `ANTHROPIC_BASE_URL=<endpoint>` to point to the translation proxy; Gemini and Antigravity subscription entries rely on their respective CLI's cached OAuth login (`~/.gemini` or `~/.antigravity`) and require no injected env vars. |
| `AGENT_BUILDER_REGISTRY_<ID>_MODEL` | string | none | yes (if enabled) | Model identifier (e.g., `claude-opus-4-5`, `qwen-7b`). Blank values fail with a descriptive error. |
| `AGENT_BUILDER_REGISTRY_<ID>_CAPABILITY_TIER` | non-negative integer | none | yes (if enabled) | Ordered capability ranking (higher = stronger). Non-integer values fail with a descriptive error. |
| `AGENT_BUILDER_REGISTRY_<ID>_COST_WEIGHT` | non-negative integer | none | yes (if enabled) | Relative cost per dispatch (lower = cheaper). Non-integer values fail with a descriptive error. |
| `AGENT_BUILDER_REGISTRY_<ID>_BUDGET_LIMIT` | non-negative integer | `0` (unlimited) | no | Maximum dispatches over the rolling window. `0` means no cap. Non-integer values fail with a descriptive error. |
| `AGENT_BUILDER_REGISTRY_<ID>_BUDGET_WINDOW` | duration string | `0` (unlimited) | no | Rolling time window for budget enforcement (e.g., `5h`, `30m`). Non-duration values fail with a descriptive error. |

**Known entry IDs and their harnesses:**
- `claude-oauth` → `claude-cli` (Anthropic Claude via OAuth/subscription)
- `local-qwen` → `claude-cli` (Local Qwen model via translation proxy)
- `local-ollama` → `ollama-native` (Native Ollama executor, no translation proxy)
- `codex` → `codex-cli` (OpenAI Codex)
- `gemini` → `gemini-cli` (Google Gemini via subscription/OAuth)
- `antigravity` → `antigravity-cli` (Antigravity `agy` CLI via subscription/OAuth)

**Example configuration for `claude-oauth`:**
```
AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENABLED=true
AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENDPOINT=https://api.anthropic.com
AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_SECRET_REF=claude-oauth-token
AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_MODEL=claude-opus-4-5
AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_CAPABILITY_TIER=3
AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_COST_WEIGHT=10
AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_BUDGET_LIMIT=0
```

**Example configuration for `local-qwen` (translation-proxy seam):**
```
AGENT_BUILDER_REGISTRY_LOCAL_QWEN_ENABLED=true
AGENT_BUILDER_REGISTRY_LOCAL_QWEN_ENDPOINT=http://localhost:8080   # LiteLLM or claude-code-router proxy URL
AGENT_BUILDER_REGISTRY_LOCAL_QWEN_MODEL=qwen2.5-coder-7b-instruct
AGENT_BUILDER_REGISTRY_LOCAL_QWEN_CAPABILITY_TIER=1
AGENT_BUILDER_REGISTRY_LOCAL_QWEN_COST_WEIGHT=1
# SECRET_REF is not set — local entries have no cloud auth
# Budget is not set — local entries are unlimited (Budget.Limit == 0)
```

**Example configuration for `local-ollama` (native executor, no translation proxy):**
```
AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_ENABLED=true
AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_HARNESS=ollama-native
AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_ENDPOINT=http://localhost:11434
AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_MODEL=qwen3:8b
AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_CAPABILITY_TIER=1
AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_COST_WEIGHT=1
# SECRET_REF is not set — local entries have no cloud auth
# Budget is not set — local entries are unlimited (Budget.Limit == 0)
```

**Example configuration for `gemini` (subscription/OAuth mode):**
```
AGENT_BUILDER_REGISTRY_GEMINI_ENABLED=true
AGENT_BUILDER_REGISTRY_GEMINI_ENDPOINT=https://gemini.google.com
AGENT_BUILDER_REGISTRY_GEMINI_MODEL=gemini-2.0-flash
AGENT_BUILDER_REGISTRY_GEMINI_CAPABILITY_TIER=2
AGENT_BUILDER_REGISTRY_GEMINI_COST_WEIGHT=2
# SECRET_REF is not set — subscription mode uses cached OAuth login from `gemini` CLI
# Budget is not set — subscription entries are unlimited (Budget.Limit == 0)
```
The `gemini` CLI must be installed and logged in via `gemini auth login` (or `gemini setup-token` for headless authentication) on the operator's system. Credentials are cached in `~/.gemini` and do not need to be injected by the executor.

**Example configuration for `antigravity` (subscription/OAuth mode):**
```
AGENT_BUILDER_REGISTRY_ANTIGRAVITY_ENABLED=true
AGENT_BUILDER_REGISTRY_ANTIGRAVITY_ENDPOINT=https://agy.google.com
AGENT_BUILDER_REGISTRY_ANTIGRAVITY_MODEL=Claude Opus 4.6 (Thinking)
AGENT_BUILDER_REGISTRY_ANTIGRAVITY_CAPABILITY_TIER=3
AGENT_BUILDER_REGISTRY_ANTIGRAVITY_COST_WEIGHT=5
# SECRET_REF is not set — subscription mode uses cached OAuth login from `agy` CLI
# Budget is not set — subscription entries are unlimited (Budget.Limit == 0)
```
The `agy` CLI must be installed and logged in (via Google Sign-In in the terminal or headless token setup) on the operator's system. Credentials are cached in `~/.antigravity` and do not need to be injected by the executor. The model token must match one of the models reported by `agy models` (e.g., "Claude Opus 4.6 (Thinking)", "Gemini 3.5 Flash (High)", "GPT-OSS 120B (Medium)").

**Note:** The native Ollama executor (`ollama-native` harness) requires the model to return structured `tool_calls` via Ollama's `/api/chat` endpoint. As of Ollama 0.17.7, `qwen3:8b` returns parseable `tool_calls`. Other models (e.g., `qwen2.5-coder:7b`) may emit bare JSON without the `<tool_call>` wrapper, preventing tool execution. Consult the Ollama model library documentation for confirmed `tool_calls` support.

When ALL enabled registry entries are local (all have empty `SECRET_REF`), the operator does NOT need to export `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` in the host environment — `agent-builder run` will start without requiring cloud credentials. The executor injects the fixed placeholder `executor.LocalProxyAuthPlaceholder` (value: `"local-proxy-no-auth"`) as `ANTHROPIC_AUTH_TOKEN` in the subprocess to satisfy the Claude Code CLI's auth check; the translation proxy ignores the token value. (`ANTHROPIC_AUTH_TOKEN` is required rather than `ANTHROPIC_API_KEY` because the current Claude Code CLI validates `ANTHROPIC_API_KEY` as a real Anthropic credential and rejects a placeholder with `Not logged in`, while `ANTHROPIC_AUTH_TOKEN` is the gateway bearer-token var passed through to `ANTHROPIC_BASE_URL`.) The translation proxy (LiteLLM, claude-code-router) converts Anthropic API requests to the local inference server's OpenAI API. See `registry.TranslationProxySeam` for the named seam constant.

For Gemini and Antigravity subscription entries, the `gemini` and `agy` CLIs use their own authentication (cached OAuth login) and do not require any token injection from the executor.

**Router quota defaults (not configurable via env vars):**
- `router.DefaultCooldown = 5m` — the fallback window applied by `OnRateLimit` when no `Retry-After` header is present. Overridable per-router via `NewWithClock(catalog, clock, cooldown)` in code (tests use small cooldowns; production uses the constant). No env-var override is exposed; the cooldown is a per-deployment code constant.
- Router state-file path (for `SaveState`/`LoadState`): no env-var default is defined. The path is passed explicitly by the caller (runtime wiring — task 095). There is no automatic startup or shutdown state save/load in this task.

**Removed variables** (rejected loudly when set — see ADR 021):
- `AGENT_BUILDER_SANDBOX_RUNTIME` — the Phase 0 `srt` selector for the rented `@anthropic-ai/sandbox-runtime` backend. Containment now runs through the Podman execution-box launcher (`AGENT_BUILDER_EXEC_BOX_LAUNCHER`). If a non-empty value is present, `agent-builder run` fails with a migration error naming the variable rather than silently ignoring it.

**Hook profile env vars** (consumed by `.claude/scripts/`, not the application itself):
- `CLAUDE_HOOK_PROFILE` — `minimal` / `standard` / `strict` (default `standard`)
- `CLAUDE_DISABLED_HOOKS` — comma-separated list of hook names to disable

---

## Runtime flags

The execution-box launcher exposes runtime flags in [interfaces.md](interfaces.md#executable-artifact-execution-box-launcher). The configuration contract for those flags is:

- `--workload agent|dev`: selects the default runtime tier (`agent` -> `runsc`, `dev` -> `runc`).
- `--runtime runc|runsc|kata`: overrides the workload default and passes the selected value to Podman `--runtime`.
- `--gate-tools PATH`: overrides the host artifact directory mounted read-only at `/opt/agent-builder/gate-tools`.
- `--print-runtime-plan`: prints the resolved workload, runtime, and source without requiring Podman.
- `--print-toolchain-plan`: validates and prints the Gate toolchain plan without requiring Podman.

## Runtime parameters

Typed values supplied by callers at construction time rather than parsed from
environment or CLI flags.

| Parameter | Type | Default | Required | Effect |
|-----------|------|---------|----------|--------|
| `loop.RetryPolicy.MaxAttempts` | non-negative integer | none | yes | Bounds Executor attempts for one picked task. `0` means mark `needs-human` immediately without running Executor or Gate; positive values permit exactly that many attempts before exhausted failures escalate. |
| `supervisor.WithRunTimeout` | `time.Duration` | disabled (`0`) | no | Bounds the wall-clock duration of one in-box `Supervisor.Run` loop. Positive values arm a deadline; expiry kills the containment box, tears it down, and records the terminal outcome as `timed-out`. Non-positive values preserve the existing no-timeout dispatch behavior. |
| `supervisor.WithSink` | `audit.Sink` | nil (disabled) | no | Optional typed audit action sink. When set, the supervisor exposes it to the in-box loop through `RunStreams.Audit` for typed action projection and Seals it before containment teardown on both the success and failure paths (the audit-chain analogue of the RunRecord close-before-teardown rule). A nil sink disables audit projection; the run behaves exactly as without it. The supervisor depends only on the `audit.Sink` interface — the concrete `BlockSink` reaches the block over `os/exec`, so no block/executor/LLM/web package enters the supervisor import graph (F-003). |
| `executor.ClaudeCLIConfig.CLIPath` | string | none for explicit config; `NewClaudeCLIFromEnv` uses `claude` | yes for explicit config | Path/name of the Claude Code CLI binary to execute. Blank explicit config fails before subprocess start. Tests may point this at a fake CLI subprocess. |
| `executor.ClaudeCLIConfig.Worktree` | path string | none | yes | Target task worktree used as the Claude CLI subprocess working directory. Blank values fail before subprocess start. |
| `executor.ClaudeCLIConfig.AuthToken` | secret string | none | one of two | `ANTHROPIC_API_KEY` value supplied to the executor. Production callers pass it via `NewClaudeCLIFromEnv`; tests can provide a fake token directly. At least one of `AuthToken` or `OAuthToken` must be non-blank. |
| `executor.ClaudeCLIConfig.OAuthToken` | secret string | none | one of two | `CLAUDE_CODE_OAUTH_TOKEN` value (Claude subscription credential from `claude setup-token`). Preferred over `AuthToken` when both are set; the executor injects exactly one credential into the subprocess. At least one of `AuthToken` or `OAuthToken` must be non-blank. |
| `executor.ClaudeCLIConfig.IngestionPolicy` | `executor.ClaudeIngestionPolicy` | `disabled` | no | Controls Claude-facing web/tool routes. `disabled` denies those events before executor context/tool execution. `reviewed` delegates to `IngestionHarness`. Unknown policy values fail before subprocess start. |
| `executor.ClaudeCLIConfig.IngestionHarness` | `*executorharness.Harness` | nil | yes when `IngestionPolicy == reviewed` | Repo-owned harness that releases Claude-facing web content or tool calls only after broker review. Reviewed policy without a harness fails before subprocess start or web/tool handling. |
| `armor.Config.Command` | argv slice | none | yes when no `armor.Config.Runner` is supplied | External armor-compatible command invoked with JSON stdin/stdout by `armor.ProcessRunner`. Missing or blank command fails closed as a block decision. |
| `armor.Config.Runner` | `armor.Runner` | process runner from `Command` | no | Fakeable invocation seam for tests or service-backed integrations. When nil, the adapter constructs a process runner from `Command`. |
| `armor.Config.Timeout` | `time.Duration` | disabled (`0`) | no | Optional per-candidate armor invocation timeout. Positive values cause timed-out invocations to return a fail-closed block decision. |
| `executorharness.ArmorConfig.Armor` | `armor.Config` | zero value | no | Armor runner/command settings used by `NewArmorGuarded`. Zero value is accepted but fails closed at review time because armor is unavailable. |
| `executorharness.ArmorConfig.BrokerTimeout` | `time.Duration` | disabled (`0`) | no | Optional timeout around the ingestion broker review in the armor-guarded executor harness. Positive values produce fail-closed block decisions on timeout. |
| `executorharness.ArmorConfig.Trace` | `executorharness.TraceRecorder` | nil | no | Optional in-process trace sink for producer-consumer evidence; nil disables trace recording without changing allow/block behavior. |
| `publisher.GitHubCLIConfig.GitPath` | string | `git` | no | Git executable used to push a verified branch to the configured remote. |
| `publisher.GitHubCLIConfig.GHPath` | string | `gh` | no | GitHub CLI executable used to find or create the PR artifact. |
| `publisher.GitHubCLIConfig.Worktree` | path string | none | yes | Target repo worktree used as the command directory for git and GitHub CLI publication. |
| `publisher.GitHubCLIConfig.Remote` | string | none | yes | Git remote name or URL used for `git push`. Blank values fail publication before subprocess start. |
| `publisher.GitHubCLIConfig.GitToken` | secret string | none | no | Optional publication token passed to subprocesses as `GIT_TOKEN` and redacted from surfaced command output. |
| `publisher.GitHubCLIConfig.GitHubToken` | secret string | none | no | Optional publication token passed to subprocesses as `GH_TOKEN` and `GITHUB_TOKEN` and redacted from surfaced command output. |
| `runtime.Config` | struct | none | yes for default `run` | Host-side assembly input for one Phase 0 run. Environment variables are parsed into this struct by `runtime.ConfigFromEnv`; tests may construct it directly. |

---

## Secrets

Sensitive configuration that lives **outside** the repo. Values are never committed —
only the names and where they come from are documented here.

| Secret | Source | Used for |
|--------|--------|----------|
| `ANTHROPIC_API_KEY` | Host environment or sandbox secret store | Claude Code CLI executor auth (API key alternative). The value must be independently revocable and is injected only as a subprocess environment variable when `CLAUDE_CODE_OAUTH_TOKEN` is absent. The executor does not read arbitrary host-home credential files by default; it runs the CLI with temporary `HOME`, `XDG_CONFIG_HOME`, and `XDG_CACHE_HOME` directories and does not log token values. |
| `CLAUDE_CODE_OAUTH_TOKEN` | Host environment or sandbox secret store | Claude Code CLI executor auth (subscription OAuth token, preferred when both credentials are set). Minted by `claude setup-token` on a Claude Pro/Max subscription. The value is injected only as a subprocess environment variable and is independently revocable from the API key. The executor does not read arbitrary host-home credential files by default. |
| `AGENT_BUILDER_GIT_TOKEN` | Host environment or sandbox secret store | git publication token. Host-side publisher receives it as `GIT_TOKEN` (redacted from publisher errors/run records). When vault is enabled (`AGENT_BUILDER_VAULT_BIN` set), the in-box path is brokered: the token is registered with vault and reaches `api.github.com` only via the egress proxy as `Authorization: Bearer`, never as a plaintext env var inside the box. |
| `AGENT_BUILDER_GITHUB_TOKEN` | Host environment or sandbox secret store | GitHub publication token. Host-side publisher receives it as `GH_TOKEN`/`GITHUB_TOKEN` (redacted from publisher errors/run records). When vault is enabled, the in-box path is brokered through vault's proxy identically to `AGENT_BUILDER_GIT_TOKEN`. |
| `VAULT_MASTER_KEY` / `VAULT_MASTER_KEY_FILE` | Host environment or file | Master encryption key for the vault daemon when vault token brokering is enabled. 32-byte hex; never logged; never auto-generated. See the env-var table above. |

**Token retrieval seam:** the four provider/publication secret values are read through the `secrets.SecretSource` interface (`internal/secrets/`). The default implementation, `EnvSecretSource`, reads directly from `os.Getenv`. The `VaultSecretSource` implementation (ADR 036, task 066) registers the git/GitHub tokens with the vault daemon and holds opaque handles; its `ProviderToken()` returns the raw env provider values unchanged (provider brokering deferred) and its `PublisherTokens()` returns `("","")` because the tokens live in vault.

**Named-provider secret resolution (task 088):** the `SecretSource` interface extends the abstraction with `NamedProviderToken(ref string) (string, error)` to resolve per-entry provider secrets at dispatch time (ADR 043). `EnvSecretSource.NamedProviderToken` derives an env-var name from the `ref` by uppercasing and replacing hyphens with underscores, prefixed with `AGENT_BUILDER_SECRET_` (e.g., `"codex-token"` → `AGENT_BUILDER_SECRET_CODEX_TOKEN`). `VaultSecretSource.NamedProviderToken` resolves the ref through vault's existing put/resolve mechanism, returning an opaque handle (never plaintext). Both implementations return `ErrSecretNotFound` when the secret is absent. This allows each registry entry to hold an independently revocable secret keyed by its `SecretRef` — revoking the Gemini key does not touch the Claude token because each is a distinct vault secret or env var.

**Dependency direction:** `runtime → secrets → vault`; `internal/secrets` imports only `internal/vault` (a sibling leaf), and `internal/vault` imports only the standard library — both verified by `go list -deps` (no import cycle).

**In-box token-brokering posture (ADR 036):** when `AGENT_BUILDER_VAULT_BIN` is set, the git/GitHub tokens are no longer present in the execution box's environment; they are injected by the egress proxy at the `api.github.com` boundary. The provider token (`ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN`) continues to be forwarded into the executor subprocess env — provider-token brokering is deferred pending the proxy-feasibility probe (task 066 TC-066-07). When vault is disabled, all four tokens follow the prior env-forwarding path unchanged.

**Rule:** secrets are never pasted into the chat, never logged, and never written into the repo. The `protect-secrets` hook blocks writes to common credential filenames.

---

## Deployment configuration

| Aspect | Value | Notes |
|--------|-------|-------|
| Container image | `localhost/agent-builder/execution-box:033` by default | Built from `containment/execution-box/Containerfile`; override with `EXEC_BOX_IMAGE` |
| Ports exposed | none | The profile exposes no inbound ports and defaults outbound egress to deny |
| Volumes / mounts | `/work` bind mount from the supplied worktree; `/scratch` tmpfs; `/opt/agent-builder/gate-tools` read-only bind mount from `EXEC_BOX_GATE_TOOLS` / `--gate-tools` | Rootfs is read-only; host home and container-engine sockets are not mounted |
| Resource floor (CPU / RAM / disk) | `2` CPU / `2g` memory / `4G` overlay storage quota (when host supports overlay size enforcement; otherwise no writable-layer disk quota) by default | PID limit `256`, shared memory `64m`, scratch tmpfs `512m` |
| OCI runtime tier | `agent` workload -> `runsc`; `dev` workload -> `runc`; explicit `--runtime` wins | Passed to rootless Podman as `--runtime`; accepted values are `runc`, `runsc`, and future `kata` |
| Runtime user/caps | workload: current non-root host uid/gid through `--userns=keep-id`; `--cap-drop=all`; egress sidecar: rootless namespace with `CAP_NET_ADMIN` only | Network administration is isolated to the trusted sidecar; no workload capability add-backs |
| Egress | default-deny; exact host:port allowlist only | Sidecar installs nftables rules before workload start; workload DNS is disabled except launcher-provided host records for allowlisted destinations |
| sandbox-runtime backend | `srt --settings <generated-json> <command...>` | Rented bootstrap isolation behind the `sandbox.Runner` seam; generated settings map exact `host:port` allowlist entries to sandbox-runtime domain allowlists |
| audit-trail block binary | `audit-trail emit` / `audit-trail verify` (resolved via `AGENT_BUILDER_AUDIT_BIN` or `$PATH`) | Optional external runtime dependency. Required only when `AGENT_BUILDER_AUDIT_RECORD` is set: the shipped `audit-trail` block (`github.com/tkdtaylor/audit-trail`) produces the hash-chained action log over the CLI subprocess seam (`audit.BlockSink`). Resolved and the chain path checked writable before dispatch; absence is a fail-fast config error, never a silent skip. |
| policy-engine block binary | `policy-engine serve --socket <path> --allow <egress-csv>` (resolved via `AGENT_BUILDER_POLICY_BIN` or `$PATH`) | Optional external runtime dependency. Required only when `AGENT_BUILDER_POLICY_BIN` is set: the shipped `policy-engine` block runs as a host-side out-of-process daemon and answers the AuthZEN `decide` call before each `sandboxBox.Create` (ADR 038). The daemon is unreachable from inside the execution box (out-of-process invariant — a compromised in-box agent cannot self-grant). Resolved and the daemon started + ping-confirmed before dispatch; a set-but-unresolvable binary or unreachable daemon is a fail-fast config error. The decide call itself is fail-closed (any error → deny). |

---

## Local model registry entry (task 094 evaluation, re-run June 2026)

For operators who wish to use locally-hosted models (e.g., on-premise deployments, low-bandwidth environments, hardware-constrained hosts, or as the quota-free / privacy-preserving backstop per ADR 043), the following describes the evaluated local model setup.

**Retained local models (two entries):** the host keeps **both** finalists, routed per task by the registry/router (ADR 043; tasks 092/095):
- **Qwen3 8B** (`ollama/qwen3:8b`) — the **generalist** local entry (general reasoning, decomposition, docs, non-code sub-tasks).
- **Qwen2.5-Coder 7B** (`ollama/qwen2.5-coder:7b`) — the **coder** local entry (code generation for the coding recipe).

Both are small (~4.7–5.3 GB), VRAM-resident on 8 GB, and fast; keeping both costs little disk and gives the router a code-vs-general choice. All other benchmarked models were removed.

**Hardware evaluated:** NVIDIA RTX 4060 Laptop (8 GB VRAM), Intel Core Ultra 9 185H, 62 GiB RAM, Ubuntu 26.04 LTS

**Benchmark results (task 094 re-run, 2026-06-27) — warm runs, code + general prompts:**

| Model | Type | Code TTFT | Code TPS | VRAM | Disposition |
|-------|------|-----------|----------|------|-------------|
| **Qwen3 8B** | Generalist (+code) | 8.27s | 44.16 | 5.34 GB | **RETAINED — generalist entry** |
| **Qwen2.5-Coder 7B** | Code-specialized | 3.38s | 52.40 | 4.72 GB | **RETAINED — coder entry** |
| Mistral 7B | General | 1.54s | 53.82 | 4.75 GB | removed |
| Llama3.1 8B | General | 2.39s | 49.78 | 5.19 GB | removed |
| Qwen2.5 14B | General | 7.95s | 11.40 | 7.08 GB | removed (low TPS) |

**Selection rationale:** Among the 7–8B candidates, speed and VRAM are **non-differentiators** (all VRAM-resident, 44–54 TPS, sub-8s TTFT once warm) — the earlier TTFT-based elimination was a cold-load artifact. The choice is therefore about capability, which this speed benchmark does not measure. Rather than force a single pick, both finalists are retained: **Qwen3 8B** for its stronger general reasoning (the system-wide quota-free backstop must be a good generalist) and **Qwen2.5-Coder 7B** for its stronger code ability (the coding reference build). The router selects between them per task. See `docs/plans/sprints/094-local-model-benchmark.md` for full methodology.

> **Routing-between-locals is a follow-up (tasks 092/095).** Both local entries have ~zero cost, so the capability/cost-first router cannot yet distinguish "use the coder for code, the generalist for everything else." Exploiting both entries needs a **specialization/domain dimension** on the entry + `RoutingSpec` (e.g. a `code` vs `general` hint) — flagged for ADR 043's router/recipe tasks.

**Translation proxy:** LiteLLM (`pip install 'litellm[proxy]'`).

**Setup (re-runnable):**

1. Install Ollama (https://ollama.ai/) and pull both models: `ollama pull qwen3:8b && ollama pull qwen2.5-coder:7b`
2. Start Ollama: `ollama serve` (http://localhost:11434)
3. Install + start LiteLLM against the desired model, e.g. the coder:
   ```bash
   litellm --model ollama/qwen2.5-coder:7b --api_base http://localhost:11434 --port 8000 --host 127.0.0.1
   ```
4. Point the Claude harness at the proxy (final wiring is tasks 091/092):
   ```bash
   export ANTHROPIC_BASE_URL="http://localhost:8000"
   export ANTHROPIC_AUTH_TOKEN="sk-local"   # any non-empty value; the local proxy ignores it
   ```

**Validation (task 094, REQ-094-02) — honest status:**
- ✓ LiteLLM proxy exposes the Ollama models over an Anthropic/OpenAI-compatible endpoint; raw `curl` round-trips return valid completions.
- ⚠ **The actual `claude` CLI round-trip against the proxy was NOT confirmed** — the CLI appeared to constrain model names. Whether the Claude *harness* truly drives a local model via the proxy (the core ADR-043 local-entry assumption) **must be proven in task 091** (likely via `ANTHROPIC_MODEL` / model aliasing). Do not treat the harness-via-proxy path as validated until then.

**Future improvements:**
- Task 091 will provide dedicated local-entry registry plumbing (replacing manual env-var config)
- Task 092 will integrate the local entry into routing (allowing fallback to local models)
- Periodic re-evaluation recommended as new models are released (benchmark methodology documented for reproducibility)

---

## Defaults policy

Defaults are safe and bounded. The execution-box profile starts from read-only, non-root, no-new-privileges, dropped workload capabilities, no host-home or container-engine socket mounts, explicit resource quotas, default-deny egress, and the agent workload mapped to `runsc`. Overrides may tune quota sizes, choose a different allowlist file, or select `runc`/`kata` explicitly, but must not weaken the underlying containment guarantees without an ADR.

**Graceful degrade exception (ADR 027):** the per-container writable-layer disk quota (`EXEC_BOX_STORAGE_SIZE` / `--storage-opt size=...`) is applied only when the host's rootless overlay container store is backed by an XFS filesystem with project quotas. On non-XFS hosts (ext4 and others) the quota flag is omitted and a `WARNING` is emitted to stderr naming the degraded control. This is an ADR-justified exception: the disk quota is a secondary anti-DoS bound on the ephemeral writable overlay, not a load-bearing trust-boundary control. The load-bearing controls — egress allowlist, read-only rootfs, `--cap-drop=all`, `--security-opt=no-new-privileges`, gVisor (`runsc`), and the CPU/memory/PID/tmpfs caps — are unaffected. An operator restores full enforcement by moving the rootless container store to an XFS volume; no code change is required.
