# Task 050: rootless egress sidecar fix (idempotent nftables + writable egress-state)

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** backlog

## Goal

Make the execution-box default-deny egress sidecar work end-to-end on rootless podman by
fixing the two bugs ADR 029 identifies, **without changing any allow/deny semantics**:

1. **Idempotent nft ruleset** — `containment/execution-box/egress-sidecar.sh` emits an
   empty `table inet agent_builder_egress { }` declaration *before* `flush table inet
   agent_builder_egress`, then the populated table. `flush` no longer targets a missing
   table on a fresh netns. The `@allowed_tcp4` set, the allow rule, the `lo`/established
   accepts, the final `reject`, and `policy drop` are unchanged.
2. **Writable per-run egress-state dir** — `containment/execution-box/run.sh` `chmod 0777`s
   the per-run `mktemp -d` egress-state directory (the bind mount the sidecar writes
   `ready`/`fail` to) so the keep-id-mapped sidecar root (host subuid 100000) can write
   the readiness markers; host and workload read them via other-read perms.

This unblocks the L6 egress probe (015), which currently fails with `TC-001 FAIL:
nftables default-deny egress rules failed to apply` and `can't create /egress-state/fail:
Permission denied`.

## Context

- Tech stack: bash. Two files: `containment/execution-box/egress-sidecar.sh` (ruleset
  emission, ~lines 24–60) and `containment/execution-box/run.sh` (egress-state dir
  creation, ~line 581). No Go code.
- **ADR 029** governs this — Option A (keep strict in-pod nftables; fix it to work
  rootless). Options B (pasta/slirp4netns) and C (gate behind rootful) were rejected. Read
  ADR 029 before implementing.
- Reproduction-confirmed on the real host (podman 5.7.0, rootless, keep-id, subuid 100000):
  nft applies fine in the pod netns with the idempotent `table {}` / `flush` / `table {…}`
  idiom (APPLY-OK); and a `0777` egress-state dir lets the mapped-root sidecar write
  `ready`/`fail` while host (`HOST-READ-OK`) and workload (`WORKLOAD-READ-OK`) read them.
- The egress allowlist is the **load-bearing** control for the token-in-box risk. This
  task touches only ruleset *emission order* and the readiness-dir *mode* — not the
  deny/allow logic, not the policy, not NET_ADMIN, not the workload cap-drop posture.
- **Model tier: balanced (sonnet)** — a small, security-relevant edit in the egress path
  of two bash files; the allow/deny semantics must be preserved exactly.
- Dependencies: builds directly on task 049 (pod/userns launch fix); independent of
  045/046/047/048.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-050-01 | `egress-sidecar.sh` emits `table inet agent_builder_egress { }` before `flush table inet agent_builder_egress`, before the populated table block; the ruleset applies idempotently on a fresh netns | must have |
| REQ-050-02 | The populated table is otherwise unchanged: `set allowed_tcp4` (`type ipv4_addr . inet_service`, `flags interval`, resolved IPv4-and-port elements), `policy drop`, `oifname "lo" accept`, `ct state established,related accept`, `ip daddr . tcp dport @allowed_tcp4 accept`, `reject` — default-deny + exact-pair allowlist preserved | must have |
| REQ-050-03 | `run.sh` makes the per-run egress-state `mktemp -d` world-writable (`chmod 0777`) immediately after creating it and before launching the sidecar, with an explanatory comment naming the keep-id subuid mapping as the reason | must have |
| REQ-050-04 | The resolved-allowlist bind mount stays `ro`; only the egress-state dir mode is widened. No other egress arg (NET_ADMIN, read-only, cap-drop, no-new-privileges, `--user 0:0`, `--dns none`, `--add-host`) changes | must have |
| REQ-050-05 | Real-host `--egress-probe` installs the default-deny ruleset (`TC-001 PASS`), reaches the allow assertion (`TC-003 PASS`) and both deny assertions (`TC-004 PASS` host + direct-IP), and the launcher exits 0 | must have |
| REQ-050-06 | `docs/spec/behaviors.md` (B-010) and `docs/spec/interfaces.md` updated in the feat commit to reference ADR 029 for the rootless idempotent-ruleset + writable readiness-dir behavior; allow/deny contract text unchanged | must have |

## Readiness gate

- [x] Test spec `050-rootless-egress-sidecar-fix-test-spec.md` exists
- [x] ADR 029 written and Accepted
- [x] All acceptance criteria below have a linked REQ ID
- [ ] No blocking dependencies (049 merged on main)

## Acceptance criteria

- [ ] [REQ-050-01] emitted ruleset: empty table decl before `flush` before populated table (TC-050-01)
- [ ] [REQ-050-02] emitted ruleset still has allowed_tcp4 set + policy drop + allow rule + reject unchanged (TC-050-02)
- [ ] [REQ-050-03] egress-state bind-mount source is mode 0777 at sidecar launch (TC-050-03)
- [ ] [REQ-050-04] resolved-allowlist mount stays ro; no other egress arg changes (TC-050-03 + diff review)
- [ ] [REQ-050-05] real-host `--egress-probe`: TC-001 PASS + TC-003 PASS + TC-004 PASS (host + IP) + exit 0 (TC-050-04)
- [ ] [REQ-050-06] spec (B-010 + interfaces.md) references ADR 029; allow/deny contract unchanged

## Verification plan

- **Highest level achievable:** **L6** — a real `--egress-probe` run that installs the
  default-deny ruleset and proves allow (allowlisted reachable) + deny (non-allowlisted +
  direct-IP refused) and exits 0 on the rootless host. The stub L5 cannot prove rootless
  nftables enforcement; the real run is the proof.
- **L5 harness:** (i) direct `egress-sidecar.sh` run with a stub `nft` capturing the
  emitted ruleset (TC-050-01/02); (ii) the stub-podman `tests/` harness extended to capture
  the egress-state bind-mount source mode at sidecar launch (TC-050-03). Both gate in
  `make check` / `make fitness`.
- **L6 evidence:** quote the verbatim `--egress-probe` output (TC-001/TC-003/TC-004 PASS +
  exit 0) from the real rootless host.
- **Cross-module state risk:** none — confined to `egress-sidecar.sh` emission + `run.sh`
  egress-state dir mode.
- **Runtime-visible surface:** the emitted nft ruleset text, the egress-state dir mode, the
  `--egress-probe` allow/deny outcome + exit code.

## Out of scope

- Egress allowlist policy / which hosts / `@allowed_tcp4` model / default-deny `policy
  drop` / NET_ADMIN / workload cap-drop posture — all unchanged.
- `--userns`/`--pod` launch mechanics (task 049) and the non-pod probe paths (014/016/033).
- ADR 029 Options B (pasta/slirp4netns) and C (rootful/runsc gating) — both rejected.
- IPv6 egress — remains fail-closed in the bootstrap filter, unchanged.

## Notes

- Keep the change surgical: the idempotency fix is two extra `printf` lines at the top of
  the ruleset heredoc in `egress-sidecar.sh`; the perms fix is one `chmod 0777` line after
  `egress_state="$(mktemp -d)"` in `run.sh`. Do not refactor the surrounding egress block.
- The `0777` mode is an ADR-029-justified, documented exception (transient, secret-free,
  per-run, `rm -rf`'d on exit). Add the comment ADR 029 requires so a future edit cannot
  silently revert it to 0700 and re-break the rootless handshake.
- The fail-loud guards from task 045 on the egress `podman run`/`pod create` stay.
