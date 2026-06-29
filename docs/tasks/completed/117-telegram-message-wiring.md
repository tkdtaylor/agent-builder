# Task 117: Telegram wiring (message-aware)

**Project:** agent-builder
**Created:** 2026-06-28
**Status:** backlog

## Goal

Wire Telegram into the async control plane: make `telegram.Adapter` emit typed `Message`s
(task 113's `supervisor.MessageSource`, deriving `MessageKind`/`GoalID` at the adapter edge),
keep `telegram.ReplyAdapter` as the outbound `Reporter` for acks/status/results, and wire both
into `assembleOrchestrate` behind config (e.g. `AGENT_BUILDER_INBOUND=telegram`) so the
env/stdin path stays the **default** for local tests. This is the **last** task in the ADR 054
decomposition — wiring + message-type mapping over adapters that already exist.

## Context

ADR 054 (the authoritative design) §2 notes Telegram already implements both seams but is not
wired in: the live orchestrate path hardcodes `newEnvGoalSource` + `newLogReporter`
(`orchestrate.go` ~L216/227). Both Telegram adapters already exist
(`internal/channel/telegram/adapter.go` `Adapter`, `reply.go` `ReplyAdapter`) — this is wiring
+ kind mapping, **not** a from-scratch build.

### Grounded current state (verified against code)

- `telegram.Adapter.Next()` returns `(supervisor.Task, bool, error)` today — a `GoalSource`.
  This task makes it satisfy the new `supervisor.MessageSource` (`Next() (Message, bool,
  error)`), deriving `MessageKind`/`GoalID` from the message text or reply-to **at the adapter
  edge**; the control plane only ever sees `Message.GoalID` (ADR 054 §2).
- `telegram.ReplyAdapter.Report(ctx, text)` already implements `supervisor.Reporter` — reused
  as-is for acks/status/results.
- `assembleOrchestrate` hardcodes the env source + log reporter; this task selects Telegram
  behind config so the env/stdin path stays the default for local testing.

### Per-message goal IDs (ADR 054 §Recommended-decomposition — load-bearing)

The adapter derives a per-message `GoalID` from the Telegram chat/message ID (or a reply-to /
threaded goalID), so independent goal tracking holds across concurrent goals. A `new-goal`
gets a fresh goalID from its message identity; a `status`/`info`/`cancel` reply-to threads the
**existing** goalID. The mapping happens at the adapter edge; the control plane sees only
`Message.GoalID`.

### Security invariants preserved (existing telegram + ADR 054 §6)

The existing Telegram envelope verification (Ed25519) + decryption (X25519+AEAD) + armor
ingestion pipeline (tasks 080/097/098) is **unchanged** — message-kind derivation happens
**after** envelope verify + armor, on already-trusted plaintext. The control plane's gates
(policy fail-closed, self-repo bright line, audit chain) fire downstream regardless of the
inbound channel. Wiring Telegram adds no path around any gate.

## Requirements

| Req ID      | Description                                                                                                                  | Priority   |
|-------------|-----------------------------------------------------------------------------------------------------------------------------|------------|
| REQ-117-01  | `telegram.Adapter` satisfies `supervisor.MessageSource`; `Next()` emits typed `Message`s (kind derived at the adapter edge) | must have  |
| REQ-117-02  | Kind/GoalID derivation maps bare text→`MsgNewGoal` (fresh goalID); `status`/`info`/`cancel` text/reply-to→the matching kind + threaded goalID | must have |
| REQ-117-03  | `ReplyAdapter` (Reporter) carries acks/status/results outbound, reused unchanged (envelope sealing intact)                  | must have  |
| REQ-117-04  | `assembleOrchestrate` selects Telegram inbound+outbound behind config; env/stdin stays the **default**; missing config fails fast | must have |
| REQ-117-05  | Per-message goal IDs derived from chat/message identity so concurrent goals track independently (no ID collision)            | must have  |
| REQ-117-06  | The envelope-verify + armor pipeline is unchanged; kind derivation runs only on verified plaintext                          | must have  |

## Readiness gate

- [ ] Task 112 merged (the async control loop + registry the Telegram messages drive)
- [ ] Task 113 merged (`MessageSource`/`Message`/`MessageKind` + the router)
- [ ] Task 114 merged (status handler — Telegram `status` answers through it)
- [ ] Task 115 merged (info handler — Telegram `info` folds through it)
- [ ] Task 116 merged (cancel handler — Telegram `cancel` tears down through it)
- [x] Task 080/097/098 merged (Telegram `Adapter`/`ReplyAdapter` + envelope/armor pipeline)
- [x] ADR 054 §2 read

## Acceptance criteria

- [ ] [REQ-117-01] TC-117-01: `var _ supervisor.MessageSource = (*telegram.Adapter)(nil)`; a verified `"add rate limiting"` update → `Message{MsgNewGoal, Goal.Spec:"add rate limiting"}`
- [ ] [REQ-117-02] TC-117-02: table — bare→`MsgNewGoal`(fresh id); `status` reply-to→`MsgStatus`(threaded id); `info …`→`MsgInfo`(id+text); `cancel`→`MsgCancel`(id); bare `status`→empty id; no panic on malformed
- [ ] [REQ-117-03] TC-117-03: `ReplyAdapter.Report` → exactly one `sendMessage` POST carrying the sealed envelope (no plaintext); task-098 sealing unchanged
- [ ] [REQ-117-04] TC-117-04: `INBOUND` unset/`env` → env source + log reporter; `=telegram` → `*telegram.Adapter` + `*telegram.ReplyAdapter`; missing telegram config → assembly error (no nil-adapter panic)
- [ ] [REQ-117-05] TC-117-05: two distinct updates → distinct `GoalID`s; an `info` reply-to the first threads the first goalID (not the second)
- [ ] [REQ-117-06] TC-117-06: a tampered-signature update and an armor-rejected update (each a would-be `cancel`) are dropped before derivation → not `MsgCancel`; derivation not reached for rejected updates

## Verification plan

- **Highest level achievable: L6** — drive new-goal/status/info/cancel over a real Telegram
  bot and observe each end to end. L2 (adapter `MessageSource` + derivation + assembler-
  selection unit tests, `-race`) is the CI ceiling (a real bot token is not CI-automatable).
- **L2 harness commands:**
  ```
  go test -race -count=1 ./internal/channel/telegram/... ./internal/cli/...
  ```
  Expected: `ok` each, no race report.
- **L3 fitness commands:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L6 (operator-run, dev host):** set `AGENT_BUILDER_INBOUND=telegram` with a real bot token
  + the SEC envelope keys; run `agent-builder orchestrate`; from the chat send a goal, then a
  `status` reply, an `info` reply, and a `cancel` reply to that goal; observe each routed and
  answered (new goal starts; status answers; info folds; cancel tears down). Record the four
  round-trips in the verify commit.

## Modules touched

- `internal/channel/telegram` (`Adapter` now satisfies `supervisor.MessageSource` — `Next()`
  emits typed `Message`s with kind/GoalID derived at the adapter edge from text/reply-to;
  `ReplyAdapter` reused unchanged).
- `internal/cli` (`assembleOrchestrate` selects Telegram inbound+outbound behind
  `AGENT_BUILDER_INBOUND`; env/stdin stays the default; fail-fast on missing telegram config).
- `docs/spec/configuration.md` (`AGENT_BUILDER_INBOUND` selector + telegram inbound/outbound).
- `docs/architecture/diagrams.md` (the inbound/outbound channel now optionally Telegram on the
  orchestrate path — a diagrammed boundary change).

(Two code modules — `internal/channel/telegram` + `internal/cli`. The adapter changes live in
the telegram package; the selection wiring lives in the CLI assembler. Within the
at-most-two-modules rule.)

## Out of scope

- The `Message`/`MessageKind`/`MessageSource` types — task 113 (this task makes the adapter
  **emit** them).
- The status/info/cancel **handler bodies** — tasks 114/115/116 (in place before 117 runs;
  this is the wiring + kind mapping that feeds them).
- The async control loop + registry + semaphore — task 112.
- Changing the Telegram envelope/armor/audit pipeline (tasks 080/097/098) — preserved
  unchanged; derivation runs only on verified plaintext.
- A from-scratch Telegram client — both adapters already exist.

## Dependencies

- **Tasks 112, 113, 114, 115, 116 — ALL hard dependencies.** The four message kinds and their
  handlers must exist before Telegram can drive them end to end. This task is **last**.
- Task 080/097/098 (Telegram adapters + envelope/armor) — merged.
- ADR 054 §2 — the authoritative design.
