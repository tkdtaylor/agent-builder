# Agent rules — rationalizations to refuse and project retros

The retro-injection hook (`.claude/scripts/inject-retros.py`) parses
this file at session start and surfaces entries that match the active
task's spec. The "Common rationalizations" table is loaded as a
fallback when no per-retro keyword match scores high enough.

Add new entries here, not to CLAUDE.md, when work is lost or significant
time is wasted to a preventable mistake. CLAUDE.md is the orientation
document; this file is the growing log of project-specific lessons.

## Common rationalizations

These are excuses agents use to skip steps. Don't fall for them.

| Excuse | Reality |
|--------|---------|
| "I'll commit after the next task too" | No. Commit now. Batched commits are impossible to untangle later. |
| "This task is too small for a test spec" | The spec defines done — without it you're guessing. Write one. |
| "I'll add tests later" | Later never comes. The test spec comes first, always. |
| "These two tasks are related, I'll do them together" | One task, one commit. If it feels too granular, the tasks are scoped correctly. |
| "The architecture doc doesn't need updating" | If you made a non-obvious design decision, write an ADR. |
| "I'll just quickly fix this other thing I noticed" | Stay on your task. Note it for later — don't scope-creep. |
| "I'll update the spec at the end of the day" | No. Spec drift is silent. Update it in the same commit, every time. |
| "The spec already covers this — close enough" | If "close enough" required reading the code to confirm, the spec is wrong. Fix it now. |
| "I'll add a 'previously this was X' note to the spec" | Don't. Rewrite the entry. The ADR carries history; the spec is a snapshot. |
| "Tests pass — the task is done" | Necessary, not sufficient. If the change is runtime-observable, run the binary. If it adds cross-module state, trace producer→consumer on the live path. Status is 🟡 until that's done. |
| "It worked in the harness — close enough to call it done" | If the harness is the same wire as the live runtime, fine. If the harness is a partial mock or a different code path, it's 🟡, not ✅. Be honest about which one this is. |
| "The unit test sets the field and the gate fires — wire is good" | A test that sets state by hand proves the gate works *given* the state. It does not prove the state ever gets set on the live path. Grep the write site and the read site and identify the live path before declaring done. |
| "The components are correct and I wired them — done" | Correct components prove nothing if the live constructor still builds with the old one. Open the construction site and *read the line*; a report saying "wired at line N" is a claim, not evidence. Then mutation-test it: revert the wire, confirm the test FAILS, restore. A test that still passes after you unwire the feature is vacuous. |
| "My wiring test passes" | Check whether it builds the collaborators by hand or goes through the live constructor (`CompleterForEntry`, `New*`, the factory). A test that hand-assembles the thing it's testing can't prove production assembles the same thing. Test through the constructor the runtime uses. |

## Failure modes (starter set)

These anti-patterns have been observed across multiple projects. When any apply, **stop and report** — do not rationalize and ship. Add project-specific entries below as your own retros accumulate; each new entry should name the concrete incident in a "Retro source:" line so future-you can reconstruct what happened.

### No self-justification of new warnings

If your change adds a new linter or typecheck warning compared to the baseline (the pre-change warning count), you must either fix the root cause or stop and report it as a blocker. "Acceptable false positive," "I'll clean it up later," and "the warning is wrong" are not labels you get to apply unilaterally — they are the agent rationalizing around a rule. If a warning is genuinely wrong, the fix is an explicit suppression (e.g. `#[allow(lint_name)]`, `# noqa`, `// eslint-disable-next-line`) with a comment explaining why, not silence.

### No smoke tests where the spec asks for assertions

If a test spec describes a specific assertion ("should return `Some(2)` when the range is unqualified"), the test you write must actually verify that assertion. A test that calls the method and checks it doesn't panic is a smoke test, not a real test. If constructing the state needed to verify the assertion is non-trivial, that is a blocker — stop and report. Do not downgrade the test to a smoke test and tell yourself it's "close enough."

### Git status must be clean after the commit

Run `git status` as your last action before declaring a task complete. It must report `nothing to commit, working tree clean`. If it shows staged, unstaged, or untracked files, you missed something — go back and fix it. The common failure is copying a task file from `backlog/` to `completed/` with `cp` instead of `git mv`, which leaves the original undeleted and the new copy unstaged.

### No dead-code delegates

A delegate method that only exists to preserve a pre-refactor API surface and has no non-test callers is a backwards-compat shim. The rule against backwards-compat shims is already in the "Never" boundaries — this is the refactor-specific version. The correct fix is to update the call sites to use the new path, not to preserve the old path with a thin wrapper.

### Parallel agent dispatches must enforce worktree isolation in two layers

When dispatching ≥2 code-modifying agents in one message, setting `isolation: "worktree"` on the Agent tool is **necessary but not sufficient**. The Claude Code harness can fail to provision a worktree — when that happens the agent reads the parent repo's `pwd`, edits files there, and commits to whatever branch the parent is on (frequently `main`), racing every other concurrent agent and any concurrent Claude session.

**Layer 1 — prompt-level fail-fast.** Every dispatch prompt must include an abort check at the top:

```
BEFORE doing any work, run `pwd`. If the path does NOT contain
'.claude/worktrees/agent-' (i.e. the harness failed to provision
a worktree for this run), STOP IMMEDIATELY. Do not edit any files,
do not run any build/test commands, do not commit. Report back:
"ABORT: no worktree provisioned, parent repo at <pwd>". The parent
session will retry the dispatch.
```

**Layer 2 — post-dispatch verification.** After every parallel dispatch completes, run `scripts/verify-worktree-isolation.sh <agent-id> [<agent-id> ...]` and check that each agent has a `worktree-agent-<id>` branch (and that no recent commit on `main` carries the agent's task signature). For any agent that bypassed isolation, `git revert` its commit and re-dispatch with the Layer-1 preamble in place.

**Why both layers:** Layer 1 stops the agent from polluting main if it can detect the missing worktree. But the agent's introspection isn't always trustworthy — Layer 2 is the parent-side audit that catches what slips through. A single layer is not enough: in a real incident, an agent that thought it was inside its worktree ran `git checkout --` to "restore main repo to clean state," which would have wiped foreign uncommitted work from a concurrent session if any had been present.

**Merge-side corollaries (2026-06-28 incident, tasks 103/104/105).** Even with `isolation: "worktree"` working, two further hazards bit during merge-back:

- **Don't tell a harness-isolated agent to also `git checkout -b <named-branch>`.** The harness already puts the agent on a `worktree-agent-<id>` branch sharing the parent `.git`. Layering a manual `git checkout -b task/NNN` on top caused two parallel agents' branch/index operations to cross in the shared object store, leaving stray, *mislabeled* branches (a `task/103` ref pointing at a task-104 commit) and even leaking a partial, broken copy of one agent's edits into the **parent** working tree. Let the harness branch be authoritative; **merge from the agent's reported commit hash**, not from a branch name you assume is correct. The agent's commit is recoverable by hash even after its branch is force-deleted (`git branch -D` does not drop the commit) — verify the tree/diff of that hash before merging.
- **Confirm the parent working tree is clean (`git status`) before every `git merge`.** A leaked/dirty parent tree makes `git merge` abort with "commit your changes or stash them" — and if you then reflexively `git branch -D` the source branch, you strand the good commit. Recovery: `git stash -u` the leak (never discard — it may be real work), re-anchor the good commit by hash (`git branch tmp <hash>`), merge, then verify `go build`/`make check` and drop the stash only once green.
- **`make check` after deleting a worktree can fail on a stale golangci-lint cache** pointing at the removed `.claude/worktrees/...` path (phantom `errcheck`/`typecheck` finding on a "no such file" path). It's not a real regression — `golangci-lint cache clean` then re-run.
- **Orphaned edits on the `main` working tree recur — stash, never discard, and surface them (2026-07-01, task 143 coordination).** While coordinating task 143 (correctly isolated in its worktree), the parent `main` tree was found dirty with an *uncommitted, divergent* attempt at the same task from a concurrent session (an inline `secrets.go` approach vs. the committed `disk_oauth_source.go` file). This is the same leak the layers above guard against, seen from the coordinator side. The parent repo already carried two prior `isolation-repair` stashes (`stash@{1}`/`stash@{2}`) — this is a *pattern*, not a one-off. Handling: `git stash push -u -m "<what/when/why>"` the leak (reversible; the canonical work is safe on the task branch), confirm `git status` clean before merging, and report it to the human rather than silently discarding — the leaked edits might be someone's real work-in-progress.

### "Done" means operationally verified, not "code merged"

A task is not done because `feat-commit` landed, tests passed, or `make fitness` was green. Those are necessary; they are not sufficient. The task is done when the **operational outcome the spec targets is observed** — either by exercising the live binary path or by a validation harness that drives the same code path the runtime uses. Until that observation exists, the task is **🟡 code merged**, not **✅ verified**.

The verification ladder, lowest to highest:

1. Code merged
2. Unit tests pass
3. `make fitness` passes
4. CI pass (if the project has CI)
5. Validation harness pass — the harness exercises the live runtime path end-to-end
6. Live binary observation — the operator (or you, via `cargo run` / `npm start` / etc.) sees the targeted behaviour in stdout, logs, or the rendered UI

Levels 1–4 give you 🟡 in the coverage tracker. Level 5 or 6 is what flips it to ✅. Never report a task complete by quoting only levels 1–4 when the task targets runtime-observable behaviour.

The shape of the trap: a feat commit lands, the unit tests pass, the executor declares done, and the next live session reveals the wire was never connected. The fix is to refuse the ✅ until the higher rung is exercised. If the harness for level 5 doesn't exist for this kind of change, that's a blocker to flag, not an excuse to claim ✅ on level 4.

### Producer-consumer trace before declaring done on cross-module state

When the diff adds a new shared state element — a struct field, an `Arc<X>`, an enum variant, a queue, a channel, a config key, a context value, an event, anything one site writes and another reads — the task is not done until you've **traced the producer and the consumer on the live runtime path**.

The trace, as a literal block in your report:

```
Write sites:
  - path/to/producer.ext:LINE  — writes inside <stage/handler/function>
Read sites:
  - path/to/consumer.ext:LINE  — reads inside <stage/handler/function>
Live path:
  <entry point> → <intermediate calls> → producer fires
                                       → consumer reads
  Producer fires BEFORE consumer reads on this path: YES / NO / UNVERIFIED
```

Manually-set-field tests (`state.foo = Some(_); assert!(gate(state))`) prove the gate works *given* the field — they do not prove the field ever gets set on the live path. The trace is what proves the wire meets. If the producer fires after the consumer reads, the feature is broken even though every narrow test passes.

If you can't produce the trace, the task is not done — report blocker. Substituting "by construction" structural tests for the trace is a downgrade and must be flagged, not buried.

### Wire the component into the live constructor, and test through that constructor

Building the correct component (a new `SecretSource`, `Planner`, `Sink`, decoder, router
adapter…) and *believing* you wired it is not the same as wiring it. This is a recurring
miss: the executor ships correct building blocks, reports them as wired, produces green
unit tests, and self-declares done — while the live constructor still builds with the old
collaborator. The feature is dead code and the tests are blind to it.

Task 143 (2026-07-01) is the reference incident. A correct `DiskOAuthSecretSource` +
`ChainedSecretSource` shipped with a report claiming `completer.go` was "wired at lines
92-95" to the chain — but the actual committed line still read `newClaudeCompleter(entry,
secrets.NewEnvSecretSource())` (the bare env source). The disk-OAuth fallback never ran;
`ask --entry claude-oauth` failed auth exactly as before the task. It survived the first
executor pass **and** an amend, and took two spec-verifier BLOCKs plus a live L6 run to
force the one-line wire in.

Why the tests didn't catch it:

- The "wiring" test constructed `NewChainedSecretSource(...)` **by hand** and asserted on
  that object. It never called the live constructor `CompleterForEntry`, so it validated
  the *component*, not the *seam*. A test that assembles the thing it tests cannot prove
  the production path assembles the same thing. (Same family as the producer-consumer
  trace and manually-set-field rules above — this is the constructor-level version.)
- The test lived in an **external `_test` package**, which structurally cannot reach the
  unexported `cmdFactory` / HOME-isolation stub seam — so it *could not* assert the live
  child env and silently degraded to `assert completer != nil`. When a test can only make
  a trivial assertion, ask whether the package boundary is forcing it, and move it
  in-package rather than accepting the smoke test.

Rules:

1. **Test through the live constructor.** If the runtime builds the object via
   `CompleterForEntry(entry)` / `router.New(...)` / `NewX(...)`, the test builds it the
   same way and asserts on the result — do not hand-assemble the collaborators.
2. **Read the claimed wire.** "Wired at line N" is a claim. Open line N and confirm the
   code is actually there before trusting the report. In task 143 the claimed line and the
   committed line disagreed twice.
3. **Mutation-test a suspect wire.** Revert the one line to the old collaborator, run the
   test, confirm it **FAILS**, then restore. A test that still passes after you unwire the
   feature is vacuous. This is what finally proved task 143's tests real: reverting to
   `NewEnvSecretSource()` failed TC-143-04/05; restoring the chained source passed them.

**Retro source:** task 143, 2026-07-01 — `completer.go` left on the bare env source
through the first executor pass and one amend; caught by a live L6 (`ask --entry
claude-oauth` → `Not logged in`) cross-checked against `claude -p` with the disk token
injected directly (→ `Paris`), then nailed by mutation test. Distinct from task 101: that
was the external *tool* not honoring a correctly-wired env var; this is the wire itself
never connected, and the tests unable to see it.

### Runtime-visible changes require running it

If the diff affects any of:

- Logging output, log levels, log routing
- CLI arguments, help text, exit codes
- TUI rendering, terminal output
- Server endpoints, HTTP responses, RPC contracts
- File outputs, generated artifacts
- Side effects observable from outside the process

…then `make check` + `make fitness` are not verification. You must **actually run the binary path that exercises the change** and quote the relevant output line(s) in your report. Static code review of runtime-observable behaviour is verification theatre.

The pattern that gets caught here: an `eprintln!` → `tracing` migration "passes" all tests because nothing tests stderr layering; the next time someone runs the binary, the TUI is flooded and the log file is empty. Eight lines of diff that a single `cargo run` would have exposed. The rule: when you change what the binary *does at runtime*, observe what the binary *now does at runtime* before claiming done.

If the environment genuinely prevents running the binary (no IB connection, no GPU, no Docker), state that explicitly in the report and downgrade the verdict to 🟡 awaiting operator verification — do not claim ✅.

### Never work directly on the default branch

Every task lives on its own `task/NNN-<slug>` branch. The first action of any task-executor invocation is `scripts/start-task.sh <NNN> <slug>` — that script picks branch (solo session) or worktree (concurrent sessions detected via `.claude/sessions/*.lock`) and sets you up on it. Working on `main` is the failure mode that lets two sessions silently overwrite each other and that makes "abandon this half-done task" require destructive operations.

The `no-commit-on-main.py` hook is the floor: it hard-blocks `git commit` on `main`/`master`/`trunk` once any `task/*` branch exists in the repo. The rule stands even when the hook is disabled — discipline > automation.

**Escape hatches** (use deliberately, not as a workaround):

- `[allow-main]` in the commit message for genuine main-only fixes (standalone doc typos, hotfix patterns, the scaffold-time `chore:` commits). Self-documenting in `git log`.
- The hook skips the check entirely when no `task/*` branches have ever existed — the project hasn't started doing task work yet.

**Worktree gotcha (concurrent-session case):** when `start-task.sh` prints `WORKTREE <path>`, your **next command must be `cd <path>`**. Every subsequent command runs from that directory. The most common silent failure: agent forgets to `cd`, edits land in the parent repo, "isolation" is fictional. The pre-existing parallel-dispatch retro covers the same shape; this is the single-task version of it.

**Cleanup is automatic** once a task branch is merged into `main`: the `auto-cleanup-merge.py` hook deletes the branch (safe `-d`, refuses if unmerged) and removes the worktree (`git worktree remove`). If you see the branch or worktree linger after a merge, check the hook's stderr — likely an unmerged ref or a force-push scenario that needs your attention.

### No `git checkout -- <path>` over uncommitted work

When you want to compare current behavior to a prior commit (linter baseline, test count, file size, anything), use `git stash` first or `git worktree add` for the comparison. **Never** reach for `git checkout HEAD -- <path>` or `git checkout <ref> -- <path>` while you have uncommitted changes you intend to keep. The checkout silently overwrites those changes with the prior commit's content, the only recovery path is the reflog (which does not capture uncommitted blobs), and `git fsck --unreachable` can return ambiguous results that look like recoverable work but aren't.

This rule applies to **all** path-checkouts, not just `src/`. `git checkout HEAD -- .` is the same hazard at full-tree scale.

The right tools for "compare to prior state":
- `git stash` + work + `git stash pop` — safe but easy to forget the pop
- `git worktree add ../baseline <ref>` — strongest, forces the comparison into a different directory
- `git diff <ref> -- <path>` / `git show <ref>:<path>` — for read-only comparisons, no checkout needed at all

### Verify external-tool behavior against the live tool, not the spec's assumption

When a task's correctness depends on how an **external tool** behaves (a CLI's auth
semantics, an API's accepted fields, an env var a subprocess honors), a unit test that
asserts *your side* of the contract — "we inject env var X" — does **not** prove the
tool honors X. The assumption is unverified until you run the real tool.

Task 101 shipped injecting a placeholder into `ANTHROPIC_API_KEY` for local
translation-proxy entries, with green unit tests, a spec-verifier APPROVE, and a
security-auditor SAFE. All three validated *the spec's assumption* (a non-empty
`ANTHROPIC_API_KEY` satisfies the CLI). The live run proved the assumption false:
Claude CLI v2.1.150 validates `ANTHROPIC_API_KEY` as a real credential and prints
`Not logged in` for a placeholder, making **zero** proxy requests. The working var is
`ANTHROPIC_AUTH_TOKEN` (the gateway bearer-token, passed through unvalidated). The
unit tests, verifier, and auditor could not catch this — they never invoked the CLI.

Rule: if a REQ encodes assumed external-tool behavior, the verification plan's L5/L6
must exercise that tool for real before ✅. Treat "the tool accepts X" as a claim to
test, not a given. **Retro source:** task 101, live L6 run 2026-06-28 — placeholder
`ANTHROPIC_API_KEY` → `Not logged in`; `ANTHROPIC_AUTH_TOKEN` → working round-trip.

### Operator gotchas: running the orchestrator live (L6)

Surfaced during the task 101 live round-trip on 2026-06-28; recorded so the next L6
run doesn't re-debug them:

- **gate-tools are gitignored, so a worktree has none.** Running `go run
  ./cmd/agent-builder run` from a `.claude/worktrees/<task>/` checkout fails box
  creation with the misleading `sandbox: create probe exited 1` — the runner swallows
  the launcher's real stderr (`missing Gate tool golangci-lint in
  …/worktrees/…/containment/execution-box/gate-tools`). Run the orchestrator from the
  **main checkout** (which has populated `gate-tools`), or copy them into the worktree.
- **The agent workload defaults to the `runsc` (gVisor) runtime.** If only `runc` is
  registered (`podman info` → OCIRuntime `runc`), set `EXEC_BOX_RUNTIME=runc` or the
  create probe fails. `containment/execution-box/run.sh --print-runtime-plan` shows
  the selected runtime; `--probe --runtime runc` validates the box in isolation.
- **The executor seam needs *structured* tool calls, and the proxy routing decides
  whether they survive.** With corrected task 101 the auth/plumbing works, but the
  full branch+gate round-trip (REQ-094-02) still failed — root cause is tool-call
  serialization, not "the model is dumb." Drill-down (2026-06-28): the model produces
  *correct* tool-call intent, but it only reaches the Claude CLI as an executable
  `tool_use` block if the whole chain preserves structure. LiteLLM's **`openai/`**
  provider (Ollama's OpenAI-compat `/v1`) returns tool calls as plain-text JSON →
  the CLI can't execute them; LiteLLM's **`ollama_chat/`** provider (Ollama native
  `/api/chat`) returns structured `tool_calls` → `tool_use` round-trips. AND it is
  model-specific: `qwen3:8b` emits Ollama-parseable `tool_calls`; `qwen2.5-coder:7b`
  emits bare JSON without the `<tool_call>` wrapper (deterministic, a Modelfile
  `SYSTEM` override didn't fix it) so Ollama never parses it. Lesson: when an agentic
  loop "does nothing," check the tool-call *serialization* end-to-end (model →
  serving endpoint → proxy provider → harness) before blaming model capability. See
  `docs/operating.md` → "Driving local models" for the verified config.
