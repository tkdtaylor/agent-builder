# Behaviors

**Project:** agent-builder
**Last updated:** 2026-06-17 (task 045 — host-portable disk quota)

What the system does, observably. Each behavior describes a triggering condition, the system's response, and any externally-visible side effects. This is the "you can verify this from outside the process" view.

Not in this file:
- *How* it does it (that's in source code; the contract is here, the implementation is there)
- *Why* it does it (that's in ADRs)
- *What data it operates on* (that's in [data-model.md](data-model.md))
- *What the entry points are* (that's in [interfaces.md](interfaces.md))

---

## Format

Each behavior is a numbered subsection with three parts:

> **B-NNN: Short imperative title**
>
> - **Trigger:** what causes this behavior to fire
> - **Response:** what the system does
> - **Side effects:** observable effects beyond the immediate response (writes, emitted events, log entries, external calls)
> - **Failure modes:** how it can fail and what the system does when it does
> - *(optional)* **References:** ADRs that drove the behavior, related test specs

Behaviors are numbered `B-001`, `B-002`, … sequentially. Numbers are stable references — never reuse a number, even if a behavior is removed (mark it `B-NNN: REMOVED — see ADR-XXX` and leave the number).

---

## Core behaviors

### B-001: Run the verification gate as an ordered blocking sequence

- **Trigger:** A caller invokes the gate with a target repository worktree path.
- **Response:** The gate runs each registered Step in registration order, records a `StepResult` for each executed Step, and returns a `Verdict`. A passing Verdict contains every configured Step result. A failing Verdict stops at the first failed Step and contains no later Step results.
- **Side effects:** The gate itself writes no persistent state. Individual Steps may spawn subprocesses or read files inside the supplied worktree.
- **Failure modes:** If any Step returns `OK == false`, the Verdict returns `OK == false` and exposes the failing Step's captured output.
- **References:** ADR 002; `docs/tasks/test-specs/002-gate-orchestrator-core-test-spec.md`.

### B-002: Run native Go verification steps against the target worktree

- **Trigger:** A gate is configured with one or more native Go Steps and invoked with a target repository worktree path.
- **Response:** The native Steps shell out in the supplied worktree to `go build ./...`, `go vet ./...`, `go test ./...`, and `gofmt -l .`. Each command is blocking and returns a StepResult.
- **Side effects:** The Steps spawn local `go` or `gofmt` subprocesses with the target worktree as the working directory. `gofmt -l .` lists unformatted files without rewriting them.
- **Failure modes:** A non-zero subprocess exit fails the Step and surfaces combined stdout/stderr. The gofmt Step also fails when `gofmt -l .` exits zero but prints one or more files. A missing `go` or `gofmt` binary on `PATH` is a hard failure that identifies the missing tool.
- **References:** `docs/tasks/test-specs/003-gate-go-checks-test-spec.md`.

### B-004: Run golangci-lint against the target worktree

- **Trigger:** A gate is configured with the golangci-lint Step and invoked with a target repository worktree path.
- **Response:** The Step shells out in the supplied worktree to `golangci-lint run` using the target worktree's lint configuration and returns a StepResult.
- **Side effects:** The Step spawns a local `golangci-lint` subprocess with the target worktree as the working directory. It writes no persistent state itself.
- **Failure modes:** Any non-zero linter exit fails the Step and surfaces combined stdout/stderr. A missing `golangci-lint` binary on `PATH` is a hard failure that identifies the missing tool.
- **References:** `docs/tasks/test-specs/004-gate-golangci-lint-test-spec.md`.

### B-005: Run dep-scan against the target worktree

- **Trigger:** A gate is configured with the dep-scan Step and invoked with a target repository worktree path.
- **Response:** The Step shells out in the supplied worktree to `gods`, the Go dependency CVE scanner, and returns a StepResult. The scanner's exit code represents the high-or-above severity gate.
- **Side effects:** The Step spawns a local `gods` subprocess with the target worktree as the working directory. It writes no persistent state itself.
- **Failure modes:** Any non-zero scanner exit fails the Step and surfaces combined stdout/stderr, including CVE findings. A missing `gods` binary on `PATH` is a hard failure that identifies the missing tool.
- **References:** `docs/tasks/test-specs/005-gate-dep-scan-test-spec.md`.

### B-006: Run code-scanner against the target worktree

- **Trigger:** A gate is configured with the code-scanner Step and invoked with a target repository worktree path.
- **Response:** The Step shells out in the supplied worktree to `code-scanner`, the malware/backdoor/credential-harvest scanner, and returns a StepResult. The scanner's exit code represents the findings gate.
- **Side effects:** The Step spawns a local `code-scanner` subprocess with the target worktree as the working directory. It writes no persistent state itself.
- **Failure modes:** Any non-zero scanner exit fails the Step and surfaces combined stdout/stderr, including scanner findings. A missing `code-scanner` binary on `PATH` is a hard failure that identifies the missing tool.
- **References:** `docs/tasks/test-specs/006-gate-code-scanner-test-spec.md`.

### B-007: Select the next ready roadmap task without writing task state

- **Trigger:** A caller asks the task source for parsed candidates or the next task.
- **Response:** The task source reads the roadmap and configured task directories through `fs.FS`, parses each task file into a candidate, sorts candidates by task ID, and returns the first ready task whose dependencies are completed.
- **Side effects:** The task source performs read-side filesystem operations only. It creates no files, opens no write handle, and mutates no task status.
- **Failure modes:** Missing roadmap reads, unreadable task directories, malformed task metadata, duplicate task IDs, and dependencies that reference no parsed task return errors. When all parsed tasks are blocked, active, completed, or cyclically dependent on incomplete tasks, `Next()` returns no task and no error.
- **References:** `docs/tasks/test-specs/010-roadmap-task-source-test-spec.md`.

### B-008: Write task status without changing task content

- **Trigger:** A caller asks the task status writer to update one task ID to one of the allowed status markers: `done`, `blocked`, or `needs-human`.
- **Response:** The writer scans the configured task directories, finds the single task file whose heading matches the task ID, validates that the file has exactly one `**Status:**` metadata line, and rewrites only that line to `**Status:** <target>`. If the task is already at the target status, the call succeeds without changing file bytes.
- **Side effects:** The matched task source file is rewritten on disk only when the status line changes. Non-status bytes, line ordering, prose, requirement tables, trailing whitespace on other lines, and final newline shape are preserved byte-for-byte.
- **Failure modes:** Empty task ID, invalid target status, missing task ID, duplicate task ID, missing status line, duplicate status lines, unreadable task directories/files, stat failures, and write failures return errors. Invalid target status is rejected before any task file is opened for writing.
- **References:** `docs/tasks/test-specs/011-task-status-writer-test-spec.md`.

### B-009: Drive one task through the agent loop

- **Trigger:** A caller invokes the agent loop for one cycle with a task source, Executor, Gate, and target worktree path.
- **Response:** The loop records `pick`, asks the task source for the next ready task, records `attempt` and runs the Executor when a task exists, records `verify` and runs the Gate when the Executor reports a successful attempt, and records `advance` only when the Gate passes. A passing cycle returns a `done` outcome carrying the Executor branch.
- **Side effects:** The loop itself writes no persistent state. The supplied task source may read task files, the Executor may edit the target worktree and produce a branch, and the Gate may spawn verification subprocesses in the configured worktree.
- **Failure modes:** With no ready task, the loop returns an `idle` outcome after `pick` and calls neither Executor nor Gate. A task-source error is returned before attempt. An Executor error, Executor unsuccessful result, or failing Gate verdict returns a `fail` outcome with diagnostics and no retry count, retry decision, or escalation target.
- **References:** ADR 012; `docs/tasks/test-specs/012-agent-loop-test-spec.md`.

### B-010: Launch the execution-box containment profile with a probeable contract

- **Trigger:** An operator invokes `containment/execution-box/run.sh` with a target worktree, optionally using `--probe`, `--egress-probe`, `--egress-allowlist`, or `--print-egress-plan`.
- **Response:** The launcher validates the plain-text egress allowlist before Podman is required. For normal and egress-probe runs, it refuses root execution, verifies rootless Podman is available for the current user, detects whether the host's overlay container store can enforce per-container disk quotas (XFS required), builds the execution-box image, creates a temporary pod network namespace, starts an egress sidecar with `CAP_NET_ADMIN`, waits for the sidecar's readiness marker, and only then starts the worktree workload container with a read-only root filesystem, a writable `/work` bind, tmpfs `/scratch`, non-root uid/gid, all workload capabilities dropped, no new privileges, explicit CPU/memory/PID/shared-memory/tmpfs limits, an overlay disk quota when the host supports it (and a stderr `WARNING` when it does not), and workload DNS disabled except launcher-provided host records for allowlisted destinations. `--print-egress-plan` prints the parsed default-deny allowlist plan without requiring Podman.
- **Side effects:** The launcher builds or refreshes the local execution-box image tag, creates a temporary labeled pod and sidecar for egress-enforced runs, writes probe files only inside `/work/.execution-box-probe` and `/scratch`, writes temporary egress readiness/plan files under a host temp directory, removes temporary containers/pods on exit, and emits a `WARNING` to stderr on non-XFS hosts when `EXEC_BOX_STORAGE_SIZE` is non-empty (naming the degraded per-container disk quota control).
- **Failure modes:** Missing Podman, failed `podman info`, root invocation, missing worktree, malformed egress allowlist, unresolvable allowlisted host, egress sidecar startup failure, failed `podman create`/`podman run` (exits non-zero with a named error — never exit 0 on a non-started box), or any failed in-box probe exits non-zero and prints the failing TC marker. Static tests cover the launcher contract; only a successful rootless Podman probe proves runtime containment and egress enforcement. The per-container disk quota (`--storage-opt size=...`) degrades gracefully on non-XFS hosts: the box still launches (exit 0) with a `WARNING` on stderr; see ADR 027.
- **References:** ADR 014; ADR 015; `docs/tasks/test-specs/014-podman-containment-profile-test-spec.md`; `docs/tasks/test-specs/015-egress-allowlist-test-spec.md`.

### B-011: Apply bounded retry and escalation after failed task attempts

- **Trigger:** A caller invokes the retrying loop with a task source, Executor, Gate, target worktree path, status writer, and retry policy.
- **Response:** The retrying loop picks one ready task. With `MaxAttempts == 0`, it immediately marks the task `needs-human` and returns an escalated terminal outcome without running Executor or Gate. With `MaxAttempts > 0`, it runs the task cycle at most `MaxAttempts` times. A successful Executor plus passing Gate returns a done terminal outcome carrying the successful branch and performs no escalation write. Executor error, Executor incomplete, and Gate fail outcomes are retryable until the attempt bound is exhausted.
- **Side effects:** After each failed non-terminal attempt, the escalation hook is invoked and may return the Executor for the next attempt. When failures exhaust the bound, the retrying loop writes `needs-human` through the constrained task status-writer seam exactly once for the picked task.
- **Failure modes:** A negative attempt limit is rejected before the policy runs. Missing source, Executor, Gate, worktree path, status writer, or escalation hook is rejected at construction. Task-source errors, escalation hook errors, nil hook-returned Executors, and status-write errors are returned to the caller. No failure mode creates an unbounded retry loop.
- **References:** ADR 013; `docs/tasks/test-specs/013-escalation-retry-policy-test-spec.md`.

### B-012: Dispatch one task through a containment lifecycle

- **Trigger:** A caller invokes `Supervisor.Run()` with one configured task, one containment-box seam, and one in-box loop seam.
- **Response:** The supervisor creates one containment box for the configured task, starts the in-box loop once with the created box handle and task, then tears the box down exactly once.
- **Side effects:** When a logger is configured, the supervisor emits structured lifecycle log records for `box.created`, `loop.started`, and `box.torn_down` with the task ID, box ID, and worktree path.
- **Failure modes:** Missing dispatch dependencies fail before box creation. A box-create error is returned without teardown. A loop error is returned after teardown. A loop panic is recovered, converted into an error that includes the panic value, and returned after teardown. Teardown errors are joined with any loop or panic error.
- **References:** `docs/tasks/test-specs/017-supervisor-dispatch-test-spec.md`.

### B-013: Kill timed-out in-box runs

- **Trigger:** A caller invokes `Supervisor.Run()` with a positive wall-clock timeout and the in-box loop has not returned before that timeout expires.
- **Response:** The supervisor records a timeout error, emits a structured `box.kill.timeout` log event, calls `Kill` on the created containment box, writes a terminal run-record outcome of `timed-out` when run-record collection is configured, and tears the box down exactly once.
- **Side effects:** The containment box receives at most one kill call for the created handle. The run-record file, when configured, contains a final `run_finished` event with `outcome` set to `timed-out` and an error string naming the timeout.
- **Failure modes:** Kill errors are joined with the timeout error and returned, but teardown still runs. A fast in-box loop error before the timeout is recorded as `failed`, not `timed-out`. Non-positive timeout values leave the timeout disabled.
- **References:** `docs/tasks/test-specs/018-wall-clock-kill-test-spec.md`.

### B-014: Review ingestion and tool-call candidates before release

- **Trigger:** Inside-the-box code presents a content or tool-call candidate to the ingestion broker.
- **Response:** The broker invokes the configured guard for that candidate type. A valid `allow` decision with the same candidate ID and candidate kind makes the candidate releasable through the review `Release` helper. `block` and `quarantine` decisions preserve the guard reason and do not release the candidate.
- **Side effects:** The broker writes no persistent state and performs no tool execution or web fetch itself. The configured guard may call an external adapter in later tasks.
- **Failure modes:** Missing guard, guard error, guard timeout, context cancellation, malformed decision outcome, mismatched candidate ID, or mismatched candidate kind produces a fail-closed `block` decision and does not release candidate data.
- **References:** ADR 024; `docs/tasks/test-specs/024-ingestion-tool-call-boundary-test-spec.md`.

### B-015: Map external armor results to ingestion decisions

- **Trigger:** The armor guard adapter receives a content or tool-call candidate through the `ingestion.Guard` interface.
- **Response:** The adapter builds an armor request preserving candidate ID, candidate kind, candidate data, and provenance; invokes the configured external runner; and maps the armor response to an `ingestion.Decision`. Clean/allow/pass responses without findings return `allow`. Block/flag/deny responses return `block`. Quarantine responses return `quarantine`. Finding categories, severities, warnings, and response metadata are preserved as decision metadata.
- **Side effects:** A process-backed runner starts the configured armor-compatible command with JSON stdin and reads JSON stdout. The adapter does not fetch web content, execute tool calls, persist audit records, or modify armor source.
- **Failure modes:** Missing runner/command, runner error, timeout, non-zero process exit, malformed JSON output, malformed decision string, or explicit armor error/fail response returns a fail-closed `block` decision and no adapter error.
- **References:** ADR 024; `docs/tasks/test-specs/025-armor-guard-adapter-test-spec.md`.

### B-016: Route executor-facing events through ingestion before use

- **Trigger:** Executor-facing code receives web-ingested content or a requested tool call.
- **Response:** The executor ingestion harness constructs an `ingestion.ContentCandidate` or `ingestion.ToolCallCandidate`, records producer-consumer trace checkpoints when configured, routes the candidate through the ingestion broker, and invokes the continuation or tool executor only after a matching `allow` release.
- **Side effects:** The harness itself writes no persistent state, fetches no web content, and executes no tool calls. Caller-supplied continuations or executors run only after broker release.
- **Failure modes:** Invalid event fields fail before guard invocation. Broker `block`, `quarantine`, guard error, guard timeout, unavailable guard, malformed decision, nil continuation, or nil executor prevents continuation/execution. Directly constructed release values are invalid and return `executorharness.ErrUnreviewedRelease`.
- **References:** ADR 024; `docs/tasks/test-specs/027-executor-ingestion-tool-harness-test-spec.md`.

### B-017: Review executor-facing events with armor-backed wiring

- **Trigger:** Executor-facing web content or a tool-call request is handled by an armor-guarded executor harness.
- **Response:** The harness constructs an ingestion candidate, the broker invokes the armor guard adapter, the adapter invokes the configured armor runner or command, and only armor `allow` decisions without findings release to continuation or tool execution.
- **Side effects:** Armor is invoked through the adapter seam as an external runner or JSON stdin/stdout command. The harness itself still performs no web fetch or tool execution; caller callbacks run only after release.
- **Failure modes:** Armor `block` or `quarantine` decisions, allow-with-findings responses, missing armor command, runner errors, timeouts, malformed armor decisions, invalid event fields, and unavailable armor all prevent continuation or tool execution.
- **References:** ADR 024; `docs/tasks/test-specs/026-armor-ingestion-wiring-test-spec.md`.

### B-018: Fail closed or review Claude executor web/tool routes

- **Trigger:** Claude executor-facing code presents web-ingested content or a requested tool call through `executor.ClaudeCLI`.
- **Response:** The Claude executor declares one effective ingestion policy. `disabled` denies web/tool events before executor context or tool execution. `reviewed` requires a configured `executorharness.Harness` and delegates content/tool events to that harness, so continuations and tool executors receive only broker-reviewed release values. The zero-value policy defaults to `disabled`.
- **Side effects:** Normal Claude CLI subprocess execution for code-editing tasks remains available under the disabled policy when no web/tool event is requested. Reviewed routes may invoke armor through `executorharness.NewArmorGuarded` when the configured harness uses that adapter.
- **Failure modes:** Unknown policy names, reviewed policy without a harness, disabled web/tool routes, direct unreviewed release values, armor block/quarantine, armor unavailable, armor allow-with-findings, malformed tool arguments, guard errors, and malformed guard decisions all prevent executor context continuation or tool execution.
- **References:** ADR 024; `docs/tasks/test-specs/029-claude-ingestion-control-test-spec.md`.

### B-019: Run one configured Phase 0 task through default CLI wiring

- **Trigger:** An operator invokes `agent-builder run` with the required runtime environment configured.
- **Response:** The CLI reads the configured task root, selects the lowest-ID ready task whose dependencies are complete, constructs the Claude CLI Executor, production Gate, sandbox-runtime-backed containment box, bounded retrying in-box loop, task status writer, branch publisher, supervisor timeout, and optional RunRecord path, then dispatches that one task through `Supervisor.Run()`. A successful run prints `run completed: task <id>` to stdout.
- **Side effects:** The sandbox-runtime adapter is invoked once during box creation, the Executor attempts the selected task at most `AGENT_BUILDER_MAX_ATTEMPTS` times, the Gate verifies the configured worktree after a successful Executor attempt, the branch publisher runs only after a successful retry outcome with a non-empty branch, and the RunRecord file, when configured, contains command/stdout/stderr evidence for pick, attempt, verify, Gate summary, branch, publication artifact, and terminal outcome. With no ready task, the command prints `run idle: no ready task` and does not invoke containment, Executor, Gate, publisher, or status mutation.
- **Failure modes:** Missing task root, worktree, executor token, sandbox runtime, run timeout, max-attempts, or publish-remote configuration fails before task selection can mutate status or the Executor can run. A missing scanner tool fails in the Gate after a successful Executor attempt and records a failed terminal outcome naming the missing tool. Blank branch output prevents Gate and publication. Publication failure records a failed terminal outcome and does not mark the task done. Exhausted failed attempts are marked `needs-human` through the constrained status writer and return a failed supervisor run.
- **References:** ADR 012; ADR 013; ADR 020; `docs/tasks/test-specs/028-default-run-wiring-test-spec.md`; `docs/tasks/test-specs/034-branch-pr-publication-test-spec.md`.

### B-020: Publish verified branches as PR artifacts

- **Trigger:** The default run wiring receives a bounded retry outcome whose final attempt passed the Gate and carries a non-empty executor branch.
- **Response:** The branch publisher pushes the branch to the configured remote, checks for an existing PR for the branch, and otherwise asks GitHub CLI to create a PR. The returned PR URL or identifier is written to run output evidence.
- **Side effects:** The publisher invokes `git push <remote> <branch>` and GitHub CLI `gh pr` commands in the configured worktree. Publication command evidence is written through the RunRecord command/stdout/stderr stream when a RunRecord is configured.
- **Failure modes:** Blank branch, blank remote, git push failure, GitHub CLI failure, auth failure, or PR creation failure returns a non-success run outcome. Configured git/GitHub token values are redacted from publisher errors, CLI stderr, and RunRecord events.
- **References:** `docs/tasks/test-specs/034-branch-pr-publication-test-spec.md`.

### B-021: Verify audit chain integrity via the block's own verifier

- **Trigger:** A caller invokes `audit.VerifyChain(binPath, logfile)` after a run has produced an audit-trail chain via `audit.BlockSink`.
- **Response:** The helper invokes `audit-trail verify -logfile <path>` via `os/exec`, parses the block's JSON verdict `{ "valid": bool, "tamper_detected_at": <int|null>, "message": string }`, and returns a typed `audit.VerifyResult`. A `Valid == true` result passes the gate. A `Valid == false` result is a **block-severity** gate failure; `IsTampered()` returns true and `TamperedAt` names the sequence number the block localized.
- **Side effects:** One `audit-trail verify` subprocess is started and exits before `VerifyChain` returns. The on-disk logfile is read by the block subprocess; agent-builder does not read or mutate the logfile directly.
- **Failure modes:** A missing or non-executable `binPath`, a logfile the block cannot read, or unparseable block output returns a non-nil error wrapping `audit.ErrVerifierUnavailable`. This is distinct from a clean `Valid == false` result: `ErrVerifierUnavailable` means the verifier could not produce a verdict at all; `Valid == false` means the verifier ran and detected a tamper. An unavailable verifier is never reported as "valid". The tamper detection algorithm itself (RFC 8785 canonicalization, first-broken-link detection, edit/reorder/truncation classification) is owned by the audit-trail block and is not re-implemented here.
- **References:** ADR 026; `docs/tasks/test-specs/040-audit-verify-test-spec.md`; frozen block contract at `docs/CONTRACT.md` in `github.com/tkdtaylor/audit-trail`.

---

## Edge cases and error behaviors

> Behaviors specifically for error conditions, recovery, and edge cases. Keep them here rather than scattered through the core list — most readers want core first, edge cases on demand.

### B-003: Native tool absence fails loudly

- **Trigger:** A native Go Step runs while its required executable is absent from `PATH`.
- **Response:** The Step returns a failed StepResult.
- **Side effects:** No subprocess is started when executable lookup fails.
- **Failure modes:** The StepResult output names the missing tool and includes the lookup failure.

---

## Behavioral invariants

> Cross-cutting properties that hold across many or all behaviors. Examples:
>
> - All write operations are idempotent on retry.
> - No behavior can leave the system in an inconsistent state on partial failure (transactions / rollback / compensating action).
> - User-triggered destructive actions always require confirmation.
>
> Invariants here are stronger than ones in `SPEC.md` "Top-level invariants" — those are about system architecture; these are about observable behavior.

- There is no gate skip or bypass input. All configured Steps are blocking.
- Native Go Steps always run in the caller-supplied worktree, never implicitly in the agent-builder repo.
- The golangci-lint Step always runs in the caller-supplied worktree, never implicitly in the agent-builder repo.
- The dep-scan Step always runs in the caller-supplied worktree, never implicitly in the agent-builder repo.
- The code-scanner Step always runs in the caller-supplied worktree, never implicitly in the agent-builder repo.
- Task selection is read-only; writing task status is handled by a separate status-writer component.
- The task status writer has no content-patch or prose-editing API; its only mutation path is task ID plus constrained status marker.
- The agent loop reports failures without deciding retry count, escalation target, or mandatory stop condition.
- The retrying loop has a mandatory stop condition: each picked task runs no more than the configured non-negative `MaxAttempts`, and exhausted failures are marked `needs-human`.
- The execution-box profile exposes no host home mount, no container-engine socket mount, no privileged mode, and no capability add-back by default.
- One `Supervisor.Run()` call dispatches at most one task and always tears down a successfully created box exactly once.
- A configured supervisor timeout records `timed-out`, distinct from ordinary loop failure, and does not skip teardown.
- Ingestion candidate review fails closed: only a valid matching `allow` releases content or tool-call data.
- The armor adapter invokes armor as an external seam and does not vendor or edit armor source.
- Executor-facing web/tool events use broker-reviewed release values before continuation or execution.
- Armor-guarded executor harness wiring fails closed and releases only armor-allowed candidates.
- Claude executor web/tool routes are explicitly `disabled` or `reviewed`; prompt text or subprocess flags alone are not treated as the blocking control.
- The default `agent-builder run` wiring dispatches at most one ready task per invocation; an idle run does not create a box or run an Executor.
- The branch publisher is called only after Executor success, Gate pass, and non-empty branch capture.
- An unavailable `audit-trail` verifier binary is never reported as "valid": `VerifyChain` returns a non-nil `ErrVerifierUnavailable` error, not a passing `VerifyResult`.
- `audit.VerifyChain` does not re-implement tamper detection; it delegates to the block's `verify` verb and maps the block's verdict to a typed block-severity gate result.
