// Package executor contains concrete implementations of the supervisor.Executor seam.
// This file adds the non-agentic Completer seam and its ollama-native concrete.
// See ADR 053 §1/§2 for the design rationale.
package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/executor/ollamaclient"
	"github.com/tkdtaylor/agent-builder/internal/registry"
)

// ErrSingleShotUnsupported is the typed sentinel returned by CompleterForEntry when
// the entry's harness does not yet support non-agentic single-shot completion.
// Callers can test for it with errors.Is; the error message names the offending harness.
var ErrSingleShotUnsupported = errors.New("single-shot completion not yet supported")

// Completer sends ONE prompt to the model behind a registry entry and returns the
// raw text — the non-agentic counterpart to supervisor.Executor.Run.
// No worktree, no tools, no verification gate, no branch.
type Completer interface {
	Complete(ctx context.Context, entry registry.RegistryEntry, prompt string) (string, error)
}

// ollamaCompleter is the ollama-native implementation of Completer.
// It wraps the Chatter seam with a single non-streaming, tool-free round-trip.
type ollamaCompleter struct {
	chatter Chatter
}

// Compile-time assertion: ollamaCompleter satisfies the Completer interface.
var _ Completer = (*ollamaCompleter)(nil)

// Complete implements Completer. It sends a single user message to the ollama model
// identified by entry and returns the raw response text.
// The caller's context is threaded into Chat so a hung model cannot wedge the caller.
func (c *ollamaCompleter) Complete(ctx context.Context, entry registry.RegistryEntry, prompt string) (string, error) {
	req := ollamaclient.ChatRequest{
		Model: entry.ModelID,
		Messages: []ollamaclient.Message{
			{
				Role:    "user",
				Content: prompt,
			},
		},
		Tools:  nil,
		Stream: false,
	}

	resp, err := c.chatter.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("completer: chat: %w", err)
	}

	return resp.Message.Content, nil
}

// CompleterForEntry returns the single-shot Completer for the entry's harness.
// It dispatches on entry.Harness:
//   - HarnessOllamaNative → the ollama-native completer.
//   - HarnessClaudeCLI / HarnessCodexCLI / HarnessGeminiCLI → fail-closed with
//     a typed ErrSingleShotUnsupported error (never silently wrong — ADR 053 §2).
//   - unknown harness → fail-closed with ErrSingleShotUnsupported.
func CompleterForEntry(entry registry.RegistryEntry) (Completer, error) {
	switch entry.Harness {
	case registry.HarnessOllamaNative:
		if strings.TrimSpace(entry.Endpoint) == "" {
			return nil, fmt.Errorf("completer: ollama entry %q: blank endpoint", entry.ID)
		}
		if strings.TrimSpace(entry.ModelID) == "" {
			return nil, fmt.Errorf("completer: ollama entry %q: blank model ID", entry.ID)
		}
		client, err := ollamaclient.NewClient(entry.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("completer: create ollama client: %w", err)
		}
		return &ollamaCompleter{chatter: client}, nil

	case registry.HarnessClaudeCLI:
		return nil, fmt.Errorf("harness %q single-shot completion not yet supported: %w",
			entry.Harness, ErrSingleShotUnsupported)

	case registry.HarnessCodexCLI:
		return nil, fmt.Errorf("harness %q single-shot completion not yet supported: %w",
			entry.Harness, ErrSingleShotUnsupported)

	case registry.HarnessGeminiCLI:
		return nil, fmt.Errorf("harness %q single-shot completion not yet supported: %w",
			entry.Harness, ErrSingleShotUnsupported)

	default:
		return nil, fmt.Errorf("harness %q single-shot completion not yet supported: %w",
			entry.Harness, ErrSingleShotUnsupported)
	}
}
