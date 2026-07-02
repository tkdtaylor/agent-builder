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

	"github.com/tkdtaylor/agent-builder/internal/executorharness"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/secrets"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

const (
	// ClaudeCLIAuthEnv is the independently revocable credential passed to Claude Code.
	ClaudeCLIAuthEnv = "ANTHROPIC_API_KEY"
	// ClaudeCLIOAuthEnv is the subscription OAuth token alternative to ClaudeCLIAuthEnv.
	ClaudeCLIOAuthEnv = "CLAUDE_CODE_OAUTH_TOKEN"
	// ClaudeCLIAuthTokenEnv is the gateway/proxy bearer-token env var passed to Claude Code CLI.
	// When ANTHROPIC_BASE_URL is set (local translation-proxy mode), this var is set to the
	// placeholder and passed straight through to the custom endpoint without validation.
	// The CLI does not validate this token as a real Anthropic credential (unlike ClaudeCLIAuthEnv).
	ClaudeCLIAuthTokenEnv = "ANTHROPIC_AUTH_TOKEN"
	// ClaudeCLIBaseURLEnv redirects the Claude Code CLI to a custom endpoint.
	// For local entries, this is set to the translation-proxy URL (e.g. http://localhost:8080).
	// The translation proxy presents an Anthropic-compatible endpoint over a local OpenAI-API
	// inference server (the LiteLLM / claude-code-router pattern — see internal/registry for
	// the named TranslationProxySeam constant). When non-empty, no cloud auth is injected.
	ClaudeCLIBaseURLEnv = "ANTHROPIC_BASE_URL"

	claudeCLIHistoryEnv = "CLAUDE_CODE_SKIP_PROMPT_HISTORY"

	// LocalProxyAuthPlaceholder is a fixed sentinel injected as ANTHROPIC_AUTH_TOKEN for local
	// proxy entries. It is passed through to the translation proxy at the custom ANTHROPIC_BASE_URL
	// and is not validated by the Claude Code CLI as a real Anthropic credential.
	// The translation proxy ignores the token value; its presence is sufficient for the CLI to
	// proceed with requests to the custom endpoint. This placeholder MUST NOT be derived from
	// the operator's real authToken or oauthToken.
	LocalProxyAuthPlaceholder = "local-proxy-no-auth"
)

var (
	ErrBlankCLIPath                     = errors.New("executor: blank Claude CLI path")
	ErrBlankWorktree                    = errors.New("executor: blank worktree")
	ErrMissingClaudeToken               = errors.New("executor: missing ANTHROPIC_API_KEY")
	ErrMissingClaudeCredential          = errors.New("executor: missing both ANTHROPIC_API_KEY and CLAUDE_CODE_OAUTH_TOKEN")
	ErrBlankTaskID                      = errors.New("executor: blank task ID")
	ErrBlankTaskSpec                    = errors.New("executor: blank task spec")
	ErrMissingBranch                    = errors.New("executor: Claude CLI did not write produced branch")
	ErrUnsupportedClaudeIngestionPolicy = errors.New("executor: unsupported Claude ingestion policy")
	ErrMissingClaudeIngestionHarness    = errors.New("executor: reviewed Claude ingestion policy requires harness")
	ErrClaudeIngestionDisabled          = errors.New("executor: Claude web/tool capability disabled")
)

// ClaudeIngestionPolicy declares how Claude-facing web/tool routes are handled.
type ClaudeIngestionPolicy string

const (
	// ClaudeIngestionDisabled denies Claude-facing web/tool events before use.
	ClaudeIngestionDisabled ClaudeIngestionPolicy = "disabled"
	// ClaudeIngestionReviewed routes Claude-facing web/tool events through the harness.
	ClaudeIngestionReviewed ClaudeIngestionPolicy = "reviewed"
)

// claudeCommandCreator is a factory function for exec.Cmd. Tests override this to inject
// a stub subprocess without needing a real claude binary on PATH.
type claudeCommandCreator func(ctx context.Context, name string, args ...string) *exec.Cmd

// ClaudeCLIConfig configures the Claude Code CLI subprocess executor.
type ClaudeCLIConfig struct {
	CLIPath          string
	Worktree         string
	AuthToken        string
	OAuthToken       string
	Model            string // model id passed as --model; empty = CLI default (ADR 061)
	BaseURL          string // translation-proxy URL for local entries; empty for cloud entries
	IngestionPolicy  ClaudeIngestionPolicy
	IngestionHarness *executorharness.Harness
}

// ClaudeCLI drives Claude Code in non-interactive mode against one target worktree.
type ClaudeCLI struct {
	cliPath          string
	worktree         string
	authToken        string
	oauthToken       string
	model            string // model id passed as --model; empty = CLI default (ADR 061)
	baseURL          string // translation-proxy URL for local entries; empty = cloud mode
	ingestionPolicy  ClaudeIngestionPolicy
	ingestionHarness *executorharness.Harness
	cmdFactory       claudeCommandCreator
}

// NewClaudeCLI constructs a Claude Code CLI executor with an explicit token.
func NewClaudeCLI(config ClaudeCLIConfig) *ClaudeCLI {
	return &ClaudeCLI{
		cliPath:          strings.TrimSpace(config.CLIPath),
		worktree:         strings.TrimSpace(config.Worktree),
		authToken:        config.AuthToken,
		oauthToken:       config.OAuthToken,
		model:            strings.TrimSpace(config.Model),
		baseURL:          strings.TrimSpace(config.BaseURL),
		ingestionPolicy:  normalizeClaudeIngestionPolicy(config.IngestionPolicy),
		ingestionHarness: config.IngestionHarness,
		cmdFactory:       exec.CommandContext,
	}
}

// NewClaudeCLIFromEnv constructs a Claude Code CLI executor using ANTHROPIC_API_KEY
// or CLAUDE_CODE_OAUTH_TOKEN from the process environment. It reads no host-home credential files.
func NewClaudeCLIFromEnv(worktree string) *ClaudeCLI {
	return NewClaudeCLIFromSecretSource(worktree, secrets.NewEnvSecretSource())
}

// NewClaudeCLIFromSecretSource constructs a Claude Code CLI executor using
// the supplied SecretSource for token retrieval. This is the injection seam
// used by tests (via a fake) and will be used by task 066 for vault wiring.
func NewClaudeCLIFromSecretSource(worktree string, src secrets.SecretSource) *ClaudeCLI {
	authToken, oauthToken := src.ProviderToken()
	return NewClaudeCLI(ClaudeCLIConfig{
		CLIPath:    "claude",
		Worktree:   worktree,
		AuthToken:  authToken,
		OAuthToken: oauthToken,
	})
}

// NewClaudeCLIFromEntry constructs a ClaudeCLI adapter from a registry.RegistryEntry and
// a SecretSource. The worktree parameter is the path on disk where the CLI will operate.
//
// For cloud entries (entry.SecretRef != ""), the secret is resolved via
// secretSource.ProviderToken() — no per-entry named resolution is applied here because
// ClaudeCLI pre-dates the per-entry secret pattern; future tasks may refine this.
//
// For local entries (entry.SecretRef == ""), no cloud auth is injected. Instead,
// entry.Endpoint is set as ANTHROPIC_BASE_URL in the subprocess env so the Claude CLI
// routes its requests through the translation proxy at that URL.
func NewClaudeCLIFromEntry(entry registry.RegistryEntry, secretSource secrets.SecretSource, worktree string) *ClaudeCLI {
	if entry.SecretRef == "" {
		// Local entry: no cloud auth; redirect to translation proxy.
		return NewClaudeCLI(ClaudeCLIConfig{
			CLIPath:  "claude",
			Worktree: worktree,
			Model:    entry.ModelID,
			BaseURL:  entry.Endpoint,
		})
	}
	// Cloud entry: resolve credentials from the secret source.
	authToken, oauthToken := secretSource.ProviderToken()
	return NewClaudeCLI(ClaudeCLIConfig{
		CLIPath:    "claude",
		Worktree:   worktree,
		AuthToken:  authToken,
		OAuthToken: oauthToken,
		Model:      entry.ModelID,
	})
}

// ParseClaudeIngestionPolicy validates a text policy name from configuration.
func ParseClaudeIngestionPolicy(raw string) (ClaudeIngestionPolicy, error) {
	switch policy := ClaudeIngestionPolicy(strings.TrimSpace(raw)); policy {
	case ClaudeIngestionDisabled, ClaudeIngestionReviewed:
		return policy, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedClaudeIngestionPolicy, raw)
	}
}

// IngestionPolicy returns the effective Claude web/tool policy.
func (e *ClaudeCLI) IngestionPolicy() ClaudeIngestionPolicy {
	return e.ingestionPolicy
}

// HandleWebContent applies the configured Claude web-ingestion policy.
func (e *ClaudeCLI) HandleWebContent(ctx context.Context, event executorharness.WebContentEvent, continuation executorharness.ContentContinuation) executorharness.ContentResult {
	if err := e.validateIngestionPolicy(); err != nil {
		return executorharness.ContentResult{Err: err}
	}
	if e.ingestionPolicy == ClaudeIngestionDisabled {
		return executorharness.ContentResult{Err: ErrClaudeIngestionDisabled}
	}
	return e.ingestionHarness.HandleWebContent(ctx, event, continuation)
}

// HandleToolCall applies the configured Claude tool-call policy.
func (e *ClaudeCLI) HandleToolCall(ctx context.Context, event executorharness.ToolCallEvent, toolExecutor executorharness.ToolExecutor) executorharness.ToolCallResult {
	if err := e.validateIngestionPolicy(); err != nil {
		return executorharness.ToolCallResult{Err: err}
	}
	if e.ingestionPolicy == ClaudeIngestionDisabled {
		return executorharness.ToolCallResult{Err: ErrClaudeIngestionDisabled}
	}
	return e.ingestionHarness.HandleToolCall(ctx, event, toolExecutor)
}

// Run invokes the Claude Code CLI subprocess and returns the branch it reports.
// It forwards the supervisor-threaded ctx (task 155) into RunContext so a
// caller cancellation reaches the in-flight subprocess via cmd.Cancel.
func (e *ClaudeCLI) Run(ctx context.Context, task supervisor.Task) (supervisor.Result, error) {
	return e.RunContext(ctx, task)
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

	args := []string{"-p", prompt}
	if e.model != "" {
		args = append(args, "--model", e.model)
	}
	cmd := e.cmdFactory(ctx, e.cliPath, args...)
	cmd.Dir = e.worktree
	cmd.Env = claudeEnv(os.Environ(), e.authToken, e.oauthToken, e.baseURL, runDir)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return supervisor.Result{OK: false}, fmt.Errorf("executor: Claude CLI %q failed: %w: %s", e.cliPath, err, sanitizeCLIOutput(stdout.String(), stderr.String(), e.authToken, e.oauthToken))
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
	// Local entries (baseURL non-empty, SecretRef == "") require no cloud auth.
	// Cloud entries require at least one credential.
	if e.baseURL == "" {
		if strings.TrimSpace(e.oauthToken) == "" && strings.TrimSpace(e.authToken) == "" {
			return ErrMissingClaudeCredential
		}
	}
	if strings.TrimSpace(task.ID) == "" {
		return ErrBlankTaskID
	}
	if strings.TrimSpace(task.Spec) == "" {
		return ErrBlankTaskSpec
	}
	if err := e.validateIngestionPolicy(); err != nil {
		return err
	}
	return nil
}

func normalizeClaudeIngestionPolicy(policy ClaudeIngestionPolicy) ClaudeIngestionPolicy {
	if strings.TrimSpace(string(policy)) == "" {
		return ClaudeIngestionDisabled
	}
	return ClaudeIngestionPolicy(strings.TrimSpace(string(policy)))
}

func (e *ClaudeCLI) validateIngestionPolicy() error {
	switch e.ingestionPolicy {
	case ClaudeIngestionDisabled:
		return nil
	case ClaudeIngestionReviewed:
		if e.ingestionHarness == nil {
			return ErrMissingClaudeIngestionHarness
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedClaudeIngestionPolicy, e.ingestionPolicy)
	}
}

func buildClaudePrompt(task supervisor.Task, worktree, branchPath string) string {
	prompt := fmt.Sprintf(`You are running inside agent-builder as the Claude Code CLI executor.

Task ID: %s
Repo: %s
Task spec: %s
Worktree: %s

Read the task spec, implement the requested change in this worktree, run the relevant verification, and leave the produced git branch checked out.
When finished, write only the produced branch name to this file:
%s
`, task.ID, task.Repo, task.Spec, worktree, branchPath)

	if task.PriorFailure != "" {
		prompt += fmt.Sprintf("\nYour previous attempt failed the verification gate.\n\n%s\n", task.PriorFailure)
	}

	return prompt
}

// claudeEnv builds the subprocess environment. When baseURL is non-empty (local entry),
// it sets ANTHROPIC_BASE_URL, injects LocalProxyAuthPlaceholder as ANTHROPIC_AUTH_TOKEN
// (the gateway bearer-token var, not validated as a real credential), and omits
// ANTHROPIC_API_KEY, CLAUDE_CODE_OAUTH_TOKEN, and the operator's real credentials.
// When baseURL is empty (cloud entry), it injects exactly one cloud credential (OAuth
// preferred over API key when both are set), and no placeholder.
func claudeEnv(base []string, authToken, oauthToken, baseURL, tempHome string) []string {
	env := make([]string, 0, len(base)+5)
	for _, entry := range base {
		switch {
		case strings.HasPrefix(entry, ClaudeCLIAuthEnv+"="):
			continue
		case strings.HasPrefix(entry, ClaudeCLIOAuthEnv+"="):
			continue
		case strings.HasPrefix(entry, ClaudeCLIAuthTokenEnv+"="):
			continue
		case strings.HasPrefix(entry, ClaudeCLIBaseURLEnv+"="):
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

	if baseURL != "" {
		// Local mode: point the CLI at the translation proxy; inject placeholder sentinel as
		// ANTHROPIC_AUTH_TOKEN (the gateway bearer-token var, not validated by the CLI).
		// The proxy ignores the token value; its presence is sufficient for the CLI to
		// proceed with requests to the custom ANTHROPIC_BASE_URL.
		env = append(env, ClaudeCLIBaseURLEnv+"="+baseURL)
		env = append(env, ClaudeCLIAuthTokenEnv+"="+LocalProxyAuthPlaceholder)
	} else {
		// Cloud mode: OAuth token preferred over API key when both present (ADR 033).
		if strings.TrimSpace(oauthToken) != "" {
			env = append(env, ClaudeCLIOAuthEnv+"="+oauthToken)
		} else if strings.TrimSpace(authToken) != "" {
			env = append(env, ClaudeCLIAuthEnv+"="+authToken)
		}
	}

	env = append(env,
		"HOME="+filepath.Join(tempHome, "home"),
		"XDG_CONFIG_HOME="+filepath.Join(tempHome, "xdg-config"),
		"XDG_CACHE_HOME="+filepath.Join(tempHome, "xdg-cache"),
		claudeCLIHistoryEnv+"=1",
	)

	return env
}

func sanitizeCLIOutput(stdout, stderr, authToken, oauthToken string) string {
	output := strings.TrimSpace(strings.Join([]string{stdout, stderr}, "\n"))
	if output == "" {
		return "no output"
	}
	if authToken != "" {
		output = strings.ReplaceAll(output, authToken, "[REDACTED]")
	}
	if oauthToken != "" {
		output = strings.ReplaceAll(output, oauthToken, "[REDACTED]")
	}
	return output
}
