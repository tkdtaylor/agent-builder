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
| REQ-050-05 | Real-host `--egress-probe`: the sidecar installs the default-deny ruleset (`TC-001 PASS`) and writes its `ready`/`fail` markers under rootless keep-id; the probe advances **past the sidecar** to workload-member start (the two sidecar bugs are fixed). The real run surfaced two *further* rootless-pod constraints downstream of the sidecar (`--add-host` must be on `pod create`; runsc cannot join a rootless pod userns) — full allow/deny + exit-0 green is delivered by **task 051 under ADR 030**, not this task. See Notes. | must have |
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
- [ ] [REQ-050-05] real-host `--egress-probe`: sidecar applies default-deny (`TC-001 PASS`) + writes `ready` marker + probe advances **past the sidecar** to workload start (TC-050-04). Full exit-0 green is task 051 (ADR 030).
- [ ] [REQ-050-06] spec (B-010 + interfaces.md) references ADR 029; allow/deny contract unchanged

## Verification plan

- **Highest level achievable:** **L6 (past-sidecar)** — a real `--egress-probe` run on the
  rootless host where the sidecar installs the default-deny ruleset (`TC-001 PASS`) and
  writes its readiness markers, and the launcher advances past the sidecar to workload
  start without the nftables `No such file or directory` or `/egress-state/fail:
  Permission denied` errors. The stub L5 cannot prove rootless nftables enforcement; this
  real run is the proof that *the two sidecar bugs are fixed*. (Full allow/deny + exit-0
  green requires task 051's `--add-host`-on-pod + runc-egress-runtime changes per ADR 030,
  which are downstream of and out of scope for this task.)
- **L5 harness:** (i) direct `egress-sidecar.sh` run with a stub `nft` capturing the
  emitted ruleset (TC-050-01/02); (ii) the stub-podman `tests/` harness extended to capture
  the egress-state bind-mount source mode at sidecar launch (TC-050-03). Both run standalone
  (`bash containment/execution-box/tests/egress-rootless-test.sh`) as L5 evidence recorded
  in `coverage-tracker.md` — matching the 045–049 execution-box harness convention (not
  wired into `make check`, which runs `go test`; `make check`/`make fitness` gate the Go +
  fitness layers).
- **L6 evidence:** quote the verbatim `--egress-probe` output showing the run advances
  past the sidecar (no `No such file or directory` / no `/egress-state/fail: Permission
  denied`) — i.e. the two sidecar bugs are fixed — and name the downstream blocker the run
  then hits (the `--add-host`/runsc-pod constraints handed to task 051).
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
- **Discovery during real-host validation (2026-06-17):** fixing the two sidecar bugs let
  the probe advance past the sidecar and exposed two *further*, independent rootless-pod
  constraints downstream — (1) `--add-host` (extra host entries) must be declared on
  `podman pod create`, not on the pod member (`network cannot be configured when it is
  shared with a pod`); (2) gVisor/runsc cannot join a rootless pod's keep-id userns
  (`gofer: error setting namespace of type user ... invalid argument`), so the networked
  egress workload must run under `runc`. Both are out of scope for this sidecar task and
  are handled by **task 051 under ADR 030**. Verified on the host that with those two
  changes the egress probe reaches `TC-003`/`TC-004 PASS` and exits 0 under runc — so the
  egress allowlist (the load-bearing control) is proven enforceable rootless.
