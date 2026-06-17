# Task 040: Wire `audit-trail verify` as the block-severity integrity gate

**Project:** agent-builder
**Created:** 2026-06-16
**Status:** backlog

## Goal

Surface the block's own `audit-trail verify` as a **block-severity** integrity check over a produced run's chain — an `internal/audit` helper that invokes the block's verifier and maps `valid == false` (with `tamper_detected_at`) to a failing gate. agent-builder does **not** reimplement tamper detection; it consumes the block's frozen `verify` verb (ADR 025 decision 3 carried forward — chain integrity is a `block`-severity check, not a warning).

## Context

- Tech stack: Go
- Governing ADR: `docs/architecture/decisions/026-audit-trail-consume-shipped-block.md` (Option A; supersedes ADR 025). Decision 3 (chain-integrity severity = block) is unchanged; what changes is *who verifies* — the shipped block, not an in-repo `Verify`.
- **This task replaces the former in-repo `audit.Verify`.** The block already ships `verify() -> { valid, tamper_detected_at, message }` (CLI `audit-trail verify --logfile <path>`, exit 0 valid / 1 tamper) with RFC 8785 canonicalization and edit/reorder/truncation detection — re-implementing it is the duplication ADR 026 removes.
- Consumes the chain the `BlockSink` adapter (task 039) produces via the block's `emit`.
- **Model tier: balanced (sonnet)** — a subprocess invocation + exit-code/JSON mapping to a typed gate result; the adversarial tamper-detection itself is the block's, already tested.
- Dependencies: 039.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-040-01 | An `internal/audit` helper (e.g. `VerifyChain(binPath, logfile)`) invokes `audit-trail verify --logfile <path>` and returns a typed result `{ Valid bool; TamperedAt *int; Message string }` parsed from the block's response/exit code | must have |
| REQ-040-02 | `Valid == false` (or a non-zero/error exit that is not a clean "valid") maps to a **block-severity** gate failure that names the `tamper_detected_at` seq when the block provides it | must have |
| REQ-040-03 | A missing/non-executable block binary, or an unreadable logfile, is a hard named error — never reported as "valid" | must have |
| REQ-040-04 | The helper reaches the block over `os/exec` only — no `audit-trail` Go import, no executor/LLM/web import — `internal/audit` stays a leaf | must have |
| REQ-040-05 | `docs/spec/behaviors.md` documents the integrity gate (block invokes `audit-trail verify`; `valid == false` ⇒ block-severity failure) and `docs/spec/data-model.md` points to the block's `verify` contract for the detection boundary (which tamper classes the block catches), in the same commit | must have |

## Readiness gate

- [x] Test spec `040-audit-verify-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [ ] Blocking task 039 complete

## Acceptance criteria

- [ ] [REQ-040-01] `VerifyChain` over an intact `BlockSink`-produced chain returns `Valid == true`; the parsed result carries the block's record count/seq when present
- [ ] [REQ-040-02] A chain the block reports tampered (e.g. a byte edited on disk) yields `Valid == false` with `TamperedAt` set, and the gate treats it as block-severity (non-zero) — driven by the block's own detection, asserted against a fixture chain
- [ ] [REQ-040-03] A missing binary or unreadable logfile returns a non-nil named error, distinct from a clean `Valid == false`
- [ ] [REQ-040-04] `go list -deps ./internal/audit/...` shows no `audit-trail`/executor/LLM/web package; the helper uses `os/exec` only
- [ ] [REQ-040-05] `docs/spec/behaviors.md` (integrity gate) and `docs/spec/data-model.md` (pointer to the block's detection boundary) updated in the feat commit

## Verification plan

- **Highest level achievable:** L5 — a harness produces a real chain via the `BlockSink` (task 039), runs `VerifyChain` and asserts `Valid == true`; then tampers a byte on disk and asserts the block (through `VerifyChain`) reports `Valid == false` with `TamperedAt` set. (CI-without-binary fallback: a recorded-exec stub returning the block's documented valid/tampered JSON + exit codes; the real-binary path is opt-in and the L5 evidence states which ran.)
- **Level 5 — Validation harness command (if applicable):**
  ```
  go test -count=1 -v ./internal/audit/... -run 'TestVerifyChain'
  ```
  Expected final assertion: intact chain ⇒ `Valid == true`; tampered chain ⇒ `Valid == false` with `TamperedAt` populated; missing-binary ⇒ named error (not "valid").
- **Level 6 — Operator observation (if applicable):** optional — tamper a produced `/tmp/a.log` and run `audit-trail verify --logfile /tmp/a.log`, observing exit 1 + `tamper_detected_at`. Not required for ✅ given L5.
- **Cross-module state risk:** consumes the chain produced by task 039 and the block's frozen `verify` CLI surface. The tamper detection is the block's; this task only asserts the agent-builder side maps the block's verdict to a block-severity gate. A drift in the block's `verify` output shape is what the L5 real-binary path catches.
- **Runtime-visible surface:** the `audit-trail verify` subprocess result mapped to the typed gate result. The executor must run the harness and quote the valid/tampered verdicts.

## Out of scope

- Re-implementing tamper detection (first-broken-link, edit/reorder/truncation logic) — **owned by the block** (the duplication ADR 026 removes).
- Wiring the gate into `make check` as a standing fitness target over every run — this task delivers the helper + its block-severity semantics; the run-time invocation point is task 041's e2e (which verifies a produced chain), and a standing CLI/check surface can follow.
- Defense against a privileged full-file rewrite — the block ships signed checkpoints + Rekor anchoring for that; surfacing those verbs through agent-builder is a later integration (ADR 026 deferred list).
- Supervisor wiring (task 041), the fitness isolation check (task 042).
- **Egress-attempt audit events** — deferred and spike-gated per ADR 026 decision 2.

## Notes

- The value here is *honest severity*, not re-built crypto: a tampered chain must fail the gate (block), and an unavailable verifier must error loudly — never silently pass as "valid".
- Keep `internal/audit` a leaf: `os/exec` only — task 042's F-005 depends on it.
- Update `docs/spec/behaviors.md` + `docs/spec/data-model.md` (pointer to the block's detection boundary, not a re-spec of it) in the same commit. Do not edit spec during backlog authoring.
