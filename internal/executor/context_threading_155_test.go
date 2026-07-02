package executor

// Task 155: thread context.Context through supervisor.Executor.Run.
//
// TC-155-01: each concrete executor's Run(ctx, task) uses the PASSED-IN ctx
//            (not a freshly built context.Background()), proven by round-tripping
//            a marker value to the subprocess/HTTP-call boundary (cmdFactory for
//            the four CLI executors; the injected Chatter for OllamaNative).
// TC-155-02: a cancelled ctx aborts an in-flight CLI executor's real subprocess
//            promptly via Run(ctx, ...) — proving Run functionally delegates to
//            the existing cmd.Cancel-based termination, not merely type-compat.
// TC-155-08: full-suite regression (no dedicated unit test) — the touched-package
//            race run plus `make check` (lint + test + fitness, incl.
//            fitness-supervisor-isolation and fitness-orchestrator-no-executor)
//            proves the signature change is mechanical with no behavior drift.

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/executor/ollamaclient"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// ctx155Key is a distinguishable context key; a bare context.Background() carries
// no value for it, so a recovered marker proves the passed-in ctx was forwarded.
type ctx155Key struct{}

const ctx155Marker = "marker-155-01"

// markerContext returns a context carrying ctx155Marker under ctx155Key.
func markerContext() context.Context {
	return context.WithValue(context.Background(), ctx155Key{}, ctx155Marker)
}

// recordingCmdFactory records the ctx it is invoked with into *got and returns a
// /bin/true command (exits 0 instantly, reads no env, produces no output).
func recordingCmdFactory(got *context.Context) func(context.Context, string, ...string) *exec.Cmd {
	return func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		*got = ctx
		return exec.CommandContext(ctx, "/bin/true")
	}
}

// ctxRecordingChatter records the ctx of each Chat call and returns a terminal
// response with no tool calls, so OllamaNative.Run finishes in one iteration.
type ctxRecordingChatter struct {
	gotCtx context.Context
}

func (c *ctxRecordingChatter) Chat(ctx context.Context, _ ollamaclient.ChatRequest) (ollamaclient.ChatResponse, error) {
	c.gotCtx = ctx
	return ollamaclient.ChatResponse{
		Message: ollamaclient.Message{Role: "assistant", Content: "done"},
	}, nil
}

// assertMarker fails unless got carries the ctx155Marker under ctx155Key.
func assertMarker(t *testing.T, name string, got context.Context) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: executor did not forward any ctx to the call boundary", name)
	}
	v, ok := got.Value(ctx155Key{}).(string)
	if !ok || v != ctx155Marker {
		t.Fatalf("%s: forwarded ctx.Value(ctx155Key) = %v (ok=%v), want %q — Run built its own context.Background() instead of forwarding the passed-in ctx", name, v, ok, ctx155Marker)
	}
}

// TestTC155_01_ExecutorsForwardPassedContext proves every concrete executor's
// Run forwards the caller-supplied ctx to its subprocess/HTTP-call boundary.
func TestTC155_01_ExecutorsForwardPassedContext(t *testing.T) {
	task := supervisor.Task{ID: "155", Repo: "agent-builder", Spec: "docs/tasks/backlog/155-executor-context-threading.md"}

	t.Run("claude", func(t *testing.T) {
		var got context.Context
		// Local entry (BaseURL set, no cloud auth) so validate passes without a token.
		cli := NewClaudeCLI(ClaudeCLIConfig{
			CLIPath:  "claude",
			Worktree: t.TempDir(),
			BaseURL:  "http://localhost:8080",
		})
		cli.cmdFactory = recordingCmdFactory(&got)
		// Run may return an error (no branch file written by /bin/true); we only
		// assert the ctx reached the cmdFactory boundary.
		_, _ = cli.Run(markerContext(), task)
		assertMarker(t, "claude", got)
	})

	t.Run("codex", func(t *testing.T) {
		var got context.Context
		src := &fakeCodexSecretSource{namedTokens: map[string]string{"codex-key": "sk-codex"}}
		cli := NewCodexCLI(testCodexEntry("codex-key"), src, t.TempDir())
		cli.cmdFactory = recordingCmdFactory(&got)
		_, _ = cli.Run(markerContext(), task)
		assertMarker(t, "codex", got)
	})

	t.Run("gemini", func(t *testing.T) {
		var got context.Context
		// SecretRef "" => subscription mode, no secret resolution before cmdFactory.
		entry := registry.RegistryEntry{ID: "gemini-sub", Harness: registry.HarnessGeminiCLI, ModelID: "gemini-2.0", SecretRef: ""}
		cli := NewGeminiCLI(entry, &fakeCodexSecretSource{}, t.TempDir())
		cli.cmdFactory = recordingCmdFactory(&got)
		_, _ = cli.Run(markerContext(), task)
		assertMarker(t, "gemini", got)
	})

	t.Run("antigravity", func(t *testing.T) {
		var got context.Context
		entry := registry.RegistryEntry{ID: "agy-sub", Harness: registry.HarnessAntigravityCLI, ModelID: "agy-1", SecretRef: ""}
		cli := NewAntigravityCLI(entry, &fakeCodexSecretSource{}, t.TempDir())
		cli.cmdFactory = recordingCmdFactory(&got)
		_, _ = cli.Run(markerContext(), task)
		assertMarker(t, "antigravity", got)
	})

	t.Run("ollama", func(t *testing.T) {
		chatter := &ctxRecordingChatter{}
		cfg := OllamaNativeConfig{Endpoint: "http://localhost:11434", Model: "qwen3:8b", Worktree: t.TempDir()}
		// branchFile "" => ExtractBranch returns false, so Run finishes without a
		// commit (no git repo needed) after the single terminal Chat.
		exec, err := newOllamaNativeWithChatter(cfg, chatter, &stubToolDispatcher{})
		if err != nil {
			t.Fatalf("ollama: construct: %v", err)
		}
		_, _ = exec.Run(markerContext(), task)
		assertMarker(t, "ollama", chatter.gotCtx)
	})
}

// TestTC155_02_CancelledContextAbortsSubprocess proves a cancelled ctx passed to
// Run(ctx, ...) promptly terminates an in-flight CLI subprocess (sleep 30) — the
// existing cmd.Cancel/exec.CommandContext termination now reachable via Run.
func TestTC155_02_CancelledContextAbortsSubprocess(t *testing.T) {
	task := supervisor.Task{ID: "155", Repo: "agent-builder", Spec: "docs/tasks/backlog/155-executor-context-threading.md"}

	// sleepFactory launches a real long-sleeping subprocess bound to ctx; when ctx
	// is cancelled, exec.CommandContext kills it (SIGKILL) and cmd.Run returns.
	sleepFactory := func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "30")
	}

	run := func(t *testing.T, name string, invoke func(ctx context.Context) error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		done := make(chan error, 1)
		start := time.Now()
		go func() { done <- invoke(ctx) }()

		select {
		case err := <-done:
			if err == nil {
				t.Fatalf("%s: Run returned nil error; want a cancellation/killed-subprocess error", name)
			}
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Fatalf("%s: Run took %s to return after ctx cancel; want well under the 30s sleep", name, elapsed)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: Run did not return within 10s of ctx cancel — cancellation did not reach the subprocess", name)
		}
	}

	t.Run("claude", func(t *testing.T) {
		cli := NewClaudeCLI(ClaudeCLIConfig{CLIPath: "claude", Worktree: t.TempDir(), BaseURL: "http://localhost:8080"})
		cli.cmdFactory = sleepFactory
		run(t, "claude", func(ctx context.Context) error {
			_, err := cli.Run(ctx, task)
			return err
		})
	})

	t.Run("codex", func(t *testing.T) {
		src := &fakeCodexSecretSource{namedTokens: map[string]string{"codex-key": "sk-codex"}}
		cli := NewCodexCLI(testCodexEntry("codex-key"), src, t.TempDir())
		cli.cmdFactory = sleepFactory
		run(t, "codex", func(ctx context.Context) error {
			_, err := cli.Run(ctx, task)
			return err
		})
	})
}
