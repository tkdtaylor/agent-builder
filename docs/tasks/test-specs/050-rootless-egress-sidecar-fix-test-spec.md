# Test spec — Task 050: rootless egress sidecar fix (idempotent nftables + writable egress-state)

**Task:** 050-rootless-egress-sidecar-fix
**Created:** 2026-06-17
**ADR:** 029 (keep the strict in-pod nftables default-deny egress sidecar; fix it to work rootless)

## Context

Task 049 fixed the `--userns`/`--pod` launch conflict, which unblocked pod + sidecar
*start* and exposed two real-host bugs in the egress sidecar path on rootless podman
5.7 (cgroup v2, kernel 7.0.0, `--userns=keep-id`, subuid base 100000):

```
/scratch/agent-builder-egress.nft:1:18-37: Error: No such file or directory; did you
  mean table 'agent_builder_egress' in family inet?
TC-001 FAIL: nftables default-deny egress rules failed to apply
/usr/local/bin/execution-box-egress-sidecar: line 11: can't create /egress-state/fail:
  Permission denied
```

ADR 029 establishes (reproduction-confirmed on the real host) that **nft works fully in
the rootless pod netns** — these are two ordinary bugs, not a rootless capability wall:

- **(a) Idempotency bug.** `egress-sidecar.sh` emits `flush table inet
  agent_builder_egress` as the *first* statement, before the table exists, so `nft -f`
  errors on a fresh netns. Fix: declare an empty `table inet agent_builder_egress { }`
  *before* the `flush`, then the populated table. The `@allowed_tcp4` set, the allow
  rule, the `lo`/established accepts, the final `reject`, and `policy drop` are
  **unchanged**.
- **(b) egress-state ownership bug.** The sidecar runs `--user 0:0`; under keep-id,
  in-pod root maps to host subuid 100000, which cannot write the host-owned (`kevin`/1000)
  `/egress-state` bind mount (per-run `mktemp -d`, mode 0700). Fix: `chmod 0777` the
  per-run egress-state dir in `run.sh` so the mapped-root sidecar writes the secret-free
  `ready`/`fail` markers, which host and workload read via other-read perms.

**Load-bearing invariant (do NOT weaken):** the egress allowlist / default-deny control
of ADR 015 is preserved exactly. This task changes only (a) the *order* the otherwise
identical ruleset is emitted and (b) the *permission mode* of the transient readiness
directory. No allow/deny semantics change.

## Test cases

### TC-050-01 (L5) — the emitted nft ruleset is idempotent: table declared before flush
- **Mechanism:** run `egress-sidecar.sh` directly with a stub `nft` on `PATH` that copies
  its `-f <file>` ruleset to a capture path and exits 0, a stub resolved-allowlist
  (e.g. `1.2.3.4 443 ...` rows in the resolved format), and `EXEC_BOX_EGRESS_STATE_DIR`
  pointed at a writable temp dir. Capture the emitted ruleset text.
- **Assertion:** in the emitted ruleset, `table inet agent_builder_egress { }` (the empty
  declaration) appears **before** `flush table inet agent_builder_egress`, which appears
  **before** the populated `table inet agent_builder_egress {` block. The sidecar reaches
  its `TC-001 PASS` line and writes the `ready` marker (stub `nft` succeeds).

### TC-050-02 (L5) — allow/deny semantics unchanged (anti-weakening guard)
- **Mechanism:** same capture as TC-050-01.
- **Assertion:** the emitted populated table still contains, unchanged: `set allowed_tcp4`
  with `type ipv4_addr . inet_service` + `flags interval`; the resolved IPv4-and-port
  element(s) from the allowlist; `type filter hook output priority 0; policy drop;`;
  `oifname "lo" accept`; `ct state established,related accept`;
  `ip daddr . tcp dport @allowed_tcp4 accept`; and the trailing `reject`. (Default-deny
  and the exact-pair allowlist must survive the idempotency edit.)

### TC-050-03 (L5) — run.sh makes the per-run egress-state dir writable by the mapped-root sidecar
- **Mechanism:** drive the `--egress-probe` path of `run.sh` with the stub-podman harness
  (pattern from `tests/userns-pod-test.sh`). The stub `podman`, when it sees the sidecar
  `run -d`, stats the mode of the host directory passed as the `--mount
  type=bind,...,target=/egress-state` source and records it to a capture file, then exits.
- **Assertion:** the captured mode of the egress-state bind-mount source is `0777`
  (world-writable) at the moment the sidecar is launched — i.e. `run.sh` chmod'd the
  per-run `mktemp -d` before starting the sidecar, so a keep-id-mapped foreign-uid root
  can write the readiness markers. (The resolved-allowlist mount stays `ro`; only the
  state dir is widened.)

### TC-050-04 (L6, real host) — the real `--egress-probe` enforces default-deny and exits 0
- **Mechanism (real host, rootless podman 5.x, runsc-rootless wrapper, gate-tools
  populated):** `bash containment/execution-box/run.sh --worktree . --egress-probe`.
- **Assertion (the proof of ADR 029):** the run prints, in order:
  - `TC-001 PASS: egress sidecar installed nftables default-deny output policy` (the
    sidecar applied the ruleset — no `No such file or directory`, no `Permission denied`);
  - `TC-003 PASS: allowlisted connect succeeded: <allow_host>:<port>` (an allowlisted
    host:port is reachable);
  - `TC-004 PASS: non-allowlisted connect blocked: <deny_host>:<port>` AND `TC-004 PASS:
    direct IP bypass blocked: <deny_ip>:<port>` (a non-allowlisted host and a direct-IP
    target are both refused under the default-deny policy);
  - and the launcher **exits 0**.
  Record the verbatim output. This L6 run is the only thing that can prove rootless
  nftables enforcement; the L5 stub cannot. A pass here is the evidence for the `verify:`
  commit.

## Verification plan

- **Highest level achievable:** **L6** — a real `--egress-probe` run that installs the
  default-deny ruleset, then proves allow (allowlisted reachable) and deny (non-allowlisted
  + direct-IP refused) and exits 0 on the rootless host.
- **L5 harness:** (i) a direct `egress-sidecar.sh` invocation with stub `nft` capturing
  the emitted ruleset (TC-050-01/02); (ii) the stub-podman `tests/` harness extended to
  capture the egress-state bind-mount source mode at sidecar launch (TC-050-03). Both run
  with no live container and gate in `make check`/`make fitness`.
- **L6 evidence:** quote the verbatim `--egress-probe` output (TC-001/TC-003/TC-004 PASS
  + exit 0) from the real rootless host.
- **Cross-module state risk:** none — the change is confined to `egress-sidecar.sh`
  ruleset emission and the `run.sh` egress-state dir mode.
- **Runtime-visible surface:** the emitted nft ruleset text, the egress-state dir mode,
  and the `--egress-probe` allow/deny outcome + exit code.

## Out of scope

- The egress allowlist *policy* (which hosts/ports), the `@allowed_tcp4` model, the
  default-deny `policy drop`, NET_ADMIN on the sidecar, the workload `--cap-drop=all` /
  `--security-opt=no-new-privileges` / DNS-disabled posture — all unchanged.
- The `--userns`/`--pod` launch mechanics (task 049) and the non-pod probe paths
  (014/016/033).
- Replacing nftables with pasta/slirp4netns (ADR 029 Option B, rejected) or gating egress
  behind rootful/runsc-policy (Option C, rejected).
- IPv6 egress (remains fail-closed in the bootstrap filter, unchanged).
