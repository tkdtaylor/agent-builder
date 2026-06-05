# ADR 020: exec-sandbox run adapter seam

**Date:** 2026-06-05
**Status:** Accepted

## Context

agent-builder must run executor work inside an isolated box. During bootstrap,
that box is rented isolation; later, the produced exec-sandbox v0 replaces it.
The supervisor is trusted host-side code, so it needs a narrow contract for
starting contained work without importing or knowing a concrete backend.

The seam must also be compile-checked. Resource and egress controls are part of
the isolation contract, so accepting loosely typed maps would let backend swaps
silently drift.

## Decision

Define an internal exec-sandbox adapter interface with a single `Run` method:
command plus worktree plus typed limits in, structured result plus exit code and
error out.

The interface lives in a small package dedicated to the seam. The package owns
the request, limits, result, and backend fake types. The supervisor stores and
accepts only the interface type and does not import any concrete backend.

The fake backend is in-process and deterministic. It records requests and
returns caller-provided results or errors, allowing supervisor and seam tests to
exercise success, non-zero exit, and backend-error paths without invoking a real
isolation runtime.

## Consequences

- Rented sandbox-runtime and produced exec-sandbox v0 can be swapped behind the
  same compile-checked contract.
- Supervisor code is coupled to the isolation seam, not to backend choice.
- Limits are typed and therefore visible to tests, docs, and backend
  implementors.
- The fake backend proves adapter consumers can be tested without real
  containment; it does not claim isolation behaviour.
