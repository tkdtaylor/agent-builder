package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/secrets"
)

// claudeCompleter is the Claude-CLI single-shot Completer (ADR 059). It runs
// `claude -p <prompt>` in an isolated temp HOME and returns stdout — the ClaudeCLI
// executor without the worktree/branch/commit machinery. Auth mirrors
// NewClaudeCLIFromEntry: cloud entries resolve ProviderToken(); local (translation-proxy)
// entries set ANTHROPIC_BASE_URL + the LocalProxyAuthPlaceholder.
type claudeCompleter struct {
	cliPath    string
	authToken  string
	oauthToken string
	baseURL    string // translation-proxy URL for local entries; empty = cloud mode
	cmdFactory claudeCommandCreator
}

// Compile-time assertion: claudeCompleter satisfies the Completer interface.
var _ Completer = (*claudeCompleter)(nil)

// newClaudeCompleter builds a claudeCompleter for a Claude-harness registry entry,
// resolving cloud credentials through src (subscription/API-key) or, for local entries
// (empty SecretRef), pointing at the entry's translation-proxy endpoint.
func newClaudeCompleter(entry registry.RegistryEntry, src secrets.SecretSource) *claudeCompleter {
	c := &claudeCompleter{
		cliPath:    "claude",
		cmdFactory: exec.CommandContext,
	}
	if strings.TrimSpace(entry.SecretRef) == "" {
		// Local entry: no cloud auth; route through the translation proxy.
		c.baseURL = entry.Endpoint
	} else {
		// Cloud entry: resolve credentials from the secret source.
		c.authToken, c.oauthToken = src.ProviderToken()
	}
	return c
}

// Complete runs `claude -p <prompt>` and returns the trimmed stdout. No worktree,
// no tools, no verification gate, no branch (the Completer contract — ADR 053).
func (c *claudeCompleter) Complete(ctx context.Context, _ registry.RegistryEntry, prompt string) (string, error) {
	runDir, err := os.MkdirTemp("", "agent-builder-claude-completer-*")
	if err != nil {
		return "", fmt.Errorf("completer: create Claude CLI temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(runDir) }()

	cmd := c.cmdFactory(ctx, c.cliPath, "-p", prompt)
	cmd.Dir = runDir
	// Merge auth/HOME onto the command's existing env (os.Environ() in production; a
	// test-injected env when the cmdFactory is stubbed) so the base is preserved.
	base := cmd.Env
	if base == nil {
		base = os.Environ()
	}
	cmd.Env = claudeEnv(base, c.authToken, c.oauthToken, c.baseURL, runDir)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("completer: Claude CLI %q failed: %w: %s",
			c.cliPath, err, sanitizeCLIOutput(stdout.String(), stderr.String(), c.authToken, c.oauthToken))
	}

	return strings.TrimSpace(stdout.String()), nil
}
