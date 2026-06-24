# Contributing to agent-builder

Thanks for your interest in agent-builder. This document explains how to build,
test, and submit changes. The canonical, harness-neutral briefing for the project
is [AGENTS.md](AGENTS.md) — read it before making non-trivial changes.

## Quick start

```bash
# Fork and clone
git clone https://github.com/<your-fork>/agent-builder
cd agent-builder

# Install Go 1.26+ (see docs/architecture/tech-stack.md)

# Build and test
go build ./...
go test ./...
```

## Workflow

agent-builder uses a TDD-first, task-driven workflow (full details in
[AGENTS.md](AGENTS.md)):

1. **Open an issue first** before writing code. New features become a task file
   under `docs/tasks/backlog/NNN-name.md` (see
   [Proposing a new feature](#proposing-a-new-feature) below).
2. **Write the test spec before any implementation.** Every task has a paired
   `docs/tasks/test-specs/NNN-name-test-spec.md`. No PR without one.
3. **One task, one commit.** Do not batch unrelated changes.
4. **Commit message format:** use a conventional prefix — `feat:`, `fix:`,
   `test:`, `docs:`, `chore:` — followed by a concise summary.
5. **Update `docs/spec/` and `docs/architecture/diagrams.md` in the same commit**
   as any change to externally-visible behavior, the data model, an interface, or
   a diagrammed flow. Spec and code that disagree means one of them is wrong.

## Local CI gates

All of these must pass before pushing. Running them locally saves a CI round-trip:

```bash
gofmt -l .                        # formatting (no output = clean)
golangci-lint run                 # lints
go test ./...                     # tests
make check                        # lint + test + fitness (the verification gate)
```

The verification gate also runs supply-chain scanners (`dep-scan`, `code-scanner`)
as blocking steps; see [AGENTS.md](AGENTS.md) under *External tools*.

**Minimum Go version:** see `go.mod` (`go 1.26`) and
[docs/architecture/tech-stack.md](docs/architecture/tech-stack.md).

## Test-spec-first rule

Every task has a paired test spec in `docs/tasks/test-specs/`. The spec defines
"done" — write it before any implementation code. PRs that add behavior without a
test spec will be sent back. Browse existing specs in
[`docs/tasks/test-specs/`](docs/tasks/test-specs/) and the tracker in
[`docs/tasks/test-specs/coverage-tracker.md`](docs/tasks/test-specs/coverage-tracker.md).

## Proposing a new feature

1. Open a GitHub issue describing the use case and the problem it solves.
2. Wait for acceptance (a maintainer will comment or add a task file).
3. Once accepted, a task file lands in `docs/tasks/backlog/` with a paired
   test spec stub. You can claim it or the maintainer will assign it.

Do not start writing code before the task file exists.

## Licensing and the DCO

Code contributions are licensed under Apache-2.0 (inbound=outbound, §5). We use
the Developer Certificate of Origin (DCO) instead of a CLA — sign off commits
with `git commit -s` (appends `Signed-off-by:`). No CLA required.

## Reporting security issues

Do **not** open a public issue for security vulnerabilities. See
[SECURITY.md](SECURITY.md) for the disclosure process.

## Code of conduct

All contributors are expected to follow the project's
[Code of Conduct](CODE_OF_CONDUCT.md).
