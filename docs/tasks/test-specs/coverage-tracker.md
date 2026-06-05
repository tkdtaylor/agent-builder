# Test Coverage Tracker

**Project:** agent-builder

## Rules

- Test specs are written **before** implementation begins — no exceptions
- A task is **not** "complete" because the feat commit landed and tests passed. See the verification ladder below.
- Each row maps a task ID to its spec file, current test status, and the verification level achieved

## Coverage

| Task ID | Feature | Spec file | Tests written | Status | Verified by |
|---------|---------|-----------|---------------|--------|-------------|
| 001 | Walking skeleton & project setup | 001-walking-skeleton-test-spec.md | ✅ | 🟡 | — (pending spec-verifier) |

## Status key

| Symbol | Meaning |
|--------|---------|
| ✅ | **Verified** — validation harness exercised the live runtime path, or operator observed the targeted behaviour |
| 🟡 | **Code merged** — feat-commit landed, unit tests + fitness + CI green, but runtime/live behaviour not yet observed |
| ⏳ | In progress |
| ❌ | Not started |
| ⚠️ | Blocked |

## Verification ladder

A task earns 🟡 at levels 1–4 and ✅ only at level 5 or 6. The `Verified by` column records which level the row reached.

| Level | Evidence | Status this earns |
|-------|----------|-------------------|
| 1 | Code merged | 🟡 |
| 2 | Unit tests pass (paste verbatim final line of `make check`) | 🟡 |
| 3 | `make fitness` passes (verbatim closing line) | 🟡 |
| 4 | CI passes (`gh run watch <id> --exit-status` → success) | 🟡 |
| 5 | **Validation harness** exercises the live runtime path end-to-end — paste the command and the final assertion line | ✅ |
| 6 | **Operator-observed** — operator (or executor via `cargo run` / `npm start` / etc.) saw the targeted behaviour in stdout / logs / UI | ✅ |

If the task targets runtime-observable behaviour (logging, CLI args, TUI, server endpoints, file outputs, side effects), level 5 or 6 is **required** before flipping to ✅. If the task only adds an internal helper covered by unit tests, level 2 may be sufficient — but in that case the row's `Verified by` should explicitly say "unit-test-only; no runtime surface" so future readers don't mistake silence for verification.

## Rule

**The task-executor commits at 🟡 by default.** Only the main session (after spec-verifier APPROVE + the appropriate level-5/6 evidence) updates the row to ✅, in a separate commit titled `verify: confirm task NNN — <level-5/6 evidence>`. This keeps the verification step visible in git history and prevents "merged ≠ done" drift.
