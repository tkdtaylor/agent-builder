# Data Model

**Project:** agent-builder
**Last updated:** 2026-06-05

What data exists, how it's structured, where it lives, and what relationships hold between entities. Covers persistent storage, in-memory state, and data-on-the-wire formats.

Not in this file:
- Operations on the data (that's in [behaviors.md](behaviors.md))
- How the data is accessed (that's in [interfaces.md](interfaces.md))
- Tunable parameters (that's in [configuration.md](configuration.md))

---

## Persistent state

> Data that survives process restart. For each store, document the schema, ownership, and access pattern.

### Store: <name> (e.g. PostgreSQL `app_db`, SQLite `data.db`, S3 `bucket-name`)

**Purpose:** what this store holds and why it exists separately from any others
**Owner:** which component is the single writer (or "shared, see access rules below")
**Backup / retention:** how long data lives, how it's recovered

#### Entity: <EntityName>

```
field_name      type          notes
─────────────────────────────────────
id              uuid          primary key
created_at      timestamp     UTC, set by DB default
…
```

- **Identity:** what makes a row unique beyond the primary key (natural key, unique constraint)
- **Lifecycle:** when rows are created, when they're updated, when they're deleted (or "never")
- **Relationships:** foreign keys, parent/child, many-to-many junctions
- **Indexes:** non-obvious indexes and what they support

> Add one section per entity. For schemas that are large or change frequently, prefer pasting the canonical DDL/migration source rather than retyping field lists by hand.

---

## In-memory state

> Data that exists only during process lifetime. For long-running services this is often as important as persistent state — race conditions and lock orders live here.

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
- **Dependency scan output:** `Output` stores combined stdout/stderr for `gods` failures, including high-or-above CVE findings and scanner/tool errors. Missing `gods` stores a human-readable lookup failure naming the absent executable.
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
```

- **Identity:** `ID` is unique across parsed task files.
- **Lifecycle:** produced by task-source parsing and later consumed by the supervisor/agent loop and executor seam.
- **Relationships:** embedded in `tasksource.Candidate`.

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

- **Shape:** `*supervisor.Supervisor` stores one configured `supervisor.Task`, one `supervisor.ContainmentBox`, one `supervisor.InBoxLoop`, an optional structured logger, an optional durable run-record path, and the pre-existing exec-sandbox Runner seam.
- **Owner:** host-side runtime wiring constructs the supervisor through `supervisor.New(options...)`.
- **Lifetime:** process-local; each `Run()` call uses the currently configured task and seams for one dispatch lifecycle. When a run-record path is configured, the supervisor opens and closes one host-side record file during that lifecycle.
- **Concurrency rules:** no internal mutation occurs during `Run`; callers choose whether supplied box, loop, and logger implementations are safe to share across goroutines.
- **Bounds:** one `Run()` call creates at most one box, starts the loop at most once, writes at most one run-record file, and tears down a successfully created box exactly once.

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
timed-out     reserved terminal state for task 018 timeout handling
```

- **Identity:** the string value is written to the terminal `RunRecord` line.
- **Lifecycle:** `completed` and `failed` are produced by task 019 run-record collection. `timed-out` is part of the shared vocabulary so task 018 can add timeout production without changing the wire format.
- **Relationships:** no wall-clock timer, cancellation, or box kill behavior is implied by the `timed-out` vocabulary entry.

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
Reason      FailureReason        executor-error, executor-incomplete, or gate-fail
Err         error                optional executor error preserved for the policy consumer
```

- **Identity:** meaningful only when `Outcome.Kind == OutcomeFail`.
- **Lifecycle:** produced by the loop when attempt or verify does not complete successfully.
- **Relationships:** retry and escalation policy is intentionally absent; the escalation policy consumer decides next action.

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

> Data formats used to exchange information across process boundaries: JSON over HTTP, NDJSON log lines, protobuf messages, CSV exports, etc. Each entry is a stable contract — version it like you would an API.

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

- **Outcome values:** `completed`, `failed`, and `timed-out`. Task 019 writes `completed` and `failed`; `timed-out` is reserved for task 018's timeout producer and does not imply timeout behavior here.
- **Durability rule:** the supervisor writes stream events while `RunInside` is active, writes `run_finished`, closes the file, and only then tears down the created box.
- **Example:**

```ndjson
{"box_id":"box-019","repo":"agent-builder","run_id":"019/box-019","spec":"docs/tasks/backlog/019-run-log-collection.md","task_id":"019","timestamp":"2026-06-05T12:00:00Z","type":"run_started","version":"1","worktree":"/work/agent-builder"}
{"command":"go test ./...","run_id":"019/box-019","timestamp":"2026-06-05T12:00:01Z","type":"command","version":"1"}
{"data":"ok github.com/tkdtaylor/agent-builder/internal/supervisor\n","run_id":"019/box-019","timestamp":"2026-06-05T12:00:02Z","type":"stdout","version":"1"}
{"outcome":"completed","run_id":"019/box-019","timestamp":"2026-06-05T12:00:03Z","type":"run_finished","version":"1"}
```

---

## Derived data

> Data that's computed from other data and treated as authoritative for some purpose (caches, materialized views, indexes). Note the source and the recompute rule so consumers know what guarantees they have.

| Derived | Source | Recompute trigger | Staleness tolerance |
|---------|--------|-------------------|---------------------|
| | | | |

---

## Data invariants

> Properties that must hold across the data model. Examples:
>
> - `account.balance == sum(account.transactions.amount)` for all accounts.
> - No two `Order` rows share an `external_id` for the same `account_id`.
> - In-memory `SessionRegistry` only contains entries whose status is one of {Active, Idle, Failed, Closed}.
>
> If an invariant is enforced by code (DB constraint, runtime assertion, type system), say so.

- A Verdict with `OK == true` contains only passing StepResults.
- A Verdict with `OK == false` ends at the first failing StepResult; later configured steps do not run and do not appear in Results.
- A parsed task dependency references another parsed task ID; missing dependency references fail parsing.
- Task-source selection is deterministic: candidates are ordered by task ID, with task path as the duplicate-ID tiebreaker used only for diagnostics.
- Task status writes are constrained to `done`, `blocked`, or `needs-human`; invalid status values fail before file mutation.
- A task status write changes at most one `**Status:**` line. Missing or duplicate status lines fail instead of guessing which bytes are safe to mutate.
- Agent-loop `OutcomeDone` is possible only after pick, attempt, verify, and advance states, and only with a passing Gate verdict.
- Agent-loop `OutcomeFail` never contains retry count, retry decision, or escalation target state.
- Retry policy `MaxAttempts` is non-negative; negative values fail validation.
- Retry escalation writes only `needs-human` through the constrained task status-writer seam after exhausted failures.
- A configured RunRecord is host-side and durable: stream events are written during `RunInside`, the terminal outcome is written, and the file is closed before containment teardown.
