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
	"github.com/tkdtaylor/agent-builder/internal/secrets"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// Note: TestMain, runHelperProcess, and capturedCmd are defined in codex_cli_test.go.
// They serve as shared test infrastructure for the executor package.

// ---- Test doubles for Antigravity ----

// fakeAntigravitySecretSource is a test double for secrets.SecretSource used in Antigravity tests.
type fakeAntigravitySecretSource struct {
	namedTokens map[string]string // ref → token
}

func (f *fakeAntigravitySecretSource) ProviderToken() (string, string) {
	return "", ""
}

func (f *fakeAntigravitySecretSource) PublisherTokens() (string, string) {
	return "", ""
}

func (f *fakeAntigravitySecretSource) NamedProviderToken(ref string) (string, error) {
	tok, ok := f.namedTokens[ref]
	if !ok || tok == "" {
		return "", secrets.ErrSecretNotFound
	}
	return tok, nil
}

// Compile-time assertion: fakeAntigravitySecretSource satisfies secrets.SecretSource.
var _ secrets.SecretSource = (*fakeAntigravitySecretSource)(nil)

// testAntigravityEntry returns a RegistryEntry configured for Antigravity CLI (subscription mode).
func testAntigravityEntry(secretRef string) registry.RegistryEntry {
	return registry.RegistryEntry{
		ID:        "antigravity",
		Harness:   registry.HarnessAntigravityCLI,
		ModelID:   "Claude Opus 4.6 (Thinking)",
		SecretRef: secretRef,
	}
}

// stubAntigravityCommandFactory returns an antigravityCommandCreator that re-invokes the test binary
// as a subprocess (via TestMain/runHelperProcess) with GO_WANT_HELPER_PROCESS=1.
// stdout is the text the helper writes to stdout; exitCode is the subprocess exit code.
// The factory captures the real command (name, args) and the created *exec.Cmd in captureState.
func stubAntigravityCommandFactory(t *testing.T, stdout string, exitCode int, captureState *capturedCmd) antigravityCommandCreator {
	t.Helper()
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// Record the real (name, args) passed by the executor for assertion.
		if captureState != nil {
			captureState.setAgyCommand(name, args)
		}
		// Re-invoke ourselves as a subprocess; TestMain routes to runHelperProcess.
		cmd := exec.CommandContext(ctx, os.Args[0])
		cmd.Env = []string{
			"CODEX_HELPER_STDOUT=" + stdout,
			fmt.Sprintf("CODEX_HELPER_EXIT=%d", exitCode),
			"GO_WANT_HELPER_PROCESS=1",
		}
		if captureState != nil {
			captureState.set(cmd)
		}
		return cmd
	}
}

// ---- TC-133-01: AntigravityCLI satisfies supervisor.Executor at compile time ----

// Compile-time assertion (TC-133-01): *AntigravityCLI implements supervisor.Executor.
var _ supervisor.Executor = (*AntigravityCLI)(nil)

func TestAntigravityCLI_InterfaceSatisfied(t *testing.T) {
	// TC-133-01: NewAntigravityCLI returns a non-nil value satisfying supervisor.Executor.
	// The compile-time var _ assertion above is the primary guard; this test confirms
	// the constructor does not return nil at runtime.
	src := &fakeAntigravitySecretSource{} // subscription mode: no token needed
	entry := testAntigravityEntry("")     // empty SecretRef for subscription

	cli := NewAntigravityCLI(entry, src, "/tmp/worktree")

	if cli == nil {
		t.Fatal("NewAntigravityCLI() returned nil")
	}
}

// ---- TC-133-02: Subscription mode runs headless, secret source NOT consulted ----

func TestAntigravitySubscriptionModeRunsHeadless(t *testing.T) {
	// TC-133-02: subscription entry (SecretRef == "") invokes agy with --print, --model, --add-dir,
	// --dangerously-skip-permissions. Secret source must NOT be called. Result contains the extracted branch.
	const modelID = "Claude Opus 4.6 (Thinking)"
	const expectedBranch = "task/133-test"

	// Secret source that fails if NamedProviderToken is called.
	src := &testAntigravitySecretSourceThatFailsIfCalled{}

	// Subscription entry: empty SecretRef.
	entry := testAntigravityEntry("")
	entry.ModelID = modelID

	worktree := t.TempDir()

	// Stub subprocess: exits 0, outputs the branch marker.
	stubOut := "Running Antigravity...\nBRANCH: " + expectedBranch + "\n"
	capture := &capturedCmd{}
	cli := NewAntigravityCLI(entry, src, worktree)
	cli.cmdFactory = stubAntigravityCommandFactory(t, stubOut, 0, capture)

	task := supervisor.Task{ID: "133", Repo: "agent-builder", Spec: "docs/tasks/backlog/133-antigravity-executor-harness.md"}

	result, err := cli.Run(task)
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("result.OK = false, want true")
	}
	if result.Branch != expectedBranch {
		t.Fatalf("result.Branch = %q, want %q", result.Branch, expectedBranch)
	}

	// TC-133-02: assert the subprocess was invoked with the correct argv and env.
	cmd := capture.get()
	if cmd == nil {
		t.Fatal("subprocess command was not captured")
	}

	// Assert cmd.Dir is the worktree.
	if cmd.Dir != worktree {
		t.Fatalf("cmd.Dir = %q, want %q (the worktree path)", cmd.Dir, worktree)
	}

	// TC-133-02: Hard assert the real agy command name and args.
	cmdName, cmdArgs := capture.getAgyCommand()
	if cmdName != "agy" {
		t.Fatalf("command name = %q, want %q", cmdName, "agy")
	}

	// Helper to find an arg and its following value (for --flag value pairs).
	findArgAndValue := func(args []string, flag string) (bool, string) {
		for i, arg := range args {
			if arg == flag && i+1 < len(args) {
				return true, args[i+1]
			}
		}
		return false, ""
	}

	// Helper to check if arg is in the slice.
	hasArg := func(args []string, arg string) bool {
		for _, a := range args {
			if a == arg {
				return true
			}
		}
		return false
	}

	// Assert --print flag is present with the prompt.
	hasPrint, printVal := findArgAndValue(cmdArgs, "--print")
	if !hasPrint {
		t.Fatalf("--print flag not found in argv: %v", cmdArgs)
	}
	// The prompt should contain the task spec and task ID sections.
	if !strings.Contains(printVal, "Task ID: 133") {
		t.Fatalf("--print value does not contain task ID section: %q", printVal)
	}
	if !strings.Contains(printVal, "Worktree:") {
		t.Fatalf("--print value does not contain worktree section: %q", printVal)
	}

	// Assert --model flag is present with the expected model.
	hasModel, modelVal := findArgAndValue(cmdArgs, "--model")
	if !hasModel {
		t.Fatalf("--model flag not found in argv: %v", cmdArgs)
	}
	if modelVal != modelID {
		t.Fatalf("--model value = %q, want %q", modelVal, modelID)
	}

	// Assert --add-dir flag is present with the worktree.
	hasAddDir, addDirVal := findArgAndValue(cmdArgs, "--add-dir")
	if !hasAddDir {
		t.Fatalf("--add-dir flag not found in argv: %v", cmdArgs)
	}
	if addDirVal != worktree {
		t.Fatalf("--add-dir value = %q, want %q", addDirVal, worktree)
	}

	// Assert --dangerously-skip-permissions flag is present.
	if !hasArg(cmdArgs, "--dangerously-skip-permissions") {
		t.Fatalf("--dangerously-skip-permissions flag not found in argv: %v", cmdArgs)
	}
}

// testAntigravitySecretSourceThatFailsIfCalled is a test double that fails if NamedProviderToken is called.
type testAntigravitySecretSourceThatFailsIfCalled struct{}

func (f *testAntigravitySecretSourceThatFailsIfCalled) ProviderToken() (string, string) {
	return "", ""
}

func (f *testAntigravitySecretSourceThatFailsIfCalled) PublisherTokens() (string, string) {
	return "", ""
}

func (f *testAntigravitySecretSourceThatFailsIfCalled) NamedProviderToken(ref string) (string, error) {
	panic("NamedProviderToken should not be called in subscription mode")
}

// Compile-time assertion: testAntigravitySecretSourceThatFailsIfCalled satisfies secrets.SecretSource.
var _ secrets.SecretSource = (*testAntigravitySecretSourceThatFailsIfCalled)(nil)

// ---- TC-133-03: Branch extraction and missing branch error ----

func TestAntigravityExtractsBranch(t *testing.T) {
	// TC-133-03 Variant A: stdout with "BRANCH: feature/x" → Branch == "feature/x".
	const expectedBranch = "feature/x"
	const modelID = "Claude Opus 4.6 (Thinking)"

	src := &testAntigravitySecretSourceThatFailsIfCalled{}
	entry := testAntigravityEntry("")
	entry.ModelID = modelID

	stubOut := "Running task...\nBRANCH: " + expectedBranch + "\n"
	cli := NewAntigravityCLI(entry, src, t.TempDir())
	cli.cmdFactory = stubAntigravityCommandFactory(t, stubOut, 0, nil)

	task := supervisor.Task{ID: "133", Repo: "agent-builder", Spec: "spec"}
	result, err := cli.Run(task)
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatal("result.OK = false, want true")
	}
	if result.Branch != expectedBranch {
		t.Fatalf("result.Branch = %q, want %q", result.Branch, expectedBranch)
	}
}

func TestAntigravityMissingBranchErrors(t *testing.T) {
	// TC-133-03 Variant B: stdout with no BRANCH line → error wrapping ErrAntigravityMissingBranch.
	const modelID = "Claude Opus 4.6 (Thinking)"

	src := &testAntigravitySecretSourceThatFailsIfCalled{}
	entry := testAntigravityEntry("")
	entry.ModelID = modelID

	// Stub subprocess with output that has no BRANCH line.
	stubOut := "Running task...\nNo branch produced.\n"
	cli := NewAntigravityCLI(entry, src, t.TempDir())
	cli.cmdFactory = stubAntigravityCommandFactory(t, stubOut, 0, nil)

	task := supervisor.Task{ID: "133", Repo: "agent-builder", Spec: "spec"}
	result, err := cli.Run(task)
	if err == nil {
		t.Fatal("Run() returned nil error, want ErrAntigravityMissingBranch")
	}
	if !errors.Is(err, ErrAntigravityMissingBranch) {
		t.Fatalf("error does not wrap ErrAntigravityMissingBranch: %v", err)
	}
	if result.OK {
		t.Fatal("result.OK = true, want false on missing branch")
	}
}

// ---- TC-133-04: Non-zero exit code returns error ----

func TestAntigravityNonZeroExitErrors(t *testing.T) {
	// TC-133-04: subprocess exits 1 → Run returns non-nil error containing "antigravity", Result.OK == false.
	const modelID = "Claude Opus 4.6 (Thinking)"

	src := &testAntigravitySecretSourceThatFailsIfCalled{}
	entry := testAntigravityEntry("")
	entry.ModelID = modelID

	// Stub subprocess: exits 1 with stderr message.
	stubOut := "Error occurred"
	cli := NewAntigravityCLI(entry, src, t.TempDir())
	cli.cmdFactory = stubAntigravityCommandFactory(t, stubOut, 1, nil)

	task := supervisor.Task{ID: "133", Repo: "agent-builder", Spec: "spec"}

	result, err := cli.Run(task)
	if err == nil {
		t.Fatal("Run() returned nil error, want non-nil on subprocess exit 1")
	}
	if result.OK {
		t.Fatal("result.OK = true, want false on subprocess failure")
	}
	if result.Branch != "" {
		t.Fatalf("result.Branch = %q, want empty on failure", result.Branch)
	}
	// TC-133-04: assert the error contains "antigravity".
	if !strings.Contains(err.Error(), "antigravity") {
		t.Fatalf("error does not contain 'antigravity': %v", err)
	}
}

// ---- TC-133-05: Blank worktree errors without invoking subprocess ----

func TestAntigravityBlankWorktreeErrors(t *testing.T) {
	// TC-133-05: blank worktree → error wrapping ErrAntigravityBlankWorktree, subprocess not invoked.
	src := &testAntigravitySecretSourceThatFailsIfCalled{}
	entry := testAntigravityEntry("")

	// A subprocess-tracking factory: if invoked, the test fails.
	subprocessInvoked := false
	cli := NewAntigravityCLI(entry, src, "")
	cli.cmdFactory = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		subprocessInvoked = true
		return exec.CommandContext(ctx, name, args...)
	}

	task := supervisor.Task{ID: "133", Repo: "agent-builder", Spec: "spec"}

	_, err := cli.Run(task)
	if err == nil {
		t.Fatal("Run() returned nil error, want ErrAntigravityBlankWorktree")
	}

	// Error must wrap ErrAntigravityBlankWorktree.
	if !errors.Is(err, ErrAntigravityBlankWorktree) {
		t.Fatalf("error does not wrap ErrAntigravityBlankWorktree: %v", err)
	}
	if subprocessInvoked {
		t.Fatal("subprocess was invoked even though worktree is blank")
	}
}

// ---- TC-133-06: Registry harness constant and loader integration ----

func TestHarnessAntigravityConstant(t *testing.T) {
	// TC-133-06: HarnessAntigravityCLI == "antigravity-cli", String() returns it, distinct from others.
	if registry.HarnessAntigravityCLI != "antigravity-cli" {
		t.Fatalf("HarnessAntigravityCLI = %q, want %q", registry.HarnessAntigravityCLI, "antigravity-cli")
	}

	// TC-133-06: String() returns the same value.
	if registry.HarnessAntigravityCLI.String() != "antigravity-cli" {
		t.Fatalf("HarnessAntigravityCLI.String() = %q, want %q", registry.HarnessAntigravityCLI.String(), "antigravity-cli")
	}

	// Verify it's distinct from the other 4 harnesses.
	others := []registry.HarnessDriver{
		registry.HarnessClaudeCLI,
		registry.HarnessCodexCLI,
		registry.HarnessGeminiCLI,
		registry.HarnessOllamaNative,
	}
	for _, other := range others {
		if registry.HarnessAntigravityCLI == other {
			t.Fatalf("HarnessAntigravityCLI is not distinct from %q", other)
		}
	}
}

// ---- TC-133-07: Gate-failure prompt injection (ADR 052 parity) ----

func TestAntigravityPromptIncludesFailureSectionWhenPriorFailureSet(t *testing.T) {
	// TC-133-07 Variant A: when task.PriorFailure != "", prompt includes failure section.
	task := supervisor.Task{
		ID:           "133",
		Repo:         "agent-builder",
		Spec:         "/tasks/133.md",
		PriorFailure: "Failed step: golangci-lint\nOutput:\nerr: unused variable\nFix these issues before producing the branch.",
	}
	prompt := buildAntigravityPrompt(task, "/worktree")

	// Assert: contains "previous attempt"
	if !strings.Contains(prompt, "previous attempt") {
		t.Errorf("prompt missing 'previous attempt', got:\n%s", prompt)
	}

	// Assert: contains "verification gate"
	if !strings.Contains(prompt, "verification gate") {
		t.Errorf("prompt missing 'verification gate', got:\n%s", prompt)
	}

	// Assert: contains the step name from PriorFailure
	if !strings.Contains(prompt, "golangci-lint") {
		t.Errorf("prompt missing 'golangci-lint', got:\n%s", prompt)
	}

	// Assert: contains the step output from PriorFailure
	if !strings.Contains(prompt, "unused variable") {
		t.Errorf("prompt missing 'unused variable', got:\n%s", prompt)
	}
}

func TestAntigravityPromptOmitsFailureSectionWhenPriorFailureEmpty(t *testing.T) {
	// TC-133-07 Variant B: when task.PriorFailure == "", prompt omits failure section.
	task := supervisor.Task{
		ID:   "133",
		Repo: "agent-builder",
		Spec: "/tasks/133.md",
		// PriorFailure is zero-value ""
	}
	prompt := buildAntigravityPrompt(task, "/worktree")

	// Assert: does NOT contain "previous attempt"
	if strings.Contains(prompt, "previous attempt") {
		t.Errorf("prompt should not contain 'previous attempt' when PriorFailure is empty, got:\n%s", prompt)
	}

	// Assert: does NOT contain "verification gate"
	if strings.Contains(prompt, "verification gate") {
		t.Errorf("prompt should not contain 'verification gate' when PriorFailure is empty, got:\n%s", prompt)
	}

	// Assert: core content is present
	if !strings.Contains(prompt, "Task ID: 133") {
		t.Errorf("core prompt missing 'Task ID: 133', got:\n%s", prompt)
	}
}
