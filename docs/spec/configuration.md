# Configuration

**Project:** agent-builder
**Last updated:** 2026-06-19 (task 068 — checkpoint signer seam)

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
| `ANTHROPIC_API_KEY` | secret string | none | no (one of two) | Independently revocable Claude Code CLI credential injected into the subprocess environment. The executor accepts this OR `CLAUDE_CODE_OAUTH_TOKEN` (OAuth token preferred when both are set). The executor fails before subprocess start when both are absent. |
| `CLAUDE_CODE_OAUTH_TOKEN` | secret string | none | no (one of two) | Subscription OAuth token alternative to `ANTHROPIC_API_KEY`. When set, it is preferred over the API key and injected into the subprocess environment. Minted by `claude setup-token` on a Claude Pro/Max subscription. The executor fails before subprocess start when both this and `ANTHROPIC_API_KEY` are absent. |
| `AGENT_BUILDER_TASK_ROOT` | path | none | yes for `agent-builder run` | Root containing `docs/plans/roadmap.md` and `docs/tasks/{backlog,active,completed}` for task selection and constrained status writes |
| `AGENT_BUILDER_WORKTREE` | path | none | yes for `agent-builder run` | Target repo worktree passed to the Claude CLI Executor, Gate, and Podman execution-box containment probe |
| `AGENT_BUILDER_CLAUDE_CLI` | path/name | `claude` | no | Claude Code CLI executable used by default run wiring |
| `AGENT_BUILDER_EXEC_BOX_LAUNCHER` | path/name | `containment/execution-box/run.sh` | no | Podman execution-box launcher invoked by the default run containment adapter (`podman.Runner`). Tests may point this at a fake launcher. |
| `AGENT_BUILDER_RUN_RECORD` | path | disabled | no | Host-side RunRecord NDJSON path for `agent-builder run`; blank disables record writing without disabling dispatch |
| `AGENT_BUILDER_AUDIT_RECORD` | path | disabled | no | Hash-chained audit-trail chain logfile for `agent-builder run`. When set, the supervisor projects each action-class lifecycle event (containment, pick, attempt, verify, publish, escalate, finish) through `audit.BlockSink` into this file (one `audit-trail emit` per event) and Seals it before containment teardown. Blank/absent disables the audit chain without disabling dispatch (mirrors `AGENT_BUILDER_RUN_RECORD`). When set, the `audit-trail` binary must resolve and the path must be writable **before dispatch** — an unresolvable binary or unwritable path is a fail-fast configuration error; auditing is never silently skipped. |
| `AGENT_BUILDER_AUDIT_BIN` | path/name | `audit-trail` on `$PATH` | no | The `audit-trail` block binary used to produce the audit chain when `AGENT_BUILDER_AUDIT_RECORD` is set. Falls back to resolving `audit-trail` on `$PATH`. Tests may point this at a prebuilt binary. |
| `AGENT_BUILDER_RUN_TIMEOUT` | duration string | none | yes for `agent-builder run` | Explicit supervisor wall-clock timeout and sandbox request timeout for one default run |
| `AGENT_BUILDER_MAX_ATTEMPTS` | non-negative integer | none | yes for `agent-builder run` | Explicit bounded retry attempt count for the selected task |
| `AGENT_BUILDER_PUBLISH_REMOTE` | string | none | yes for `agent-builder run` | Git remote name or URL used by the branch publisher after Gate success |
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
| `VAULT_MASTER_KEY` | secret string (hex) | none | required when vault is enabled and `VAULT_MASTER_KEY_FILE` is unset | 32-byte hex-encoded master key for the vault daemon's encryption. Validated to decode to exactly 32 bytes; an invalid or short key is a fail-fast error. **Never auto-generated** — absence when vault is enabled fails loud (a silent ephemeral key would lose secrets across restarts). Never logged. |
| `VAULT_MASTER_KEY_FILE` | path | none | alternative to `VAULT_MASTER_KEY` | Path to a file containing the hex master key. **Takes precedence over `VAULT_MASTER_KEY`** when both are set. The file contents are read and validated identically to `VAULT_MASTER_KEY`; the key value is never logged. |

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

**Token retrieval seam:** the four provider/publication secret values are read through the `secrets.SecretSource` interface (`internal/secrets/`). The default implementation, `EnvSecretSource`, reads directly from `os.Getenv`. The `VaultSecretSource` implementation (ADR 036, task 066) registers the git/GitHub tokens with the vault daemon and holds opaque handles; its `ProviderToken()` returns the raw env provider values unchanged (provider brokering deferred) and its `PublisherTokens()` returns `("","")` because the tokens live in vault. The dependency direction is one-way: `runtime → secrets → vault`; `internal/secrets` imports only `internal/vault` (a sibling leaf), and `internal/vault` imports only the standard library — both verified by `go list -deps` (no import cycle).

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
