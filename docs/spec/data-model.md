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

---

## Wire / interchange formats

> Data formats used to exchange information across process boundaries: JSON over HTTP, NDJSON log lines, protobuf messages, CSV exports, etc. Each entry is a stable contract — version it like you would an API.

### Format: <name> (e.g. evaluation NDJSON, scanner result JSON)

- **Producer:** what writes this
- **Consumer:** what reads it (including humans / external tools)
- **Schema:** field-by-field, with types and required/optional markers
- **Versioning:** how schema changes are signaled (top-level `version` field, separate filename, etc.)
- **Example:** a real, valid record

```
<paste a representative example>
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
