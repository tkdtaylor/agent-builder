// Package executor contains concrete implementations of the supervisor.Executor seam.
package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

const (
	// ClaudeCLIAuthEnv is the independently revocable credential passed to Claude Code.
	ClaudeCLIAuthEnv = "ANTHROPIC_API_KEY"

	claudeCLIHistoryEnv = "CLAUDE_CODE_SKIP_PROMPT_HISTORY"
)

var (
	ErrBlankCLIPath       = errors.New("executor: blank Claude CLI path")
	ErrBlankWorktree      = errors.New("executor: blank worktree")
	ErrMissingClaudeToken = errors.New("executor: missing ANTHROPIC_API_KEY")
	ErrBlankTaskID        = errors.New("executor: blank task ID")
	ErrBlankTaskSpec      = errors.New("executor: blank task spec")
	ErrMissingBranch      = errors.New("executor: Claude CLI did not write produced branch")
)

// ClaudeCLIConfig configures the Claude Code CLI subprocess executor.
type ClaudeCLIConfig struct {
	CLIPath   string
	Worktree  string
	AuthToken string
}

// ClaudeCLI drives Claude Code in non-interactive mode against one target worktree.
type ClaudeCLI struct {
	cliPath   string
	worktree  string
	authToken string
}

// NewClaudeCLI constructs a Claude Code CLI executor with an explicit token.
func NewClaudeCLI(config ClaudeCLIConfig) *ClaudeCLI {
	cliPath := strings.TrimSpace(config.CLIPath)
	if cliPath == "" {
		cliPath = "claude"
	}

	return &ClaudeCLI{
		cliPath:   cliPath,
		worktree:  strings.TrimSpace(config.Worktree),
		authToken: config.AuthToken,
	}
}

// NewClaudeCLIFromEnv constructs a Claude Code CLI executor using ANTHROPIC_API_KEY
// from the process environment. It reads no host-home credential files.
func NewClaudeCLIFromEnv(worktree string) *ClaudeCLI {
	return NewClaudeCLI(ClaudeCLIConfig{
		Worktree:  worktree,
		AuthToken: os.Getenv(ClaudeCLIAuthEnv),
	})
}

// Run invokes the Claude Code CLI subprocess and returns the branch it reports.
func (e *ClaudeCLI) Run(task supervisor.Task) (supervisor.Result, error) {
	return e.RunContext(context.Background(), task)
}

// RunContext invokes the Claude Code CLI subprocess and returns the branch it reports.
func (e *ClaudeCLI) RunContext(ctx context.Context, task supervisor.Task) (supervisor.Result, error) {
	if err := e.validate(task); err != nil {
		return supervisor.Result{}, err
	}

	runDir, err := os.MkdirTemp("", "agent-builder-claude-cli-*")
	if err != nil {
		return supervisor.Result{}, fmt.Errorf("executor: create Claude CLI temp dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(runDir)
	}()

	branchPath := filepath.Join(runDir, "produced-branch.txt")
	prompt := buildClaudePrompt(task, e.worktree, branchPath)

	cmd := exec.CommandContext(ctx, e.cliPath, "-p", prompt)
	cmd.Dir = e.worktree
	cmd.Env = claudeEnv(os.Environ(), e.authToken, runDir)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return supervisor.Result{OK: false}, fmt.Errorf("executor: Claude CLI failed: %w: %s", err, sanitizeCLIOutput(stdout.String(), stderr.String(), e.authToken))
	}

	branchBytes, err := os.ReadFile(branchPath)
	if err != nil {
		return supervisor.Result{OK: false}, fmt.Errorf("%w: %s", ErrMissingBranch, branchPath)
	}
	branch := strings.TrimSpace(string(branchBytes))
	if branch == "" {
		return supervisor.Result{OK: false}, ErrMissingBranch
	}

	return supervisor.Result{Branch: branch, OK: true}, nil
}

func (e *ClaudeCLI) validate(task supervisor.Task) error {
	if strings.TrimSpace(e.cliPath) == "" {
		return ErrBlankCLIPath
	}
	if strings.TrimSpace(e.worktree) == "" {
		return ErrBlankWorktree
	}
	if strings.TrimSpace(e.authToken) == "" {
		return ErrMissingClaudeToken
	}
	if strings.TrimSpace(task.ID) == "" {
		return ErrBlankTaskID
	}
	if strings.TrimSpace(task.Spec) == "" {
		return ErrBlankTaskSpec
	}
	return nil
}

func buildClaudePrompt(task supervisor.Task, worktree, branchPath string) string {
	return fmt.Sprintf(`You are running inside agent-builder as the Claude Code CLI executor.

Task ID: %s
Repo: %s
Task spec: %s
Worktree: %s

Read the task spec, implement the requested change in this worktree, run the relevant verification, and leave the produced git branch checked out.
When finished, write only the produced branch name to this file:
%s
`, task.ID, task.Repo, task.Spec, worktree, branchPath)
}

func claudeEnv(base []string, token, tempHome string) []string {
	env := make([]string, 0, len(base)+5)
	for _, entry := range base {
		switch {
		case strings.HasPrefix(entry, ClaudeCLIAuthEnv+"="):
			continue
		case strings.HasPrefix(entry, "HOME="):
			continue
		case strings.HasPrefix(entry, "XDG_CONFIG_HOME="):
			continue
		case strings.HasPrefix(entry, "XDG_CACHE_HOME="):
			continue
		case strings.HasPrefix(entry, claudeCLIHistoryEnv+"="):
			continue
		default:
			env = append(env, entry)
		}
	}

	env = append(env,
		ClaudeCLIAuthEnv+"="+token,
		"HOME="+filepath.Join(tempHome, "home"),
		"XDG_CONFIG_HOME="+filepath.Join(tempHome, "xdg-config"),
		"XDG_CACHE_HOME="+filepath.Join(tempHome, "xdg-cache"),
		claudeCLIHistoryEnv+"=1",
	)

	return env
}

func sanitizeCLIOutput(stdout, stderr, token string) string {
	output := strings.TrimSpace(strings.Join([]string{stdout, stderr}, "\n"))
	if output == "" {
		return "no output"
	}
	if token != "" {
		output = strings.ReplaceAll(output, token, "[REDACTED]")
	}
	return output
}
