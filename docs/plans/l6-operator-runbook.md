# L6 Operator Runbook — the manual work

**Project:** agent-builder
**Created:** 2026-06-16
**Purpose:** The single home for every **hands-on, operator-only** step that promotes Phase 0 and Phase 1 from L5 (fake-provider) to **L6 (live runtime)**. Everything here needs your host, your credentials, or your judgement — it is deliberately *not* in the task backlog. The backlog holds only codeable/automatable work; this runbook holds what a human must do.

> **Dependency on the backlog.** The two automation tasks — [043 host preflight doctor](../tasks/backlog/043-l6-host-preflight-doctor.md) (`make l6-preflight`) and [044 probe harness](../tasks/backlog/044-l6-probe-harness.md) (`make l6-probe`) — build the tools this runbook drives. **Clear those two from the backlog first**; until they merge, the `make l6-*` targets below do not exist and the probe steps fall back to the manual per-probe commands in [phase0-l6-verification-checklist.md](phase0-l6-verification-checklist.md).

---

## Host snapshot (observed 2026-06-16, Ubuntu 26.04 LTS, x86_64)

The roadmap's L6 blocker notes predate this host and are **stale** — most prerequisites are already satisfied. Verified present and ready, **no action needed**:

- rootless **Podman** 5.7.0 (`Rootless=true`)
- **`runsc`** (gVisor) `release-20260601.0`, spec 1.2.1 — installed at `/usr/local/bin/runsc`
- **`srt`** (`@anthropic-ai/sandbox-runtime`) — installed via **nvm** (node v24.14.0), **not** snap, so the snap-confine blocker the checklist warns about **does not apply here**
- **`claude`** 2.1.150 — logged in (`claude.ai`, a personal address)
- **`gh`** 2.46.0 — authenticated (tkdtaylor)
- **`bwrap`** 0.11.1

**The only real provisioning gaps are the three in Section 1.** Re-confirm the whole picture with `make l6-preflight` (task 043) before trusting this snapshot — it may have drifted by the time you run it.

---

## Section 1 — Provision the host (operator-only)

Three concrete steps. Each ends with a verification command.

### 1a. Wire `runsc` into rootless Podman

The binary is installed but Podman's default runtime is still `runc`; `--runtime runsc` will fail until it's registered. Add it to your **rootless user** config (`~/.config/containers/containers.conf`):

```toml
[engine.runtimes]
runsc = ["/usr/local/bin/runsc"]
```

Verify Podman now resolves the runtime and a container actually boots under gVisor:

```bash
podman --runtime runsc run --rm docker.io/library/alpine uname -a
# success: prints a kernel string from inside the gVisor sandbox (not the host's 7.0.0 kernel)
```

> **Rootless gVisor caveat.** If the run fails on cgroup delegation, add `ignore-cgroups` per the gVisor + Podman rootless guide (gvisor.dev/docs/user_guide/quick_start/podman) — either via a `--runtime-flag` or a runsc wrapper. This is the one step that may need a host-specific tweak; everything else is registration-only.

### 1b. Populate the execution-box Gate-toolchain directory

The execution-box mounts a read-only tools dir at `/opt/agent-builder/gate-tools` (default source: `containment/execution-box/gate-tools/`). `go`/`gofmt` come from the image; you supply the other three. Per [gate-toolchain.manifest](../../containment/execution-box/gate-toolchain.manifest):

| Tool | Where to get it |
|------|-----------------|
| `golangci-lint` | `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest` (or distro/release binary) |
| `gods` (dep-scan, Go) | `curl -fsSL https://raw.githubusercontent.com/tkdtaylor/dep-scan/main/install.sh \| bash` |
| `code-scanner` | from the `code-scanner` skill / its release binary |

Drop the three executables into `containment/execution-box/gate-tools/` (or point `EXEC_BOX_GATE_TOOLS` / `--gate-tools` at wherever they live). Verify the launcher sees them all:

```bash
containment/execution-box/run.sh --print-toolchain-plan
# success: each of golangci-lint, gods, code-scanner resolves to a path (no "missing" lines)
```

### 1c. Configure a git remote for PR publication

There is no remote configured (the project is private-for-now). The publication probes (tasks 028, 032, 034) open a **real PR**, so they need a remote to push to. **Operator decision** — pick one:

- **Dedicated sandbox repo (recommended)** — keeps real PR noise out of the project history:
  ```bash
  gh repo create tkdtaylor/agent-builder-l6-sandbox --private
  git remote add l6 git@github.com:tkdtaylor/agent-builder-l6-sandbox.git
  # then pass AGENT_BUILDER_PUBLISH_REMOTE=l6 to the publication probes
  ```
- **A private origin for this repo** — if you want the L6 PRs against the real project:
  ```bash
  gh repo create tkdtaylor/agent-builder --private --source=. --remote=origin
  ```

Verify:

```bash
git remote -v        # success: at least one remote with a fetch+push URL
```

---

## Section 2 — Pre-flight gate

Once Section 1 is done, run the doctor (task 043) until it reports **READY**. It re-checks every prerequisite, flags the snap-confine `srt` condition (moot on this host but caught defensively), and exits non-zero while anything is missing:

```bash
make l6-preflight
# success: overall verdict READY, exit 0
```

Baseline gate must also be green:

```bash
make check      # -> All checks passed.
make fitness    # -> Fitness checks passed.
```

---

## Section 3 — Run the Phase 0 live probes

Run the harness (task 044). It executes the 9 probes in the prescribed closing order (014 → 015 → 016 → 021 → 030-ledger → 022 → 028 → 033 → 034 → 032), skips any whose prereqs are absent, and writes a paste-ready evidence file:

```bash
make l6-probe
# produces an evidence file: one row per task — id, command, verbatim final line, PASS/SKIP/FAIL
```

The exact verbatim command and success criterion for each probe live in [phase0-l6-verification-checklist.md](phase0-l6-verification-checklist.md) — the harness runs those same commands; the checklist is the reference if you need to run one by hand or debug a SKIP/FAIL.

---

## Section 4 — Promote 🟡 → ✅ (human-reviewed)

The harness produces evidence; **it does not edit the tracker or commit** — that stays a human step, by the *no unattended self-modification* invariant. For each probe that came back PASS, you promote its row in [coverage-tracker.md](../tasks/test-specs/coverage-tracker.md):

- One task per commit, on a task branch, **not batched** (per CLAUDE.md commit rules).
- Paste the verbatim final line from the evidence file into the `Verified by` column.
- Commit message: `verify: confirm task NNN — <L6 evidence>`, then merge.

The 9 rows to promote: **014, 015, 016, 021, 028, 030, 032, 033, 034** (plus closing the 022 "real Claude" note).

---

## Section 5 — Phase 1 live probe

Phase 1 (exec-sandbox v0 / Podman swap) has its own L6 residual: the live-Podman e2e, which needs `runsc` wired (1a) and the Gate-toolchain dir populated (1b):

```bash
AGENT_BUILDER_LIVE_PODMAN=1 go test -count=1 -v ./tests/e2e -run TestPhase1LivePodman
# success: ok ./tests/e2e — box runs under Podman+runsc, gate runs inside the box
# (skips if Podman/runsc unavailable; config-errors if the Gate-toolchain dir is absent)
```

Promote Phase 1's row the same way as Section 4.

---

## Definition of done

L6 is complete when:

- All 9 Phase 0 rows are ✅ in the tracker with live (non-fake) evidence recorded.
- `TestPhase1LivePodman` passes and Phase 1's row is ✅.
- Both phases' roadmap acceptance notes are updated from "accepted at L5 / L6 pending" to "accepted at L6."

At that point the "is it actually usable" question from the original assessment closes: the orchestrator is proven to build a task in real isolation, under gVisor, with a real Claude executor, publishing a real PR — not just against fakes.
