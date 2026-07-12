# Ecosystem seam plan (2026-07-11)

Cross-repo coordination for the feature round planned on 2026-07-11 from a full review of agent-builder and its sibling blocks. The review found the individual blocks healthy and the highest value at the seams between them: signals emitted that nothing consumes (memory-guard deletion hashes, armor incidents kept in a private store), and guarantees advertised that are not real yet because the other side of the seam is unbuilt (vault identity binding, durable identity isolation in memory-guard). Each item below is a task file in its own repo, written to be executable standalone; this document records the cross-repo dependency graph. The live execution tracker for working through every backlog is `archive/ecosystem-backlog-execution-plan-2026-07-11.md`.

Related decision: ADR 065 (durable execution via a thin run journal; Temporal rejected).

## Survey corrections found during planning

Task planning verified every briefed gap against the code and corrected four stale survey findings. Executors should trust the task files, not the original survey:

- The policy obligation loop is already closed in agent-builder: tasks 072/073 consume `tier_select`, `vault_injection_floor`, and `audit_emit` end-to-end (Adopted L5 in the roadmap). Task IDs 164-166 were repurposed for residual hardening gaps in the same area.
- exec-sandbox's attestation is not random bytes: ADR 014 (2026-06-20) shipped ephemeral per-run self-attestation. Tasks 020-021 upgrade it to host-key attestation via ADR 014's documented reopening condition.
- memory-guard's ADR-004 (identity propagation) is Accepted, not Proposed, and exact-string identity isolation already shipped with its task 009. Task 016 covers the genuine remainder: isolation that survives a restart, seam-scoped lookup, and a shared scope.
- memory-guard's audit-sink machinery (OCSF builders, fail-open wrapper, deletion events) shipped with its task 010; task 017 adds only the real socket transport to audit-trail.

## Workstreams and task map

| Workstream | Repo: tasks | Depends on |
|---|---|---|
| Policy/obligation hardening | agent-builder: 164 (tier_select value allowlist, fail closed), 165 (memoryguard `validate_read` client verb), 166 (audit policy transport failures distinctly from denies) | 164 → 166; 165 feeds 172 |
| Durable run journal + sustained autonomy | agent-builder: 167 (RunStore), 168 (resume), 169 (autonomy loop) | 167 → 168 → 169; ADR 065 |
| Human approval pause/resume | agent-builder: 170 (pause on require_approval), 171 (channel routing + resume) | 167, 168; 170 → 171 |
| Persistent cross-session memory | agent-builder: 172 (guarded memory store), 173 (PlanStore on it); memory-guard: 015 (file-backed store) | 165 and 167 → 172 → 173; 172 pairs with memory-guard 015 but runs against the current guard too |
| Daemon + schedules | agent-builder: 174 (daemon), 175 (scheduled goals) | 167, 168; 174 → 175 |
| Skill system | agent-builder: 176 (ADR 066 + registry seam), 177 (coding as first skill) | 176 → 177 |
| Identity substrate adoption | agent-mesh: 008 (verified-principal contract + `verify-identity` verb + fixtures), 009 (importable library package); policy-engine: 009 (identity subjects + per-identity rate limits); vault: 011 (SPIFFE binding seam); memory-guard: 016 (durable identity isolation) | agent-mesh 008 before 009; agent-mesh 008 unblocks vault 011 and policy-engine 009's principal validation (policy-engine 009 may start earlier with its documented trusted-as-given caveat); memory-guard 016 needs 015 |
| Signed sandbox attestation | exec-sandbox: 020 (trust-root ADR, host-key upgrade of ADR 014), 021 (implementation); vault: 010 (verify at handle binding) | exec-sandbox 020 → 021 → vault 010 (vault 010's seam and fixtures may start earlier; only the payload constant waits) |
| Shared forensic log | armor: 134 (emit incidents to audit-trail); memory-guard: 017 (audit socket sink; deletion hashes finally get a consumer); audit-trail: 020 (indexed query API) | none (the emit contract is frozen and shipped) |
| dep-scan spine hygiene | dep-scan: 112 (cache-verdict attribution), 113 (version pin channel); code-scanner: 001 (stable `--format json` contract), 002 (gate cached verdicts + shared pin); reverse-engineer: 001 (shared pin via sync script) | code-scanner 001 is free; code-scanner 002 needs dep-scan 112 and 113; reverse-engineer 001 needs dep-scan 113 |

## Sequencing

Wave 1 (no cross-repo blockers, any order): agent-builder 164-168, agent-mesh 008-009, exec-sandbox 020-021, audit-trail 020, armor 134, memory-guard 015, dep-scan 112-113, code-scanner 001, policy-engine 009 (trusted-as-given caveat).

Wave 2 (unblocked by wave 1): agent-builder 169-177, vault 010-011, memory-guard 016-017, code-scanner 002, reverse-engineer 001.

Within agent-builder the chains are 164 → 166, 167 → 168 → 169, 170 → 171, (165, 167) → 172 → 173, 174 → 175, 176 → 177; everything else is independent.

## Working the plan

One task = one repo = one branch, per each repo's own workflow. When a task's dependency lives in another repo, the task file names it; do not start the dependent task until the prerequisite is merged and verified in its home repo. agent-builder remains the composition point: where a seam needs a joint contract (attestation trust root, identity propagation), the producing repo owns the ADR and the consuming repo's task references it.
