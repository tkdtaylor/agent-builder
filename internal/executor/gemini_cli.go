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
	// GeminiAPIKeyEnv is the environment variable the Gemini CLI reads for auth.
	// The Google Gemini CLI uses GEMINI_API_KEY for authentication.
	GeminiAPIKeyEnv = "GEMINI_API_KEY"
)

var (
	// ErrGeminiSecretNotFound indicates the Gemini API key could not be resolved.
	ErrGeminiSecretNotFound = errors.New("executor: gemini API key not found")
	// ErrGeminiBlankWorktree indicates the worktree path is blank.
	ErrGeminiBlankWorktree = errors.New("executor: blank gemini worktree")
	// ErrGeminiMissingBranch indicates the Gemini CLI did not report a produced branch.
	ErrGeminiMissingBranch = errors.New("executor: gemini CLI did not report produced branch")
)

// geminiCommandCreator is a factory function for exec.Cmd. Tests override this to inject
// a stub subprocess without needing a real gemini binary on PATH.
type geminiCommandCreator func(ctx context.Context, name string, args ...string) *exec.Cmd

// GeminiCLI drives the Gemini CLI subprocess for one target worktree.
// It implements supervisor.Executor.
//
// The auth token is resolved at dispatch time via secrets.SecretSource.NamedProviderToken
// using the SecretRef from the RegistryEntry. The resolved token is injected as
// GEMINI_API_KEY into the subprocess environment (never stored beyond the Run call).
type GeminiCLI struct {
	entry        registry.RegistryEntry
	secretSource secrets.SecretSource
	worktree     string
	cmdFactory   geminiCommandCreator
}

// Compile-time assertion: GeminiCLI satisfies supervisor.Executor.
var _ supervisor.Executor = (*GeminiCLI)(nil)

// NewGeminiCLI constructs a GeminiCLI adapter from a RegistryEntry and a SecretSource.
// The worktree parameter is the path on disk where the Gemini CLI will operate.
// Auth is resolved at Run time via entry.SecretRef → secretSource.NamedProviderToken.
func NewGeminiCLI(entry registry.RegistryEntry, secretSource secrets.SecretSource, worktree string) *GeminiCLI {
	return &GeminiCLI{
		entry:        entry,
		secretSource: secretSource,
		worktree:     strings.TrimSpace(worktree),
		cmdFactory:   exec.CommandContext,
	}
}

// Run invokes the Gemini CLI subprocess and returns the branch it produces.
// It resolves the API key at call time via secretSource.NamedProviderToken.
func (g *GeminiCLI) Run(ctx context.Context, task supervisor.Task) (supervisor.Result, error) {
	return g.run(ctx, task)
}

// run is the internal implementation that accepts an explicit context.
func (g *GeminiCLI) run(ctx context.Context, task supervisor.Task) (supervisor.Result, error) {
	if strings.TrimSpace(g.worktree) == "" {
		return supervisor.Result{}, ErrGeminiBlankWorktree
	}

	var apiKey string
	// Branch on SecretRef early: empty means subscription/OAuth mode; non-empty means API-key mode.
	// For API-key mode, resolve the auth token BEFORE creating the subprocess command.
	if g.entry.SecretRef != "" {
		// API-key mode: resolve the auth token at dispatch time — never cache it on the struct.
		var err error
		apiKey, err = g.secretSource.NamedProviderToken(g.entry.SecretRef)
		if err != nil {
			return supervisor.Result{}, fmt.Errorf("%w: SecretRef=%q: %w", ErrGeminiSecretNotFound, g.entry.SecretRef, err)
		}
	}

	prompt := buildGeminiPrompt(task, g.worktree)

	// Gemini CLI invocation: gemini --model <model> <prompt>
	// The model flag sets which Gemini model is used.
	// The prompt is passed as a positional argument.
	args := []string{
		"--model", g.entry.ModelID,
		prompt,
	}

	cmd := g.cmdFactory(ctx, "gemini", args...)
	cmd.Dir = g.worktree

	// Use the cmd's existing Env if the factory already set one (e.g. in tests);
	// otherwise start from the process environment.
	base := cmd.Env
	if base == nil {
		base = os.Environ()
	}

	// Configure the environment based on auth mode.
	if g.entry.SecretRef == "" {
		// Subscription mode: use cached OAuth login, no API key injection.
		cmd.Env = geminiSubscriptionEnv(base, g.entry.ModelID)
	} else {
		// API-key mode: inject the resolved token.
		cmd.Env = geminiEnv(base, g.entry.ModelID, apiKey)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return supervisor.Result{OK: false}, fmt.Errorf(
			"executor: gemini CLI failed: %w: %s",
			err,
			sanitizeGeminiOutput(stdout.String(), stderr.String(), apiKey),
		)
	}

	branch := extractGeminiBranch(stdout.String())
	if branch == "" {
		return supervisor.Result{OK: false}, ErrGeminiMissingBranch
	}

	return supervisor.Result{Branch: branch, OK: true}, nil
}

// buildGeminiPrompt constructs the task prompt passed to the Gemini CLI.
func buildGeminiPrompt(task supervisor.Task, worktree string) string {
	prompt := fmt.Sprintf(`You are running inside agent-builder as the Gemini CLI executor.

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

// geminiEnv constructs the subprocess environment with the Gemini API key injected.
// It takes the base environment (e.g. os.Environ()), strips any pre-existing
// GEMINI_API_KEY, then injects the resolved key. This mirrors the pattern used
// by codexEnv and preserves other env vars (including test helper vars).
func geminiEnv(base []string, modelID, apiKey string) []string {
	env := make([]string, 0, len(base)+2)
	for _, entry := range base {
		if strings.HasPrefix(entry, GeminiAPIKeyEnv+"=") {
			continue // strip pre-existing key — ours replaces it
		}
		env = append(env, entry)
	}
	env = append(env,
		GeminiAPIKeyEnv+"="+apiKey,
		"GEMINI_MODEL="+modelID,
	)
	return env
}

// geminiSubscriptionEnv constructs the subprocess environment for subscription/OAuth mode.
// It takes the base environment, strips any pre-existing GEMINI_API_KEY (to force OAuth),
// sets GEMINI_MODEL, and preserves HOME and other env vars so the gemini CLI uses its
// cached OAuth login (~/.gemini).
func geminiSubscriptionEnv(base []string, modelID string) []string {
	env := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if strings.HasPrefix(entry, GeminiAPIKeyEnv+"=") {
			continue // strip pre-existing key — force OAuth
		}
		env = append(env, entry)
	}
	env = append(env, "GEMINI_MODEL="+modelID)
	return env
}

// extractGeminiBranch parses the branch name from Gemini CLI stdout.
// The Gemini CLI is expected to write "BRANCH: <branch-name>" as the last output line.
func extractGeminiBranch(stdout string) string {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "BRANCH:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "BRANCH:"))
		}
	}
	return ""
}

// sanitizeGeminiOutput trims and redacts the API key from combined subprocess output.
func sanitizeGeminiOutput(stdout, stderr, apiKey string) string {
	output := strings.TrimSpace(strings.Join([]string{stdout, stderr}, "\n"))
	if output == "" {
		return "no output"
	}
	if apiKey != "" {
		output = strings.ReplaceAll(output, apiKey, "[REDACTED]")
	}
	return output
}
