# Test Spec 014: Podman containment profile (execution box)

**Linked task:** [`docs/tasks/backlog/014-podman-containment-profile.md`](../backlog/014-podman-containment-profile.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-004 | ❌ |
| REQ-002 | TC-002, TC-005 | ❌ |
| REQ-003 | TC-003 | ❌ |

## Test cases
### TC-001: Read-only rootfs with writable worktree + tmpfs (happy path)
- **Requirement:** REQ-001
- **Input:** Launch the box; in-box probe writes to a worktree path, a tmpfs scratch path, and to `/`
- **Expected output:** Worktree write succeeds (exit 0); tmpfs write succeeds (exit 0); write to `/` fails (read-only filesystem error)
- **Edge cases:** nested dirs under worktree; tmpfs size limit reached

### TC-002: No socket, no host-home, non-root, dropped caps (happy path)
- **Requirement:** REQ-002
- **Input:** In-box probe runs `id`, scans `/var/run` and known socket paths, checks for host-home mount, reads capability set
- **Expected output:** `id` reports non-root uid/gid; no `*podman*.sock`/docker socket present; no host home mounted; cap set is the minimal/dropped set
- **Edge cases:** socket bind-mounted under an alternate path; CAP_SETUID/CAP_SYS_ADMIN must NOT be present

### TC-003: Resource quotas applied and enforced (happy path)
- **Requirement:** REQ-003
- **Input:** Launch box with configured cpu/mem/pids/disk limits; inspect host cgroup; run a probe that attempts to exceed pids/mem
- **Expected output:** Cgroup config reflects the configured limits; over-limit attempt is throttled/OOM-killed/fork-blocked as appropriate
- **Edge cases:** unset limit defaults; pids cap hit during build fan-out

### TC-004 (NEGATIVE/escape): Write to rootfs is denied
- **Requirement:** REQ-001
- **Input:** In-box probe attempts to create/modify a file at `/`, `/usr`, `/etc`
- **Expected output:** All denied with read-only filesystem error; non-zero exit

### TC-005 (NEGATIVE/escape): Container socket is not reachable
- **Requirement:** REQ-002
- **Input:** In-box probe attempts to talk to a podman/docker socket
- **Expected output:** No socket file exists; connection attempt fails

## Notes
Framework: integration test launching a box via rootless Podman + in-box probe scripts; assertions on exit codes, filesystem errors, `id` output, socket inventory, and host-side cgroup values. Containment is observed at L6 — quote actual probe output. No gVisor dependency here (runtime tiering is task 016), so runc is sufficient for this profile's checks.
