package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/registry"
)

// antigravityCompleter is the Antigravity (`agy`) single-shot Completer (ADR 059). It
// runs `agy --print <prompt> --model <model>` and returns stdout — the AntigravityCLI
// executor without the worktree/branch/commit machinery and without the agentic-mode
// flags (--add-dir, --dangerously-skip-permissions). Subscription/OAuth only: the
// process environment is inherited so agy reads its ~/.antigravity keyring; no key is
// injected.
type antigravityCompleter struct {
	cliPath    string
	modelID    string
	cmdFactory antigravityCommandCreator
}

// Compile-time assertion: antigravityCompleter satisfies the Completer interface.
var _ Completer = (*antigravityCompleter)(nil)

// newAntigravityCompleter builds an antigravityCompleter for an agy registry entry.
func newAntigravityCompleter(entry registry.RegistryEntry) *antigravityCompleter {
	return &antigravityCompleter{
		cliPath:    "agy",
		modelID:    strings.TrimSpace(entry.ModelID),
		cmdFactory: exec.CommandContext,
	}
}

// Complete runs `agy --print <prompt> --model <model>` and returns the trimmed stdout.
// No worktree, no tools, no verification gate, no branch (the Completer contract).
func (a *antigravityCompleter) Complete(ctx context.Context, _ registry.RegistryEntry, prompt string) (string, error) {
	runDir, err := os.MkdirTemp("", "agent-builder-agy-completer-*")
	if err != nil {
		return "", fmt.Errorf("completer: create agy temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(runDir) }()

	args := []string{"--print", prompt, "--model", a.modelID}
	cmd := a.cmdFactory(ctx, a.cliPath, args...)
	cmd.Dir = runDir

	// Subscription mode: inherit the full process environment so agy can read its
	// keyring at ~/.antigravity (HOME preserved). No API key is injected.
	base := cmd.Env
	if base == nil {
		base = os.Environ()
	}
	cmd.Env = base

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("completer: antigravity CLI %q failed: %w: %s",
			a.cliPath, err, sanitizeAntigravityOutput(stdout.String(), stderr.String()))
	}

	return strings.TrimSpace(stdout.String()), nil
}
