package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/registry"
)

// testAgyCompleterEntry returns an Antigravity-harness registry entry (subscription mode).
func testAgyCompleterEntry(model string) registry.RegistryEntry {
	return registry.RegistryEntry{
		ID:        "antigravity-test",
		Harness:   registry.HarnessAntigravityCLI,
		ModelID:   model,
		SecretRef: "", // subscription/OAuth
	}
}

// stubAgyCompleterFactory re-invokes the test binary as a stub subprocess and captures
// the real (name, args) plus the created *exec.Cmd. Because the agy completer inherits
// the command's env (rather than overwriting it), GO_WANT_HELPER_PROCESS survives.
func stubAgyCompleterFactory(t *testing.T, stdout string, exitCode int, cap *capturedCmd) antigravityCommandCreator {
	t.Helper()
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if cap != nil {
			cap.setAgyCommand(name, args)
		}
		cmd := exec.CommandContext(ctx, os.Args[0])
		cmd.Env = []string{
			"CODEX_HELPER_STDOUT=" + stdout,
			fmt.Sprintf("CODEX_HELPER_EXIT=%d", exitCode),
			"GO_WANT_HELPER_PROCESS=1",
		}
		if cap != nil {
			cap.set(cmd)
		}
		return cmd
	}
}

// TC-136-01: CompleterForEntry returns a non-nil completer for HarnessAntigravityCLI.
func TestCompleterForEntryAntigravityReturnsCompleter(t *testing.T) {
	c, err := CompleterForEntry(testAgyCompleterEntry("Gemini 3.1 Pro (High)"))
	if err != nil {
		t.Fatalf("CompleterForEntry(agy) error = %v, want nil", err)
	}
	if c == nil {
		t.Fatal("CompleterForEntry(agy) returned nil completer")
	}
	if errors.Is(err, ErrSingleShotUnsupported) {
		t.Fatal("agy single-shot should be supported, got ErrSingleShotUnsupported")
	}
}

// TC-136-02: agy completer runs `agy --print <prompt> --model <model>`, returns trimmed
// stdout, and does NOT pass the agentic-mode flags.
func TestAgyCompleterRunsPrintModeAndReturnsStdout(t *testing.T) {
	cap := &capturedCmd{}
	comp := newAntigravityCompleter(testAgyCompleterEntry("Gemini 3.1 Pro (High)"))
	comp.cmdFactory = stubAgyCompleterFactory(t, "Paris\n", 0, cap)

	got, err := comp.Complete(context.Background(), registry.RegistryEntry{}, "What is the capital of France?")
	if err != nil {
		t.Fatalf("Complete error = %v", err)
	}
	if got != "Paris" {
		t.Fatalf("Complete = %q, want %q", got, "Paris")
	}

	name, args := cap.getAgyCommand()
	if name != "agy" {
		t.Errorf("command name = %q, want %q", name, "agy")
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--print", "What is the capital of France?", "--model", "Gemini 3.1 Pro (High)"} {
		if !argsContain(args, want) {
			t.Errorf("args %v missing %q", args, want)
		}
	}
	for _, forbidden := range []string{"--add-dir", "--dangerously-skip-permissions"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("single-shot args must not contain %q; got %v", forbidden, args)
		}
	}
}

// TC-136-03: a non-zero agy exit returns a sanitized error; result empty.
func TestAgyCompleterNonZeroExitSanitizedError(t *testing.T) {
	comp := newAntigravityCompleter(testAgyCompleterEntry("Gemini 3.1 Pro (High)"))
	comp.cmdFactory = stubAgyCompleterFactory(t, "model error", 1, &capturedCmd{})

	got, err := comp.Complete(context.Background(), registry.RegistryEntry{}, "hi")
	if err == nil {
		t.Fatal("expected error on non-zero exit")
	}
	if got != "" {
		t.Errorf("result = %q, want empty on error", got)
	}
	if !strings.Contains(err.Error(), "antigravity") {
		t.Errorf("error %q should name antigravity", err.Error())
	}
}

// TC-136-04: an already-cancelled context propagates and yields an empty result.
func TestAgyCompleterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	comp := newAntigravityCompleter(testAgyCompleterEntry("Gemini 3.1 Pro (High)"))
	comp.cmdFactory = stubAgyCompleterFactory(t, "Paris", 0, &capturedCmd{})

	got, err := comp.Complete(ctx, registry.RegistryEntry{}, "hi")
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if got != "" {
		t.Errorf("result = %q, want empty on error", got)
	}
}

// argsContain reports whether want is present verbatim in args.
func argsContain(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
