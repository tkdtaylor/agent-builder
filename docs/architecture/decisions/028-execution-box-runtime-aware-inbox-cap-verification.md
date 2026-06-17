# ADR 028: In-box resource-cap verification is runtime-aware (gVisor defers cpu/pids to host-side inspect)

**Date:** 2026-06-17
**Status:** Accepted
**Task:** governs task 048 (`containment/execution-box/probe.sh` TC-003 in-box block + `containment/execution-box/run.sh` probe-path launch guard)
**Related:** ADR 027 (storage-quota graceful degrade — same "verify where you reliably can, degrade a secondary signal explicitly" principle), ADR 016 (tiered runtime selection seam: `runc` → `runsc` → `kata`), ADR 014 (rootless Podman execution-box profile)

## Context

The execution-box `--probe` runs an in-box probe (`containment/execution-box/probe.sh`).
Its **TC-003** block (probe.sh lines 110–145) asserts that **cpu, memory, AND pids**
cgroup limits are *readable inside the box* at `/sys/fs/cgroup` — cgroup-v2 paths
(`cpu.max` / `memory.max` / `pids.max`) with a cgroup-v1 fallback
(`cpu/cpu.cfs_quota_us`, `memory/memory.limit_in_bytes`, `pids/pids.max`). If any of
the three is not visible the probe fails:

```
fail TC-003 "expected cgroup limits, got cpu=$cpu_limited memory=$memory_limited pids=$pids_limited"
```

This assertion was authored against **runc** behavior, where the in-box cgroupfs
mirrors the host's cgroup-v2 tree. ADR 016 then made the runtime tiered (`agent` →
`runsc` by default), so the same in-box probe now runs under gVisor — a runtime whose
in-box cgroupfs is a *different artifact* than runc's. The assertion was never
re-validated against gVisor.

### Reproduced live (this host: rootless Podman 5.7.0, cgroup v2, `runsc`/gVisor registered)

- **Under runc**, in-box `/sys/fs/cgroup` shows the host's cgroup-v2 files:
  `cpu.max=200000 100000`, `pids.max=256`, `memory.max=2147483648` — **all three
  visible**, TC-003 **PASSES**.
- **Under gVisor (`runsc`)**, in-box `/sys/fs/cgroup` is a **partial cgroup-v1-style
  emulation** (`cpu/ cpuacct/ cpuset/ devices/ job/ memory/ pids/` directories). It
  exposes the **memory** limit but **not** the cpu or pids limit files. probe.sh
  already tries the v1 fallback paths; gVisor simply does not surface cpu/pids there.
  TC-003 **FAILS**: `expected cgroup limits, got cpu=unknown memory=yes pids=unknown`.

### The caps are applied and enforced under gVisor — only the in-box *view* differs

This is the load-bearing fact. The launcher's **host-side** TC-003 inspect
(`run.sh` lines 544–559, via `podman inspect`) confirms the container was created with
`NanoCpus=2000000000`, `PidsLimit=256`, `Memory=2147483648`. gVisor enforces these at
the sentry/sandbox boundary; they are simply **not reflected in the in-sandbox
cgroupfs view**. So under gVisor:

- **Cap enforcement** is verified at the authoritative (host-inspect) layer — already
  green in `run.sh`'s host-side TC-003.
- Only the **in-box cgroupfs visibility** of cpu/pids is absent.

gVisor's in-box cgroupfs is an *intentionally partial emulation*. It is not a
documented, stable contract; the set of surfaced controllers and files varies by
gVisor version. Coupling a containment assertion to it would couple the probe to
gVisor internals that can change or break on upgrade — and, worse, could give *false
confidence* (a future gVisor that emulates a controller file without enforcing it
would pass an in-box check that proves nothing).

This is the same shape as ADR 027: a *secondary verification signal* (here, the
in-box cgroupfs readout) is unavailable on a legitimate runtime, while the
*load-bearing fact it stands in for* (the cap is actually applied) is verifiable at a
more authoritative layer (host-side `podman inspect`). The fix is to verify where the
signal is reliable and defer the unreliable signal explicitly — never to stop
checking the cap, and never to chase the unstable surface.

### Co-located mislabel bug on the probe-path launch guard

The same probe run surfaced a launcher mislabel. On the probe path (`run.sh` line
571):

```
podman start --attach "$cid" || die "podman start failed: container did not run (exit $?)"
```

This conflates two distinct outcomes: a genuine **podman launch failure** (podman's
own exit 125 — image/runtime/OCI error, container never started) versus the
**in-box probe's own non-zero exit** (a real TC-00x failure *inside* a container that
started fine). Today both render as `podman start failed: container did not run`,
which mislabels a legitimate in-box probe failure as a launch failure and obscures
the actual failing check. The workload-run path (lines 658–663) and the egress-probe
path (lines 640–651) already distinguish exit 125 from the inner exit; the probe-path
`podman start --attach` callsite was not updated to match. This is the same bug class
ADR 027 named and task 045 fixed on the workload-run path — task 048 fixes the
remaining probe-path callsite.

## Options considered

### Option A — Runtime-aware in-box verification: assert what the runtime reliably exposes, defer the rest to host-side inspect (recommended)

Make the in-box TC-003 resource-cap check **runtime-aware**, keyed on the
`EXEC_BOX_RUNTIME` value the probe already reads (probe.sh line 15):

- **Under `runc`** (and any runtime that mirrors host cgroups in-box): keep asserting
  all three (cpu, memory, pids) visible in-box — **unchanged, no weakening**.
- **Under `runsc` (gVisor)**: assert what gVisor reliably exposes in-box (**memory**),
  and treat the launcher's host-side `podman inspect` of NanoCpus/PidsLimit/Memory
  (already a hard check in `run.sh`'s TC-003 HOST block) as the **authoritative**
  cpu/pids cap check. The probe must **still fail loudly** if the host-side inspect
  shows cpu/pids caps are missing. This is "defer the in-box visibility *signal* to
  the host-side enforcement *record*," **not** "stop checking caps."

- **Pros**
  - Verifies each cap at the layer where the signal is reliable for that runtime; no
    cap stops being checked under any runtime.
  - Keeps the runc assertion fully intact — the stricter in-box check stays exactly
    as load-bearing as it is today on the reproducibility/dev tier.
  - Does not couple the probe to gVisor's unstable in-box cgroupfs emulation, so a
    gVisor upgrade cannot silently break or silently falsely-pass the probe.
  - Consistent with ADR 027's established "verify where reliable, defer the secondary
    signal explicitly" pattern — one principle, two controls.
- **Cons**
  - The probe now has two verification shapes (runc / runsc), which the test spec must
    cover explicitly and which a reader must hold in mind.
  - Under gVisor, cpu/pids cap verification depends on the host-side inspect being run
    and checked — the in-box probe alone no longer re-proves cpu/pids. (Mitigated:
    the host-side inspect is in the same `--probe` invocation and already a hard
    `die`-on-missing check, so a single `--probe` run still proves all three caps.)
  - Introduces a runtime conditional into probe.sh, a small amount of new branching in
    a security-relevant script.

  *Sketch:* in probe.sh's TC-003 block, branch on `$runtime`. For `runsc`, require
  `memory_limited=yes` in-box and emit a PASS line that names cpu/pids as
  host-inspect-authoritative (e.g. `TC-003 PASS: memory cap visible in-box; cpu/pids
  caps verified host-side under runsc`); do **not** fail on `cpu=unknown`/`pids=unknown`
  *as long as* the host-side TC-003 inspect passed. For every other runtime, keep the
  existing all-three in-box assertion. The host-side cpu/pids `die`-on-zero checks in
  `run.sh` stay exactly as they are — they are what makes the gVisor branch safe.

### Option B — Chase a gVisor-specific in-box cgroup path (rejected)

Keep the in-box-only model and teach probe.sh to read cpu/pids from whatever
gVisor-specific in-box location does expose them (or accept gVisor's partial tree by
probing additional emulated paths until cpu/pids are found).

- **Pros**
  - Keeps a single "all caps proven in-box" mental model across runtimes.
  - No dependency on the host-side inspect for the gVisor cpu/pids signal.
- **Cons**
  - gVisor's in-box cgroupfs is an **intentionally partial emulation, not a stable
    contract** — the surfaced controllers/files vary by gVisor version, so the probe
    would couple a containment assertion to gVisor internals that can change or break
    on upgrade.
  - Risks **false confidence**: an emulated controller file that is present but not
    backed by enforcement would pass an in-box check that proves nothing about the
    actual cap. The host-side inspect has no such ambiguity.
  - Reproduction shows gVisor does **not** surface cpu/pids limit files anywhere in
    the in-box tree on the tested version, so there may be no reliable path to chase
    at all — the option may not even be implementable today.

  *Sketch:* extend the TC-003 fallback ladder with gVisor-emulated paths and/or parse
  `/proc`-derived limits inside the sandbox; accept the partial tree when memory is
  present and cpu/pids are read from the gVisor-specific location.

## Recommendation

**Option A.** The deciding factor is **trust-boundary correctness over surface
uniformity**: a containment probe must verify each cap at the layer where the signal
actually means something, and host-side `podman inspect` is the authoritative,
runtime-stable record of what cap the container was created with — whereas gVisor's
in-box cgroupfs is an unstable emulation that can both break (false negative) and
mislead (false positive) across versions. Option B buys a uniform "all in-box" story
at the cost of coupling a security assertion to a non-contract and risking confidence
in a signal that may not reflect enforcement; on the reproduced gVisor version there
may be no path to chase regardless. Option A keeps the strict runc check untouched,
keeps every cap checked under every runtime, and is the only option consistent with
ADR 027's already-accepted "verify where reliable, defer the secondary signal
explicitly, never silently weaken" principle.

## Decision

Adopt Option A for task 048:

1. **Runtime-aware in-box TC-003.** The in-box cap check branches on
   `EXEC_BOX_RUNTIME`. Under `runc` (and any non-`runsc` runtime), all three caps
   (cpu, memory, pids) must be visible in-box — **unchanged**. Under `runsc`, memory
   must be visible in-box, and cpu/pids are verified by the launcher's host-side
   `podman inspect` rather than the in-box cgroupfs.
2. **No cap goes unchecked.** Under `runsc`, the probe still fails loudly if the
   host-side inspect shows cpu or pids caps are missing. Deferring the *in-box
   visibility signal* to the *host-side enforcement record* is not the same as
   dropping the check.
3. **Probe-path launch guard distinguishes launch failure from in-box failure.** The
   probe-path `podman start --attach` callsite reports a genuine podman launch failure
   (exit 125) as "container did not start", and propagates the container/probe's own
   non-zero exit as a **probe failure** (not mislabeled as a launch failure) — matching
   the workload-run and egress-probe paths already fixed in task 045.

### Reopening condition

If gVisor (or a future tiered runtime — Kata/Firecracker per ADR 016) exposes cpu and
pids limits in-box as a **documented, stable contract**, tighten the in-box assertion
back to all-three for that runtime. The runc branch is the reference shape to restore
to; the gVisor deferral exists only because the in-box surface is currently unstable,
and it should be retired the moment that surface becomes a contract.

Accepted by the orchestrator on 2026-06-17 (concurring with the recommendation).
The `runsc` deferral is an **allowlist**: only `runsc` takes the relaxed (memory-in-box
+ cpu/pids-host-side) path; `runc` and any **unknown/future** runtime (e.g. Kata/
Firecracker per ADR 016) default to the **strict** all-three-in-box check, so a new
runtime fails closed rather than silently inheriting the relaxed path.

## Consequences

**Containment posture (stated explicitly).** Under gVisor, the execution-box's cpu and
pids caps are verified at the **host-side `podman inspect`** layer rather than in-box.
This does **not** weaken enforcement: gVisor applies and enforces the caps at the
sentry/sandbox boundary regardless of the in-box cgroupfs view, and the host-side
inspect is the authoritative, version-stable record of what the container was created
with. The memory cap remains additionally verified in-box under gVisor. Under runc the
posture is **unchanged** — all three caps are still proven in-box. The trade-off is
narrow and named: only the *in-box visibility* of cpu/pids under gVisor is deferred,
and it is deferred to a *stronger* signal, not dropped.

**Residual-gap analysis (does deferring cpu/pids to host-side inspect leave a real
hole?).** No material gap, with one named assumption:
- Host-side inspect proves the container was *created* with the caps; gVisor's
  enforcement model applies them at the sandbox boundary. The thing the old in-box
  check added was an independent in-sandbox confirmation that the cap was *visible to
  the workload* — which is a weaker property than enforcement, and under gVisor that
  in-box view is an emulation that does not track enforcement anyway. So the deferral
  does not lose an enforcement signal; it loses a redundant, runtime-unreliable
  visibility signal.
- The one assumption made explicit: this trusts that gVisor enforces the caps it was
  configured with (the same trust ADR 016 already places in `runsc` as an isolation
  tier). If that assumption is ever in question, the answer is a gVisor-enforcement
  test, not an in-box cgroupfs read — the in-box read would not detect a gVisor
  enforcement bug either.

**What becomes easier.**
- `--probe` passes under the default `agent` → `runsc` runtime on cgroup-v2 hosts,
  unblocking L6 runtime verification on the standard agent tier (today TC-003 fails
  there for a non-issue).
- The probe is decoupled from gVisor's in-box cgroupfs emulation, so a gVisor upgrade
  cannot silently break TC-003 over a presentation difference.
- A legitimate in-box probe failure under `--probe` is now reported as a probe failure
  with its failing TC marker, not mislabeled as a podman launch failure.

**What becomes harder.**
- probe.sh now has two TC-003 shapes (runc / runsc); a reader must know which runtime a
  given probe ran under to interpret the PASS line, and the test spec must exercise
  both.
- Under gVisor, a single `--probe` run is required to prove all three caps (in-box
  memory + host-side cpu/pids); the in-box probe output alone no longer re-proves
  cpu/pids. This is acceptable because both checks live in the same `--probe`
  invocation.

**Governs task 048.** This ADR governs the task-048 change to `probe.sh` (TC-003
in-box block) and `run.sh` (probe-path launch guard). The task's test spec must cover:
1. **runc unchanged** — under `runc`, the in-box TC-003 asserts all three caps (cpu,
   memory, pids) visible in-box, exactly as today; the assertion is not weakened.
2. **runsc deferral with no dropped check** — under `runsc`, memory is asserted
   visible in-box, cpu/pids are verified via the host-side `podman inspect`, and the
   probe **still FAILS** if the host-side inspect shows cpu or pids caps missing.
3. **probe-path mislabel fixed** — a genuine podman launch failure (exit 125) on the
   probe path is reported as "container did not start"; an in-box probe non-zero exit
   is reported/propagated as a **probe failure** with its failing TC marker, not
   mislabeled as a launch failure.

**Spec updates land with the code in task 048 (flagged, not edited here).** The
following `docs/spec/` entries describe the in-box / probe-path contract and must be
rewritten in place to reflect the runtime-aware behavior in the same commit as the
task-048 code change:
- `docs/spec/interfaces.md` line 548 — the `--probe` / `TC-003` description. Today it
  documents host-side CPU/memory/PID/SHM inspection but does not state the **in-box**
  cgroup-visibility contract at all; task 048 should add that the in-box cap check is
  runtime-aware (runc: all three in-box; runsc: memory in-box + cpu/pids host-side
  authoritative) and reference ADR 028.
- `docs/spec/behaviors.md` B-010 (lines 101–103) — the launcher-response and
  failure-mode entries. The failure-mode line's "any failed in-box probe exits
  non-zero and prints the failing TC marker" should be made precise on the probe path:
  a podman launch failure (125) is distinguished from an in-box probe failure, and the
  in-box cap check is runtime-aware. Reference ADR 028 alongside ADR 027.
- No `data-model.md` or `configuration.md` change is expected — the resource-cap
  *values* and env knobs (`EXEC_BOX_CPUS`, `EXEC_BOX_MEMORY`, `EXEC_BOX_PIDS_LIMIT`)
  are unchanged; only how the cap is *verified in-box under each runtime* changes.
