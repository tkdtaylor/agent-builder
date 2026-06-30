package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/executor"
	"github.com/tkdtaylor/agent-builder/internal/registry"
)

// fakeAskCompleter is an executor.Completer test double recording its inputs.
type fakeAskCompleter struct {
	answer    string
	err       error
	gotEntry  registry.RegistryEntry
	gotPrompt string
}

func (f *fakeAskCompleter) Complete(_ context.Context, entry registry.RegistryEntry, prompt string) (string, error) {
	f.gotEntry = entry
	f.gotPrompt = prompt
	return f.answer, f.err
}

// withCompleterFactory swaps the package-level completerForEntry seam for the duration
// of a test and restores it afterwards.
func withCompleterFactory(t *testing.T, fn func(registry.RegistryEntry) (executor.Completer, error)) {
	t.Helper()
	orig := completerForEntry
	completerForEntry = fn
	t.Cleanup(func() { completerForEntry = orig })
}

// TC-137-01: `ask` is registered and listed in usage; no prompt → ExitUsage.
func TestAskSubcommandRegisteredAndUsage(t *testing.T) {
	var usage bytes.Buffer
	printUsage(&usage)
	if !strings.Contains(usage.String(), "ask") {
		t.Errorf("top-level usage does not list the ask subcommand:\n%s", usage.String())
	}

	var stderr bytes.Buffer
	code := Main(Config{Args: []string{"ask"}, Stdout: &bytes.Buffer{}, Stderr: &stderr})
	if code != ExitUsage {
		t.Errorf("ask with no prompt exit = %d, want ExitUsage (%d)", code, ExitUsage)
	}
	if stderr.Len() == 0 {
		t.Error("ask with no prompt wrote nothing to stderr")
	}
}

// TC-137-02: ask selects the entry, prints the completion, ExitOK; the fake receives the
// joined prompt and the selected entry.
func TestAskPrintsCompletionFromSelectedEntry(t *testing.T) {
	fake := &fakeAskCompleter{answer: "  Paris\n"}
	withCompleterFactory(t, func(_ registry.RegistryEntry) (executor.Completer, error) {
		return fake, nil
	})

	var stdout, stderr bytes.Buffer
	code := Main(Config{
		Args:   []string{"ask", "--entry", defaultCLIClaudeEntryID, "What is the capital of France?"},
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if code != ExitOK {
		t.Fatalf("ask exit = %d (%s), want ExitOK", code, stderr.String())
	}
	if stdout.String() != "Paris\n" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "Paris\n")
	}
	if fake.gotPrompt != "What is the capital of France?" {
		t.Errorf("completer got prompt %q, want the joined question", fake.gotPrompt)
	}
	if fake.gotEntry.ID != defaultCLIClaudeEntryID {
		t.Errorf("completer got entry id %q, want %q", fake.gotEntry.ID, defaultCLIClaudeEntryID)
	}
}

// TC-137-03: an unknown --entry id is a usage error; nothing on stdout.
func TestAskUnknownEntryErrors(t *testing.T) {
	withCompleterFactory(t, func(_ registry.RegistryEntry) (executor.Completer, error) {
		t.Fatal("completerForEntry should not be called for an unknown entry")
		return nil, nil
	})

	var stdout, stderr bytes.Buffer
	code := Main(Config{
		Args:   []string{"ask", "--entry", "nope", "hello"},
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if code == ExitOK {
		t.Error("ask with unknown entry returned ExitOK, want non-OK")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty on error", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Error("expected an error message on stderr")
	}
}

// TC-137-04: a completer-construction error (unsupported harness) surfaces; stdout empty.
func TestAskCompleterErrorSurfaced(t *testing.T) {
	withCompleterFactory(t, func(_ registry.RegistryEntry) (executor.Completer, error) {
		return nil, executor.ErrSingleShotUnsupported
	})

	var stdout, stderr bytes.Buffer
	code := Main(Config{
		Args:   []string{"ask", "--entry", defaultCLIClaudeEntryID, "hello"},
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if code == ExitOK {
		t.Error("ask returned ExitOK despite a completer error, want non-OK")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty on error", stdout.String())
	}
	if !strings.Contains(stderr.String(), "single-shot") {
		t.Errorf("stderr %q should surface the unsupported-harness error", stderr.String())
	}
}
