# Tech Stack

**Project:** agent-builder
**Last updated:** 2026-06-24

## Core stack

| Layer | Technology | Rationale |
|-------|-----------|-----------|
| Language | Go 1.26.3 | Single static binary, strong stdlib, fast compile-test loop, and an import graph that fitness checks can assert on to enforce package-isolation invariants. |
| Framework | None (standard library only) | Unix-philosophy composition over a framework. The orchestrator is small, typed seams wired by hand; `go.mod` declares **no third-party dependencies** (no `go.sum`), which keeps the supply-chain surface of the trusted outside-the-box code minimal. |
| Database | None | State lives in the target repo (git branches/PRs), in plain-text task files, and in append-only NDJSON run records / the `audit-trail` hash chain. No datastore to operate. |
| Containment | Rootless Podman + tiered OCI runtime (`runc` → gVisor `runsc` → Kata/Firecracker) | The execution-box profile is a product artifact: read-only rootfs, scratch tmpfs, non-root, dropped caps, resource quotas, default-deny egress. Workload tier picks the runtime (`agent` → `runsc`, `dev` → `runc`); rootless networked paths fall back to `runc` (ADR 030). |

## Development tooling

| Tool | Purpose |
|------|---------|
| Git | Version control; one task = one branch, merged to `main` after the verify gate. |
| `go build` / `go run` | Compile and run the orchestrator (`./cmd/agent-builder`). |
| golangci-lint (`.golangci.yml`) | Lint gate; part of `make check`. |
| gofmt | Formatting (`make format`). |
| make | Verification gate driver — `make check` = `lint test fitness`. |
| `scripts/check-mermaid.py` | Zero-dependency Mermaid validator for `docs/architecture/diagrams.md`; exits non-zero so it can gate an audit. |

## Testing

| Tool | Scope |
|------|-------|
| Go `testing` (stdlib) | Unit and integration tests (60 `*_test.go` files); deterministic fakes (`FakeSink`, `FakeRunner`) exercise seams without the live stack. |
| Fitness functions (`make fitness`) | Executable architecture guards F-001…F-007 — no-docker, gate-blocking, supervisor/audit/policy isolation, no-srt, exec-sandbox-default. Block-severity; see `docs/spec/fitness-functions.md`. |
| L5/L6 validation harness | End-to-end acceptance against live containment (`make l6-preflight` / `l6-probe`); the verification ladder's L5/L6 evidence is what promotes a task to ✅. |

## External tools & blocks

agent-builder is the **assembly layer** — it composes ecosystem blocks over their published CLI contracts rather than vendoring them. Each is opt-in via an env-var-supplied binary path:

| Tool / block | Role | Opt-in via |
|--------------|------|-----------|
| exec-sandbox | Default contained-command run backend (north-star) | `AGENT_BUILDER_EXEC_SANDBOX_BIN` |
| audit-trail | Hash-chained audit log + signed checkpoints | `AGENT_BUILDER_AUDIT_RECORD` / `AGENT_BUILDER_AUDIT_BIN` |
| vault | Token brokering over a Unix socket (opaque handles) | `AGENT_BUILDER_VAULT_BIN` |
| policy-engine | Fail-closed AuthZEN authorization gate | `AGENT_BUILDER_POLICY_BIN` |
| armor | LLM guard on the web-ingestion + tool-call path | wired in `internal/executorharness` |
| code-scanner | Malware/backdoor scan in the verification gate | gate toolchain |
| dep-scan (`gods`) | Supply-chain CVE scan of pulled modules | gate toolchain |
| gh / git | Clone target repos, push branches, open PRs | host PATH |

## Notes

- **No third-party Go dependencies by design.** Adding one is an "ask first" decision (see `AGENTS.md`) — it widens the trusted-code supply-chain surface. Prefer the stdlib or an out-of-process CLI seam.
- Minimum Go: **1.26** (`go.mod`). gVisor `runsc` Go-toolchain compatibility is recorded through the containment probe (ADR 016).
- The blocks above are consumed over CLI/socket contracts, not imported — `internal/audit`, `internal/vault`, `internal/secrets`, and `internal/policy` are stdlib-only leaves, enforced by the isolation fitness checks.
