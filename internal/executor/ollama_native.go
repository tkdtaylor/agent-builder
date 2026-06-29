// Package executor contains concrete implementations of the supervisor.Executor seam.
package executor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/executor/ollamaclient"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// Chatter is the seam the loop uses to call the Ollama API.
// Implemented by *ollamaclient.Client in production; stub in tests.
type Chatter interface {
	Chat(ctx context.Context, req ollamaclient.ChatRequest) (ollamaclient.ChatResponse, error)
}

// ToolDispatcher is the seam the loop uses to execute tool calls.
// Implemented by *ollamatoolset.ToolSet in production; stub in tests.
type ToolDispatcher interface {
	Dispatch(toolName string, argsJSON string) (string, error)
	ToolSchemas() []ollamaclient.Tool
	ExtractBranch() (string, bool) // reads the reserved branch file
}

// OllamaNativeConfig configures the native executor.
type OllamaNativeConfig struct {
	Endpoint      string // Ollama base URL, e.g. "http://localhost:11434"
	Model         string // model ID, e.g. "qwen3:8b"
	MaxIterations int    // hard cap; 0 uses default (30)
	Worktree      string // absolute path to the task worktree
}

// OllamaNative implements supervisor.Executor for native Ollama inference.
type OllamaNative struct {
	chatter       Chatter
	toolDispatcher ToolDispatcher
	cfg           OllamaNativeConfig
}

// NewOllamaNative creates a new OllamaNative executor using ollamaclient.NewClient.
func NewOllamaNative(cfg OllamaNativeConfig, toolset ToolDispatcher) (*OllamaNative, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("ollama_native: blank endpoint")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("ollama_native: blank model")
	}
	if strings.TrimSpace(cfg.Worktree) == "" {
		return nil, fmt.Errorf("ollama_native: blank worktree")
	}
	if toolset == nil {
		return nil, fmt.Errorf("ollama_native: nil tool dispatcher")
	}

	chatter, err := ollamaclient.NewClient(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("ollama_native: create client: %w", err)
	}

	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 30
	}
	cfg.MaxIterations = maxIter

	return &OllamaNative{
		chatter:        chatter,
		toolDispatcher: toolset,
		cfg:            cfg,
	}, nil
}

// newOllamaNativeWithChatter is an unexported test helper to inject a custom Chatter.
func newOllamaNativeWithChatter(cfg OllamaNativeConfig, chatter Chatter, toolset ToolDispatcher) (*OllamaNative, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("ollama_native: blank endpoint")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("ollama_native: blank model")
	}
	if strings.TrimSpace(cfg.Worktree) == "" {
		return nil, fmt.Errorf("ollama_native: blank worktree")
	}
	if chatter == nil {
		return nil, fmt.Errorf("ollama_native: nil chatter")
	}
	if toolset == nil {
		return nil, fmt.Errorf("ollama_native: nil tool dispatcher")
	}

	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 30
	}
	cfg.MaxIterations = maxIter

	return &OllamaNative{
		chatter:        chatter,
		toolDispatcher: toolset,
		cfg:            cfg,
	}, nil
}

// Run implements supervisor.Executor. It drives the in-process agentic loop:
// initial prompt → Chat → tool_calls? → dispatch to tool set → append result →
// repeat until no tool_calls OR hard iteration cap → extract branch → Result{Branch, OK}
func (o *OllamaNative) Run(t supervisor.Task) (supervisor.Result, error) {
	// Check for context cancellation early
	ctx := context.Background() // TODO: context should come from supervisor

	// Initialize messages with the initial prompt
	messages := []ollamaclient.Message{
		{
			Role: "user",
			Content: fmt.Sprintf(
				"Task ID: %s\nRepo: %s\nSpec: %s\n\nYou are an autonomous coding agent. Use the provided tools to complete this task.",
				t.ID, t.Repo, t.Spec,
			),
		},
	}

	// Prepare tools from the tool dispatcher
	tools := o.toolDispatcher.ToolSchemas()

	maxIterations := o.cfg.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 30
	}

	// Main agentic loop
	for iteration := 0; iteration < maxIterations; iteration++ {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return supervisor.Result{}, ctx.Err()
		default:
		}

		// Build the chat request
		req := ollamaclient.ChatRequest{
			Model:    o.cfg.Model,
			Messages: messages,
			Tools:    tools,
			Stream:   false,
		}

		// Call the Ollama API
		resp, err := o.chatter.Chat(ctx, req)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return supervisor.Result{}, err
			}
			return supervisor.Result{}, fmt.Errorf("chat call %d: %w", iteration+1, err)
		}

		// Append the assistant's response to messages
		messages = append(messages, resp.Message)

		// If there are no tool calls, the loop terminates
		if len(resp.Message.ToolCalls) == 0 {
			// Terminal response reached
			branch, ok := o.toolDispatcher.ExtractBranch()
			if !ok {
				// If extraction fails, return a result with empty branch but OK=false
				return supervisor.Result{OK: false}, nil
			}
			// Capture the model's worktree edits onto the produced branch. The agentic
			// loop (not the model) owns this commit so the published branch deterministically
			// contains the work — finish_branch only records the name. Without this the
			// branch is an empty label and the gate would verify uncommitted files.
			if err := o.commitWorktree(branch, t.ID); err != nil {
				return supervisor.Result{OK: false}, err
			}
			return supervisor.Result{Branch: branch, OK: true}, nil
		}

		// Dispatch each tool call
		for _, tc := range resp.Message.ToolCalls {
			toolName := tc.Function.Name
			// Arguments is json.RawMessage (Ollama returns an object); forward the
			// raw object JSON as a string for the dispatcher to unmarshal.
			argsJSON := string(tc.Function.Arguments)

			// Call the tool dispatcher
			result, err := o.toolDispatcher.Dispatch(toolName, argsJSON)
			if err != nil {
				// Append error as a tool response
				messages = append(messages, ollamaclient.Message{
					Role:    "tool",
					Name:    toolName,
					Content: fmt.Sprintf("error: %v", err),
				})
			} else {
				// Append the tool result
				messages = append(messages, ollamaclient.Message{
					Role:    "tool",
					Name:    toolName,
					Content: result,
				})
			}
		}
	}

	// Hard cap reached, return escalation signal
	return supervisor.Result{OK: false}, nil
}

// commitWorktree creates (or resets) the produced branch at the current worktree
// state and commits all changes onto it, so the branch handed to the publisher
// actually contains the model's edits. The loop owns this step because the model's
// finish_branch tool only records a branch name. Identity is set per-command so the
// commit succeeds even on a repo without a configured user.
func (o *OllamaNative) commitWorktree(branch, taskID string) error {
	worktree := o.cfg.Worktree
	steps := [][]string{
		{"checkout", "-B", branch},
		{"add", "-A"},
		{
			"-c", "user.name=agent-builder",
			"-c", "user.email=agent-builder@local",
			"commit", "-m", fmt.Sprintf("agent-builder: complete task %s", taskID),
		},
	}
	for _, args := range steps {
		cmd := exec.Command("git", args...)
		cmd.Dir = worktree
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("commit worktree (git %s): %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Compile-time assertion that OllamaNative implements supervisor.Executor
var _ supervisor.Executor = (*OllamaNative)(nil)
