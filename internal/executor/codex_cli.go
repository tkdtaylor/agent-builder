// Package executor contains concrete implementations of the supervisor.Executor seam.
package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/secrets"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

const (
	// CodexAPIKeyEnv is the environment variable the Codex CLI reads for auth.
	// Codex CLI uses OPENAI_API_KEY for authentication (OpenAI API key).
	CodexAPIKeyEnv = "OPENAI_API_KEY"
)

var (
	// ErrCodexSecretNotFound indicates the Codex API key could not be resolved.
	ErrCodexSecretNotFound = errors.New("executor: codex API key not found")
	// ErrCodexBlankWorktree indicates the worktree path is blank.
	ErrCodexBlankWorktree = errors.New("executor: blank codex worktree")
	// ErrCodexMissingBranch indicates the Codex CLI did not report a produced branch.
	ErrCodexMissingBranch = errors.New("executor: codex CLI did not report produced branch")
)

// commandCreator is a factory function for exec.Cmd. Tests override this to inject
// a stub subprocess without needing a real codex binary on PATH.
type commandCreator func(ctx context.Context, name string, args ...string) *exec.Cmd

// CodexCLI drives the Codex CLI subprocess for one target worktree.
// It implements supervisor.Executor.
//
// The auth token is resolved at dispatch time via secrets.SecretSource.NamedProviderToken
// using the SecretRef from the RegistryEntry. The resolved token is injected as
// OPENAI_API_KEY into the subprocess environment (never stored beyond the Run call).
type CodexCLI struct {
	entry        registry.RegistryEntry
	secretSource secrets.SecretSource
	worktree     string
	cmdFactory   commandCreator
}

// Compile-time assertion: CodexCLI satisfies supervisor.Executor.
var _ supervisor.Executor = (*CodexCLI)(nil)

// NewCodexCLI constructs a CodexCLI adapter from a RegistryEntry and a SecretSource.
// The worktree parameter is the path on disk where the Codex CLI will operate.
// Auth is resolved at Run time via entry.SecretRef → secretSource.NamedProviderToken.
func NewCodexCLI(entry registry.RegistryEntry, secretSource secrets.SecretSource, worktree string) *CodexCLI {
	return &CodexCLI{
		entry:        entry,
		secretSource: secretSource,
		worktree:     strings.TrimSpace(worktree),
		cmdFactory:   exec.CommandContext,
	}
}

// Run invokes the Codex CLI subprocess and returns the branch it produces.
// It resolves the API key at call time via secretSource.NamedProviderToken.
func (c *CodexCLI) Run(task supervisor.Task) (supervisor.Result, error) {
	return c.run(context.Background(), task)
}

// run is the internal implementation that accepts an explicit context.
func (c *CodexCLI) run(ctx context.Context, task supervisor.Task) (supervisor.Result, error) {
	if strings.TrimSpace(c.worktree) == "" {
		return supervisor.Result{}, ErrCodexBlankWorktree
	}

	// Resolve the auth token at dispatch time — never cache it on the struct.
	apiKey, err := c.secretSource.NamedProviderToken(c.entry.SecretRef)
	if err != nil {
		return supervisor.Result{}, fmt.Errorf("%w: SecretRef=%q: %w", ErrCodexSecretNotFound, c.entry.SecretRef, err)
	}

	prompt := buildCodexPrompt(task, c.worktree)

	// Codex CLI invocation: codex --model <model> --approval-policy never-require <prompt>
	// The model flag sets which OpenAI model Codex uses.
	// --approval-policy never-require enables unattended (non-interactive) operation.
	args := []string{
		"--model", c.entry.ModelID,
		"--approval-policy", "never-require",
		prompt,
	}

	cmd := c.cmdFactory(ctx, "codex", args...)
	cmd.Dir = c.worktree
	// Use the cmd's existing Env if the factory already set one (e.g. in tests);
	// otherwise start from the process environment. Then inject the API key.
	base := cmd.Env
	if base == nil {
		base = os.Environ()
	}
	cmd.Env = codexEnv(base, c.entry.ModelID, apiKey)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return supervisor.Result{OK: false}, fmt.Errorf(
			"executor: codex CLI failed: %w: %s",
			err,
			sanitizeCodexOutput(stdout.String(), stderr.String(), apiKey),
		)
	}

	branch := extractCodexBranch(stdout.String())
	if branch == "" {
		return supervisor.Result{OK: false}, ErrCodexMissingBranch
	}

	return supervisor.Result{Branch: branch, OK: true}, nil
}

// buildCodexPrompt constructs the task prompt passed to the Codex CLI.
func buildCodexPrompt(task supervisor.Task, worktree string) string {
	prompt := fmt.Sprintf(`You are running inside agent-builder as the Codex CLI executor.

Task ID: %s
Repo: %s
Task spec: %s
Worktree: %s

Read the task spec, implement the requested change in this worktree, run the relevant verification, and leave the produced git branch checked out.
When finished, write the produced branch name as the last line of your output in the format:
BRANCH: <branch-name>
`, task.ID, task.Repo, task.Spec, worktree)

	if task.PriorFailure != "" {
		prompt += fmt.Sprintf("\nYour previous attempt failed the verification gate.\n\n%s\n", task.PriorFailure)
	}

	return prompt
}

// codexEnv constructs the subprocess environment with the Codex API key injected.
// It takes the base environment (e.g. os.Environ()), strips any pre-existing
// OPENAI_API_KEY, then injects the resolved key. This mirrors the pattern used
// by claudeEnv and preserves other env vars (including test helper vars).
func codexEnv(base []string, modelID, apiKey string) []string {
	env := make([]string, 0, len(base)+2)
	for _, entry := range base {
		if strings.HasPrefix(entry, CodexAPIKeyEnv+"=") {
			continue // strip pre-existing key — ours replaces it
		}
		env = append(env, entry)
	}
	env = append(env,
		CodexAPIKeyEnv+"="+apiKey,
		"CODEX_MODEL="+modelID,
	)
	return env
}

// extractCodexBranch parses the branch name from Codex CLI stdout.
// The Codex CLI is expected to write "BRANCH: <branch-name>" as the last output line.
func extractCodexBranch(stdout string) string {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "BRANCH:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "BRANCH:"))
		}
	}
	return ""
}

// sanitizeCodexOutput trims and redacts the API key from combined subprocess output.
func sanitizeCodexOutput(stdout, stderr, apiKey string) string {
	output := strings.TrimSpace(strings.Join([]string{stdout, stderr}, "\n"))
	if output == "" {
		return "no output"
	}
	if apiKey != "" {
		output = strings.ReplaceAll(output, apiKey, "[REDACTED]")
	}
	return output
}
