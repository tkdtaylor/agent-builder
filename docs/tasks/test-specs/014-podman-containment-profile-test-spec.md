# Test Spec 014: Podman containment profile (execution box)

**Linked task:** [`docs/tasks/backlog/014-podman-containment-profile.md`](../backlog/014-podman-containment-profile.md)
**Written:** 2026-06-04
**Status:** complete

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-004 | ✅ |
| REQ-002 | TC-002, TC-005 | ✅ |
| REQ-003 | TC-003, TC-006 | ✅ |

## Acceptance criteria
- The profile lives under `containment/` and is a product execution-box artifact, not a development container definition.
- The launcher can be inspected without Podman and refuses to run when Podman is unavailable, so CI can prove the contract statically without faking runtime containment.
- Runtime validation is a separate probe path: when rootless Podman is available, launching the profile runs in-box probes and prints per-TC PASS/FAIL lines.
- The profile uses rootless Podman only; it must not depend on host home mounts, a container-engine socket, or privileged/capability-bearing execution.
- Static tests reference every TC marker below and assert the profile/launcher/probe files contain the security controls they are meant to exercise.

## Test cases
### TC-001: Read-only rootfs with writable worktree + tmpfs (happy path)
- **Requirement:** REQ-001
- **Input:** Launch the profile with a repo worktree mounted at `/work` and tmpfs scratch mounted at `/scratch`; run the in-box probe.
- **Expected output:** Probe prints `TC-001 PASS`; creating files under `/work` and `/scratch` succeeds; creating or modifying files directly under `/` fails with a read-only-filesystem or permission-denied error.
- **Static assertions:** launcher passes `--read-only`, exactly one writable bind for `/work`, and a tmpfs mount for `/scratch`.
- **Edge cases:** nested directories under `/work`; scratch size reaches the configured tmpfs limit without making any other rootfs path writable.

### TC-002: No socket, no host-home, non-root, dropped caps (happy path)
- **Requirement:** REQ-002
- **Input:** In-box probe runs `id`, inspects the mount table, scans known container-engine socket paths, checks for host-home material, and reads capability state.
- **Expected output:** Probe prints `TC-002 PASS`; `id -u` and `id -g` are non-zero; no container-engine socket exists; no host home path is mounted; the launcher uses `--cap-drop=all`, `--security-opt=no-new-privileges`, and a non-root user.
- **Static assertions:** launcher has no host home bind and no socket bind; profile has no cap add-back for the Go build profile.
- **Edge cases:** socket bind-mounted under an alternate path; privileged mode, CAP_SETUID, and CAP_SYS_ADMIN must not be present.

### TC-003: Resource quotas applied and enforced (happy path)
- **Requirement:** REQ-003
- **Input:** Launch the profile with its default CPU, memory, PID, and disk/tmpfs limits; collect `podman inspect` output and run the quota probe.
- **Expected output:** Probe prints `TC-003 PASS`; inspect output shows explicit CPU, memory, PID, and storage/tmpfs limits; an over-limit PID or memory probe is blocked, throttled, or killed by the runtime.
- **Static assertions:** launcher passes `--cpus`, `--memory`, `--pids-limit`, `--shm-size`, tmpfs `size=`, and an overlay/storage size limit when supported by Podman.
- **Edge cases:** unset environment overrides fall back to safe defaults; PID cap is high enough for modest Go builds but low enough to prevent fork fan-out.

### TC-004 (NEGATIVE/escape): Write to rootfs is denied
- **Requirement:** REQ-001
- **Input:** In-box probe attempts to create or modify files at `/`, `/usr`, and `/etc`.
- **Expected output:** Probe prints `TC-004 PASS`; all writes are denied and the probe fails loudly if any attempt succeeds.
- **Edge cases:** existing writable tmpfs locations do not mask `/`, `/usr`, or `/etc`.

### TC-005 (NEGATIVE/escape): Container socket is not reachable
- **Requirement:** REQ-002
- **Input:** In-box probe checks `/run`, `/var/run`, `/tmp`, and the process environment for container-engine socket paths.
- **Expected output:** Probe prints `TC-005 PASS`; no socket file exists and no socket environment variable is present.
- **Edge cases:** socket path hidden under an alternate runtime directory; symlink to a socket.

### TC-006: Rootless launcher contract and missing-runtime behavior
- **Requirement:** REQ-003
- **Input:** Invoke the launcher on a host without Podman, or with a non-root Podman installation unavailable to the current user.
- **Expected output:** Launcher exits non-zero with a clear message naming Podman as unavailable; no partial container is left running.
- **Static assertions:** launcher refuses UID 0, passes `--userns=keep-id`, does not use privileged mode, and documents the runtime-visible probe command.
- **Edge cases:** `podman info` fails even though the binary exists; launcher still reports the exact failing command path instead of pretending runtime validation happened.

## Notes
Framework: Go static contract tests under `tests/containment/` plus the runtime harness `containment/execution-box/run.sh --probe` when rootless Podman is available. Static tests keep `make check` meaningful in CI without Podman; runtime validation remains the only evidence for L6 and must quote actual probe output. No gVisor dependency here (runtime tiering is task 016), so the default OCI runtime is sufficient for this profile's checks.
