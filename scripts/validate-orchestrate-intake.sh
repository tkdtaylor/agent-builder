#!/usr/bin/env bash
#
# validate-orchestrate-intake.sh — L5 verification script for orchestrate intake.
# Verifies both auto escape-hatch and interactive clarifying session flows
# against the compiled binary in a clean sandbox.

set -euo pipefail

# Ensure we run from the repo root
REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

echo "=== Building agent-builder binary ==="
BIN_DIR=$(mktemp -d)
trap 'rm -rf "$BIN_DIR"' EXIT

go build -o "$BIN_DIR/agent-builder" ./cmd/agent-builder

echo "=== Setting up sandbox environment ==="
SANDBOX_DIR="$BIN_DIR/sandbox"
mkdir -p "$SANDBOX_DIR/task_root/docs/plans"
mkdir -p "$SANDBOX_DIR/worktree"
mkdir -p "$SANDBOX_DIR/shims"

# Write dummy docs/plans/roadmap.md
echo "# Roadmap" > "$SANDBOX_DIR/task_root/docs/plans/roadmap.md"
echo "module test" > "$SANDBOX_DIR/task_root/go.mod"

# Write shims to bypass validation/scanners
for shim in git dep-scan code-scanner golangci-lint armor gods; do
  echo -e "#!/bin/sh\nexit 0" > "$SANDBOX_DIR/shims/$shim"
  chmod +x "$SANDBOX_DIR/shims/$shim"
done

# Write dummy private key (hex, 128 characters)
echo "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff" > "$SANDBOX_DIR/signing.key"

# Base environment variables
export PATH="$SANDBOX_DIR/shims:$PATH"
export AGENT_BUILDER_INBOUND="env"
export AGENT_BUILDER_TASK_ROOT="$SANDBOX_DIR/task_root"
export AGENT_BUILDER_WORKTREE="$SANDBOX_DIR/worktree"
export AGENT_BUILDER_PUBLISH_REMOTE="origin"
export AGENT_BUILDER_RUN_TIMEOUT="5m"
export AGENT_BUILDER_MAX_ATTEMPTS="2"
export ANTHROPIC_API_KEY="test-key-not-used"
export AGENT_BUILDER_WORKER_SIGNING_KEY="$SANDBOX_DIR/signing.key"
export AGENT_BUILDER_POLICY_RISK="low"
export AGENT_BUILDER_POLICY_SOCKET=""
export AGENT_BUILDER_VAULT_SOCKET=""

echo "=== 1. Validating AGENT_BUILDER_INTAKE=auto escape hatch ==="
export AGENT_BUILDER_INTAKE="auto"
export AGENT_BUILDER_GOAL_SPEC="repo: github.com/tkdtaylor/exec-sandbox
spec: build feature X"
export AGENT_BUILDER_GOAL_ID="goal-auto"

# Run auto mode - should exit 0 immediately without waiting on stdin
OUT_AUTO=$("$BIN_DIR/agent-builder" orchestrate < /dev/null 2>&1)
echo "Output from auto mode:"
echo "$OUT_AUTO"
echo

if [[ "$OUT_AUTO" == *"clarifying"* ]] || [[ "$OUT_AUTO" == *"ready"* ]]; then
  echo "FAIL: Expected auto mode to bypass clarification pause." >&2
  exit 1
fi
echo "OK: Auto escape hatch verified successfully."
echo

echo "=== 2. Validating interactive intake session ==="
unset AGENT_BUILDER_INTAKE
export AGENT_BUILDER_GOAL_SPEC="fix bugs"
export AGENT_BUILDER_GOAL_ID="goal-interactive"

# Run interactive session by feeding input over a pipe
OUT_INTERACTIVE=$(echo -e "info goal-interactive repo: github.com/tkdtaylor/exec-sandbox\nconfirm goal-interactive" | "$BIN_DIR/agent-builder" orchestrate 2>&1)

echo "Output from interactive mode:"
echo "$OUT_INTERACTIVE"
echo

# Assertions
if ! echo "$OUT_INTERACTIVE" | grep -q -i "repository"; then
  echo "FAIL: Expected output to contain prompt for repository" >&2
  exit 1
fi

if ! echo "$OUT_INTERACTIVE" | grep -q -i "confirm goal-interactive"; then
  echo "FAIL: Expected output to contain confirm prompt" >&2
  exit 1
fi

echo "OK: Interactive intake session verified successfully."
echo "=== All L5 intake validation checks passed! ==="
