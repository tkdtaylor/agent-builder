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
| `runsc` (gVisor) | release-20260601.0, spec 1.2.1 | `/usr/local/bin/runsc` | installed; needs a **rootless cgroup wrapper** + registration → step 1a (done & verified 2026-06-16: `4.19.0-gvisor`) |
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

**Provisioning status on this host (2026-06-16):** steps 1a (runsc cgroup wrapper + registration), 1b (gate-tools populated), and 1c (`l6` sandbox remote) are all ✅ done & verified. With `srt` exported onto PATH, `make l6-preflight` reports **READY** (all 10 rows PASS — confirmed 2026-06-16). **The only thing you must do each new shell is the srt PATH export** (the nvm gotcha above); then you're clear to run Section 3's probes.

---

## Section 1 — Provision the host (operator-only)

Three concrete steps. Each ends with a verification command and its expected output.

### 1a. Register `runsc` into rootless Podman

**What:** the `runsc` binary is installed but Podman's default runtime is still `runc`; `--runtime runsc` fails until it's registered in your **rootless user** config `~/.config/containers/containers.conf`.

> **This host needs a cgroup wrapper (verified 2026-06-16).** Registering the bare `/usr/local/bin/runsc` and running `podman --runtime runsc …` fails rootless with:
> `OCI runtime error: runsc: creating container: systemd error: Access denied as the requested operation requires interactive authentication`.
> That's runsc trying to create a **systemd cgroup scope**, which a rootless user session can't do without polkit. The fix is a one-line wrapper that runs runsc with `-ignore-cgroups` ("don't configure cgroups"), and registering *that* wrapper as `runsc`. Both substeps below were run and verified on this host (`uname -r` → `4.19.0-gvisor`).

**Step 1 — create the cgroup wrapper** (`~/.local/bin` is already on PATH). Copy-paste:

```bash
mkdir -p ~/.local/bin
cat > ~/.local/bin/runsc-rootless <<'EOF'
#!/bin/sh
# runsc wrapper for rootless Podman: skip cgroup configuration so runsc does not
# try to create a systemd scope (denied without polkit in a rootless session).
exec /usr/local/bin/runsc -ignore-cgroups "$@"
EOF
chmod +x ~/.local/bin/runsc-rootless
~/.local/bin/runsc-rootless --version    # expect: runsc version release-20260601.0
```

**Step 2 — register the wrapper as the `runsc` runtime.** First find out whether the config file already exists:

```bash
ls -l ~/.config/containers/containers.conf 2>/dev/null \
  && echo ">>> EXISTS — use the EDIT path below" \
  || echo ">>> MISSING — use the CREATE path below"
```

**CREATE path** (file does not exist). Copy-paste this whole block; it makes the directory and writes the file:

```bash
mkdir -p ~/.config/containers
cat >> ~/.config/containers/containers.conf <<'EOF'

[engine.runtimes]
runsc = ["$HOME/.local/bin/runsc-rootless"]
EOF
```

**EDIT path** (file already exists — e.g. you previously registered the bare `/usr/local/bin/runsc`). Open it and point the `runsc =` line at the wrapper; if an `[engine.runtimes]` section already exists, edit the line under it rather than adding a second section:

```bash
${EDITOR:-nano} ~/.config/containers/containers.conf   # opens nano (or your $EDITOR)
# ensure these two lines are present, together:
#   [engine.runtimes]
#   runsc = ["$HOME/.local/bin/runsc-rootless"]
```

> One-liner to repoint an existing bare-runsc registration without opening an editor:
> ```bash
> sed -i 's#^runsc = .*#runsc = ["$HOME/.local/bin/runsc-rootless"]#' ~/.config/containers/containers.conf
> ```

**Confirm the registration** (either path):

```bash
grep -A1 '\[engine.runtimes\]' ~/.config/containers/containers.conf
# expect:
#   [engine.runtimes]
#   runsc = ["$HOME/.local/bin/runsc-rootless"]
```

**Verify** a container boots under gVisor via the registered name (this is what the 016/032 probes use):

```bash
podman --runtime runsc run --rm docker.io/library/alpine uname -r
# expect: a kernel string ending in "-gvisor" (verified here: 4.19.0-gvisor) —
#         NOT the host's "7.0.0-22-generic". The -gvisor suffix proves you're in the sandbox.
```

### 1b. Populate the execution-box Gate-toolchain directory

**What:** the execution-box mounts a read-only tools dir at `/opt/agent-builder/gate-tools` (default source: `containment/execution-box/gate-tools/`). `go`/`gofmt` come from the image; you supply `golangci-lint`, `gods`, and `code-scanner`, per [gate-toolchain.manifest](../../containment/execution-box/gate-toolchain.manifest). **This was wired and verified on this host on 2026-06-16** — the commands below are the exact ones used; run them verbatim, no substitution needed. `golangci-lint`/`gods` are static binaries; `code-scanner` is your Python tool at `$HOME/Code/Public/code-scanner/cli/code-scanner` (it's self-contained but needs its `semgrep-rules/` directory copied alongside it for the offline ruleset).

Copy-paste the whole block — it creates the dir, copies all three tools by their real resolved paths plus the semgrep ruleset, and sets execute bits:

```bash
mkdir -p containment/execution-box/gate-tools
cp "$(command -v golangci-lint)" containment/execution-box/gate-tools/                                   # $HOME/go/bin/golangci-lint
cp "$(command -v gods)"          containment/execution-box/gate-tools/                                   # $HOME/.local/bin/gods
cp    $HOME/Code/Public/code-scanner/cli/code-scanner   containment/execution-box/gate-tools/
cp -r $HOME/Code/Public/code-scanner/cli/semgrep-rules  containment/execution-box/gate-tools/
chmod +x containment/execution-box/gate-tools/code-scanner \
         containment/execution-box/gate-tools/golangci-lint \
         containment/execution-box/gate-tools/gods
```

> These copied binaries are host-specific and large (golangci-lint is ~40 MB), so `containment/execution-box/gate-tools/*` is **git-ignored** — they will not show up as changes to commit.

**Verify** the launcher resolves all three (no "missing" lines):

```bash
containment/execution-box/run.sh --print-toolchain-plan
# expect (verified on this host):
#   TC-001 PLAN: mount golangci-lint=…/gate-tools/golangci-lint
#   TC-002 PLAN: golangci-lint version=golangci-lint has version 2.12.2 …
#   TC-001 PLAN: mount gods=…/gate-tools/gods
#   TC-001 PLAN: mount code-scanner=…/gate-tools/code-scanner
#   TC-002 PLAN: code-scanner version=code-scanner 1.0.0
```

> **In-box execution caveat (does not block Phase 0 probes).** The box image is `golang:1.26-alpine` with **no python3**, so `code-scanner` (a `#!/usr/bin/env python3` script) can be *mounted and presence-validated* inside the box but cannot actually *scan* there. This is fine for the L6 probes: the in-box gate runs agent-builder's own `make check`, which does **not** invoke `code-scanner`, and the probe only checks the tool is present on PATH. `code-scanner` only needs to execute when the box later runs the Gate against a built block — at which point add `RUN apk add --no-cache python3` to [containment/execution-box/Containerfile](../../containment/execution-box/Containerfile) (a separate, tracked change).
>
> (If you ever need the first two fresh: `golangci-lint` → `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`; `gods` → `curl -fsSL https://raw.githubusercontent.com/tkdtaylor/dep-scan/main/install.sh | bash`.)

### 1c. Configure a git remote for PR publication

**What:** the publication probes (tasks 028, 032, 034) open a **real PR**, so they need a remote whose **default branch exists** — the publisher runs `git push <remote> <branch>` then `gh pr create --head <branch> --fill`, and `gh pr create` targets the repo's default branch as the PR base. An *empty* repo has no default branch and PR creation fails, so the remote must have `main` pushed. **Done & verified on this host on 2026-06-16** using the dedicated-sandbox option below.

> **This host already has it.** `git remote -v` now shows the `l6` remote pointing at the private `tkdtaylor/agent-builder-l6-sandbox` repo (default branch `main` pushed). You do **not** need to redo 1c. The commands below are recorded for a fresh host / re-provision.

**Check the current state first:**

```bash
git remote -v
# this host today: shows l6 -> https://github.com/tkdtaylor/agent-builder-l6-sandbox.git (fetch & push)
```

If it already shows a remote you trust for throwaway L6 PRs, skip to verify. Otherwise **pick one** (operator decision):

- **Dedicated sandbox repo (recommended)** — keeps real PR noise out of project history. `gh` is authenticated as `tkdtaylor`. This one command creates the **private** repo, wires the remote `l6`, **and pushes `main` as the PR base** in a single step:
  ```bash
  gh auth setup-git   # configure git to use gh's token for github.com https pushes (once per host)
  gh repo create tkdtaylor/agent-builder-l6-sandbox --private --source=. --remote=l6 --push
  # then pass AGENT_BUILDER_PUBLISH_REMOTE=l6 to the publication probes (tasks 028/032/034)
  ```
- **A private origin for this repo** — if you want the L6 PRs against the real project:
  ```bash
  gh auth setup-git
  gh repo create tkdtaylor/agent-builder --private --source=. --remote=origin --push
  # then pass AGENT_BUILDER_PUBLISH_REMOTE=origin
  ```

**Verify** the remote exists *and* the repo has a default branch (both are required for PR creation):

```bash
git remote -v
# expect: l6   https://github.com/tkdtaylor/agent-builder-l6-sandbox.git (fetch)
#         l6   https://github.com/tkdtaylor/agent-builder-l6-sandbox.git (push)
gh repo view tkdtaylor/agent-builder-l6-sandbox --json visibility,defaultBranchRef \
  --jq '{visibility, defaultBranch: .defaultBranchRef.name}'
# expect: {"visibility":"PRIVATE","defaultBranch":"main"}
```

---

## Section 2 — Pre-flight gate

**What:** `make l6-preflight` (task 043) re-checks every prerequisite — tool presence, rootless Podman, git remote, and the baseline gate — and refuses to call the host READY until all pass. It catches the snap-confine `srt` condition defensively (moot on this host) and exits non-zero while anything is missing.

**On this host, export `srt` onto PATH first** (the nvm gotcha — otherwise the `srt` row reports MISSING), then run the gate:

```bash
export PATH="$HOME/.nvm/versions/node/v24.14.0/bin:$PATH"
make l6-preflight
```

**Verified output on this host (2026-06-16, after Section 1 + srt on PATH)** — all 10 rows PASS, exit 0:

```
PASS   podman (binary)
PASS   runsc
PASS   bwrap
PASS   srt
PASS   claude
PASS   gh
PASS   git-remote
PASS   podman-rootless
PASS   make-check
PASS   make-fitness
READY
```

If you skip the `srt` export you'll instead see `MISSING srt …` and a final `NOT READY` — that's the nvm gotcha, not a real gap. `runsc` reports PASS for **presence**; the live-runtime registration (1a) is exercised later by the 016/032 probes, not by preflight.

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

**Host-side execution (Claude and gate placement).** In probes 022, 028, 032, and 034, the **`claude` CLI executor runs on this host** (your machine) — it is **not** inside the Podman execution-box. The orchestrator invokes `claude run --prompt "..." …` as a host-side process, which produces a branch to the host's git, and then runs the gate (linter/tests/scanner) **inside** the Podman sandbox. This separation is load-bearing: the executor needs to edit files and produce a branch (host operations); the gate needs to run in a trusted sandbox (box operations). For probes 022 and 028, the executor writes a stub/real task to the host's `AGENT_BUILDER_TASK_ROOT`. For probe 032, the executor completes a real task against a real remote. All three use `AGENT_BUILDER_CLAUDE_CLI=claude` (the real CLI) and require `ANTHROPIC_API_KEY` set in your environment. Do **not** try to run `claude` inside the box — the box has read-only rootfs and no network (only `--dns none`), so it cannot reach the Anthropic API.

The evidence file has **one pipe-delimited row per step** (`TASK-<id> | <command> | <final-output-line> | <status>`), paste-ready for the tracker's `Verified by` column. The exact command and success criterion for each probe (reproduced below) are the authoritative reference in [phase0-l6-verification-checklist.md](phase0-l6-verification-checklist.md) — `make l6-probe` runs these same commands:

| # | Task | Command (run by the harness) | Success criterion |
|---|------|------------------------------|-------------------|
| 1 | 014 | `containment/execution-box/run.sh --gate-tools <dir> --worktree . --probe` | probe runs inside the box, exits 0 (no "podman unavailable") |
| 2 | 015 | `containment/execution-box/run.sh --gate-tools <dir> --worktree . --egress-probe` | allowlisted host reachable, denied host refused |
| 3 | 016 | `containment/execution-box/run.sh --gate-tools <dir> --worktree . --runtime runsc --probe` | box starts under runsc, probe exits 0 |
| 4 | 021 | `env AGENT_BUILDER_LIVE_SRT=1 AGENT_BUILDER_LIVE_SRT_ALLOW_HOST=<allow> AGENT_BUILDER_LIVE_SRT_DENY_HOST=<deny> go test -count=1 -v ./tests/sandbox -run TestSandboxRuntimeLiveHarness_TC002_TC003` | `ok ./tests/sandbox` — real srt invoked, allow/deny observed |
| 5 | 030 | ledger step — observe 014/015/016/021 green, record | SKIPs automatically if any of 014/015/016/021 did not run |
| 6 | 022 | `env ANTHROPIC_API_KEY=<key> … go run ./cmd/agent-builder run` (see Section 1c) | real claude invoked host-side, `Result.Branch` set, `Result.OK == true` |
| 7 | 028 | `env ANTHROPIC_API_KEY=<key> … go run ./cmd/agent-builder run` (see Section 1c) | task selected, branch produced host-side, gate runs inside box, `run_finished` persisted |
| 8 | 033 | `containment/execution-box/run.sh --gate-tools <dir> --worktree . --probe` | gate toolchain mounted, `make check` runs to completion inside the box |
| 9 | 034 | `env AGENT_BUILDER_LIVE_PUBLISH=1 AGENT_BUILDER_LIVE_PUBLISH_REMOTE=<remote> go test -count=1 -v ./tests/publisher -run TestLiveBranchPRPublication_TC034` | a real PR is opened against the `l6` sandbox remote (capture the URL) |
| 10 | 032 | `env AGENT_BUILDER_LIVE_E2E=1 AGENT_BUILDER_LIVE_E2E_REMOTE=<remote> go test -count=1 -v ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032` | task selected, branch produced host-side, PR recorded LIVE against `l6`, gate passed |

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
