# Data Model

**Project:** agent-builder
**Last updated:** 2026-06-29 (task 133 — Antigravity harness driver added)

What data exists, how it's structured, where it lives, and what relationships hold between entities. Covers persistent storage, in-memory state, and data-on-the-wire formats.

Not in this file:
- Operations on the data (that's in [behaviors.md](behaviors.md))
- How the data is accessed (that's in [interfaces.md](interfaces.md))
- Tunable parameters (that's in [configuration.md](configuration.md))

---

## Persistent state

agent-builder runs as a **stateless orchestrator process**. It owns no database,
cache, or process-managed datastore — each `agent-builder run` invocation parses its
inputs fresh and discards all in-process state when it returns. The durable artifacts
it produces or mutates live in files outside the process and are owned as follows:

| Durable artifact | Owner / writer | Format & location | Documented in |
|---|---|---|---|
| RunRecord | `internal/supervisor` (single host-side writer per run) | NDJSON file at the configured `RunRecordPath` | [Wire / interchange formats](#wire--interchange-formats) below |
| Task `**Status:**` markers | `tasksource.StatusWriter` (constrained single-line mutation) | The target repo's `docs/tasks/*.md` files | [In-memory state → Task Source](#state-task-source) (`WritableStatus`) |
| Audit chain | the external **audit-trail block**, appended via `audit.BlockSink` | JSONL hash chain at the configured logfile; the block owns the on-disk format | [In-memory state → Audit Sink Seam](#state-audit-sink-seam) |

agent-builder holds only the **typed writer seam** for each — never a schema it owns.
A new durable artifact (a real datastore, an index, a cache) would be documented here
as its own subsection; today there are none.

---

## In-memory state

This is where agent-builder's data actually lives: process-local value types and
seams that exist only for the duration of one `agent-builder run` invocation. Each
entry notes its shape, owner, lifetime, concurrency rules, and bounds.

### State: Verification Gate

- **Shape:** `*gate.Gate` owns an ordered slice of registered `gate.Step` implementations. Each registered step stores the stable step name captured at construction plus the Step implementation.
- **Owner:** callers construct the gate through `gate.New(steps ...Step)` and pass it to the component that needs verification.
- **Lifetime:** process-local; no gate state is persisted. Each `Verify(repoPath)` call returns a fresh `Verdict`.
- **Concurrency rules:** no internal mutation occurs during `Verify` except inside Step implementations; callers choose whether individual Steps are safe to share across goroutines.
- **Bounds:** bounded by the configured step list.

#### Value: `gate.Verdict`

```
field       type                notes
────────────────────────────────────────────────────────────
OK          bool                true only when every executed step passed
Results     []gate.StepResult   ordered outcomes for executed steps
```

- **Identity:** a Verdict is scoped to one `Verify(repoPath)` call.
- **Lifecycle:** created by `Gate.Verify`; consumed by future CLI, agent loop, log, and escalation renderers.
- **Relationships:** `Results` preserves execution order and contains no entries for steps not run after a prior blocking failure.

#### Value: `gate.StepResult`

```
field       type            notes
────────────────────────────────────────────────────────────
Name        string          registered Step name
OK          bool            step pass/fail outcome
Output      string          captured human-readable stdout/stderr or message
Duration    time.Duration   elapsed time measured by the gate
```

- **Identity:** the `Name` is the registered Step name captured by `gate.New`; a Step cannot override it from `Run`.
- **Lifecycle:** produced by one Step execution and appended to the Verdict before the gate continues or short-circuits.
- **Relationships:** belongs to exactly one Verdict.
- **Native Go command output:** `Output` stores combined stdout/stderr for native Go subprocess failures. For `gofmt -l .`, non-empty output stores the listed unformatted files and makes the Step fail even when the subprocess exits zero. Missing native tools store a human-readable lookup failure naming the absent executable.
- **Lint command output:** `Output` stores combined stdout/stderr for `golangci-lint run` failures, including lint findings and configuration/tool errors. Missing `golangci-lint` stores a human-readable lookup failure naming the absent executable.
- **Dependency scan output:** `Output` stores combined stdout/stderr for `dep-scan` failures, including high-or-above CVE findings and scanner/tool errors. When `go.sum` is absent, the Step passes and no scanner is invoked. Missing `dep-scan` (with `go.sum` present) stores a human-readable lookup failure naming the absent executable.
- **Code scan output:** `Output` stores combined stdout/stderr for `code-scanner` failures, including malware, backdoor, credential-harvest, and scanner/tool errors. Missing `code-scanner` stores a human-readable lookup failure naming the absent executable.

### State: Task Source

- **Shape:** `*tasksource.Source` stores an `fs.FS`, one roadmap path, and an ordered list of task directories. It owns no cache and has no write-capable filesystem handle.
- **Owner:** callers construct it with `tasksource.New(fsys, roadmapPath, taskDirs...)`.
- **Lifetime:** process-local; each `Candidates()` or `Next()` call reparses the source files and returns fresh values.
- **Concurrency rules:** no internal mutation occurs after construction.
- **Bounds:** bounded by the number of `.md` task files in the configured directories.

#### Value: `supervisor.Task`

```
field       type      notes
────────────────────────────────────────────────────────────
ID          string    zero-padded task ID from `# Task NNN:`
Repo        string    project/repo name from `**Project:**`
Spec        string    path to the task file the executor must satisfy
PriorFailure string   non-empty only on retry attempt N≥2; formatted gate-failure detail from previous attempt
```

- **Identity:** `ID` is unique across parsed task files.
- **Lifecycle:** produced by task-source parsing and later consumed by the supervisor/agent loop and executor seam. The `PriorFailure` field is populated by the retry loop after a failed gate verification, forwarding formatted failure detail to the next executor attempt.
- **Relationships:** embedded in `tasksource.Candidate`. The first attempt always has `PriorFailure == ""`; subsequent attempts receive formatted gate-failure detail from `loop.FormatFailure` if the previous attempt failed the gate.

### State: Claude CLI Executor

- **Shape:** `*executor.ClaudeCLI` stores a Claude CLI executable path, one target worktree path, one in-memory auth token value supplied at construction time, one effective web/tool ingestion policy, and an optional executor ingestion harness for reviewed routes.
- **Owner:** callers construct it with `executor.NewClaudeCLI` or `executor.NewClaudeCLIFromEnv` and pass it through the `supervisor.Executor` seam.
- **Lifetime:** process-local; no executor state is persisted. Each `Run(Task)` call creates an executor-owned temporary directory for the branch-output file and temporary CLI home/XDG directories, then removes it before returning.
- **Concurrency rules:** no internal synchronization is provided. Callers should give each concurrent task attempt its own executor instance or otherwise ensure the configured worktree is not shared unsafely.
- **Bounds:** one `Run(Task)` call starts at most one Claude CLI subprocess.

#### Value: `executor.ClaudeIngestionPolicy`

```
value       meaning
────────────────────────────────────────────────────────────
disabled    deny Claude-facing web/tool events before executor context or tool execution
reviewed    route Claude-facing web/tool events through the configured executor harness
```

- **Identity:** policy is scoped to one `*executor.ClaudeCLI` instance.
- **Lifecycle:** produced by caller configuration or by `ParseClaudeIngestionPolicy`; copied into `*executor.ClaudeCLI`; not persisted.
- **Default:** blank zero-value configuration normalizes to `disabled` for fail-closed behavior. Text parsing rejects blank or unknown policy strings.

#### Value: `executor.ClaudeCLIConfig`

```
field             type                         notes
────────────────────────────────────────────────────────────
CLIPath           string                       Claude Code CLI path/name; required for explicit config, while `NewClaudeCLIFromEnv` supplies `claude`
Worktree          string                       target task worktree used as subprocess working directory
AuthToken         string                       secret supplied as `ANTHROPIC_API_KEY` in subprocess env
IngestionPolicy   ClaudeIngestionPolicy        web/tool route policy; zero value defaults to `disabled`
IngestionHarness  *executorharness.Harness     required when `IngestionPolicy == reviewed`
```

- **Identity:** configuration is scoped to one executor instance.
- **Lifecycle:** produced by caller configuration, copied into `*executor.ClaudeCLI`, and not persisted.
- **Relationships:** `AuthToken` corresponds to the `ANTHROPIC_API_KEY` secret documented in `configuration.md`. `IngestionHarness` is normally produced by `executorharness.NewArmorGuarded` for reviewed web/tool routes.

#### Value: `supervisor.Result` from `executor.ClaudeCLI`

```
field       type      notes
────────────────────────────────────────────────────────────
Branch      string    non-blank branch name read from the executor-owned branch-output file
OK          bool      true only after successful subprocess exit and branch capture
```

- **Lifecycle:** produced by `(*executor.ClaudeCLI).Run` and consumed by the agent loop.
- **Relationships:** missing, blank, or unavailable branch output produces `OK == false` plus an error, so the loop treats the attempt as failed before Gate verification.

### State: Executor Registry

- **Shape:** `*registry.Catalog` stores an in-process map of executor entries keyed by stable ID, and a deterministic ordering list for stable list operations.
- **Owner:** callers construct it with `registry.NewCatalog()` and add entries via `RegisterEntry(e RegistryEntry)`. The runtime wiring initializes the catalog from `registry.LoadFromEnv()` at dispatch (ADR 043, task 095); when the loader returns no entries, the runtime synthesizes a single default Claude entry (`claude-default`, capability tier 1, cost weight 1) so single-provider deployments still resolve to the Claude CLI Executor.
- **Lifetime:** process-local; no registry state is persisted. Entries are registered before dispatch and remain stable for the duration of a run.
- **Concurrency rules:** read operations (`LookupEntry`, `ListEntries`) use reader locks. Write operations (`RegisterEntry`) use writer locks and panic on duplicate IDs.
- **Bounds:** one `RegisterEntry` call adds at most one entry; duplicate IDs panic.

#### Value: `registry.RegistryEntry`

```
field            type               notes
────────────────────────────────────────────────────────────
ID               string             stable handle, e.g. "claude-oauth", "local-qwen"
Harness          registry.HarnessDriver  which harness runs the loop
CapabilityTier   int                ordered: higher = stronger
CostWeight       int                relative cost per dispatch; lower = cheaper
ModelID          string             model identifier (e.g. "claude-opus-4-5")
Endpoint         string             base URL the harness points at (cloud API or local proxy)
SecretRef        string             which vault secret to resolve (NOT the secret itself)
Budget           registry.QuotaBudget    configured cap over a rolling window, or zero for unlimited
Usage            int                running tally against Budget
Availability     registry.Availability   available or exhausted-until ResetAt
```

- **Identity:** `ID` is the stable registry key.
- **Lifecycle:** parsed from environment by `LoadFromEnv` at runtime startup; populated entries are registered into the Catalog.
- **Relationships:** `SecretRef` names a secret resolved by the vault at dispatch time (never stored in plaintext). `Availability` is mutable state owned by the router and updated as quota signals arrive.

#### Value: `registry.HarnessDriver`

```
value              notes
──────────────────────────────────────────────────────────────────────
claude-cli         Claude Code CLI harness (also used for local models via translation proxy)
codex-cli          Codex CLI harness
gemini-cli         Google Gemini CLI harness (subscription/OAuth)
antigravity-cli    Antigravity (`agy`) CLI harness (subscription/OAuth)
ollama-native      Native Ollama executor harness (direct /api/chat invocation; no translation proxy)
```

- **Identity:** discriminates the executor harness to run.
- **Lifecycle:** set at entry construction from environment; immutable for the entry's lifetime.
- **Relationships:** **one harness can back many entries** — e.g., `claude-cli` backs both `claude-oauth` (cloud) and `local-qwen` (local model via proxy); `ollama-native` backs `local-ollama` and any other Ollama-native entries.

#### Value: `registry.QuotaBudget`

```
field            type               notes
────────────────────────────────────────────────────────────
Limit            int                maximum dispatches over the window; 0 = unlimited
Window           time.Duration      rolling time window (e.g. 5h)
```

- **Identity:** scoped to one entry's quota model.
- **Lifecycle:** parsed from optional env vars `BUDGET_LIMIT` and `BUDGET_WINDOW` at entry construction.
- **Semantics:** `Limit == 0` means no quota cap (typical for local entries). A non-zero `Limit` caps dispatches over a rolling window.

#### Value: `registry.Availability`

```
field            type               notes
────────────────────────────────────────────────────────────
Status           registry.AvailStatus   available or exhausted
ResetAt          time.Time          when the entry becomes available again (for exhausted entries)
```

- **Identity:** scoped to one entry's current availability state.
- **Lifecycle:** initialized to `AvailStatusAvailable` at entry construction; updated by the router as quota signals arrive.
- **Semantics:** `Status == available` means the entry can be selected. `Status == exhausted` means the entry is skipped until the clock passes `ResetAt`.

#### Value: `registry.AvailStatus`

```
value          notes
────────────────────────────────────────────────────────────
available      entry can be selected for dispatch
exhausted      entry is skipped until ResetAt (rate-limited, quota exceeded, or similar)
```

- **Identity:** discriminates current availability.
- **Lifecycle:** set to `available` at entry construction; transitioned to `exhausted` and back by the router as quota signals arrive and reset times pass.

#### Value: `tasksource.Candidate`

```
field           type                notes
────────────────────────────────────────────────────────────
Task            supervisor.Task     executor-facing task shape
Status          tasksource.Status   normalized ready/active/blocked/needs-human/completed state
Dependencies    []string            task IDs that must be completed before this task is ready
```

- **Identity:** inherits `Task.ID`.
- **Lifecycle:** produced by `Source.Candidates`; consumed by `Source.Next`.
- **Relationships:** dependencies must reference parsed candidate IDs. `Next()` treats only `StatusReady` candidates with all dependencies in `StatusCompleted` as actionable.

#### Value: `tasksource.WritableStatus`

```
value          notes
────────────────────────────────────────────────────────────
done           status-only writer marker for completed work; parsed as `StatusCompleted`
blocked        status-only writer marker for blocked work; parsed as `StatusBlocked`
needs-human    status-only writer marker for work that requires human attention; parsed as `StatusNeedsHuman`
```

- **Identity:** the string value is the marker written into a task file's `**Status:**` metadata line.
- **Lifecycle:** provided by callers to `StatusWriter.WriteStatus`; persisted only as the task file status marker.
- **Relationships:** the reader accepts writer-produced markers. `done` is normalized to completed for dependency checks; `needs-human` is non-ready and is skipped by `Next()`.

### State: Supervisor Dispatch

- **Shape:** `*supervisor.Supervisor` stores one configured `supervisor.Task`, one `supervisor.ContainmentBox`, one `supervisor.InBoxLoop`, an optional structured logger, an optional durable run-record path, an optional wall-clock run timeout, and the pre-existing exec-sandbox Runner seam.
- **Owner:** host-side runtime wiring constructs the supervisor through `supervisor.New(options...)`.
- **Lifetime:** process-local; each `Run()` call uses the currently configured task and seams for one dispatch lifecycle. When a run-record path is configured, the supervisor opens and closes one host-side record file during that lifecycle. When a positive timeout is configured, the supervisor starts one deadline for the in-box loop.
- **Concurrency rules:** no internal mutation occurs during `Run`; callers choose whether supplied box, loop, and logger implementations are safe to share across goroutines.
- **Bounds:** one `Run()` call creates at most one box, starts the loop at most once, kills the box at most once on timeout, writes at most one run-record file, and tears down a successfully created box exactly once.

#### Value: `supervisor.BoxHandle`

```
field       type      notes
────────────────────────────────────────────────────────────
ID          string    backend-meaningful created-box identifier
Worktree    string    worktree path visible to the in-box loop
```

- **Identity:** scoped to one successful `ContainmentBox.Create(task)` call.
- **Lifecycle:** produced by the containment-box seam, consumed by the in-box loop, then passed back to the box seam for teardown.
- **Relationships:** belongs to the single task dispatched by the enclosing `Supervisor.Run()` call.

#### Value: `supervisor.ErrRunTimedOut`

- **Identity:** sentinel error returned when a configured wall-clock timeout expires before `InBoxLoop.RunInside` completes.
- **Lifecycle:** produced by `Supervisor.Run()` after the timeout fires and before the run-record terminal event is written. It may be joined with a containment kill error or the killed loop's return error.
- **Relationships:** timeout returns map to `RunOutcomeTimedOut`; ordinary loop errors and recovered panics map to `RunOutcomeFailed`.

#### Value: `supervisor.RunStreams`

```
field       type        notes
────────────────────────────────────────────────────────────
Stdout      io.Writer   streamed stdout from the in-box loop
Stderr      io.Writer   streamed stderr from the in-box loop
Command     io.Writer   streamed command-log lines from the in-box loop
```

- **Identity:** scoped to one `Supervisor.Run()` call and one run-record file.
- **Lifecycle:** created by the supervisor after `ContainmentBox.Create`; passed to `InBoxLoop.RunInside`; closed when the run-record writer is finished before teardown.
- **Relationships:** each writer produces one `RunRecord` event per write. The writers are host-side, so data leaves the ephemeral box during the run instead of being copied back after teardown.

#### Value: `supervisor.RunOutcome`

```
value         notes
────────────────────────────────────────────────────────────
completed     in-box loop returned nil
failed        in-box loop returned an error or panicked
timed-out     configured wall-clock timeout expired and the supervisor attempted to kill the box
```

- **Identity:** the string value is written to the terminal `RunRecord` line.
- **Lifecycle:** produced by `Supervisor.Run()` when the terminal run record is written and then consumed by humans, tests, and future audit tooling.
- **Relationships:** `timed-out` is distinct from `failed`; a fast in-box loop error before the deadline is recorded as `failed`, not `timed-out`.

#### Value: `supervisor.Message`

```
field       type                     notes
────────────────────────────────────────────────────────────
Kind        supervisor.MessageKind   how the control loop dispatches this message
GoalID      string                   addresses status/info/cancel/confirm; the new goal's ID for new-goal
Goal        supervisor.Task          populated for MsgNewGoal
Text        string                   info payload / free-form
```

- **Identity:** a single inbound command message.
- **Lifecycle:** read from `MessageSource` by the orchestrator control loop.
- **Relationships:** `Kind` determines how the control loop handles this message.

#### Value: `supervisor.MessageKind`

An int enum representing the type of inbound operator message:
- `MsgNewGoal` (0): a fresh goal to plan
- `MsgStatus` (1): queries lifecycle state
- `MsgInfo` (2): carries new information for an in-flight goal
- `MsgCancel` (3): cancels a goal and tears down in-flight workers
- `MsgConfirm` (4): signals that clarification is complete and the orchestrator should proceed to planning (ADR 058)

### State: Default Run Wiring

- **Shape:** `runtime.Config` stores the task root, target worktree, Claude CLI executable, Claude token, sandbox-runtime executable, optional RunRecord path, supervisor timeout, max-attempt bound, publish remote, git/GitHub CLI paths, and optional publication tokens for one `agent-builder run` invocation.
- **Owner:** `internal/cli` calls `runtime.RunFromEnv` for the default `run` subcommand path. Tests may construct `runtime.Config` directly or invoke the binary with fake process shims.
- **Lifetime:** process-local; parsed once per CLI invocation and discarded after the run returns.
- **Concurrency rules:** one invocation owns one selected task and one configured worktree. Concurrent runs require external branch/worktree isolation.
- **Bounds:** one invocation selects at most one ready task. If no task is ready, it returns idle before creating a box, running an Executor, or publishing a branch.

#### Value: `runtime.Config`

```
field           type             notes
────────────────────────────────────────────────────────────
TaskRoot        string           root containing roadmap and task files
Worktree        string           target repo worktree for Executor, Gate, and sandbox probe
ClaudeCLI       string           executable path/name; blank environment defaults to claude
ClaudeToken     string           token injected as ANTHROPIC_API_KEY
SandboxRuntime  string           srt executable path/name for sandbox-runtime adapter
RunRecordPath   string           optional NDJSON output path
RunTimeout      time.Duration    explicit wall-clock bound for one supervisor run
MaxAttempts     int              non-negative retry attempt bound
PublishRemote   string           git remote name or URL used for branch push
GitCLI          string           git executable path/name; blank environment defaults to git
GitHubCLI       string           gh executable path/name; blank environment defaults to gh
GitToken        string           optional git publication token
GitHubToken     string           optional GitHub CLI publication token
```

- **Lifecycle:** produced by `runtime.ConfigFromEnv` or tests, consumed by `runtime.Run`.
- **Relationships:** composes `tasksource.Source`, `executor.ClaudeCLI`, production `gate.Gate`, `sandboxruntime.Runner`, supervisor dispatch seams, `loop.RetryingLoop`, `tasksource.StatusWriter`, and `publisher.GitHubCLI`.
- **Failure behavior:** missing required fields fail as configuration errors before Executor attempt. Gate failures after an Executor attempt are surfaced through RunRecord stderr evidence and a failed terminal outcome. Publication failures after Gate success are surfaced through RunRecord stderr evidence and a failed terminal outcome without writing `done`.

### State: Branch Publisher

- **Shape:** `*publisher.GitHubCLI` stores git and GitHub CLI executable paths, the target worktree, the publish remote, and optional in-memory git/GitHub token values.
- **Owner:** default run wiring constructs the publisher through `publisher.NewGitHubCLI` and passes it to the in-box retry wiring.
- **Lifetime:** process-local; no publisher state is persisted. Each `Publish` call starts at most one `git push`, one `gh pr view`, and, when no existing PR is found, one `gh pr create`.
- **Concurrency rules:** no internal synchronization is provided. Callers should give each concurrent task run its own publisher instance or otherwise ensure the configured worktree is not shared unsafely.
- **Bounds:** one `Publish` call operates on exactly one branch and one remote.

#### Value: `publisher.Request`

```
field       type              notes
────────────────────────────────────────────────────────────
Task        supervisor.Task   task whose branch is being published
Worktree    string            target repo worktree used as command directory
Branch      string            non-blank verified executor branch
Remote      string            non-blank git remote name or URL
```

- **Identity:** scoped to one publication attempt for one task branch.
- **Lifecycle:** produced by runtime wiring only after Executor success and Gate pass; consumed by `publisher.Publisher`.
- **Relationships:** `Branch` is copied from `loop.RetryOutcome.Branch`. Blank branch and blank remote fail before a git or GitHub CLI command is invoked.

#### Value: `publisher.Result`

```
field       type      notes
────────────────────────────────────────────────────────────
Branch      string    published branch
PRURL       string    PR URL when the publisher can parse one
PRID        string    PR identifier or PR number when available
```

- **Lifecycle:** produced by the publisher after push plus existing-PR lookup or PR creation; consumed by runtime wiring for stdout and RunRecord evidence.
- **Relationships:** `PRURL` or `PRID` is the PR artifact recorded for Phase 0 branch publication.

### State: Agent Loop

- **Shape:** `*loop.Loop` stores a `loop.TaskSource`, a `supervisor.Executor`, a `supervisor.Gate`, and the target worktree path supplied at construction.
- **Owner:** inside-the-box runtime wiring constructs the loop from the stable seams.
- **Lifetime:** process-local; each `RunOnce()` call returns a fresh `Outcome`.
- **Concurrency rules:** no internal mutation occurs during `RunOnce`; callers choose whether the supplied source, executor, and gate implementations are safe to share.
- **Bounds:** one `RunOnce()` call attempts at most one task and runs at most one gate verification.

#### Value: `loop.State`

```
value      notes
────────────────────────────────────────────────────────────
pick       task-source selection is running
attempt    executor attempt is running for the picked task
verify     gate verification is running against the configured worktree path
advance    gate passed and the cycle can advance to the next task
```

- **Identity:** states are ordered entries in an `Outcome.Trace`.
- **Lifecycle:** produced during one `RunOnce()` call.
- **Relationships:** `advance` appears only in a done outcome after a passing Gate verdict.

#### Value: `loop.Outcome`

```
field       type              notes
────────────────────────────────────────────────────────────
Kind        OutcomeKind       idle, done, or fail
Task        supervisor.Task   picked task, empty only for idle/no-task
Branch      string            executor branch; required for done, optional diagnostic for fail
Verdict     gate.Verdict      gate result when verification ran
Failure     loop.Failure      policy-free failure diagnostics for fail outcomes
Trace       []loop.State      observed state sequence for this cycle
```

- **Identity:** one Outcome belongs to one `RunOnce()` call.
- **Lifecycle:** produced by the loop and returned to the caller for status, escalation, or runtime wiring.
- **Relationships:** `OutcomeDone` carries the branch returned by the Executor and a passing Verdict. `OutcomeFail` can carry executor error diagnostics or a failing Verdict, but it carries no retry count, retry decision, or escalation target. `OutcomeIdle` records only the pick state and calls neither Executor nor Gate.

#### Value: `loop.Failure`

```
field       type                 notes
────────────────────────────────────────────────────────────
Reason      FailureReason        executor-error, executor-incomplete, gate-fail, or blocked-action
Err         error                optional executor error preserved for the policy consumer
Blocked     *BlockedAction       non-nil ONLY when Reason == blocked-action (ADR 055 seam 4, task 121); names the denied resource/action + reason
```

- **Identity:** meaningful only when `Outcome.Kind == OutcomeFail`.
- **Lifecycle:** produced by the loop when attempt or verify does not complete successfully.
- **Relationships:** retry and escalation policy is intentionally absent; the escalation policy consumer decides next action.

#### Value: `loop.BlockedAction` (ADR 055 seam 4, task 121)

```
field       type     notes
────────────────────────────────────────────────────────────
Resource    string   the denied policy resource ID (e.g. the recipe name)
Action      string   the denied policy action (e.g. "spawn-worker")
Reason      string   the human-readable deny reason
```

- **Identity:** describes one policy denial of a NECESSARY action; carried by a `loop.Failure` with `Reason == FailureBlockedAction` and by `orchestrator.SubGoalOutcome.Blocked`.
- **Lifecycle:** produced at the orchestrator's spawn-worker gate (`classifyBlockedSpawn` / `loop.ClassifyBlockedAction`) on a non-allow decision for a needed sub-goal; consumed by `loop.ReevaluationPolicy`.
- **Relationships:** carries NO allow set and grants nothing — it only describes the denial. A blocked action with an empty Resource AND Action AND Reason is rejected (`ErrEmptyBlockedAction`).

#### Value: `loop.Reevaluation` / `loop.ReevaluationOutcome` / `loop.Escalation` (ADR 055 seam 4, task 121)

```
Reevaluation:
field             type       notes
────────────────────────────────────────────────────────────
AllowedResources  []string   the FRESH re-derived plan's allow set (Plan.AllowedResources)
StillBlocked      bool       whether the re-derived plan STILL needs the denied resource

ReevaluationOutcome:
field             type                          notes
────────────────────────────────────────────────────────────
Kind              ReevaluationOutcomeKind       "resolved" (replan routed around) or "escalated" (bound exhausted)
Reevaluations     int                           number of replans performed (0..MaxReevaluations)
AllowedResources  []string                      the allow set APPLIED for the last attempt — always the re-derived set, NEVER previous ∪ denied
Escalation        Escalation                    populated only when Kind == escalated
StatusWrite       tasksource.StatusWriteResult  the needs-human write (only on escalation)

Escalation:
field             type                          notes
────────────────────────────────────────────────────────────
Status            tasksource.WritableStatus     always needs-human for a blocked-action escalation
Blocked           BlockedAction                 the denied action + reason surfaced to the human
Reevaluations     int                           how many replans were attempted before escalating
```

- **Identity:** the bounded-reevaluation result for one blocked action.
- **Lifecycle:** `ReevaluationPolicy.ReevaluateBlocked` replans up to `MaxReevaluations`; on exhaustion-with-still-blocked it writes `needs-human` once and returns an `Escalation`.
- **Relationships:** the never-self-grant invariant is structural — the applied `AllowedResources` is exactly the replanner's re-derived `Plan.AllowedResources`; there is no field or code path that unions the previous set with the denied resource.

#### Value: `orchestrator.SubGoalOutcome` (ADR 055 seam 4, task 123)

```
field                    type                        notes
────────────────────────────────────────────────────────────
SubGoal                  string                      the sub-goal spec text (Task.Spec)
Recipe                   string                      the recipe used for dispatch
Success                  bool                        whether the worker dispatch succeeded
Detail                   string                      branch/PR on success, failure reason on failure
Blocked                  *loop.BlockedAction         non-nil ONLY when dispatch failed due to a policy deny on a NECESSARY sub-goal (task 121)
ReevaluationOutcome      loop.ReevaluationOutcome    the bounded-reevaluation result on a blocked outcome (task 123); zero-valued when Blocked == nil
```

- **Identity:** the typed outcome of dispatching one sub-goal from a plan.
- **Lifecycle:** produced by `orchestrator.dispatchOne` and aggregated by `orchestrator.dispatchPlan`; carried in a `PlanResult` and rendered to the operator.
- **Relationships:** `Blocked` is non-nil only when `Success == false` and the failure is specifically a policy denial on a necessary action. `ReevaluationOutcome` is populated post-join by `dispatchPlan` when `Blocked != nil` and a `StatusWriter` is configured, folding the result of bounded reevaluation into the outcome. The never-self-grant invariant is preserved end-to-end: `ReevaluationOutcome.AllowedResources` is exactly the re-derived plan's set, never unioning the previous plan's allow set with the denied resource.

### State: Ingestion Boundary

- **Shape:** `internal/ingestion` owns immutable-by-convention value types for web-content candidates, tool-call candidates, guard decisions, and broker reviews. Constructors copy mutable bytes/JSON before returning candidates.
- **Owner:** inside-the-box producer code constructs candidates before adding web-ingested content to executor context or before executing a tool call. The broker consumes the candidates and a configured `Guard`.
- **Lifetime:** process-local; no candidate, decision, or review is persisted by this package. Future run-record/audit integration consumes decision metadata separately.
- **Concurrency rules:** candidate and decision values are pass-by-value. Callers choose whether the configured `Guard` implementation is safe to share across goroutines.
- **Bounds:** one broker review evaluates exactly one candidate and returns exactly one decision.

#### Value: `ingestion.ContentCandidate`

```
field          type                  notes
────────────────────────────────────────────────────────────
ID             ingestion.CandidateID stable correlation ID, caller-supplied or deterministically derived
Content        []byte                attacker-reachable content bytes copied at construction
SourceURI      string                normalized http/https source URI
MediaType      string                explicit media type; blank input becomes application/octet-stream
RetrievedAt    time.Time             retrieval timestamp or zero value when unavailable
Provenance     ingestion.Provenance  task/executor origin metadata
```

- **Identity:** `ID` joins the content candidate to its guard decision.
- **Lifecycle:** produced by `NewContentCandidate`; consumed by `Broker.ReviewContent`.
- **Relationships:** a valid content candidate must be reviewed before release to executor context.

#### Value: `ingestion.ToolCallCandidate`

```
field          type                  notes
────────────────────────────────────────────────────────────
ID             ingestion.CandidateID stable correlation ID, caller-supplied or deterministically derived
ToolName       string                non-blank requested tool name
Arguments      json.RawMessage       compact, valid JSON arguments copied at construction
TargetURI      string                optional normalized http/https target URI
Provenance     ingestion.Provenance  task/executor origin metadata
```

- **Identity:** `ID` joins the tool-call candidate to its guard decision.
- **Lifecycle:** produced by `NewToolCallCandidate`; consumed by `Broker.ReviewToolCall`.
- **Relationships:** a valid tool-call candidate must be reviewed before execution.

#### Value: `ingestion.Decision`

```
field          type                       notes
────────────────────────────────────────────────────────────
CandidateID    ingestion.CandidateID      candidate being decided
Kind           ingestion.CandidateKind    content or tool-call
Outcome        ingestion.DecisionOutcome  allow, block, or quarantine
Reason         string                     guard/fail-closed reason
Metadata       map[string]string          guard-specific decision metadata
```

- **Identity:** `(CandidateID, Kind)` must match the reviewed candidate for the broker to accept the decision.
- **Lifecycle:** produced by a `Guard` or by the broker's fail-closed path; consumed by review `Release` helpers and future audit/run-record code.
- **Relationships:** `allow` is the only outcome that releases candidate data. `block` and `quarantine` preserve decision metadata but do not release candidate data.

### State: Executor Ingestion Harness

- **Shape:** `internal/executorharness` owns event input structs for executor-facing web content and tool calls, result structs that expose the produced candidate and broker decision, opaque release values for allowed candidates, and optional trace events for producer-consumer evidence.
- **Owner:** inside-the-box executor-facing wiring constructs a harness with an `ingestion.Broker` and passes web/tool events through it before continuation or execution.
- **Lifetime:** process-local. Events, results, release values, and trace events are not persisted by this package.
- **Concurrency rules:** `Harness` is pass-by-value and contains the caller-supplied broker and trace recorder. Callers choose whether those collaborators are safe to share across goroutines.
- **Bounds:** one harness call constructs one candidate, performs one broker review, and invokes at most one continuation or tool executor.

#### Value: `executorharness.WebContentEvent`

```
field          type                  notes
────────────────────────────────────────────────────────────
ID             ingestion.CandidateID optional caller-supplied correlation ID
Content        []byte                executor-facing web content bytes
SourceURI      string                required http/https source URI
MediaType      string                optional media type; blank becomes ingestion default
RetrievedAt    time.Time             retrieval timestamp or zero value when unavailable
Provenance     ingestion.Provenance  task/executor origin metadata
```

- **Lifecycle:** produced by executor-facing web ingestion code; consumed by `Harness.HandleWebContent`.
- **Relationships:** maps directly to `ingestion.ContentInput`.

#### Value: `executorharness.ToolCallEvent`

```
field          type                  notes
────────────────────────────────────────────────────────────
ID             ingestion.CandidateID optional caller-supplied correlation ID
ToolName       string                requested tool name
Arguments      json.RawMessage       requested tool arguments
TargetURI      string                optional http/https target URI
Provenance     ingestion.Provenance  task/executor origin metadata
```

- **Lifecycle:** produced by executor-facing tool-call code; consumed by `Harness.HandleToolCall`.
- **Relationships:** maps directly to `ingestion.ToolCallInput`.

#### Value: `executorharness.ContentRelease` / `executorharness.ToolCallRelease`

- **Shape:** opaque values with unexported validity state and accessor methods that return copied candidate data.
- **Identity:** each valid release corresponds to one broker-reviewed `allow` candidate.
- **Lifecycle:** produced only by `Harness.HandleWebContent` or `Harness.HandleToolCall` after broker release; consumed by caller-supplied continuation or executor callbacks.
- **Relationships:** zero-value or externally constructed releases are invalid and return `ErrUnreviewedRelease`.

#### Value: `executorharness.ArmorConfig`

```
field          type                  notes
────────────────────────────────────────────────────────────
Armor          armor.Config          external armor runner/command/timeout configuration
BrokerTimeout  time.Duration         optional timeout around the broker review call
Trace          TraceRecorder         optional producer-consumer trace sink
```

- **Lifecycle:** produced by inside-the-box runtime wiring and consumed by `executorharness.NewArmorGuarded`.
- **Relationships:** composes `armor.NewGuard`, `ingestion.NewBroker`, and `executorharness.New` into one armor-backed harness.
- **Failure behavior:** missing or failing armor configuration is preserved as a fail-closed broker decision, not a constructor error.

### State: Audit Sink Seam

- **Shape:** `internal/audit` owns a typed, closed-enum `AuditAction` constant set, the `AuditEvent` value type, the `Sink` interface, the `BlockSink` production CLI adapter, and the in-process `FakeSink`. Supervisor wiring (task 041) is a separate component.
- **Owner:** supervisor-side wiring constructs a `Sink` and passes it through the seam. Tests use `FakeSink`.
- **Lifetime:** process-local per run. No event or seal is persisted by the seam itself; persistence is the responsibility of the `BlockSink` production implementation, which appends to the audit-trail block's JSONL log file via subprocess.
- **Concurrency rules:** no internal synchronization is provided by `FakeSink` or `BlockSink`. Callers must serialize access or supply their own locking.
- **Isolation:** `internal/audit` is a strict leaf package — it imports no executor, LLM, or web-fetch packages (enforced by the F-005 fitness check, task 042).

#### Value: `audit.AuditAction` (closed enum)

```
constant              string value        notes
───────────────────────────────────────────────────────────────────
ActionContainment     "containment"       containment box created; launcher identity recorded
ActionPick            "pick"              task selected from the task source
ActionAttempt         "attempt"           executor attempt started for the picked task
ActionVerify          "verify"            gate verification started; verdict required
ActionPublish         "publish"           branch pushed and PR artifact recorded
ActionEscalate        "escalate"          retry exhausted; status written as needs-human
ActionFinish          "finish"            run lifecycle complete; outcome recorded
ActionPolicyDecision  "policy-decision"   policy engine decision recorded; emitted by audit_emit obligation (task 073)
ActionChannelReject   "channel-reject"    secure channel (Telegram/worker transport) rejected a message; emitted with reason in EventDetail.Reason (task 080)
ActionTamper          "tamper"            memory-guard delete-verify reported tamper; EventDetail.TamperDetected=true (task 084)
ActionGoalIntake      "goal-intake"       orchestrator accepted a goal for planning (fleet-audit, task 085)
ActionPlanDecided     "plan-decided"      orchestrator issued the spawn-plan decision (fleet-audit, task 085)
ActionSpawnDecided    "spawn-decided"     orchestrator issued a per-sub-goal spawn-worker decision; EventDetail.PolicyDecision=allow/deny, Reason=recipe (fleet-audit, task 085)
ActionCompletion      "completion"        orchestrator finished aggregating the plan result (fleet-audit, task 085)
```

The last four (`goal-intake`/`plan-decided`/`spawn-decided`/`completion`) are the
**Tier-1 orchestrator's fleet-audit events** (task 085 / ADR 050 §4). They append to
the SAME `audit.Sink` chain the workers write to, so a single chain is tamper-evident
across both tiers; `audit-trail verify` validates the combined chain.

- **Closed:** `AuditAction.Valid()` returns false for any value not in the constant set above. Raw stdout/stderr actions do not exist in this taxonomy (raw output stays in the 019 RunRecord).
- **Identity:** the string value is the stable `action` field used in the block's `emit` wire format.

#### Value: `audit.AuditEvent`

```
field       type              notes
─────────────────────────────────────────────────────────────
Action      AuditAction       required; must be Valid()
RunID       string            run correlation ID (e.g. "NNN/box-NNN")
TaskID      string            task being acted on
Verdict     AuditVerdict      required for ActionVerify; "pass" or "fail"
Outcome     AuditOutcome      optional for ActionFinish; "completed", "failed", or "timed-out"
Detail      EventDetail       optional typed structured context; fields relevant per action
```

- **Identity:** an event is scoped to one run and one action occurrence; no primary key is assigned at this layer.
- **Lifecycle:** constructed by supervisor wiring, validated by `audit.Validate`, and appended to a `Sink` implementation.
- **Relationships:** maps onto the `audit-trail` block's `emit` wire fields (see ADR 026 mapping table).
- **Validation rule:** `audit.Validate(ev)` returns a `*ValidationError` naming the offending field when `Action` is unset/unknown or when a `verify` event lacks a valid `Verdict`. A valid optional-field absence (e.g., a `pick` event with no `Detail`) passes validation.

#### Value: `audit.EventDetail`

```
field             type      notes
─────────────────────────────────────────────────────────────────────────────
Launcher          string    containment launcher path (containment events)
Branch            string    executor-produced branch (publish events)
Remote            string    git remote used for publication (publish events)
Attempt           int       1-based attempt number (attempt/escalate events)
PolicyDecision    string    policy engine decision string — "allow", "deny", or "require_approval" (policy-decision events; task 073)
PolicyReason      string    human-readable reason from the policy engine response (policy-decision events; task 073)
Reason            string    free-text reason for rejection or diagnostic events, e.g. "unknown_key", "replay_detected", "armor_blocked" (channel-reject events; task 080)
```

- **Identity:** embedded in `AuditEvent`; carries only the non-zero fields relevant to the action. `PolicyDecision` and `PolicyReason` are set only for `ActionPolicyDecision` events; `Reason` is set only for `ActionChannelReject` events; they are zero-valued on all other event types.
- **Lifecycle:** constructed at the call site with named fields (no `map[string]any`).
- **Channel-reject note:** `ActionChannelReject` events are emitted to the `audit.Sink` seam by secure channel implementations (Telegram adapter in task 080, orchestrator↔worker transport in task 083). Serialization of `Reason` to the audit-trail block's CLI format is deferred to orchestrator integration (task 081) — no live `BlockSink` path today silently drops the field.

#### Value: `audit.AuditVerdict`

```
constant      string value   notes
────────────────────────────
VerdictPass   "pass"         gate verification passed
VerdictFail   "fail"         gate verification failed
```

#### Value: `audit.AuditOutcome`

```
constant          string value   notes
──────────────────────────────────────────────────────────────
OutcomeCompleted  "completed"    in-box loop returned nil
OutcomeFailed     "failed"       in-box loop returned error or panicked
OutcomeTimedOut   "timed-out"    configured wall-clock timeout expired
```

#### Value: `audit.VerifyResult`

```
field       type      notes
─────────────────────────────────────────────────────────────
Valid       bool      true when the block reported "valid": true (chain intact)
TamperedAt  *int      seq of the first tampered entry, or nil when intact / unknown
Message     string    human-readable message from the block's verify response
```

- **Identity:** scoped to one `VerifyChain` call.
- **Lifecycle:** produced by `audit.VerifyChain` or `audit.VerifyChainWithRunner`; consumed by gate-severity logic or tests.
- **Relationships:** `IsTampered()` returns `!Valid` and is the block-severity gate predicate. A `Valid == false` result with `TamperedAt != nil` names the sequence number the block localized.
- **Detection boundary:** the tamper detection algorithm (RFC 8785 JCS canonicalization, first-broken-link `prev_hash` chain walk, edit/reorder/truncation classification) is entirely owned by the `audit-trail` block. The agent-builder `VerifyResult` carries only what the block reports. For the authoritative detection contract see `docs/CONTRACT.md` in `github.com/tkdtaylor/audit-trail`.

#### Sentinel: `audit.ErrVerifierUnavailable`

- **Identity:** sentinel error returned when `VerifyChain` cannot invoke the verifier or parse its response (binary missing, non-executable, logfile unreadable, or unparseable output).
- **Semantics:** distinct from a clean `Valid == false` result. `ErrVerifierUnavailable` means "we could not produce a verdict"; `Valid == false` means "the block ran and detected a tamper". Callers use `errors.Is(err, audit.ErrVerifierUnavailable)` to distinguish the two failure modes. An unavailable verifier is never reported as valid.

#### Component: `audit.BlockSink`

- **Shape:** `*audit.BlockSink` implements `audit.Sink` by mapping each `AuditEvent` onto one `audit-trail emit` CLI subprocess call. The block owns the on-disk chain format (JSONL, SHA-256 hash chain, RFC 8785 canonicalization, genesis sentinel); agent-builder owns only the typed-event→argv mapping.
- **Construction:** `audit.NewBlockSink(binPath, logfile)` for production; `audit.NewBlockSinkWithRunner(logfile, runner)` for tests (injectable `ExecRunner` seam records argv without I/O).
- **Frozen CLI contract:** `docs/CONTRACT.md` in `github.com/tkdtaylor/audit-trail`. The on-disk chain format is owned entirely by the block and is not documented here — see the contract file for the authoritative format.
- **Sealed state:** after `Seal()`, further `Append` calls return `ErrAfterSeal`; `Seal` is idempotent.
- **Error handling:** missing/non-executable binary, non-zero block exit, or unparseable `{seq,hash}` response each produce a non-nil named error; the adapter never degrades to a silent no-op when a logfile path is configured.

#### Mapping: `AuditEvent` → `audit-trail emit` CLI argv (v0 CLI path)

The v0 transport is a CLI subprocess per event. The IPC-socket transport (`audit-trail serve --socket`) is deferred per ADR 026 Option B and is not part of this mapping.

| `AuditEvent` field | CLI flag | Notes |
|---|---|---|
| `Action` (enum string) | `-action <verb>` | e.g. `containment`, `pick`, `attempt`, `verify`, `publish`, `escalate`, `finish` |
| constant `"agent-builder"` + `"/" + RunID` | `-actor <id>` | emitting identity + run correlation |
| launcher / task id / branch@remote | `-target <resource>` | resource the action touched; see target derivation below |
| `Verdict` (verify) / `Outcome` (finish) | `-decision <d>` | only emitted when the event carries a verdict or outcome; absent for all other actions |
| `-logfile <path>` | `-logfile <path>` | JSONL log file path; block appends and reconstructs chain state on open |

**Target derivation:**

| Action | `-target` value |
|---|---|
| `containment` | `Detail.Launcher` (e.g. `podman`) |
| `publish` | `Detail.Branch + "@" + Detail.Remote` |
| all others | `"task/" + TaskID` |

**Deferred fields (IPC transport, not v0 CLI):**

- `context` (integer/string values from RunID, TaskID, Detail sub-fields) — block convention accepts this on the IPC path only; the v0 CLI has no `-context` flag.
- `refs` (attestation references) — IPC path only; deferred per ADR 026 Option B.
- `ts` — the block sets this itself on each `emit` call; agent-builder does not pass `-ts`.

### State: Armor Guard Adapter

- **Shape:** `internal/armor.Guard` owns one external invocation runner and an optional timeout. `ProcessRunner` invokes an armor-compatible command with JSON stdin and parses JSON stdout. Tests can supply an in-process `Runner`.
- **Owner:** inside-the-box runtime wiring constructs the adapter and passes it to `ingestion.NewBroker`.
- **Lifetime:** process-local; no adapter request or response is persisted by this package.
- **Concurrency rules:** no internal mutable state is required by `Guard`; callers choose whether a supplied `Runner` implementation is safe to share.
- **Bounds:** one guard decision invokes at most one external armor request.

#### Value: `armor.Request`

```
field          type                  notes
────────────────────────────────────────────────────────────
candidate_id   string                ingestion candidate correlation ID
kind           string                content or tool-call
content        string                content candidate bytes represented as text for armor-compatible JSON
source_uri     string                content source URI when applicable
media_type     string                content media type when applicable
tool_name      string                requested tool name when applicable
arguments      json.RawMessage       tool-call arguments when applicable
target_uri     string                tool-call target URI when applicable
provenance     map[string]string     task_id and executor metadata when available
```

- **Identity:** `(candidate_id, kind)` joins the external request to the ingestion decision.
- **Lifecycle:** produced by `armor.Guard`; consumed by an external command/service runner.
- **Relationships:** maps directly from `ingestion.ContentCandidate` or `ingestion.ToolCallCandidate`.

#### Value: `armor.Response`

```
field       type                    notes
────────────────────────────────────────────────────────────
decision    string                  allow/clean/pass, block/flag/deny, quarantine, or error/fail
reason      string                  optional guard reason
findings    []armor.Finding         guard findings that become decision metadata
warnings    []string                non-blocking warnings preserved in metadata
metadata    map[string]string       external guard metadata copied into the decision
```

- **Identity:** scoped to one external armor invocation.
- **Lifecycle:** produced by the runner; consumed by `armor.Guard`.
- **Relationships:** findings force `allow`-like responses to fail closed as `block`; malformed decisions fail closed as `block`.

#### Value: `armor.Finding`

```
field       type      notes
────────────────────────────────────────────────────────────
category    string    finding category such as prompt-injection, exfiltration, unsafe-tool-call
severity    string    external guard severity label
message     string    human-readable finding reason
```

- **Lifecycle:** produced inside `armor.Response`; copied into `ingestion.Decision.Metadata`.
- **Relationships:** categories and severities are sorted/deduplicated in decision metadata for deterministic assertions.

### State: Retry Escalation Policy

- **Shape:** `*loop.RetryingLoop` stores a `loop.TaskSource`, current `supervisor.Executor`, `supervisor.Gate`, target worktree path, `loop.StatusWriter`, and `loop.RetryPolicy`.
- **Owner:** inside-the-box runtime wiring constructs it around the policy-free loop seams.
- **Lifetime:** process-local; each `RunOnce()` call picks at most one task and returns a fresh `RetryOutcome`.
- **Concurrency rules:** no internal synchronization is provided. Callers choose whether supplied source, Executor, Gate, status writer, and hook implementations are safe to share.
- **Bounds:** one `RunOnce()` call performs no more than `RetryPolicy.MaxAttempts` Executor attempts. `MaxAttempts == 0` performs no Executor or Gate attempt.

#### Value: `loop.RetryPolicy`

```
field          type                  notes
────────────────────────────────────────────────────────────
MaxAttempts    int                   non-negative bound for one picked task
Escalate       loop.EscalationHook   called only after failed non-terminal attempts
```

- **Identity:** scoped to one retrying loop configuration.
- **Lifecycle:** constructed by `loop.NewRetryPolicy`; consumed by `loop.NewRetryingLoop`.
- **Relationships:** the hook may return the same Executor for bootstrap or a different Executor for router-like escalation.

#### Value: `loop.EscalationRequest`

```
field             type                  notes
────────────────────────────────────────────────────────────
Task              supervisor.Task       picked task being retried
Attempt           int                   1-based failed attempt number
Outcome           loop.Outcome          policy-free failed attempt outcome
CurrentExecutor   supervisor.Executor   executor that produced the failed attempt
```

- **Identity:** produced once for each failed attempt that still has another retry remaining.
- **Lifecycle:** passed to `loop.EscalationHook`; not persisted.
- **Relationships:** the returned Executor becomes the producer for the next attempt.

#### Value: `loop.RetryOutcome`

```
field          type                         notes
────────────────────────────────────────────────────────────
Kind           loop.RetryOutcomeKind        idle, done, or escalated
Task           supervisor.Task              picked task, empty only for idle
Branch         string                       successful branch for done
Attempts       int                          number of Executor attempts performed
LastOutcome    loop.Outcome                 final policy-free attempt outcome
StatusWrite    tasksource.StatusWriteResult result of terminal needs-human write
Advanced       bool                         true when caller can advance past task
Terminal       bool                         true when this retry cycle is complete
```

- **Identity:** one RetryOutcome belongs to one `RetryingLoop.RunOnce()` call.
- **Lifecycle:** produced by the retrying loop and consumed by runtime wiring or tests.
- **Relationships:** `Kind == done` carries a successful branch and no status write. `Kind == escalated` carries the exhausted task and the status-writer result. `Kind == idle` carries no task and performs no side effects.

---

## Wire / interchange formats

Data formats agent-builder exchanges across process boundaries. Each is a stable
contract, versioned like an API.

### Format: RunRecord NDJSON

- **Producer:** `internal/supervisor` writes the file on the trusted host side during one `Supervisor.Run()` dispatch lifecycle.
- **Consumer:** humans, tests, and the future audit-trail block read it after the containment box is torn down.
- **Encoding:** UTF-8 NDJSON. Each non-empty line is an independent JSON object, not an array or a multi-line JSON document.
- **Versioning:** required top-level `version` string. Current version is `"1"`.
- **Common fields:** every event contains `version`, `type`, `run_id`, and `timestamp` (`time.RFC3339Nano`, UTC).
- **Event types:**

```
type            required event-specific fields
────────────────────────────────────────────────────────────
run_started     task_id, repo, spec, box_id, worktree
command         command
stdout          data
stderr          data
run_finished    outcome; error when outcome is failed or timed-out
```

- **Outcome values:** `completed`, `failed`, and `timed-out`. `timed-out` means the configured supervisor wall-clock deadline expired and the supervisor attempted to kill the containment box before deterministic teardown.
- **Durability rule:** the supervisor writes stream events while `RunInside` is active, writes `run_finished`, closes the file, and only then tears down the created box. The default run wiring writes command/stdout/stderr stream events for selected task, Executor attempt, Gate verification, passing Gate summary, produced branch, Gate failure detail, and terminal finish evidence.
- **Example:**

```ndjson
{"box_id":"box-019","repo":"agent-builder","run_id":"019/box-019","spec":"docs/tasks/completed/019-run-log-collection.md","task_id":"019","timestamp":"2026-06-05T12:00:00Z","type":"run_started","version":"1","worktree":"/work/agent-builder"}
{"command":"go test ./...","run_id":"019/box-019","timestamp":"2026-06-05T12:00:01Z","type":"command","version":"1"}
{"data":"ok github.com/tkdtaylor/agent-builder/internal/supervisor\n","run_id":"019/box-019","timestamp":"2026-06-05T12:00:02Z","type":"stdout","version":"1"}
{"outcome":"completed","run_id":"019/box-019","timestamp":"2026-06-05T12:00:03Z","type":"run_finished","version":"1"}
```

---

## Derived data

None. agent-builder maintains no caches, materialized views, or indexes. Every value
is recomputed from source on each run: `tasksource` reparses the roadmap and task
files on every `Candidates()`/`Next()` call (no cache), and the Gate recomputes its
`Verdict` on every `Verify(repoPath)` call. There is no derived datum treated as
authoritative that could go stale.

---

## Data invariants

Properties that must hold across the data model. Each is enforced in code (type
system, constructor validation, or runtime assertion) at the package noted by the
type it constrains.

- A Verdict with `OK == true` contains only passing StepResults.
- A Verdict with `OK == false` ends at the first failing StepResult; later configured steps do not run and do not appear in Results.
- A parsed task dependency references another parsed task ID; missing dependency references fail parsing.
- Task-source selection is deterministic: candidates are ordered by task ID, with task path as the duplicate-ID tiebreaker used only for diagnostics.
- Task status writes are constrained to `done`, `blocked`, or `needs-human`; invalid status values fail before file mutation.
- A task status write changes at most one `**Status:**` line. Missing or duplicate status lines fail instead of guessing which bytes are safe to mutate.
- Agent-loop `OutcomeDone` is possible only after pick, attempt, verify, and advance states, and only with a passing Gate verdict.
- Agent-loop `OutcomeFail` never contains retry count, retry decision, or escalation target state.
- Ingestion broker release is possible only after a valid `allow` decision whose candidate ID and kind match the reviewed candidate.
- Ingestion guard error, timeout, unavailable guard, malformed decision, explicit `block`, and explicit `quarantine` outcomes do not release candidate data.
- Armor adapter invocation failure, timeout, non-zero process exit, malformed JSON, malformed decision string, or explicit armor error response maps to an ingestion `block` decision.
- Retry policy `MaxAttempts` is non-negative; negative values fail validation.
- Retry escalation writes only `needs-human` through the constrained task status-writer seam after exhausted failures.
- A configured RunRecord is host-side and durable: stream events are written during `RunInside`, the terminal outcome is written, and the file is closed before containment teardown.
- A default runtime configuration selects at most one task and fails missing required configuration before Executor attempt.
- `audit.VerifyResult.Valid == true` implies `TamperedAt == nil`; the block never reports both intact and a tampered sequence number simultaneously.
- `audit.ErrVerifierUnavailable` and `audit.VerifyResult{Valid: false}` are mutually exclusive: the error path means no verdict was produced; the `Valid == false` path means the block produced and reported a tamper verdict.
