# Test spec — Task 075: F-008 fitness — diagrams render (`fitness-diagrams-render`)

**Linked task:** `docs/tasks/backlog/075-fitness-diagrams-render.md`
**Written:** 2026-06-24
**Status:** ready

## Context

`scripts/check-mermaid.py` is a maintained, zero-dependency linter that flags Mermaid
code-block syntax GitHub's renderer rejects with "Unable to render rich display /
Parse error". A diagram that fails to render is **invisible drift** — the `docs/spec`
treats `docs/architecture/diagrams.md` as authoritative spec, yet a broken block shows
a reader nothing. The drift audit (2026-06-24) found the script runs only inside the
audit's Layer 3 dispatch; nothing in the continuous verification gate exercises it, so
a broken diagram can merge unnoticed.

This task closes that gap by wiring `check-mermaid.py` into the gate as fitness rule
**F-008** (`fitness-diagrams-render`), following the exact extension procedure
documented in `docs/spec/fitness-functions.md` ("Add new rules by: append a row, add a
`fitness-<rule>` target, add it to the `fitness` umbrella prerequisites").

The pattern mirrors the existing F-001..F-007 fitness rules:
- A new `fitness-diagrams-render` Makefile target wraps the underlying tool
  (`python3 scripts/check-mermaid.py`) and surfaces its exit code.
- The target is a prerequisite of `make fitness` (and listed in `.PHONY`), so it runs
  as part of `make check`.
- It is documented as an F-008 row in `docs/spec/fitness-functions.md`.

**Distinction from the other rules:** F-001..F-007 are import-graph / source-scan
checks. F-008 is a **`hygiene`-category** docs-correctness check — the asserted
invariant is "every Mermaid block in the tracked docs renders on GitHub", and the
threshold is `0 render hazards`. The check delegates entirely to the existing script;
this task adds no new linter logic, only the gate wiring and the spec row.

## Requirements coverage

| Req ID     | Test cases  | Covered? |
|------------|-------------|----------|
| REQ-075-01 | TC-075-01   | yes      |
| REQ-075-02 | TC-075-02   | yes      |
| REQ-075-03 | TC-075-03   | yes      |
| REQ-075-04 | TC-075-04   | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-075-01 — `fitness-diagrams-render` passes on the clean tree

- **Requirement:** REQ-075-01
- **Level:** L5 (fitness check; `make fitness-diagrams-render` on the clean tree)
- **Test file / harness:** `Makefile` target `fitness-diagrams-render`

**Input:** `make fitness-diagrams-render` on the current tree (all Mermaid blocks valid).

**Expected output:**
- Target exits 0.
- Prints a PASS line that reflects the underlying script's success, e.g.:
  ```
  PASS fitness-diagrams-render: all Mermaid blocks render on GitHub (no parse hazards).
  ```
  (Exact wording is implementation-defined; the script's own
  `✓ N Mermaid block(s) checked, no GitHub-render hazards found.` may be surfaced
  instead of or alongside the PASS line. Exit code 0 is the load-bearing assertion.)

**Edge cases:**
- The target invokes `python3 scripts/check-mermaid.py` with no file arguments, so it
  scans `README.md` and every `*.md` under `docs/` (the script's default scope) —
  including `docs/architecture/diagrams.md`.

---

### TC-075-02 — A broken Mermaid block makes the check fail (negative)

- **Requirement:** REQ-075-02
- **Level:** L5 (negative evidence; demonstrated without committing a broken diagram)
- **Pattern:** same negative-evidence convention as F-001..F-007

**Input:** A temporary synthetic Markdown file (or a transient edit to a scratch copy)
containing a `mermaid` fence with a known GitHub-render hazard — e.g. a `;` inside a
node label, or `[` `]` in a `sequenceDiagram` message — passed to the target's
underlying script. The broken fixture is **not** committed.

**Expected output:**
- `make fitness-diagrams-render` (or the underlying `python3 scripts/check-mermaid.py
  <broken-file>` it wraps) exits non-zero.
- The output names the offending file and line and describes the hazard, e.g.:
  ```
  <file>:<line>: ';' in a Mermaid label/message — Mermaid treats it as a statement separator
  ```
- The non-zero exit propagates so `make fitness` and `make check` would fail if a
  committed diagram were broken.

**Note:** Demonstrated per the established F-001..F-007 convention — the broken block
is tested against the script transiently, not permanently committed. The verify commit
records the FAIL output as evidence.

---

### TC-075-03 — F-008 documented in `docs/spec/fitness-functions.md`

- **Requirement:** REQ-075-03
- **Level:** L5 (spec grep)

**Assertions:** `docs/spec/fitness-functions.md` gains an F-008 row in the Rules table with:
- **ID:** `F-008`
- **Rule:** names the "Mermaid diagrams render on GitHub" invariant.
- **Category:** `hygiene`.
- **Asserts:** every Mermaid block in the tracked docs is free of GitHub-render hazards.
- **Threshold:** `0 render hazards`.
- **Check command:** `make fitness-diagrams-render`.
- **Severity:** `block`.
- A one-line *why* (e.g. "diagrams.md is authoritative spec; a block that fails to
  render is invisible drift a reader never sees").
- The "Last updated" date at the top of the file is bumped to the task date.

---

### TC-075-04 — `fitness-diagrams-render` wired into `make fitness`; `make check` green

- **Requirement:** REQ-075-04
- **Level:** L5 (Makefile inspection + run)

**Assertions:**
- `fitness-diagrams-render` appears in the `fitness:` prerequisite list in the
  `Makefile` (so it runs as part of `make check`).
- `fitness-diagrams-render` appears in the `.PHONY` list.
- `make fitness` prints the F-008 PASS line among the other fitness check lines and
  exits 0 (`All fitness checks passed.`).
- `make check` → `All checks passed.` (exit 0; all existing checks plus F-008 pass).

---

## Verification plan

- **Highest level achievable:** L5 — the fitness check's observable surface is its own
  stdout / exit code; running it and seeing PASS (plus a demonstrated FAIL against a
  transient broken fixture) is the verification. Mirrors the F-001..F-007 pattern.
- **L5 harness command:**
  ```
  make fitness-diagrams-render
  make fitness
  make check
  ```
  Expected:
  - `make fitness-diagrams-render` → PASS (exit 0).
  - `make fitness` → `All fitness checks passed.` (exit 0; F-008 in the list).
  - `make check` → `All checks passed.` (exit 0).
- **Negative evidence (TC-075-02):**
  Demonstrate against a transient broken Mermaid fixture that the script produces the
  correct FAIL output and non-zero exit. Document in the `Verified by` column.

## Out of scope

- Changing or extending `scripts/check-mermaid.py` itself — this task only wires the
  existing tool into the gate. New hazard patterns are a separate change to the script.
- A full Mermaid parser — the script is an intentional heuristic linter for the
  GitHub-render offenders the project has actually hit; F-008 inherits that scope.
- Auto-fixing broken diagrams — the gate reports; the fix is the author's.
- Wiring the script into a git hook or CI separately — `make check` (and the `strict`
  profile's Stop-time fitness run) is the single gate this task targets.
