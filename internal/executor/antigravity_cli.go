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

var (
	// ErrAntigravityBlankWorktree indicates the worktree path is blank.
	ErrAntigravityBlankWorktree = errors.New("executor: blank antigravity worktree")
	// ErrAntigravityNoChanges indicates the worktree has no changes to commit.
	ErrAntigravityNoChanges = errors.New("executor: agy produced no changes to commit")
)

// antigravityCommandCreator is a factory function for exec.Cmd. Tests override this to inject
// a stub subprocess without needing a real agy binary on PATH.
type antigravityCommandCreator func(ctx context.Context, name string, args ...string) *exec.Cmd

// AntigravityCLI drives the Antigravity (`agy`) CLI subprocess for one target worktree.
// It implements supervisor.Executor.
//
// Antigravity uses subscription/OAuth authentication via its own keyring (~/.antigravity),
// similar to task 132 (Gemini subscription mode). No API key is injected; the resolver
// is not consulted. The SecretRef must be empty ("") for subscription entries.
// The agy CLI is invoked in print mode with --model, --add-dir, --dangerously-skip-permissions,
// and the task prompt. The subprocess inherits the process environment (HOME preserved)
// so agy can access its keyring.
//
// Isolation is provided by the outer exec-sandbox perimeter; --dangerously-skip-permissions
// is safe only because the executor runs inside that sandbox. Comment in the code explains
// why we skip agy's own sandbox flag.
type AntigravityCLI struct {
	entry      registry.RegistryEntry
	worktree   string
	cmdFactory antigravityCommandCreator
}

// Compile-time assertion: AntigravityCLI satisfies supervisor.Executor.
var _ supervisor.Executor = (*AntigravityCLI)(nil)

// NewAntigravityCLI constructs an AntigravityCLI adapter from a RegistryEntry and a worktree.
// The entry must have SecretRef == "" (subscription mode); no secrets are resolved at Run time.
func NewAntigravityCLI(entry registry.RegistryEntry, _ secrets.SecretSource, worktree string) *AntigravityCLI {
	return &AntigravityCLI{
		entry:      entry,
		worktree:   strings.TrimSpace(worktree),
		cmdFactory: exec.CommandContext,
	}
}

// Run invokes the Antigravity CLI subprocess and returns the branch it produces.
// Subscription mode: no API key resolution; inherits env and keyring from ~/.antigravity.
func (a *AntigravityCLI) Run(ctx context.Context, task supervisor.Task) (supervisor.Result, error) {
	return a.run(ctx, task)
}

// run is the internal implementation that accepts an explicit context.
func (a *AntigravityCLI) run(ctx context.Context, task supervisor.Task) (supervisor.Result, error) {
	if strings.TrimSpace(a.worktree) == "" {
		return supervisor.Result{}, ErrAntigravityBlankWorktree
	}

	prompt := buildAntigravityPrompt(task, a.worktree)

	// Antigravity CLI invocation in print mode:
	// agy --print "<prompt>" --model <model> --add-dir <worktree> --dangerously-skip-permissions
	// The prompt is the value of --print, not a positional argument.
	args := []string{
		"--print", prompt,
		"--model", a.entry.ModelID,
		"--add-dir", a.worktree,
		"--dangerously-skip-permissions",
	}

	cmd := a.cmdFactory(ctx, "agy", args...)
	cmd.Dir = a.worktree

	// Subscription mode: inherit the full process environment so agy can access
	// its keyring at ~/.antigravity (set by Google Sign-In auth).
	base := cmd.Env
	if base == nil {
		base = os.Environ()
	}
	cmd.Env = base

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return supervisor.Result{OK: false}, fmt.Errorf(
			"executor: antigravity CLI failed: %w: %s",
			err,
			sanitizeAntigravityOutput(stdout.String(), stderr.String()),
		)
	}

	// Resolve the branch name: prefer agy's BRANCH: line, else fall back to task/<id>.
	branch := extractAntigravityBranch(stdout.String())
	if branch == "" {
		branch = "task/" + task.ID
	}

	// Check if there are any changes to commit. Git add -A succeeds even on no changes,
	// so we verify by attempting to commit and detecting the "nothing to commit" error.
	if err := a.commitWorktree(branch, task.ID); err != nil {
		// If the commit fails due to no changes, return a descriptive error.
		if errors.Is(err, errWorktreeNoChanges) {
			return supervisor.Result{OK: false}, ErrAntigravityNoChanges
		}
		return supervisor.Result{OK: false}, err
	}

	return supervisor.Result{Branch: branch, OK: true}, nil
}

// buildAntigravityPrompt constructs the task prompt passed to the Antigravity CLI.
func buildAntigravityPrompt(task supervisor.Task, worktree string) string {
	prompt := fmt.Sprintf(`You are running inside agent-builder as the Antigravity CLI executor.

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

// extractAntigravityBranch parses the branch name from Antigravity CLI stdout.
// The agy CLI is expected to write "BRANCH: <branch-name>" as the last output line.
func extractAntigravityBranch(stdout string) string {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "BRANCH:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "BRANCH:"))
		}
	}
	return ""
}

// sanitizeAntigravityOutput trims and returns the combined subprocess output.
// Subscription mode has no API key to redact (agy uses ~/.antigravity keyring),
// so sanitization is a safe no-op.
func sanitizeAntigravityOutput(stdout, stderr string) string {
	output := strings.TrimSpace(strings.Join([]string{stdout, stderr}, "\n"))
	if output == "" {
		return "no output"
	}
	return output
}

// errWorktreeNoChanges is a sentinel error indicating that the worktree had no
// changes to commit (git reported "nothing to commit").
var errWorktreeNoChanges = errors.New("executor: worktree has no changes to commit")

// commitWorktree creates (or resets) the produced branch at the worktree's current
// state and commits all changes onto it. The executor owns this step to ensure the
// published branch deterministically contains agy's edits. Identity is set per-command
// so the commit succeeds even on a repo without a configured user.
// If the worktree has no changes, it returns errWorktreeNoChanges (not a fatal error,
// but a signal that the run produced no work).
func (a *AntigravityCLI) commitWorktree(branch, taskID string) error {
	worktree := a.worktree

	// Check for changes first (git add -A, then git status).
	// This lets us detect "no changes" before creating the branch.
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = worktree
	if out, err := cmd.CombinedOutput(); err != nil {
		outStr := strings.TrimSpace(string(out))
		return fmt.Errorf("commit worktree (git add): %w: %s", err, outStr)
	}

	// Check if there are staged changes via git status.
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = worktree
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(out))
		return fmt.Errorf("commit worktree (git status): %w: %s", err, outStr)
	}

	// If there are no staged changes, return early without creating a branch.
	if strings.TrimSpace(string(out)) == "" {
		return errWorktreeNoChanges
	}

	// Now create the branch and commit.
	steps := [][]string{
		{"checkout", "-B", branch},
		{
			"-c", "user.name=agent-builder",
			"-c", "user.email=agent-builder@local",
			"commit", "-m", fmt.Sprintf("agent-builder: complete task %s", taskID),
		},
	}
	for _, args := range steps {
		cmd := exec.Command("git", args...)
		cmd.Dir = worktree
		out, err := cmd.CombinedOutput()
		if err != nil {
			outStr := strings.TrimSpace(string(out))
			return fmt.Errorf("commit worktree (git %s): %w: %s", strings.Join(args, " "), err, outStr)
		}
	}
	return nil
}
