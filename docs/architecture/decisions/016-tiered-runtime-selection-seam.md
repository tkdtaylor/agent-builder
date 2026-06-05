# ADR 016: Tiered Runtime Selection Seam

**Status:** accepted
**Date:** 2026-06-05
**Task:** 016 - Tiered OCI runtime selection seam

## Context

agent-builder's execution box runs one target repo worktree under rootless Podman. ADR 014 deliberately deferred OCI runtime tiering so the initial profile could prove filesystem, user, capability, and quota constraints first. The architecture still needs a runtime seam: ordinary development and reproducibility runs should be able to use `runc`, agent/untrusted-code runs should default to gVisor `runsc`, and a future Kata/Firecracker tier should fit without changing the execution-box profile shape.

The known risk is Go toolchain compatibility under gVisor. Some Go build flows can hit syscalls that `runsc` does not implement. The runtime seam therefore needs both a selector and an observable compatibility probe instead of silently declaring `runsc` safe.

## Decision

Add workload-tier and OCI-runtime selection to `containment/execution-box/run.sh`.

The launcher now resolves a workload tier before Podman starts:

- `agent` defaults to `runsc`;
- `dev` defaults to `runc`;
- `--runtime runc|runsc|kata` overrides the workload default;
- `EXEC_BOX_WORKLOAD` and `EXEC_BOX_RUNTIME` provide environment overrides;
- `--print-runtime-plan` prints the resolved workload/runtime/source without requiring Podman.

The selected runtime is passed to workload and probe containers as Podman `--runtime <name>`. The launcher labels the workload with the selected workload and runtime, exports `EXEC_BOX_WORKLOAD` and `EXEC_BOX_RUNTIME` into the box, checks Podman/PATH runtime availability before launch, and verifies probe containers by inspecting `.HostConfig.Runtime` before attach.

The in-box containment probe prints `TC-016-RUNTIME PASS` with the selected workload/runtime. When the selected runtime is `runsc`, the probe writes a trivial Go module under `/scratch` and runs `CGO_ENABLED=0 go build`; success prints `TC-016-GO PASS`, while failure prints `TC-016-GO FAIL` with captured output and exits non-zero.

## Recorded compatibility finding

Local L6 runtime proof is pending in this environment because rootless Podman is unavailable on `PATH`; consequently `runsc` cannot be exercised here. The required operator command is:

```bash
containment/execution-box/run.sh --worktree . --runtime runsc --probe
```

Promotion to verified status requires quoting both:

- `TC-016-RUNTIME PASS: workload=agent runtime=runsc`
- `TC-016-GO PASS: go build trivial module succeeded under runsc`

If that probe fails with an unimplemented syscall or other gVisor restriction, the fallback is explicit rather than silent: run the workload with `--runtime runc` for reproducible/dev execution while recording the syscall gap, and use the future Kata/Firecracker tier for stronger isolation once implemented. The execution-box defaults remain `agent -> runsc` and `dev -> runc`; compatibility evidence controls verification status, not the existence of the seam.

## Rationale

Keeping rootless Podman as the only substrate preserves ADR 014 and ADR 015. Selecting the OCI runtime with Podman `--runtime` is the narrowest seam that changes isolation tier without weakening filesystem, user, capability, quota, or egress controls.

The workload tier is intentionally small. It maps the two current uses to safe defaults while leaving explicit override available for operators who need reproducibility, local debugging, or fallback after a gVisor compatibility failure.

`--print-runtime-plan` mirrors `--print-egress-plan`: it gives static tests and operators a deterministic way to inspect the resolved contract without requiring Podman. Runtime verification still requires launching the box because the active OCI runtime is a host/container effect.

## Consequences

Agent workloads now request `runsc` by default. Hosts without gVisor installed fail closed with an unavailable-runtime message instead of silently falling back to Podman's default runtime. Operators can select `--workload dev` or `--runtime runc` when they need local dev execution on hosts without `runsc`.

Task 016 can reach code-merged status with static tests and `make check`, but cannot be marked verified until the L6 `runsc` probe records the runtime and Go build result.
