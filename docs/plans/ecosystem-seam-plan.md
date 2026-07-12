# Ecosystem seam plan (2026-07-11)

Cross-repo coordination for the feature round planned on 2026-07-11 from a full review of agent-builder and its sibling blocks. The review found the individual blocks healthy and the highest value at the seams between them: signals emitted that nothing consumes (policy obligations, deletion hashes, armor incidents), and guarantees advertised that are not real yet because the other side of the seam is unbuilt (vault identity binding, memory-guard read isolation). Each item below is a task file in its own repo, written to be executable standalone; this document only records the cross-repo dependency graph and sequencing. Task details live in each repo's `docs/tasks/backlog/`.

Related decision: ADR 065 (durable execution via a thin run journal; Temporal rejected).

## Workstreams and task map

| Workstream | Repo: tasks | Depends on |
|---|---|---|
| Close the policy obligation loop | agent-builder: 164 (tier_select), 165 (vault_injection_floor), 166 (audit_emit decision trace) | none |
| Durable run journal + sustained autonomy | agent-builder: 167 (RunStore), 168 (resume), 169 (autonomy loop) | 167 → 168 → 169; ADR 065 |
| Human approval pause/resume | agent-builder: 170 (pause on require_approval), 171 (Telegram routing + resume) | 167; 170 → 171 |
| Persistent cross-session memory | agent-builder: 172 (guarded memory store), 173 (PlanStore on it); memory-guard: 015 (real MemoryStore) | 172 pairs with memory-guard 015; 172 → 173 |
| Daemon + schedules | agent-builder: 174 (daemon), 175 (scheduled goals) | 167, 168; 174 → 175 |
| Skill system | agent-builder: 176 (ADR 066 + registry seam), 177 (coding as first skill) | 176 → 177; benefits from 169, 173 |
| Identity substrate adoption | agent-mesh: 008 (identity-propagation contract), 009 (importable package); policy-engine: 009 (identity subjects + per-identity rate limits); vault: 011 (SPIFFE binding seam); memory-guard: 016 (read isolation) | agent-mesh 008 unblocks the other three; memory-guard 016 also needs 015 |
| Signed sandbox attestation | exec-sandbox: 020 (trust-root ADR), 021 (implementation); vault: 010 (verify at handle binding) | exec-sandbox 020 → 021 → vault 010 |
| Shared forensic log | armor: 134 (emit incidents to audit-trail); memory-guard: 017 (emit detections + deletion hashes); audit-trail: 020 (indexed query API) | none (emit contract is frozen and shipped) |
| dep-scan spine hygiene | dep-scan: 112 (cached-verdict attribution), 113 (version pin channel); code-scanner: 001 (consume JSON contract), 002 (gate cached verdicts + shared pin); reverse-engineer: 001 (shared pin) | code-scanner 001 is free; code-scanner 002 and reverse-engineer 001 need dep-scan 112/113 |

## Sequencing

Wave 1 (no cross-repo blockers, any order): agent-builder 164-169, agent-mesh 008-009, exec-sandbox 020-021, audit-trail 020, armor 134, memory-guard 015, dep-scan 112-113, code-scanner 001.

Wave 2 (unblocked by wave 1): agent-builder 170-177, vault 010-011, policy-engine 009, memory-guard 016-017, code-scanner 002, reverse-engineer 001.

Within agent-builder the chains are 167 → 168 → 169, 170 → 171, 172 → 173, 174 → 175, 176 → 177; everything else is independent.

## Working the plan

One task = one repo = one branch, per each repo's own workflow. When a task's dependency lives in another repo, the task file names it; do not start the dependent task until the prerequisite is merged and verified in its home repo. agent-builder remains the composition point: where a seam needs a joint contract (attestation trust root, identity propagation), the producing repo owns the ADR and the consuming repo's task references it.
