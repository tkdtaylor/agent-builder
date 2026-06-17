# ADR 027: Execution-box per-container disk quota degrades gracefully on non-XFS hosts

**Date:** 2026-06-17
**Status:** Accepted
**Task:** governs task 045 (`containment/execution-box/run.sh` implementation)
**Related:** ADR 014 (rootless Podman execution-box profile), ADR 016 (tiered runtime selection seam)

## Context

`containment/execution-box/run.sh` builds a `common_args` array (around lines
443–469) that is shared by both the probe path (`podman create … --probe`) and the
workload path (`podman run …`). That array unconditionally includes the
per-container overlay disk quota:

```
--storage-opt "size=$storage_size"      # storage_size default "4G" (EXEC_BOX_STORAGE_SIZE)
```

Podman's `overlay` storage driver can only honor `overlay.size` (and
`overlay.inodes`) when the backing filesystem of the rootless container store is
**XFS** with project quotas. On a host whose container storage lives on **ext4**
(the common case for developer and operator machines), `podman` rejects the
option outright:

```
Error: configure storage: storage option overlay.size and overlay.inodes only
supported for backingFS XFS. Found extfs
```

This was reproduced live on the current host (ext4, rootless Podman 5.7.0, `runsc`
registered). The consequence is that **every container-launching probe fails**: the
execution-box never starts on this filesystem, even though the underlying isolation
controls (gVisor, read-only rootfs, cap-drop, egress deny) are all available. A bare
`podman --runtime runsc run … alpine uname -r` succeeds on the same host precisely
because it sets no size quota.

Two facts shape the decision:

1. **The disk quota is a secondary control, not a load-bearing one.** The
   load-bearing trust-boundary controls of the execution-box are the egress
   allowlist, read-only rootfs, `--cap-drop=all`, `--security-opt=no-new-privileges`,
   rootless Podman + tiered gVisor (`runsc`), and the `--cpus`/`--memory`/
   `--pids-limit`/tmpfs caps. The `--storage-opt size` per-container quota is an
   *anti-DoS* bound on writable-layer growth. Losing it does not move any
   trust boundary; it only removes one runaway-disk guard, and only for the
   ephemeral writable overlay (the worktree is a host bind mount and `/scratch`
   is a separately-sized tmpfs, both unaffected).

2. **There is a co-located bug that must be fixed in the same task.** The launcher
   currently does not reliably surface a `podman` create/run failure as a non-zero
   exit — the reproduced failure left the probe exiting `0` despite the container
   never starting (the `podman` error went to stderr, but the launcher returned
   success). This violates the project's fail-fast / "crash loudly on unexpected
   state" invariant and, worse, would let the verification gate read a non-started
   box as a pass. Both issues live in the same `common_args`/launch region, so they
   are fixed together.

## Options considered

### Option A — Graceful-degrade: detect enforceability, omit the flag with a loud warning (recommended)

Probe whether the per-container size quota is enforceable on this host (backing FS
is XFS / the overlay driver accepts `size=`). When enforceable, apply
`--storage-opt "size=$storage_size"` exactly as today. When not enforceable, **omit
the flag** and emit a clear stderr `WARNING` naming the degraded control, then
continue. An explicit empty `EXEC_BOX_STORAGE_SIZE` deliberately disables the quota
without a warning (operator opt-out). Separately, every `podman create`/`podman run`
invocation has its exit code checked so a real launch failure surfaces non-zero.

- **Pros**
  - The box runs on the common ext4 operator host, unblocking L6 on most dev
    machines, while keeping the quota wherever the host can actually enforce it.
  - The degradation is explicit and logged — it satisfies fail-loud by *warning*
    on the secondary control while *failing* on real launch errors. No silent
    weakening.
  - Reversible by the operator with zero code change: move the container store to
    XFS (or a project-quota XFS volume) and the quota returns automatically.
  - Keeps the load-bearing controls untouched; the trade-off is narrow and named.
- **Cons**
  - Adds a host-capability probe to the launch path (one extra `podman` call or a
    targeted parse of `podman info` backing-FS), a small amount of new logic.
  - Two run-time behaviors now exist (quota / no-quota) that the test spec must
    cover explicitly, including the warning text.
  - A host that *should* have XFS but is misconfigured degrades quietly-ish (a
    warning, not a hard stop) — mitigated by the warning being on stderr and
    recorded as evidence.

  *Sketch:* before assembling `common_args`, resolve `storage_quota_supported` by
  inspecting the rootless container store's backing FS (e.g. parse
  `podman info --format '{{.Store.GraphRoot}}'` + the driver, or do a cheap
  `podman create` capability probe and discard). If supported and
  `EXEC_BOX_STORAGE_SIZE` is non-empty, append `--storage-opt "size=$storage_size"`;
  else skip it and, when non-empty, `printf 'execution-box: WARNING …' >&2`. Wrap
  each `podman create`/`podman run` so a non-zero exit is captured and re-raised as
  a non-zero launcher exit with a named error.

### Option B — Hard-require XFS: fail closed with a remediation message

Detect the backing FS; if it cannot enforce the quota, `die` with a clear message
telling the operator to move the container store to XFS.

- **Pros**
  - The quota is never silently absent — the box only ever runs in its fully-quota'd
    configuration.
  - Simplest possible mental model: one configuration, always enforced.
  - Forces the strongest anti-DoS posture by construction.
- **Cons**
  - Blocks the execution-box on ext4, which is the *common* operator/developer
    filesystem — this would keep L6 red on most dev machines for a *secondary*
    control, a disproportionate cost.
  - Treats an anti-DoS bound as a trust-boundary control, which it is not; it
    over-weights a non-load-bearing guard.
  - Pushes a non-trivial host-reconfiguration (XFS + project quotas) onto every
    operator before they can run a single probe.

  *Sketch:* same detection as Option A, but the non-XFS branch calls
  `die "per-container disk quota requires XFS backing storage; move the rootless
  container store to an XFS volume with project quotas"`.

### Option C — Always drop the quota everywhere

Remove `--storage-opt size` unconditionally; never set a per-container disk quota.

- **Pros**
  - Trivially portable — runs identically on every filesystem.
  - Removes the detection logic entirely; smallest diff.
- **Cons**
  - Throws away the control on hosts (XFS) where it *is* enforceable and *is*
    cheap to keep — a strict regression of the existing anti-DoS posture.
  - Makes the `EXEC_BOX_STORAGE_SIZE` knob and the `TC-003` storage assertion dead
    contract surface that no longer does anything.
  - Silently weakens a documented default with no operator signal.

  *Sketch:* delete the `--storage-opt` line from `common_args` and the
  storage-limit assertion from the `TC-003` host inspect; update spec to say the
  quota is unsupported.

## Recommendation

**Option A.** The deciding factor is that the quota is a *secondary, reversible*
control while host portability is a *gating* concern: a non-load-bearing anti-DoS
bound should never block the box from starting on the most common operator
filesystem (Option B's failure mode), nor should portability be bought by discarding
the control where it genuinely works (Option C's regression). Option A keeps the
control where the host can enforce it, degrades explicitly and audibly where it
cannot, and stays reversible — the operator restores full enforcement by moving the
container store to XFS, with no code change. It is the only option that satisfies
both "keep the control where it's real" and "fail loud, never silently weaken."

The co-located swallowed-error bug is fixed under the same task regardless of which
option is chosen, because surfacing a real `podman` launch failure as a non-zero
launcher exit is a verification-gate invariant independent of the quota question.

## Decision

Adopt Option A for task 045:

1. **Detect enforceability.** The launcher determines whether the rootless overlay
   store can enforce `overlay.size` on this host before assembling `common_args`.
2. **Apply when supported.** When enforceable and `EXEC_BOX_STORAGE_SIZE` is
   non-empty, pass `--storage-opt "size=$storage_size"` exactly as today.
3. **Degrade loudly when not.** When not enforceable, omit the flag and emit a
   clear stderr `WARNING` that the per-container disk-quota control is unavailable on
   this filesystem (degraded containment, explicitly logged). The box still launches.
4. **Honor an explicit opt-out.** An empty `EXEC_BOX_STORAGE_SIZE` deliberately
   disables the quota without warning (operator's stated choice).
5. **Fail loud on real launch errors.** Every `podman create` / `podman run`
   invocation checks its exit code; a genuine launch failure surfaces as a non-zero
   exit from `run.sh` with a named error. The launcher must never return success when
   the container did not start.

Accepted by the orchestrator on 2026-06-17 (concurring with the recommendation);
governs task 045.

## Consequences

**Containment trade-off (stated explicitly).** On non-XFS hosts the execution-box
runs **without a per-container writable-layer disk quota**. The load-bearing
controls — egress allowlist, read-only rootfs, `--cap-drop=all`,
`--security-opt=no-new-privileges`, gVisor (`runsc`), and the CPU/memory/PID/tmpfs
caps — are **unaffected**. The worktree (`/work`) is a host bind mount and `/scratch`
is a separately-sized tmpfs, so neither depends on the overlay size quota. Security
severity of the degradation is **low/secondary** (an anti-DoS bound on the ephemeral
writable overlay) and **reversible**: an operator restores full enforcement by
moving the rootless container store onto an XFS volume with project quotas.

**What becomes easier.**
- The execution-box and all container-launching L6 probes run on ext4 hosts, which
  unblocks L6 verification on the common developer/operator machine.
- A real `podman` launch failure can no longer masquerade as a passing probe — the
  verification gate reads a non-started box as a failure, as intended.

**What becomes harder.**
- The launcher now has two valid runtime shapes (quota / no-quota), so reasoning
  about "did the box get its quota?" requires reading the warning line, not assuming.
- A host that *should* be XFS but is misconfigured degrades to a warning rather than
  a hard stop; operators must watch for the `WARNING` in probe evidence.

**Governs task 045.** This ADR governs the `run.sh` implementation in task 045. The
task's test spec must cover:
1. **Quota applied when supported** — on an enforceable host (or a faked
   enforceable-store detection), `--storage-opt "size=…"` is present and the
   `TC-003` host inspect still asserts the storage limit.
2. **Quota skipped + warning emitted when not** — on a non-enforceable host (or
   faked non-XFS detection), the `--storage-opt size` flag is absent, the box still
   launches, and the documented stderr `WARNING` is emitted. The `TC-003` storage
   assertion must tolerate the degraded (no-quota) inspect shape on such hosts rather
   than failing.
3. **A podman launch failure surfaces non-zero** — a forced `podman create`/`podman
   run` failure causes `run.sh` to exit non-zero with a named error (no silent
   exit-0).

**Spec updates land with the code in task 045 (noted, not done here).** The
following `docs/spec/` entries describe the unconditional quota and must be rewritten
in place to reflect the conditional/degrading behavior in the same commit as the
task-045 code change:
- `docs/spec/configuration.md` — the `EXEC_BOX_STORAGE_SIZE` row (overlay storage
  size is applied only when the backing FS can enforce it; empty disables it; a
  warning is emitted when unenforceable) and the Deployment "Resource floor" row.
- `docs/spec/behaviors.md` — the launcher response entry that lists
  "explicit CPU/memory/PID/shared-memory/tmpfs/overlay limits" (overlay limit is
  now conditional on backing-FS support).
- `docs/spec/interfaces.md` — the `--probe` / `TC-003` description, which asserts
  host-side storage-quota inspection (now conditional on enforceability).
- `docs/spec/configuration.md` Defaults policy — note that the disk quota degrades
  gracefully on non-XFS hosts as a documented, ADR-justified exception rather than a
  weakening of the load-bearing controls.
