# ADR 014: Rootless Podman Execution-Box Profile

**Status:** accepted
**Date:** 2026-06-05
**Task:** 014 — Podman containment profile

## Context

agent-builder needs a product containment artifact for the agent execution box. The box is exec-sandbox v0 Tier 1 minus supervisor orchestration: it must run one target repo worktree inside an untrusted agent environment while the trusted supervisor stays outside.

The project invariants already choose rootless Podman as the substrate and reserve `containment/` as the allowed product container path. Task 014 turns that invariant into a concrete profile that later supervisor and exec-sandbox adapter tasks can invoke.

## Decision

Define the execution-box profile under `containment/execution-box/` and launch it with rootless Podman.

The profile:

- runs only for a non-root host user and uses `--userns=keep-id` with the current host uid/gid in the container;
- sets `--read-only` on the root filesystem;
- mounts exactly one writable repo worktree at `/work`;
- mounts scratch as tmpfs at `/scratch` with an explicit size and no device or setuid bits;
- does not bind the host home directory;
- does not bind any container-engine socket;
- uses `--cap-drop=all` and `--security-opt=no-new-privileges`;
- adds back no Linux capabilities for the Go-build profile;
- applies CPU, memory, PID, shared-memory, tmpfs, and overlay storage-size limits;
- labels the container so host-side inspection can find the execution-box run.

The default OCI runtime remains Podman's configured runtime for this task. Runtime tier selection (`runc` / `runsc` / Kata or Firecracker) is deferred to task 016.

## Rationale

Rootless Podman keeps the containment substrate aligned with the project invariant and avoids requiring a privileged daemon. A read-only rootfs narrows accidental or malicious persistence. A single writable worktree plus tmpfs scratch gives build tools the mutability they need without making host state broadly reachable.

No capability add-backs are required for normal Go build, vet, test, and formatting flows. Starting from `--cap-drop=all` means future tasks must justify any additional capability with a specific build need instead of inheriting a broad default set.

The resource limits are explicit because containment is not only filesystem isolation. CPU, memory, PID, shared-memory, tmpfs, and overlay storage bounds make runaway behavior observable and constrainable by host inspection.

## Consequences

The profile is runtime-observable: static tests can prove the launcher contract, but only a rootless Podman probe can prove L6 containment. Environments without Podman must report that blocker and keep task 014 at code-merged status until an operator runs the probe.

Future supervisor work should call the profile as a product artifact instead of embedding ad hoc Podman flags. Future runtime-tier work may add a runtime selector, but must preserve the profile's filesystem, socket, user, capability, and resource-limit guarantees.
