# ADR 030: The networked (egress-pod) rootless execution-box path runs under runc; gVisor is unavailable on that path only

**Date:** 2026-06-17
**Status:** Accepted
**Task:** governs the egress-path runtime selection in `containment/execution-box/run.sh` (egress-pod workload runtime resolution + the `--add-host`-on-`podman pod create` mechanical prerequisite)
**Related:** ADR 029 (rootless nftables egress-sidecar fix — the same phase; this builds on it), ADR 016 (tiered OCI runtime selection seam: `agent` → `runsc`, `dev` → `runc`, explicit `--runtime`), ADR 015 (default-deny egress allowlist — the load-bearing control this ADR preserves intact), ADR 014 (rootless Podman execution-box substrate invariant), ADR 027 / ADR 028 (the "keep the load-bearing control, degrade the secondary defense-in-depth signal explicitly, with a named revisit condition" precedent this ADR matches)

## Context

This is the third decision in a single phase that hardened the rootless egress path end-to-end:

1. **Task 049** fixed a `--userns`/`--pod` launch conflict — `--userns=keep-id` is now declared once on `podman pod create` (the infra container) and pod members inherit it, rather than each member re-declaring it. That unblocked pod creation.
2. **ADR 029** then fixed two sidecar startup bugs that the unblocked pod start exposed — an idempotent nftables ruleset (declare-before-flush) and a world-writable per-run egress-state readiness directory — proving the strict default-deny nftables filter works under rootless Podman.
3. **This ADR (030)** records the runtime non-composition that the now-working egress path surfaced on the real host: under rootless Podman, a *networked* execution-box workload cannot have **both** in-pod nftables egress filtering **and** the gVisor (`runsc`) runtime tier.

The egress design (ADR 015) requires the workload to **join** the sidecar's pod so it shares the network namespace the sidecar applies the `policy drop` nftables ruleset to. Under rootless Podman the pod's infra container owns a `--userns=keep-id` user namespace (task 049), and every pod member joins that pre-existing userns. gVisor's gofer process cannot enter a pre-existing rootless pod userns — so the two controls are mutually exclusive for a networked rootless workload.

### Reproduction evidence (real host: rootless Podman 5.7.0, `runsc` release-20260601.0 via the `~/.local/bin/runsc-rootless` wrapper = `runsc -ignore-cgroups`, kernel 7.0.0, cgroup v2, `--userns=keep-id`, subuid base 100000)

1. **Egress filtering works rootless end-to-end under `runc`.** With the task-050 sidecar fixes (idempotent nft ruleset + writable egress-state, ADR 029) plus moving `--add-host` from the workload member to `podman pod create`, the egress probe under `--runtime runc` exits **0**:
   - `TC-003 PASS: allowlisted connect succeeded: api.github.com:443 ... open`
   - `TC-004 PASS: non-allowlisted connect blocked: example.com:443` (unresolvable under `--dns none`, no add-host)
   - `TC-004 PASS: direct IP bypass blocked: 1.1.1.1:443` (refused by the nftables default-deny)

   **The load-bearing egress control is proven rootless.**

2. **gVisor cannot join a rootless pod's userns — a general gVisor/rootless limitation, not our bug.** `podman run --pod <keep-id-pod> --runtime runsc ...` fails:
   ```
   runsc: creating container: cannot create gofer process: gofer: error setting
     namespace of type user and path "/proc/<pid>/ns/user": invalid argument
   ```
   The **same** image and args under `--runtime runsc` **without** a pod (`--userns=keep-id` standalone) succeed (`RUNSC-STANDALONE-OK`). So `runsc` works standalone but its gofer cannot enter the user namespace owned by a pod's infra container. The `-ignore-cgroups` wrapper does not address this; it is a namespace-join limitation in the gofer.

3. **Architectural cause of the non-composition.** ADR 015's egress design requires the workload to **join** the sidecar's pod to share the netns the sidecar filters. Joining the pod means joining the pod's `keep-id` userns. gVisor's gofer cannot enter that pre-existing userns. Therefore in-pod nftables egress (needs pod-join) and gVisor (cannot pod-join, rootless) are mutually exclusive for a networked rootless workload. Rootful would avoid the keep-id userns join, and Kata/Firecracker have different namespace models — both are documented revisit paths, not in scope now (substrate is rootless Podman by invariant, ADR 014).

This is the same shape as ADR 027 and ADR 028: a *secondary defense-in-depth signal* (here, gVisor syscall interception) is unavailable on a legitimate runtime path, while the *load-bearing control it sits beside* (the default-deny egress allowlist) is fully enforced. The fix is to keep the load-bearing control working where it must run and degrade the secondary signal explicitly, with a named revisit condition — never to weaken the load-bearing control to keep the secondary one.

## Options considered

### Option A — Run the networked rootless workload under `runc`; keep egress nftables fully enforced; document gVisor as unavailable on the egress path only (recommended)

On the rootless egress (pod) path, resolve the workload runtime to `runc` regardless of the `agent`-tier `runsc` default, because the workload must join the pod's keep-id userns and gVisor's gofer cannot. The default-deny nftables allowlist (ADR 015) is fully enforced via the shared pod netns. Non-networked paths (`--probe`, plain offline workload runs) keep the ADR-016 defaults (`agent` → `runsc` standalone). An operator who explicitly passes `--runtime runsc` together with the egress path is **failed loudly** with a message pointing at this ADR, rather than silently downgraded.

- **Pros**
  - Keeps the **load-bearing** egress control exactly as ADR 015 specifies — exact resolved IPv4-and-port allowlist, kernel-enforced `policy drop`, sidecar-owned `CAP_NET_ADMIN`, workload `--cap-drop=all` — on the standard rootless substrate (ADR 014). Zero egress-semantics change.
  - The trade-off is narrow and named: gVisor syscall interception is lost **only** on the networked rootless path; every non-networked path keeps `runsc` standalone per ADR 016.
  - The runc workload still carries the full non-gVisor containment stack: rootless + `--userns=keep-id` + `--cap-drop=all` + `--security-opt=no-new-privileges` + read-only rootfs + default seccomp + the cpu/memory/pids/tmpfs caps. A rootless escape lands as an unprivileged subuid (base 100000), not host root.
  - Failing loudly on `--runtime runsc` + egress prevents a silent, surprising downgrade and prevents an operator from believing they have gVisor when they do not.
  - Reproduction-backed and reversible: the `runc`-on-egress resolution is a localized rule in the launcher; if a future gVisor or runtime tier removes the userns-join wall, the rule is removed.
- **Cons**
  - The networked rootless path loses gVisor's syscall-interception defense-in-depth. Bounded: egress (not gVisor) is the load-bearing control for the token-in-box risk; gVisor is a second layer, and the rest of the containment stack still applies to the runc workload.
  - The launcher now resolves runtime differently on the egress path than on the `--probe`/plain paths, so a reader must know which path a run took to know which runtime it got. The selection rule and the loud-failure guard must be documented and test-covered so the divergence is explicit, not surprising.

  *Sketch:* in the egress (pod) block of `run.sh`, before building `workload_args`, resolve the egress-path workload runtime to `runc` (overriding the `agent`→`runsc` default), and ensure the `--runtime` value carried into `workload_args` is `runc`. If the operator explicitly passed `--runtime runsc` (a non-empty `runtime_override` equal to `runsc`) **and** the egress path is taken, `die` with a message naming this ADR and the userns-join limitation rather than launching. Keep `--add-host` declared on `podman pod create` (the mechanical prerequisite that lets the pod resolve allowlisted hosts while the workload runs `--dns none`).

### Option B — Keep gVisor by replacing in-pod nftables with pasta/slirp4netns host/port egress filtering (rejected)

Drop the pod-joined nftables sidecar so the workload need not join the pod userns, and filter egress at the rootless network backend (pasta/slirp4netns) host/port allow options — letting the workload run standalone under `runsc`.

- **Pros**
  - Preserves gVisor on the networked path.
  - Removes the pod-join requirement (and thus the userns-join wall) entirely.
- **Cons**
  - **Coarser semantics — a regression of the load-bearing control.** ADR 015's control is an exact resolved-IPv4-and-port allowlist with a kernel-enforced `policy drop`. pasta/slirp4netns host/port filtering does not give the same exact-pair model and trades the human-reviewable, packet-filter-enforced contract for a coarser one — the very property ADR 015 exists to guarantee.
  - **Directly contradicts ADR 029**, which evaluated and rejected the pasta/slirp4netns swap two decisions ago (its Option B) on exactly these grounds. Reopening it here to save a secondary defense-in-depth layer inverts the priority the invariants set: the egress allowlist is load-bearing, gVisor is not.
  - Large blast radius: rewrites the egress enforcement layer, the readiness handshake, and the probe contract — to preserve a secondary signal at the cost of the primary control.

  *Sketch:* drop the in-pod nftables sidecar; configure the rootless network backend with allow options derived from the resolved allowlist; rewrite `egress-probe.sh` and the ADR-015 two-layer model so the workload can run `runsc` standalone.

### Option C — Adopt a rootful, Kata, or Firecracker networked tier so gVisor (or stronger) can coexist with in-pod egress filtering (deferred, not rejected on merit)

Run the networked execution-box under a rootful Podman pod (no keep-id userns to join) or under a Kata/Firecracker tier whose namespace model does not impose the gofer userns-join wall, so a syscall/VM isolation layer can coexist with the pod-joined nftables filter.

- **Pros**
  - Would restore (or exceed) gVisor-grade isolation on the networked path while keeping the exact-pair nftables allowlist — the best-of-both outcome.
  - The right long-term shape if the project ever adopts a rootful or VM-based networked tier.
- **Cons**
  - **Out of scope for the rootless-Podman bootstrap.** ADR 014 fixes the current substrate as rootless Podman; rootful and Kata/Firecracker are explicitly future tiers, not present ones. Adopting one to solve a secondary-signal gap on the bootstrap path is a substrate change far larger than the gap warrants.
  - Rootful reintroduces a host-root trust surface the rootless invariant deliberately removed; Kata/Firecracker add a VM tier with its own provisioning, performance, and verification cost not yet built.
  - Pure deferral, not a fix available today — it changes nothing on the current rootless host.

  *Sketch:* add a networked rootful or Kata/Firecracker tier to the runtime seam (ADR 016) and route the egress path to it; out of scope here, recorded as a revisit path.

### Option D — Upstream a gVisor fix so the gofer can enter a pre-existing rootless pod userns (deferred, external)

Wait for / contribute a gVisor change whose gofer can join an already-created rootless pod userns, then route the egress path back to `runsc`.

- **Pros**
  - Would directly remove the root cause and let Option A's `runc`-on-egress rule retire cleanly.
- **Cons**
  - **Not actionable on the bootstrap timeline.** It depends on an upstream change agent-builder does not control and cannot schedule.
  - Until it ships, the egress path still needs a working runtime — which is Option A regardless.

  *Sketch:* track upstream gVisor; when a release lands whose gofer can enter a pre-existing rootless pod userns, re-test `--runtime runsc` on the egress path and, if green, remove the `runc`-on-egress resolution and the loud-failure guard.

## Recommendation

**Option A.** The deciding factor is **priority of controls under the threat model**: the egress allowlist is *the* load-bearing compensating control for the accepted token-in-box risk (CLAUDE.md invariant, ADR 015), and gVisor is a defense-in-depth second layer. Option B preserves the secondary layer by regressing the primary control to a coarser model — and re-opens a swap ADR 029 already rejected — which inverts the priority the invariants set. Options C and D would restore gVisor *and* keep the exact-pair allowlist, and are the genuine long-term answers, but both are out of the rootless-Podman bootstrap's scope (ADR 014) or out of its control (upstream), so neither is available today. Option A is the only choice that keeps the load-bearing egress control exactly intact on the standard rootless substrate, keeps the full non-gVisor containment stack on the runc workload, loses the secondary signal **only** on the networked path, and fails loudly rather than silently when an operator asks for the impossible combination — consistent with the ADR 027/028 principle of keeping the load-bearing control and degrading the secondary signal explicitly with a named revisit condition.

## Decision

Adopt Option A:

1. **Egress path runs `runc`.** When the rootless egress (pod) path is taken, the workload runtime resolves to `runc`, regardless of the `agent`-tier `runsc` default, because the workload must join the pod's `--userns=keep-id` userns (declared on `podman pod create`, task 049) and gVisor's gofer cannot enter a pre-existing rootless pod userns. The default-deny nftables allowlist (ADR 015) is fully enforced via the shared pod netns.

2. **Non-networked paths are unchanged.** Plain `--probe` runs and offline (non-egress) workload runs keep the ADR-016 defaults: `agent` → `runsc` standalone, `dev` → `runc`, explicit `--runtime` honored. gVisor is unavailable **only** on the networked rootless egress path.

3. **Explicit `--runtime runsc` + egress fails loudly.** If an operator explicitly passes `--runtime runsc` and the egress path is taken, the launcher `die`s with a clear message naming this ADR and the gofer-userns-join limitation, rather than silently downgrading to `runc`. Silent downgrade is rejected: it would let an operator believe they have gVisor syscall isolation when they do not, which is a worse failure than a loud refusal. (An operator who wants gVisor must drop the egress path; an operator who wants egress must accept `runc`.)

4. **`--add-host` on `podman pod create` is in scope as the mechanical prerequisite.** The allowlisted host records are declared on the pod (infra container) so the pod resolves allowlisted destinations while the workload runs `--dns none`; this is the change that, together with ADR 029's sidecar fixes, makes the rootless egress probe exit 0 under `runc`.

### Reopening condition

Flip the egress path back to gVisor (`runsc`) when **either** of the following becomes true and is re-tested green on the rootless host:

- A **gVisor release whose gofer can enter a pre-existing rootless pod userns** ships — verifiable by re-running `podman run --pod <keep-id-pod> --runtime runsc ...` and observing it create the gofer instead of failing with `error setting namespace of type user ... invalid argument`. When that passes, remove the `runc`-on-egress resolution and the loud-failure guard and restore the `agent`→`runsc` default on the egress path.
- A **rootful or Kata/Firecracker networked tier is adopted** (a deliberate substrate decision beyond ADR 014) whose namespace model does not impose the gofer userns-join wall — route the egress path to that tier with `runsc` (or stronger) restored.

Until one of those holds, the egress path runs `runc` and `--runtime runsc` + egress fails loudly.

Accepted by the orchestrator on 2026-06-17 (concurring with the recommendation). The default-deny invariant and the exact-host-and-port allowlist semantics of ADR 015 are explicitly preserved; this decision changes only which OCI runtime the *networked* rootless workload runs under, never the egress allow/deny contract.

## Consequences

**Security posture — what is preserved (stated explicitly).** The **load-bearing** control is intact: the execution-box egress filter remains the strict in-pod nftables default-deny sidecar of ADR 015 — `policy drop` output chain, `CAP_NET_ADMIN` isolated to the trusted sidecar, an allow set restricted to the resolved exact IPv4-and-port pairs of the human-reviewed allowlist, and the workload running `--cap-drop=all` / `--security-opt=no-new-privileges` / non-root / `--dns none`. The runc workload on the egress path **also** keeps the full non-gVisor containment stack: rootless Podman + `--userns=keep-id` + `--cap-drop=all` + `--security-opt=no-new-privileges` + read-only rootfs + default seccomp + the explicit cpu/memory/pids/tmpfs caps. A rootless escape from the runc workload lands as an **unprivileged subuid** (base 100000), not host root.

**Security posture — what is lost (honest and bounded).** On the networked rootless path **only**, the workload loses gVisor's syscall-interception layer (the sentry/gofer boundary). Against the threat model this is a *secondary* loss: egress is THE load-bearing control for the token-in-box risk, and it is fully enforced; gVisor is defense-in-depth, and the rest of the stack still applies. The loss is scoped — every non-networked path (`--probe`, offline builds) keeps `runsc` standalone — and named, not silent. This is the ADR 027/028 pattern applied a third time: keep the load-bearing control, degrade the secondary explicitly.

**What becomes easier.**
- The strict default-deny egress probe (`run.sh --worktree . --egress-probe`) runs to completion under `runc` on the real rootless host, exiting 0 with `TC-003`/`TC-004` PASS — the L6 runtime proof ADR 015 requires.
- An operator requesting the impossible `--runtime runsc` + egress combination gets a clear, ADR-referenced error instead of a confusing gofer-namespace failure or a silent downgrade.

**What becomes harder.**
- The launcher resolves the workload runtime differently on the egress path (`runc`) than on the `--probe`/plain paths (`agent`→`runsc`), so a reader must know which path a run took to know which runtime it got. The selection rule and the loud-failure guard need an explanatory comment and a test-spec assertion so a future edit cannot silently route `runsc` onto the egress path (which would re-break it) or silently downgrade `--runtime runsc` instead of failing.

**Verification.** The proof of this decision is the **real-host** `--egress-probe` under the resolved `runc` runtime: `containment/execution-box/run.sh --worktree . --egress-probe` must reach the allow assertion (`TC-003 PASS: allowlisted connect succeeded`) and both deny assertions (`TC-004 PASS` for the non-allowlisted host and the direct-IP target), with the launcher exiting **0**. Separately, `--runtime runsc --egress-probe` must `die` with the ADR-030 message and a non-zero exit. This is an L6 runtime observation on rootless Podman; the static/stub L5 path cannot prove the rootless runtime non-composition and therefore cannot promote the row beyond code-merged.

**Spec updates land with the code (flagged, not edited here).** The implementation task must rewrite the following `docs/spec/` entries in place, in the same commit as the code change, referencing ADR 030:
- `docs/spec/behaviors.md` B-010 (the launcher response / side-effects / failure-mode entries, lines ~101–103) — state that the networked (egress-pod) rootless path runs the workload under `runc` (gVisor unavailable on that path only, because the workload joins the pod's keep-id userns), that non-networked paths keep the ADR-016 `agent`→`runsc` default, and that `--runtime runsc` combined with the egress path fails loudly with an ADR-030 message. Reference ADR 030 alongside ADR 015/016/029.
- `docs/spec/interfaces.md` — the `--runtime`/`--egress-probe` descriptions (lines ~546, ~549) and the runtime-tier note: document that on the egress path the resolved runtime is `runc` regardless of the `agent` default, and that explicit `--runtime runsc` + egress is refused. Reference ADR 030.
- `docs/spec/architecture.md` — the execution-box profile row's runtime-tier description (line ~53) and the egress-sidecar row (line ~54): note that the networked rootless workload runs `runc` (gVisor on the non-networked paths only). Reference ADR 030.
- No `data-model.md` or `configuration.md` change is expected — the allowlist format, the `@allowed_tcp4` model, the `EXEC_BOX_*` knobs, and the workload-tier names are all unchanged; only which runtime the networked rootless workload resolves to changes.
