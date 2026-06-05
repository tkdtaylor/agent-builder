// Package publisher publishes verified executor branches as PR artifacts.
package publisher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

var (
	// ErrBlankBranch means a caller attempted publication without a branch.
	ErrBlankBranch = errors.New("publisher: blank branch")

	// ErrBlankRemote means a caller attempted publication without a remote.
	ErrBlankRemote = errors.New("publisher: blank remote")
)

// Request describes one verified branch that should become a PR artifact.
type Request struct {
	Task     supervisor.Task
	Worktree string
	Branch   string
	Remote   string
}

// Result identifies the published PR artifact.
type Result struct {
	Branch string
	PRURL  string
	PRID   string
}

// Publisher is the fakeable publication seam.
type Publisher interface {
	Publish(context.Context, Request) (Result, error)
}

// GitHubCLIConfig configures the git/gh-backed publisher.
type GitHubCLIConfig struct {
	GitPath     string
	GHPath      string
	Worktree    string
	Remote      string
	GitToken    string
	GitHubToken string
}

// GitHubCLI publishes branches using git push and GitHub CLI PR commands.
type GitHubCLI struct {
	gitPath     string
	ghPath      string
	worktree    string
	remote      string
	gitToken    string
	gitHubToken string
}

// NewGitHubCLI returns a git/gh-backed branch publisher.
func NewGitHubCLI(config GitHubCLIConfig) *GitHubCLI {
	gitPath := strings.TrimSpace(config.GitPath)
	if gitPath == "" {
		gitPath = "git"
	}
	ghPath := strings.TrimSpace(config.GHPath)
	if ghPath == "" {
		ghPath = "gh"
	}
	return &GitHubCLI{
		gitPath:     gitPath,
		ghPath:      ghPath,
		worktree:    strings.TrimSpace(config.Worktree),
		remote:      strings.TrimSpace(config.Remote),
		gitToken:    config.GitToken,
		gitHubToken: config.GitHubToken,
	}
}

// Publish pushes the branch and returns an existing or newly-created PR artifact.
func (p *GitHubCLI) Publish(ctx context.Context, request Request) (Result, error) {
	request.Worktree = firstNonBlank(request.Worktree, p.worktree)
	request.Remote = firstNonBlank(request.Remote, p.remote)
	request.Branch = strings.TrimSpace(request.Branch)
	if request.Branch == "" {
		return Result{}, ErrBlankBranch
	}
	if request.Remote == "" {
		return Result{}, ErrBlankRemote
	}

	if _, err := p.run(ctx, p.gitPath, "push", request.Remote, request.Branch); err != nil {
		return Result{}, fmt.Errorf("publisher: git push branch %s: %w", request.Branch, err)
	}

	if output, err := p.run(ctx, p.ghPath, "pr", "view", "--head", request.Branch, "--json", "url,number", "--jq", ".url"); err == nil {
		return resultFromOutput(request.Branch, output), nil
	}

	output, err := p.run(ctx, p.ghPath, "pr", "create", "--head", request.Branch, "--fill")
	if err != nil {
		return Result{}, fmt.Errorf("publisher: gh pr create for branch %s: %w", request.Branch, err)
	}
	return resultFromOutput(request.Branch, output), nil
}

func (p *GitHubCLI) run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if strings.TrimSpace(p.worktree) != "" {
		cmd.Dir = p.worktree
	}
	cmd.Env = p.commandEnv()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String() + stderr.String()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, Redact(output, p.secrets()))
	}
	return Redact(stdout.String(), p.secrets()), nil
}

func (p *GitHubCLI) commandEnv() []string {
	env := os.Environ()
	if p.gitToken != "" {
		env = append(env, "GIT_TOKEN="+p.gitToken)
	}
	if p.gitHubToken != "" {
		env = append(env, "GH_TOKEN="+p.gitHubToken, "GITHUB_TOKEN="+p.gitHubToken)
	}
	return env
}

func (p *GitHubCLI) secrets() []string {
	return []string{p.gitToken, p.gitHubToken}
}

func resultFromOutput(branch, output string) Result {
	output = strings.TrimSpace(output)
	result := Result{Branch: branch}
	if output == "" {
		return result
	}
	for _, field := range strings.Fields(output) {
		if parsed, err := url.Parse(field); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			result.PRURL = field
			result.PRID = prIDFromURL(field)
			return result
		}
	}
	result.PRID = firstLine(output)
	return result
}

func prIDFromURL(raw string) string {
	re := regexp.MustCompile(`/pull/([0-9]+)(?:$|[?#])`)
	matches := re.FindStringSubmatch(raw)
	if len(matches) == 2 {
		return matches[1]
	}
	return ""
}

func firstLine(output string) string {
	line, _, _ := strings.Cut(output, "\n")
	return strings.TrimSpace(line)
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// Redact replaces configured secret values in externally-visible text.
func Redact(text string, secrets []string) string {
	redacted := text
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		redacted = strings.ReplaceAll(redacted, secret, "[REDACTED]")
	}
	return redacted
}
