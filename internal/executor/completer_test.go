package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/executor/ollamaclient"
	"github.com/tkdtaylor/agent-builder/internal/registry"
)

// --- stub helpers ---

// singleResponseChatter is a stub Chatter that returns a fixed response once.
type singleResponseChatter struct {
	resp     ollamaclient.ChatResponse
	err      error
	called   int
	captured ollamaclient.ChatRequest
}

func (s *singleResponseChatter) Chat(ctx context.Context, req ollamaclient.ChatRequest) (ollamaclient.ChatResponse, error) {
	s.called++
	s.captured = req
	if s.err != nil {
		return ollamaclient.ChatResponse{}, s.err
	}
	return s.resp, nil
}

// contextAwareChatter returns ctx.Err() when the context is already done.
type contextAwareChatter struct {
	baseErr  error
	captured ollamaclient.ChatRequest
}

func (c *contextAwareChatter) Chat(ctx context.Context, req ollamaclient.ChatRequest) (ollamaclient.ChatResponse, error) {
	c.captured = req
	if err := ctx.Err(); err != nil {
		return ollamaclient.ChatResponse{}, err
	}
	if c.baseErr != nil {
		return ollamaclient.ChatResponse{}, c.baseErr
	}
	return ollamaclient.ChatResponse{}, nil
}

// ollamaEntry is a convenience helper for a valid ollama registry entry.
func ollamaEntry() registry.RegistryEntry {
	return registry.RegistryEntry{
		ID:      "local-qwen",
		Harness: registry.HarnessOllamaNative,
		Endpoint: "http://localhost:11434",
		ModelID: "qwen3:8b",
	}
}

// --- TC-109-01: Completer interface exists and ollamaCompleter satisfies it ---

// TestTC10901_CompleterInterfaceCompileAssert verifies that ollamaCompleter satisfies
// the Completer interface at compile time. The var _ assertion in completer.go is the
// primary gate; this test makes the requirement explicit and human-readable.
func TestTC10901_CompleterInterfaceCompileAssert(t *testing.T) {
	// Compile-time assertion (mirrors the one in completer.go).
	var _ Completer = (*ollamaCompleter)(nil)
	t.Log("ollamaCompleter satisfies Completer interface")
}

// --- TC-109-02: ollama completer returns the model's text via a stub Chatter ---

// TestTC10902_OllamaCompleterReturnsCannedContent verifies that Complete returns
// resp.Message.Content verbatim, with nil error, exactly one Chat call.
func TestTC10902_OllamaCompleterReturnsCannedContent(t *testing.T) {
	stub := &singleResponseChatter{
		resp: ollamaclient.ChatResponse{
			Message: ollamaclient.Message{
				Role:    "assistant",
				Content: "coding-agent: do the thing",
			},
		},
	}
	c := &ollamaCompleter{chatter: stub}

	got, err := c.Complete(context.Background(), ollamaEntry(), "decompose this goal")

	if err != nil {
		t.Fatalf("Complete returned unexpected error: %v", err)
	}
	if got != "coding-agent: do the thing" {
		t.Errorf("Complete returned %q; want %q", got, "coding-agent: do the thing")
	}
	if stub.called != 1 {
		t.Errorf("Chat called %d time(s); want exactly 1 (single round-trip)", stub.called)
	}
}

// --- TC-109-03: request carries exactly one user message, no tools, no stream ---

// TestTC10903_RequestShape verifies the ChatRequest sent by the completer is non-agentic:
// one user message with the prompt verbatim, Tools nil, Stream false, Model from entry.
func TestTC10903_RequestShape(t *testing.T) {
	const promptSentinel = "PROMPT-SENTINEL"
	stub := &singleResponseChatter{
		resp: ollamaclient.ChatResponse{
			Message: ollamaclient.Message{Role: "assistant", Content: "ok"},
		},
	}
	c := &ollamaCompleter{chatter: stub}
	entry := ollamaEntry()

	if _, err := c.Complete(context.Background(), entry, promptSentinel); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	req := stub.captured

	if req.Model != entry.ModelID {
		t.Errorf("req.Model = %q; want %q", req.Model, entry.ModelID)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("len(req.Messages) = %d; want 1", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("req.Messages[0].Role = %q; want %q", req.Messages[0].Role, "user")
	}
	if req.Messages[0].Content != promptSentinel {
		t.Errorf("req.Messages[0].Content = %q; want %q", req.Messages[0].Content, promptSentinel)
	}
	if len(req.Messages[0].ToolCalls) != 0 {
		t.Errorf("req.Messages[0].ToolCalls = %v; want empty (non-agentic)", req.Messages[0].ToolCalls)
	}
	if len(req.Tools) != 0 {
		t.Errorf("req.Tools = %v; want nil/empty (no tool schemas)", req.Tools)
	}
	if req.Stream {
		t.Error("req.Stream = true; want false (non-streaming)")
	}
}

// --- TC-109-04: CompleterForEntry returns the ollama completer for HarnessOllamaNative ---

// TestTC10904_CompleterForEntryOllama verifies dispatcher returns a non-nil ollama
// completer for a valid ollama entry and errors on blank endpoint or model.
func TestTC10904_CompleterForEntryOllama(t *testing.T) {
	t.Run("valid ollama entry", func(t *testing.T) {
		c, err := CompleterForEntry(ollamaEntry())
		if err != nil {
			t.Fatalf("CompleterForEntry returned error: %v", err)
		}
		if c == nil {
			t.Fatal("CompleterForEntry returned nil Completer")
		}
		// Type assertion: dynamic type must be *ollamaCompleter
		if _, ok := c.(*ollamaCompleter); !ok {
			t.Errorf("CompleterForEntry returned %T; want *ollamaCompleter", c)
		}
	})

	t.Run("blank endpoint", func(t *testing.T) {
		e := ollamaEntry()
		e.Endpoint = ""
		c, err := CompleterForEntry(e)
		if err == nil {
			t.Error("expected error for blank endpoint, got nil")
		}
		if c != nil {
			t.Error("expected nil Completer for blank endpoint")
		}
	})

	t.Run("blank model", func(t *testing.T) {
		e := ollamaEntry()
		e.ModelID = ""
		c, err := CompleterForEntry(e)
		if err == nil {
			t.Error("expected error for blank model, got nil")
		}
		if c != nil {
			t.Error("expected nil Completer for blank model")
		}
	})
}

// --- TC-109-05: CompleterForEntry fails closed for cloud harnesses + unknown ---

// TestTC10905_CompleterForEntryFailsClosed verifies each cloud harness and an unknown
// harness return nil Completer, non-nil error, errors.Is(err, ErrSingleShotUnsupported),
// and a message naming the harness.
func TestTC10905_CompleterForEntryFailsClosed(t *testing.T) {
	cases := []struct {
		name    string
		harness registry.HarnessDriver
		wantMsg string // substring that must appear in the error message
	}{
		{
			name:    "claude-cli",
			harness: registry.HarnessClaudeCLI,
			wantMsg: "claude-cli",
		},
		{
			name:    "codex-cli",
			harness: registry.HarnessCodexCLI,
			wantMsg: "codex-cli",
		},
		{
			name:    "gemini-cli",
			harness: registry.HarnessGeminiCLI,
			wantMsg: "gemini-cli",
		},
		{
			name:    "unknown-harness",
			harness: registry.HarnessDriver("made-up"),
			wantMsg: "made-up",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := registry.RegistryEntry{
				ID:      "cloud-x",
				Harness: tc.harness,
			}
			c, err := CompleterForEntry(entry)

			if c != nil {
				t.Errorf("CompleterForEntry(%q) returned non-nil Completer; want nil (fail-closed)", tc.harness)
			}
			if err == nil {
				t.Fatalf("CompleterForEntry(%q) returned nil error; want non-nil", tc.harness)
			}
			if !errors.Is(err, ErrSingleShotUnsupported) {
				t.Errorf("errors.Is(err, ErrSingleShotUnsupported) = false; want true. err = %v", err)
			}
			msg := err.Error()
			if !contains(msg, tc.wantMsg) {
				t.Errorf("error message %q does not name harness %q", msg, tc.wantMsg)
			}
			if !contains(msg, "not yet supported") {
				t.Errorf("error message %q does not contain %q", msg, "not yet supported")
			}
		})
	}
}

// contains is a small helper to avoid importing strings in test assertions.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOfSubstring(s, sub) >= 0)
}

func indexOfSubstring(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// --- TC-109-06: Chatter error / cancelled context propagates ---

// TestTC10906_ErrorPropagation verifies that Complete propagates Chatter errors and
// cancelled-context errors; no zero-value text leaks on the error path.
func TestTC10906_ErrorPropagation(t *testing.T) {
	t.Run("chatter error wraps sentinel", func(t *testing.T) {
		sentinel := errors.New("chatter-sentinel-error")
		stub := &singleResponseChatter{err: sentinel}
		c := &ollamaCompleter{chatter: stub}

		got, err := c.Complete(context.Background(), ollamaEntry(), "p")
		if err == nil {
			t.Fatal("Complete returned nil error; want non-nil")
		}
		if !errors.Is(err, sentinel) {
			t.Errorf("errors.Is(err, sentinel) = false; want true. err = %v", err)
		}
		if got != "" {
			t.Errorf("Complete returned text %q on error path; want empty string", got)
		}
	})

	t.Run("cancelled context propagates", func(t *testing.T) {
		// The contextAwareChatter returns ctx.Err() when context is done — mirrors
		// ollamaclient's real cancellation behaviour.
		stub := &contextAwareChatter{}
		c := &ollamaCompleter{chatter: stub}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately before calling Complete

		got, err := c.Complete(ctx, ollamaEntry(), "p")
		if err == nil {
			t.Fatal("Complete returned nil error; want non-nil (context cancelled)")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("errors.Is(err, context.Canceled) = false; want true. err = %v", err)
		}
		if got != "" {
			t.Errorf("Complete returned text %q on cancelled-context path; want empty string", got)
		}
	})
}
