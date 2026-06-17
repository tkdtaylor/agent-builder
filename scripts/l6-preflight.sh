#!/usr/bin/env bash
# scripts/l6-preflight.sh — L6 host readiness preflight check
#
# Checks all prerequisites from docs/plans/phase0-l6-verification-checklist.md
# and emits a structured readiness report. Exits non-zero if NOT READY.
#
# Output format (one row per prerequisite):
#   PASS   <check-name>
#   FAIL   <check-name>  — <one-line remediation hint>
#   MISSING <check-name> — <one-line remediation hint>
# Final line: READY  or  NOT READY
#
# Testability seam:
#   Set L6_PREFLIGHT_PATH to a directory of stub binaries. This value is
#   prepended to PATH so every command -v / invocation uses stubs first.
#   Make check / fitness are also faked this way (a stub 'make' on the PATH).
#
# Usage:
#   bash scripts/l6-preflight.sh
#   L6_PREFLIGHT_PATH=/tmp/stubs bash scripts/l6-preflight.sh

set -euo pipefail

# ─── compute repo root early (before PATH may be replaced) ───────────────────
# Use bash parameter expansion to avoid requiring 'dirname' on PATH.

_SCRIPT="${BASH_SOURCE[0]}"
_SCRIPT_DIR="${_SCRIPT%/*}"
# Resolve to absolute path without relying on 'dirname' or 'readlink'
case "$_SCRIPT_DIR" in
    /*) ;;
    *) _SCRIPT_DIR="$(cd "$_SCRIPT_DIR" && pwd)" ;;
esac
REPO_ROOT="$(cd "$_SCRIPT_DIR/.." && pwd)"

# ─── injectable PATH seam (REQ-043-05) ───────────────────────────────────────
#
# When L6_PREFLIGHT_PATH is set, use it as the EXCLUSIVE PATH for all command
# lookups. This ensures test stubs fully replace real system tools so that
# TC-043-01 through TC-043-04 can run without any live host tooling leaking in.
#
# When L6_PREFLIGHT_PATH is unset, use the normal host PATH unchanged.

if [ -n "${L6_PREFLIGHT_PATH:-}" ]; then
    export PATH="${L6_PREFLIGHT_PATH}"
fi

# ─── state ────────────────────────────────────────────────────────────────────

READY=1  # flipped to 0 on any FAIL or MISSING

# ─── output helpers ───────────────────────────────────────────────────────────

emit_pass() {
    printf 'PASS   %s\n' "$1"
}

emit_fail() {
    local name="$1"
    local hint="$2"
    printf 'FAIL   %s — %s\n' "$name" "$hint"
    READY=0
}

emit_missing() {
    local name="$1"
    local hint="$2"
    printf 'MISSING %s — %s\n' "$name" "$hint"
    READY=0
}

# ─── tool-presence checks (fast, actionable signal first) ─────────────────────

# podman — binary present?
# (rootless check is a separate row further below)
if command -v podman > /dev/null 2>&1; then
    emit_pass "podman (binary)"
else
    emit_missing "podman (binary)" \
        "install rootless Podman: https://podman.io/getting-started/installation"
fi

# runsc (gVisor)
if command -v runsc > /dev/null 2>&1; then
    emit_pass "runsc"
else
    emit_missing "runsc" \
        "install gVisor: https://gvisor.dev/docs/user_guide/install/"
fi

# bwrap (bubblewrap)
if command -v bwrap > /dev/null 2>&1; then
    emit_pass "bwrap"
else
    emit_missing "bwrap" \
        "install bubblewrap: sudo apt install bubblewrap  (or equivalent)"
fi

# srt (@anthropic-ai/sandbox-runtime) — run it to detect snap-confine blocker
#
# REQ-043-03: if srt is present but exits with the snap-confine string, emit
# a snap-SPECIFIC FAIL with a distinct remediation hint. If it exits with any
# other non-zero status, emit a generic FAIL. If absent, MISSING.
if command -v srt > /dev/null 2>&1; then
    srt_output="$(srt --version 2>&1)" && srt_exit=0 || srt_exit=$?
    if [ "$srt_exit" -ne 0 ]; then
        if printf '%s' "$srt_output" | grep -qF "snap-confine has elevated permissions and is not confined"; then
            emit_fail "srt" \
                "snap-confine blocker detected — install srt outside snap: npm i -g @anthropic-ai/sandbox-runtime (not via snap)"
        else
            emit_fail "srt" \
                "srt exits non-zero (exit ${srt_exit}): ${srt_output}"
        fi
    else
        emit_pass "srt"
    fi
else
    emit_missing "srt" \
        "install sandbox-runtime: npm i -g @anthropic-ai/sandbox-runtime"
fi

# claude (Claude Code CLI)
if command -v claude > /dev/null 2>&1; then
    emit_pass "claude"
else
    emit_missing "claude" \
        "install Claude Code CLI: https://docs.anthropic.com/claude-code"
fi

# gh (GitHub CLI)
if command -v gh > /dev/null 2>&1; then
    emit_pass "gh"
else
    emit_missing "gh" \
        "install GitHub CLI: https://cli.github.com  then: gh auth login"
fi

# git remote — is a remote configured?
# (git binary absent is a distinct case from no remote configured)
if ! command -v git > /dev/null 2>&1; then
    emit_missing "git-remote" \
        "git is not on PATH — install git"
else
    remote_output="$(git remote -v 2>/dev/null || true)"
    if [ -n "$remote_output" ]; then
        emit_pass "git-remote"
    else
        emit_missing "git-remote" \
            "no git remote configured — run: git remote add origin <url>"
    fi
fi

# podman rootless — REQ-043-04: is Podman running in rootless mode?
# Only checked if podman binary is present.
if command -v podman > /dev/null 2>&1; then
    rootless_output="$(podman info --format '{{.Host.Security.Rootless}}' 2>/dev/null)" \
        && rootless_exit=0 || rootless_exit=$?
    if [ "$rootless_exit" -ne 0 ]; then
        emit_fail "podman-rootless" \
            "podman info failed (exit ${rootless_exit}) — ensure rootless Podman is configured: podman info --format '{{.Host.Security.Rootless}}' must print 'true'"
    elif [ "$rootless_output" = "true" ]; then
        emit_pass "podman-rootless"
    else
        emit_fail "podman-rootless" \
            "podman info reports rootless=${rootless_output} — configure rootless Podman: see https://podman.io/getting-started/installation#user-configuration"
    fi
fi

# ─── gate checks (slowest — last so fast checks surface first) ────────────────

if make -C "$REPO_ROOT" check > /dev/null 2>&1; then
    emit_pass "make-check"
else
    emit_fail "make-check" \
        "make check failed — fix lint/test/fitness errors before running L6 probes"
fi

if make -C "$REPO_ROOT" fitness > /dev/null 2>&1; then
    emit_pass "make-fitness"
else
    emit_fail "make-fitness" \
        "make fitness failed — fix fitness function violations before running L6 probes"
fi

# ─── verdict ──────────────────────────────────────────────────────────────────

if [ "$READY" -eq 1 ]; then
    printf 'READY\n'
    exit 0
else
    printf 'NOT READY\n'
    exit 1
fi
