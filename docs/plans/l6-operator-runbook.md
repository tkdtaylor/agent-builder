# L6 Operator Runbook — the manual work

**Project:** agent-builder
**Created:** 2026-06-16
**Last verified against this host:** 2026-06-16
**Purpose:** The single home for every **hands-on, operator-only** step that promotes Phase 0 and Phase 1 from L5 (fake-provider) to **L6 (live runtime)**. Everything here needs your host, your credentials, or your judgement — it is deliberately *not* in the task backlog. The backlog holds only codeable/automatable work; this runbook holds what a human must do.

> **Automation status — ready.** The two automation tasks this runbook drives are **done and merged**: `make l6-preflight` (task 043) and `make l6-probe` (task 044) exist on `main` now. You no longer fall back to running the per-probe commands by hand — the harness runs them in order and collects evidence. The per-probe reference (exact command + success criterion for each task) lives in [phase0-l6-verification-checklist.md](phase0-l6-verification-checklist.md); use it only to debug a SKIP/FAIL or to run one probe manually.

---

## Host snapshot (verified on THIS host, 2026-06-16)

Ubuntu 26.04 LTS, x86_64, kernel 7.0.0-22-generic. Verified present and working — **no install action needed**:

| Tool | Version | Path | Notes |
|------|---------|------|-------|
| Podman | 5.7.0 | `/usr/bin/podman` | rootless `true` ✓ |
| `runsc` (gVisor) | release-20260601.0, spec 1.2.1 | `/usr/local/bin/runsc` | installed, **but not yet registered in Podman** → step 1a |
| `srt` | `@anthropic-ai/sandbox-runtime` 0.0.54 | `~/.nvm/versions/node/v24.14.0/bin/srt` | via **nvm** (node v24.14.0), **not** snap → snap-confine blocker does **not** apply. ⚠ only on `PATH` when nvm's node is active → see the gotcha below |
| `claude` | 2.1.150 | `~/.local/bin/claude` | confirm it is still logged in (`claude` → check account) |
| `gh` | 2.46.0 | `/usr/bin/gh` | authenticated as `tkdtaylor` ✓ (`gh auth status`) |
| `bwrap` | 0.11.1 | `/usr/bin/bwrap` | ✓ |
| `go` | 1.26.4 | — | ✓ |

> **⚠ srt-on-nvm gotcha (the one that bites first).** `srt` is installed under nvm, so a plain non-login shell does **not** have it on `PATH` — `command -v srt` returns nothing and `make l6-preflight` reports `srt MISSING`. Put it on `PATH` for the whole L6 session before running anything:
> ```bash
> export PATH="$HOME/.nvm/versions/node/v24.14.0/bin:$PATH"
> command -v srt    # expect: $HOME/.nvm/versions/node/v24.14.0/bin/srt
> ```

**The real provisioning gaps on this host are the three in Section 1** (register runsc, populate gate-tools, configure a git remote). Everything else is already satisfied. Re-confirm the whole picture with `make l6-preflight` (Section 2) before trusting this snapshot — it may have drifted.

---

## Section 1 — Provision the host (operator-only)

Three concrete steps. Each ends with a verification command and its expected output.

### 1a. Register `runsc` into rootless Podman

**What:** the `runsc` binary is installed but Podman's default runtime is still `runc`; `--runtime runsc` fails until it's registered in your **rootless user** config `~/.config/containers/containers.conf`.

**First, find out whether that file already exists** (on this host, as of 2026-06-16, it does **not**):

```bash
ls -l ~/.config/containers/containers.conf 2>/dev/null \
  && echo ">>> EXISTS — use the EDIT path below" \
  || echo ">>> MISSING — use the CREATE path below"
```

**CREATE path** (file does not exist — this is the current state on this host). Copy-paste this whole block; it makes the directory and writes the file:

```bash
mkdir -p ~/.config/containers
cat >> ~/.config/containers/containers.conf <<'EOF'

[engine.runtimes]
runsc = ["/usr/local/bin/runsc"]
EOF
```

**EDIT path** (file already exists — it drifted since this was written). Open it and add the runsc line; if an `[engine.runtimes]` section already exists, add the `runsc = …` line *under* it rather than creating a second section:

```bash
${EDITOR:-nano} ~/.config/containers/containers.conf   # opens nano (or your $EDITOR)
# ensure these two lines are present, together:
#   [engine.runtimes]
#   runsc = ["/usr/local/bin/runsc"]
```

**Confirm the file now has the entry** (either path):

```bash
grep -A1 '\[engine.runtimes\]' ~/.config/containers/containers.conf
# expect:
#   [engine.runtimes]
#   runsc = ["/usr/local/bin/runsc"]
```

**Verify** Podman resolves the runtime and a container boots under gVisor:

```bash
podman --runtime runsc run --rm docker.io/library/alpine uname -r
# expect: a gVisor kernel string (e.g. "4.4.0") — NOT the host's "7.0.0-22-generic".
#         The differing kernel version is the proof you're inside gVisor, not on the host.
```

> **Rootless gVisor caveat.** If the run fails on cgroup delegation, add `ignore-cgroups` per the gVisor + Podman rootless guide (gvisor.dev/docs/user_guide/quick_start/podman) — via a `--runtime-flag` or a runsc wrapper. This is the only step that may need a host-specific tweak; everything else is registration-only.

### 1b. Populate the execution-box Gate-toolchain directory

**What:** the execution-box mounts a read-only tools dir at `/opt/agent-builder/gate-tools` (default source: `containment/execution-box/gate-tools/`, which **does not exist yet** on this host). `go`/`gofmt` come from the image; you supply the other three, per [gate-toolchain.manifest](../../containment/execution-box/gate-toolchain.manifest).

**Check what's already on PATH** (on this host: `golangci-lint` and `gods` are present, `code-scanner` is not):

```bash
for t in golangci-lint gods code-scanner; do printf '%-14s ' "$t"; command -v "$t" || echo "NOT on PATH"; done
# this host today:
#   golangci-lint  $HOME/go/bin/golangci-lint
#   gods           $HOME/.local/bin/gods
#   code-scanner   NOT on PATH   <-- the one you must obtain
```

**Create the dir and copy in the two you already have** (uses whatever path each resolves to, so it stays correct if they move):

```bash
mkdir -p containment/execution-box/gate-tools
cp "$(command -v golangci-lint)" containment/execution-box/gate-tools/
cp "$(command -v gods)"          containment/execution-box/gate-tools/
```

**Obtain `code-scanner`** — it ships with the `code-scanner` skill (its release binary), not via PATH. Once you have the executable, copy it in and make everything executable:

```bash
cp /path/to/code-scanner containment/execution-box/gate-tools/   # <-- replace with the real path
chmod +x containment/execution-box/gate-tools/*
```

(Missing either of the first two? `golangci-lint` → `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`; `gods` → `curl -fsSL https://raw.githubusercontent.com/tkdtaylor/dep-scan/main/install.sh | bash`.)

**Verify** the launcher resolves all three:

```bash
containment/execution-box/run.sh --print-toolchain-plan
# expect: golangci-lint, gods, and code-scanner each resolve to a path — no "missing" lines.
```

### 1c. Configure a git remote for PR publication

**What:** the publication probes (tasks 028, 032, 034) open a **real PR**, so they need a remote. **Check the current state first** (on this host: no remote — the project is private-for-now):

```bash
git remote -v
# this host today: prints nothing (no remote configured)
```

If it already prints a remote you trust for throwaway L6 PRs, skip to verify. Otherwise **pick one** (operator decision):

- **Dedicated sandbox repo (recommended)** — keeps real PR noise out of project history. `gh` is already authenticated as `tkdtaylor`, so this is copy-paste:
  ```bash
  gh repo create tkdtaylor/agent-builder-l6-sandbox --private
  git remote add l6 git@github.com:tkdtaylor/agent-builder-l6-sandbox.git
  # then pass AGENT_BUILDER_PUBLISH_REMOTE=l6 to the publication probes (tasks 028/032/034)
  ```
- **A private origin for this repo** — if you want the L6 PRs against the real project:
  ```bash
  gh repo create tkdtaylor/agent-builder --private --source=. --remote=origin
  # then pass AGENT_BUILDER_PUBLISH_REMOTE=origin
  ```

**Verify:**

```bash
git remote -v
# expect: at least one remote with a fetch (and push) URL, e.g.
#   l6   git@github.com:tkdtaylor/agent-builder-l6-sandbox.git (fetch)
#   l6   git@github.com:tkdtaylor/agent-builder-l6-sandbox.git (push)
```

---

## Section 2 — Pre-flight gate

**What:** `make l6-preflight` (task 043) re-checks every prerequisite — tool presence, rootless Podman, git remote, and the baseline gate — and refuses to call the host READY until all pass. It catches the snap-confine `srt` condition defensively (moot on this host) and exits non-zero while anything is missing.

```bash
make l6-preflight
# expect once Section 1 is done AND srt is on PATH: a PASS row per prerequisite, final line READY, exit 0.
```

**Current output on this host (before Section 1, srt not yet on PATH)** — for reference, this is what NOT READY looks like:

```
PASS   podman (binary)
PASS   runsc
PASS   bwrap
MISSING srt — install sandbox-runtime: npm i -g @anthropic-ai/sandbox-runtime
PASS   claude
PASS   gh
MISSING git-remote — no git remote configured — run: git remote add origin <url>
PASS   podman-rootless
PASS   make-check
PASS   make-fitness
NOT READY
```

Here the two `MISSING` rows are exactly the gaps to close: `srt` (put nvm's node on `PATH` — see the gotcha) and `git-remote` (step 1c). `runsc` reports PASS for **presence**; the live-runtime registration (1a) is exercised later by the 016/032 probes, not by preflight.

The baseline gate is also confirmed by preflight (`make-check` / `make-fitness` rows), but you can run them directly:

```bash
make check      # expect: All checks passed.
make fitness    # expect: Fitness checks passed.   (includes F-005 fitness-audit-isolation)
```

---

## Section 3 — Run the Phase 0 live probes

**What:** `make l6-probe` (task 044) runs all 10 closing-order steps in order — the 9 binary probes plus the 030 ledger step — gating each on its prerequisites (a probe whose prereq is absent is `SKIP`, not `FAIL`, and the run continues), and writes a paste-ready evidence file. It calls `l6-preflight` first and refuses to run real probes if the host is NOT READY.

```bash
make l6-probe
# expect (host READY): 10 status rows in closing order, each PASS or SKIP, then a summary line.
#   writes evidence to docs/plans/l6-evidence.txt
```

**Closing order (the exact 10 steps):** 014 → 015 → 016 → 021 → 030 (ledger) → 022 → 028 → 033 → 034 → 032 (capstone).

The evidence file has **one pipe-delimited row per step** (`TASK-<id> | <command> | <final-output-line> | <status>`), paste-ready for the tracker's `Verified by` column. The exact command and success criterion for each probe (reproduced below) are the authoritative reference in [phase0-l6-verification-checklist.md](phase0-l6-verification-checklist.md) — `make l6-probe` runs these same commands:

| # | Task | Command (run by the harness) | Success criterion |
|---|------|------------------------------|-------------------|
| 1 | 014 | `containment/execution-box/run.sh --gate-tools <dir> --worktree . --probe` | probe runs inside the box, exits 0 (no "podman unavailable") |
| 2 | 015 | `containment/execution-box/run.sh --gate-tools <dir> --worktree . --egress-probe` | allowlisted host reachable, denied host refused |
| 3 | 016 | `containment/execution-box/run.sh --gate-tools <dir> --worktree . --runtime runsc --probe` | box starts under runsc, probe exits 0 |
| 4 | 021 | `env AGENT_BUILDER_LIVE_SRT=1 AGENT_BUILDER_LIVE_SRT_ALLOW_HOST=<allow> AGENT_BUILDER_LIVE_SRT_DENY_HOST=<deny> go test -count=1 -v ./tests/sandbox -run TestSandboxRuntimeLiveHarness_TC002_TC003` | `ok ./tests/sandbox` — real srt invoked, allow/deny observed |
| 5 | 030 | ledger step — observe 014/015/016/021 green, record | SKIPs automatically if any of 014/015/016/021 did not run |
| 6 | 022 | `env AGENT_BUILDER_CLAUDE_CLI=claude go run ./cmd/agent-builder run …` | real claude invoked, `Result.Branch` set, `Result.OK == true` |
| 7 | 028 | `env AGENT_BUILDER_CLAUDE_CLI=claude AGENT_BUILDER_SANDBOX_RUNTIME=srt go run ./cmd/agent-builder run --task-root docs/tasks/…` | task selected, run executed in box, `run_finished` persisted |
| 8 | 033 | `containment/execution-box/run.sh --gate-tools <dir> --worktree . --probe` | gate toolchain mounted, `make check` runs to completion inside the box |
| 9 | 034 | `env AGENT_BUILDER_PUBLISH_REMOTE=<remote> AGENT_BUILDER_GH_CLI=gh AGENT_BUILDER_GITHUB_TOKEN=<token> go test -count=1 -v ./tests/publisher -run TestBranchPRPublication` | a real PR is opened (capture the URL) |
| 10 | 032 | `env AGENT_BUILDER_CLAUDE_CLI=claude AGENT_BUILDER_SANDBOX_RUNTIME=srt AGENT_BUILDER_PUBLISH_REMOTE=<remote> AGENT_BUILDER_GH_CLI=gh go test -count=1 -v ./tests/e2e -run TestPhase0EndToEndAcceptance` | task selected, branch produced, PR recorded LIVE, gate passed |

> **Sanity-check the wiring without a live host:** `make l6-probe` is `bash scripts/l6-probe.sh`; add `--dry-run` (i.e. `bash scripts/l6-probe.sh --dry-run`) to print the 10 rows and exercise the gating logic without invoking any real probe. On this host today a dry-run shows 014/015/016/022/033 as `DRY-RUN` and 021/028/030/032/034 as `SKIP` (srt not on PATH + no git remote) — which is exactly the gap Section 1 closes.

---

## Section 4 — Promote 🟡 → ✅ (human-reviewed)

The harness produces evidence; **it does not edit the tracker or commit** — that stays a human step, by the *no unattended self-modification* invariant. For each probe that came back PASS, promote its row in [coverage-tracker.md](../tasks/test-specs/coverage-tracker.md):

- One task per commit, on a task branch, **not batched** (per CLAUDE.md commit rules).
- Paste the verbatim final line from the evidence file into the `Verified by` column.
- Commit message: `verify: confirm task NNN — <L6 evidence>`, then merge.

**The 9 rows currently 🟡 and awaiting L6 promotion: 014, 015, 016, 021, 028, 030, 032, 033, 034.** (The evidence file has 10 rows because it also emits **022**, which is already ✅ at L5 — its row carries an open "real Claude CLI/auth" note you can close at the same time, but it is not one of the 9 pending promotions.)

---

## Section 5 — Phase 1 live probe

Phase 1 (exec-sandbox v0 / Podman swap) has its own L6 residual: the live-Podman e2e, which needs `runsc` registered (1a) and the Gate-toolchain dir populated (1b):

```bash
AGENT_BUILDER_LIVE_PODMAN=1 go test -count=1 -v ./tests/e2e -run TestPhase1LivePodman
# expect: ok ./tests/e2e — box runs under Podman+runsc, gate runs inside the box.
# (skips if Podman/runsc unavailable; config-errors if the Gate-toolchain dir is absent)
```

Promote Phase 1's row the same way as Section 4.

---

## Section 6 — Audit-trail L6 (optional, now available)

The audit-trail chain (tasks 038–042) shipped during this session and is verified at L5 with the **real** `audit-trail` binary. There is no required L6 residual, but you can confirm the live operator path end-to-end:

```bash
# 1. Put the shipped block binary on PATH (or pass AGENT_BUILDER_AUDIT_BIN):
export PATH="$HOME/Code/Public/audit-trail:$PATH"
command -v audit-trail            # expect: .../audit-trail

# 2. Drive a run with the audit chain enabled, then verify it with the block's own verifier:
AGENT_BUILDER_AUDIT_RECORD=/tmp/audit.log <your run command>
audit-trail verify --logfile /tmp/audit.log
# expect: {"valid": true, "tamper_detected_at": null, "message": "chain intact"}
```

`make fitness` already enforces audit isolation as a standing gate (`PASS fitness-audit-isolation: …`) — no operator action needed for that.

---

## Definition of done

L6 is complete when:

- All 9 pending Phase 0 rows (014, 015, 016, 021, 028, 030, 032, 033, 034) are ✅ in the tracker with live (non-fake) evidence recorded, and the 022 "real Claude" note is closed.
- `TestPhase1LivePodman` passes and Phase 1's row is ✅.
- Both phases' roadmap acceptance notes are updated from "accepted at L5 / L6 pending" to "accepted at L6."

At that point the "is it actually usable" question from the original assessment closes: the orchestrator is proven to build a task in real isolation, under gVisor, with a real Claude executor, publishing a real PR — not just against fakes.
