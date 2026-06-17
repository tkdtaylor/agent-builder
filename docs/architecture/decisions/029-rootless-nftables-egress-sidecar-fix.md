# ADR 029: Keep the strict in-pod nftables default-deny egress sidecar and fix it to work rootless

**Date:** 2026-06-17
**Status:** Accepted
**Task:** governs the egress-sidecar rootless fix (`containment/execution-box/egress-sidecar.sh` ruleset emission + `containment/execution-box/run.sh` egress-state dir permissions)
**Related:** ADR 015 (default-deny execution-box egress allowlist — the control this ADR preserves), ADR 014 (rootless Podman execution-box profile), ADR 027 / ADR 028 (same "verify where you reliably can, keep the load-bearing control intact" principle from tasks 045/048), task 049 (`--userns`/`--pod` launch conflict — the change that exposed this probe failure)

## Context

The execution-box's load-bearing compensating control for the accepted token-in-box
risk is egress: a default-deny in-pod nftables sidecar (ADR 015) installs a
`policy drop` output chain and allows only the resolved allowlisted IPv4-and-port
pairs in the `@allowed_tcp4` set. This is the control the CLAUDE.md invariants and
`autonomous-builder.md` name as load-bearing — **it does not get weakened.**

Task 049 fixed a `--userns`/`--pod` launch conflict (the pod's infra container now
declares `--userns=keep-id` at `podman pod create`, and pod members inherit it
rather than each re-declaring it). That fix unblocked pod creation and, in doing so,
exposed two real-host failures in the egress sidecar that the prior launch-conflict
masked. Running the runtime proof on the real rootless host —

```
bash containment/execution-box/run.sh --worktree . --egress-probe
```

— fails (rootless Podman 5.7.0, cgroup v2, kernel 7.0.0, `--userns=keep-id`, subuid
base 100000) with two distinct errors:

```
/scratch/agent-builder-egress.nft:1:18-37: Error: No such file or directory; did you
  mean table 'agent_builder_egress' in family inet?
TC-001 FAIL: nftables default-deny egress rules failed to apply
/usr/local/bin/execution-box-egress-sidecar: line 11: can't create /egress-state/fail:
  Permission denied
```

These are **two independent bugs**, both in the sidecar startup path, neither of
which is a rootless capability wall:

### Root cause (a): the nft ruleset is not idempotent — it flushes a table before declaring it

`egress-sidecar.sh` (lines 24–25) emits `flush table inet agent_builder_egress` as the
**first** statement of the ruleset, before any `table inet agent_builder_egress { … }`
declaration exists. On a fresh netns (every run) the table does not yet exist, so `nft
-f` errors `No such file or directory` and the sidecar fails `TC-001`. The kernel's own
hint — *"did you mean table 'agent_builder_egress' in family inet?"* — is the proof
that nft reached netfilter and the family/table name are correct; the only problem is
ordering.

This was confirmed directly on the host: with `--user 0:0 --cap-add=NET_ADMIN` in a
`--userns=keep-id` pod member, applying a ruleset via the idempotent idiom —

```
table inet t { }
flush table inet t
table inet t { chain output { type filter hook output priority 0; policy drop; … } }
```

— returns APPLY-OK and `nft list ruleset` shows the drop-policy chain installed.
**nft works fully in the rootless pod netns.** Root cause (a) is an idempotency bug in
the ruleset text, not a capability or rootless limitation.

### Root cause (b): the egress-state dir is host-owned and the keep-id-mapped sidecar root cannot write it

The sidecar runs `--user 0:0`. Under `--userns=keep-id`, in-pod root maps to host
subuid **100000**, which cannot write the host-owned (`kevin`/1000) `/egress-state`
bind mount — the per-run `mktemp -d` created at `run.sh` line 581 (mode 0700). So the
sidecar's `fail()` helper (line 11) cannot create `/egress-state/fail`, and on the
success path it could not write `/egress-state/ready` either; the readiness handshake
between sidecar, launcher, and workload breaks.

This was confirmed end-to-end on the host: when the host state dir is created mode
**0777**, the sidecar's mapped-root writes `ready`/`fail` at mode 0644, the **host**
(`kevin`) reads them via other-read permission (`HOST-READ-OK`), and the **workload**
pod member reads them via its read-only mount (`WORKLOAD-READ-OK`). All three legs
confirmed. The state dir is a per-run transient `mktemp -d` containing only the
`ready`/`fail` markers and the resolved-allowlist/add-hosts plan files — **no secrets**
— and is `rm -rf`'d on exit (line 590). World-writable mode on this transient,
secret-free, per-run directory is acceptable and must be documented.

This is the same shape as ADR 027 and ADR 028: a load-bearing control was failing on a
legitimate runtime (real rootless host) for a mechanical reason, and the fix is to make
the control *work where it must run* — never to weaken or gate it.

## Options considered

### Option A — Keep the strict in-pod nftables default-deny sidecar and fix it to work rootless (recommended)

Two surgical fixes, both in the sidecar startup path, with **no change to the
allow/deny semantics**:

1. **Make the nft ruleset idempotent.** Declare the table before flushing it: emit
   `table inet agent_builder_egress { }`, then `flush table inet agent_builder_egress`,
   then the populated `table inet agent_builder_egress { set allowed_tcp4 { … } chain
   output { … policy drop; … } }`. `flush` now always targets a table that exists. The
   `@allowed_tcp4` set, the `ip daddr . tcp dport @allowed_tcp4 accept` allow rule, the
   `oifname "lo"` / `ct state established,related` accepts, the final `reject`, and the
   `policy drop` default-deny are **unchanged**.
2. **Create the host egress-state dir world-writable.** `chmod 0777` the per-run
   `mktemp -d` in `run.sh` so the keep-id-mapped sidecar root can write the
   `ready`/`fail` readiness markers; host and workload read them via other-read
   permission.

- **Pros**
  - Keeps the **load-bearing** default-deny egress control exactly as ADR 015
    specifies — exact-host-and-port allowlist, `policy drop`, sidecar-owned
    `CAP_NET_ADMIN`, workload `--cap-drop=all`. Zero semantic change.
  - Both fixes are mechanical and reproduction-backed: nft demonstrably works in the
    rootless pod netns, and the state-dir permission fix was verified across all three
    read/write legs.
  - The strict egress path now runs on the **real rootless host**, which is the
    standard operator/agent substrate (ADR 014) — unblocking the L6 `--egress-probe`
    proof that ADR 015 requires before the task can pass the verification gate.
  - Smallest possible diff; fully reversible (revert two edits) and introduces no new
    runtime mode, no new trust boundary, no coarser filter model.
- **Cons**
  - The egress-state dir is world-writable (0777). Mitigated and bounded: it is a
    per-run `mktemp -d`, secret-free (only `ready`/`fail` markers and the
    resolved-plan/add-hosts text), and `rm -rf`'d on exit; the 0777 is a documented,
    ADR-justified exception, not a weakening of a load-bearing control.
  - The idempotent idiom adds one extra (empty) table declaration line to the emitted
    ruleset — trivial, but the test spec must assert the emission order so a future
    edit cannot reintroduce the flush-before-declare ordering.

  *Sketch:* in `egress-sidecar.sh`, change the heredoc/printf block so the first emitted
  statement is `printf 'table inet agent_builder_egress { }\n'`, immediately followed by
  `printf 'flush table inet agent_builder_egress\n'`, then the existing populated-table
  emission unchanged. In `run.sh`, after `egress_state="$(mktemp -d)"` (line 581), add
  `chmod 0777 "$egress_state"` with a comment naming the keep-id subuid mapping as the
  reason. Nothing else in the egress block changes.

### Option B — Replace nftables with pasta/slirp4netns host/port egress filtering (rejected)

Drop the in-pod nftables sidecar and instead filter egress at the rootless network
backend (pasta or slirp4netns) using its host/port allow options.

- **Pros**
  - Removes `CAP_NET_ADMIN` from the picture entirely (no privileged sidecar at all).
  - Filtering at the user-mode network backend is a different, arguably simpler trust
    surface for some deployments.
- **Cons**
  - **Unnecessary rearchitecture for a non-problem.** Reproduction proves nftables
    works fully in the rootless pod netns; there is no capability wall to route around.
    Replacing a demonstrably-working load-bearing control to fix an ordering bug and a
    directory permission is disproportionate.
  - **Coarser semantics.** ADR 015's control is an exact resolved-IPv4-and-port
    allowlist with a kernel-enforced `policy drop`. pasta/slirp4netns host/port
    filtering does not give the same exact-pair model and would trade the
    human-reviewable, packet-filter-enforced contract for a coarser one — a regression
    against the very property ADR 015 was written to guarantee.
  - Large blast radius: rewrites the egress enforcement layer, the readiness handshake,
    and the probe contract, all to avoid a two-line fix.

  *Sketch:* drop `egress-sidecar.sh` and the sidecar `podman run`; configure the pod's
  network with `pasta`/`slirp4netns` allow options derived from the resolved allowlist;
  rewrite `egress-probe.sh` expectations and the ADR-015 two-layer model.

### Option C — Gate strict egress behind rootful / runsc-network-policy and document a rootless constraint (rejected)

Keep nftables for rootful or a runsc network-policy path, and document that the
strict default-deny egress filter is unavailable (or degraded) under rootless Podman.

- **Pros**
  - Would be the right shape *if* rootless genuinely could not run the strict filter —
    matching the ADR 027/028 "degrade a secondary signal explicitly" pattern.
- **Cons**
  - **Based on a false premise.** There is no rootless constraint to document: the
    strict nftables path works under rootless Podman once the ordering and state-dir
    bugs are fixed (both reproduction-confirmed). Documenting a constraint that does not
    exist would mislead operators and could invite a real future weakening "to match the
    documented limitation."
  - Egress is the **load-bearing** control, not a secondary signal like the storage
    quota (ADR 027) or the in-box cgroup readout (ADR 028) — gating or degrading it is
    categorically different and is exactly what the invariants forbid.
  - Adds a second egress code path (rootful vs rootless) to maintain for no benefit,
    since the single strict path already runs everywhere the box runs.

  *Sketch:* branch the egress block on rootful-vs-rootless; keep nftables only on the
  rootful/runsc-policy branch; emit a `WARNING` and a documented "egress filter
  unavailable under rootless" note on the rootless branch.

## Recommendation

**Option A.** The deciding factor is **blast radius versus the actual defect**: the
observed failures are a ruleset ordering bug and a directory permission, both fixable in
two reproduction-confirmed lines, while egress is the single load-bearing control the
security model rests on. Option B rearchitects a working load-bearing control and trades
the exact-pair allowlist for a coarser model to solve a problem that does not exist
(nft works rootless). Option C documents a rootless constraint that the reproduction
disproves, and would treat the load-bearing egress control like a degradable secondary
signal — which it is not. Option A is the only option that fixes the real defect, keeps
ADR 015's default-deny allowlist semantics exactly intact, and runs the strict path on
the standard rootless substrate, consistent with the ADR 027/028 principle of *making
the load-bearing control work where it must run rather than weakening or gating it*.

## Decision

Adopt Option A:

1. **Idempotent nft ruleset.** `egress-sidecar.sh` emits an empty
   `table inet agent_builder_egress { }` declaration first, then
   `flush table inet agent_builder_egress`, then the populated table. `flush` never
   targets a missing table again. The `@allowed_tcp4` set, the allow rule
   (`ip daddr . tcp dport @allowed_tcp4 accept`), the `lo`/established-related accepts,
   the final `reject`, and the `policy drop` default-deny are **unchanged**.
2. **World-writable per-run egress-state dir.** `run.sh` `chmod 0777`s the per-run
   `mktemp -d` egress-state directory so the keep-id-mapped sidecar root (host subuid
   100000) can write the `ready`/`fail` readiness markers; the host and the workload
   read them via other-read permission. The directory remains a transient, secret-free
   `mktemp -d`, `rm -rf`'d on exit.

### Reopening condition

If a future change makes the sidecar run as the host-owning uid (e.g. drops
`--userns=keep-id` for the sidecar, or maps the state dir owner into the pod), tighten
the egress-state dir back to 0700 — the 0777 exists **only** because the keep-id subuid
mapping prevents the mapped-root sidecar from writing a host-owned 0700 dir, and it
should be retired the moment that mapping no longer forces it.

Accepted by the orchestrator on 2026-06-17 (concurring with the recommendation).
The default-deny invariant and the exact-host-and-port allow/deny allowlist semantics
of ADR 015 are explicitly preserved; neither fix touches them.

## Consequences

**Egress control posture (stated explicitly — UNCHANGED).** The execution-box egress
control remains the strict in-pod nftables default-deny sidecar of ADR 015: `policy
drop` output chain, `CAP_NET_ADMIN` isolated to the trusted sidecar, workload
`--cap-drop=all` / `--security-opt=no-new-privileges` / non-root / DNS-disabled, and an
allow set restricted to the resolved exact IPv4-and-port pairs of the human-reviewed
allowlist. **No allow/deny semantics change. The default-deny invariant is intact.** The
only changes are (a) the order in which the otherwise-identical ruleset is emitted and
(b) the permission mode of the transient readiness-handshake directory.

**Security severity of the 0777 state dir (bounded).** The world-writable mode applies
to a per-run `mktemp -d` that holds only the `ready`/`fail` markers and the
resolved-plan/add-hosts text — **no secrets, no tokens, no credentials** — and is
removed on exit. It is not on the egress data path and grants no network reachability.
A local unprivileged process could write a spurious `ready`/`fail` marker, but the
sidecar still installs the `policy drop` ruleset before writing `ready`, so a forged
marker cannot bypass the filter; at worst it races the readiness handshake within a
single run. The exposure is local, transient, and secret-free. It is a documented,
ADR-justified exception, not a weakening of a load-bearing control.

**What becomes easier.**
- The strict default-deny egress probe (`run.sh --worktree . --egress-probe`) runs to
  completion on the real rootless host, unblocking the L6 runtime proof ADR 015 requires
  to promote egress work beyond code-merged status.
- The sidecar ruleset is now idempotent, so it applies cleanly on every fresh netns and
  on re-runs without an ordering error.

**What becomes harder.**
- The emitted ruleset has one extra (empty) table-declaration line, and the readiness
  directory carries a deliberately permissive mode; both require an explanatory comment
  and a test-spec assertion so a future edit cannot silently revert the ordering or
  tighten the dir back to 0700 and re-break the rootless handshake.

**Verification.** The proof of this decision is the **real-host** `--egress-probe`:
`containment/execution-box/run.sh --worktree . --egress-probe` must reach both the
allow assertion (an allowlisted host:port connects — `TC-003 PASS`) and the deny
assertions (a non-allowlisted host and a direct-IP target are both refused — `TC-004
PASS`), with the sidecar's `TC-001 PASS` (nftables default-deny output policy
installed) printed first, and the launcher exiting **0**. This is an L6 runtime
observation on rootless Podman; the static/stub L5 path cannot prove rootless nftables
enforcement and therefore cannot promote the row beyond code-merged. Recording the
`TC-001`/`TC-003`/`TC-004` PASS lines plus exit 0 from a rootless-host probe run is the
evidence required for the `verify:` commit.

**Spec updates land with the code (flagged, not edited here).** The following
`docs/spec/` entries describe the egress sidecar/probe contract and must be confirmed
or rewritten in place in the same commit as the fix:
- `docs/spec/behaviors.md` B-010 (lines 100–104) — the launcher response/side-effects/
  failure-mode entries for the egress sidecar and `--egress-probe`. The default-deny
  behavior is unchanged; reference ADR 029 alongside ADR 015 where the egress sidecar
  startup and readiness-marker handshake are described, and note the egress-state dir is
  a transient world-writable readiness directory.
- `docs/spec/interfaces.md` (line 549, and the Podman external-dependency row at line
  69) — the `--egress-probe` `TC-003`/`TC-004` description and the egress sidecar
  startup. The allow/deny contract is unchanged; the rootless idempotent-ruleset and
  state-dir fix is an implementation correction that makes the existing contract hold on
  rootless, so the interface text needs at most an ADR 029 reference, not a contract
  change.
- No `data-model.md` or `configuration.md` change is expected — the allowlist format,
  the `@allowed_tcp4` model, and the egress env knobs are all unchanged.
