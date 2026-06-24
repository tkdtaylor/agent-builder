# Task 075: F-008 fitness — diagrams render (`fitness-diagrams-render`)

**Project:** agent-builder
**Created:** 2026-06-24
**Status:** backlog

## Goal

Wire the existing `scripts/check-mermaid.py` linter into the verification gate as
fitness rule **F-008** (`fitness-diagrams-render`), so a Mermaid block that GitHub
cannot render fails `make check` instead of merging as invisible drift.

Add a `make fitness-diagrams-render` target that runs `python3 scripts/check-mermaid.py`
(no arguments → scans `README.md` and every `*.md` under `docs/`, including
`docs/architecture/diagrams.md`) and surfaces its exit code. Wire it into the `fitness`
umbrella target and `.PHONY`, and document it as an F-008 row in
`docs/spec/fitness-functions.md` (category `hygiene`, severity `block`).

This adds **no new linter logic** — the script already exists and is maintained. The
task is gate wiring plus the spec row.

## Context

The drift audit (2026-06-24) found that `scripts/check-mermaid.py` runs only inside the
audit's Layer 3 dispatch — nothing in the continuous gate (`make check` = `lint test
fitness`) exercises it. `docs/architecture/diagrams.md` is **authoritative spec** (its
own header says so), so a block with a GitHub parse error renders nothing to a reader
while still passing every existing check. F-008 makes "the diagrams render" a
machine-checkable, blocking invariant.

The script (`scripts/check-mermaid.py`) is zero-dependency and already exits non-zero
on any hazard, so the Makefile target is a thin wrapper — mirror the shape of the
existing `fitness-*` targets (a recipe that runs the tool, prints a PASS line on
success, and lets the non-zero exit propagate on failure).

**Pattern to mirror:** the documented extension procedure in
`docs/spec/fitness-functions.md` ("How to run" → "Add new rules by"), and the simplest
existing single-tool fitness targets. Unlike F-003..F-007 (Go import-graph scans),
F-008 delegates to a Python script, so the recipe is `python3 scripts/check-mermaid.py`
rather than a `go list -deps … | grep` pipeline.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                 | Priority  |
|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-075-01 | `make fitness-diagrams-render` runs `python3 scripts/check-mermaid.py` over the default doc set and exits 0 with a PASS line on the clean tree.                                                                               | must have |
| REQ-075-02 | A Mermaid block with a GitHub-render hazard (e.g. `;` in a label, `[`/`]` in a sequence message) makes the check exit non-zero, naming the offending file/line. Demonstrated per the F-001..F-007 negative-evidence convention (transient fixture, not committed). | must have |
| REQ-075-03 | `docs/spec/fitness-functions.md` gains an F-008 row: rule, category `hygiene`, asserts "every Mermaid block renders on GitHub", threshold `0 render hazards`, check command `make fitness-diagrams-render`, severity `block`, one-line why; "Last updated" date bumped. | must have |
| REQ-075-04 | `fitness-diagrams-render` is a prerequisite of `make fitness` and listed in `.PHONY`; `make fitness` prints the F-008 PASS line and `make check` exits 0 (`All checks passed.`).                                              | must have |

## Readiness gate

- [x] Test spec `075-fitness-diagrams-render-test-spec.md` exists (written first)
- [ ] `scripts/check-mermaid.py` present and passing on the clean tree
  (`python3 scripts/check-mermaid.py` → exit 0) — the wiring target depends on it
- [ ] `make check` green on main before starting

## Acceptance criteria

- [ ] [REQ-075-01] TC-075-01: `make fitness-diagrams-render` exits 0 and prints a PASS line on the clean tree
- [ ] [REQ-075-02] TC-075-02: a transient broken Mermaid fixture produces non-zero exit and a hazard message naming the file/line
- [ ] [REQ-075-03] TC-075-03: F-008 row present in `fitness-functions.md` with the fields above; date bumped
- [ ] [REQ-075-04] TC-075-04: `fitness-diagrams-render` in the `fitness:` prerequisite + `.PHONY`; `make fitness` lists F-008 PASS; `make check` → `All checks passed.`

## Verification plan

- **Highest level achievable:** L5 — the fitness check's observable surface is its own
  stdout / exit code. Running `make fitness-diagrams-render` and seeing PASS (plus a
  demonstrated FAIL against a transient broken fixture) is the verification. Mirrors the
  F-001..F-007 pattern exactly.
- **Harness command:**
  ```
  make fitness-diagrams-render
  make fitness
  make check
  ```
  Expected:
  - `make fitness-diagrams-render` → PASS (exit 0).
  - `make fitness` → `All fitness checks passed.` (exit 0; F-008 in the list).
  - `make check` → `All checks passed.` (exit 0).
- **Runtime observation:** the check is itself a runtime-observable CLI surface; the L5
  evidence is the quoted PASS line and the demonstrated negative FAIL output.

## Out of scope

- Modifying `scripts/check-mermaid.py` (new hazard patterns, a real parser) — F-008
  wires the tool as-is.
- Auto-fixing broken diagrams.
- A separate git-hook or CI invocation — `make check` is the single gate this targets.
