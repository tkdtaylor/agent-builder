package loop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
	"github.com/tkdtaylor/agent-builder/internal/tasksource"
)

// TC-107-01: supervisor.Task.PriorFailure field compiles; zero-value is ""
func TestTaskPriorFailureFieldExists(t *testing.T) {
	// Construct a Task without setting PriorFailure
	task := supervisor.Task{
		ID:   "001",
		Repo: "exec-sandbox",
		Spec: "/tasks/001.md",
	}

	// Verify zero value is empty string
	if task.PriorFailure != "" {
		t.Errorf("expected zero PriorFailure to be empty string, got %q", task.PriorFailure)
	}

	// Verify we can set it
	task.PriorFailure = "test failure"
	if task.PriorFailure != "test failure" {
		t.Errorf("expected PriorFailure to be set, got %q", task.PriorFailure)
	}
}

// TC-107-02: loop.FormatFailure gate-fail output
func TestFormatFailureGateFail(t *testing.T) {
	outcome := Outcome{
		Kind: OutcomeFail,
		Failure: Failure{Reason: FailureGate},
		Verdict: gate.Verdict{
			OK: false,
			Results: []gate.StepResult{
				{Name: "go-build", OK: true, Output: ""},
				{Name: "go-test", OK: false, Output: "FAIL: TestFoo panicked\nexit status 1"},
			},
		},
	}

	result := FormatFailure(outcome)

	// Verify required content
	if !strings.Contains(result, "go-test") {
		t.Errorf("expected go-test in output, got: %s", result)
	}
	if !strings.Contains(result, "FAIL: TestFoo panicked") {
		t.Errorf("expected step output in result, got: %s", result)
	}
	if !strings.Contains(result, "verification gate") {
		t.Errorf("expected 'verification gate' in result, got: %s", result)
	}
	if !strings.Contains(result, "Fix these issues") {
		t.Errorf("expected 'Fix these issues' in result, got: %s", result)
	}

	// Verify go-build is NOT included (only first failing step)
	if strings.Contains(result, "go-build") && strings.Index(result, "go-test") < strings.Index(result, "go-build") {
		t.Errorf("expected only first failing step, but go-build appears in result: %s", result)
	}
}

// TC-107-03-A: FormatFailure FailureExecutorError
func TestFormatFailureExecutorError(t *testing.T) {
	outcome := Outcome{
		Kind: OutcomeFail,
		Failure: Failure{
			Reason: FailureExecutorError,
			Err:    errors.New("subprocess: exit status 2"),
		},
	}

	result := FormatFailure(outcome)

	if result == "" {
		t.Errorf("expected non-empty result for executor error")
	}
	if !strings.Contains(result, "error") {
		t.Errorf("expected 'error' in result for executor error, got: %s", result)
	}
	if strings.Contains(result, "verification gate") {
		t.Errorf("expected NO 'verification gate' for executor error, got: %s", result)
	}
}

// TC-107-03-B: FormatFailure FailureExecutorIncomplete
func TestFormatFailureExecutorIncomplete(t *testing.T) {
	outcome := Outcome{
		Kind:    OutcomeFail,
		Failure: Failure{Reason: FailureExecutorIncomplete},
	}

	result := FormatFailure(outcome)

	if result == "" {
		t.Errorf("expected non-empty result for executor incomplete")
	}
	if !strings.Contains(result, "branch") {
		t.Errorf("expected 'branch' in result for executor incomplete, got: %s", result)
	}
	if strings.Contains(result, "verification gate") {
		t.Errorf("expected NO 'verification gate' for executor incomplete, got: %s", result)
	}
}

// TC-107-04: Truncation at MaxFailureOutputBytes
func TestMaxFailureOutputBytesConstant(t *testing.T) {
	if MaxFailureOutputBytes != 2000 {
		t.Errorf("expected MaxFailureOutputBytes == 2000, got %d", MaxFailureOutputBytes)
	}
}

// TC-107-04: Truncation behavior with long output
func TestFormatFailureTruncatesLongOutput(t *testing.T) {
	longOutput := strings.Repeat("x", 3000)
	outcome := Outcome{
		Kind: OutcomeFail,
		Failure: Failure{Reason: FailureGate},
		Verdict: gate.Verdict{
			OK: false,
			Results: []gate.StepResult{
				{Name: "golangci-lint", OK: false, Output: longOutput},
			},
		},
	}

	result := FormatFailure(outcome)

	// Verify the full 3000-char output is not present
	if strings.Contains(result, longOutput) {
		t.Errorf("expected truncation of 3000-char output, but full output appears in result")
	}

	// Verify that the truncated output is present but capped
	// The output should have at most MaxFailureOutputBytes worth of x's
	// Extract just the output line(s) to verify the cap
	outputStart := strings.Index(result, "Output:\n")
	if outputStart < 0 {
		t.Errorf("expected 'Output:' in formatted result")
		return
	}
	outputStart += len("Output:\n")
	outputEnd := strings.LastIndex(result, "\n\nFix these issues")
	if outputEnd < 0 {
		outputEnd = len(result)
	}

	outputSection := result[outputStart:outputEnd]
	xCount := strings.Count(outputSection, "x")

	// The x count should be <= MaxFailureOutputBytes (accounting for newlines)
	if xCount > MaxFailureOutputBytes {
		t.Errorf("expected at most %d x characters in output section, got %d", MaxFailureOutputBytes, xCount)
	}
}

// TC-107-05: Loop threads PriorFailure into attempt 2 from attempt 1's failed gate verdict
func TestRetryingLoopThreadsPriorFailure(t *testing.T) {
	// Create a capturing executor that records all tasks it receives
	capturingExecutor := &capturingExecutor{receivedTasks: []*supervisor.Task{}}

	// Create a stubbed gate that fails on attempt 1, passes on attempt 2
	stubbedGate := &stubbedGate{
		attempts:        0,
		failOnFirstCall: true,
	}

	// Create a stub status writer
	statusWriter := &stubbedStatusWriter{}

	// Create the retrying loop
	source := &constantTaskSource{
		task: supervisor.Task{
			ID:   "test-001",
			Repo: "test-repo",
			Spec: "test.md",
		},
	}

	policy, _ := NewRetryPolicy(2, BootstrapEscalationHook)
	loop, err := NewRetryingLoop(source, capturingExecutor, stubbedGate, "/test/worktree", statusWriter, policy)
	if err != nil {
		t.Fatalf("failed to create retrying loop: %v", err)
	}

	// Run once
	outcome, err := loop.RunOnce()
	if err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}

	// Verify the outcome is done
	if outcome.Kind != RetryOutcomeDone {
		t.Errorf("expected RetryOutcomeDone, got %v", outcome.Kind)
	}

	// Verify we received exactly 2 tasks
	if len(capturingExecutor.receivedTasks) != 2 {
		t.Errorf("expected 2 received tasks, got %d", len(capturingExecutor.receivedTasks))
	}

	// Verify first attempt has empty PriorFailure
	if capturingExecutor.receivedTasks[0].PriorFailure != "" {
		t.Errorf("expected first attempt PriorFailure to be empty, got %q", capturingExecutor.receivedTasks[0].PriorFailure)
	}

	// Verify second attempt has non-empty PriorFailure containing the step name
	if capturingExecutor.receivedTasks[1].PriorFailure == "" {
		t.Errorf("expected second attempt PriorFailure to be non-empty")
	}

	if !strings.Contains(capturingExecutor.receivedTasks[1].PriorFailure, "go-fmt") {
		t.Errorf("expected 'go-fmt' in second attempt PriorFailure, got: %s", capturingExecutor.receivedTasks[1].PriorFailure)
	}

	if !strings.Contains(capturingExecutor.receivedTasks[1].PriorFailure, "file.go") {
		t.Errorf("expected 'file.go' in second attempt PriorFailure, got: %s", capturingExecutor.receivedTasks[1].PriorFailure)
	}
}

// TC-107-06: Loop does not set PriorFailure when attempt 1 succeeds
func TestRetryingLoopDoesNotSetPriorFailureOnSuccess(t *testing.T) {
	capturingExecutor := &capturingExecutor{receivedTasks: []*supervisor.Task{}}
	stubbedGate := &stubbedGate{
		attempts:        0,
		failOnFirstCall: false, // Pass on first call
	}
	statusWriter := &stubbedStatusWriter{}

	source := &constantTaskSource{
		task: supervisor.Task{
			ID:   "test-002",
			Repo: "test-repo",
			Spec: "test.md",
		},
	}

	policy, _ := NewRetryPolicy(3, BootstrapEscalationHook)
	loop, err := NewRetryingLoop(source, capturingExecutor, stubbedGate, "/test/worktree", statusWriter, policy)
	if err != nil {
		t.Fatalf("failed to create retrying loop: %v", err)
	}

	outcome, err := loop.RunOnce()
	if err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}

	if outcome.Kind != RetryOutcomeDone {
		t.Errorf("expected RetryOutcomeDone, got %v", outcome.Kind)
	}

	if len(capturingExecutor.receivedTasks) != 1 {
		t.Errorf("expected 1 received task (no retries), got %d", len(capturingExecutor.receivedTasks))
	}

	if capturingExecutor.receivedTasks[0].PriorFailure != "" {
		t.Errorf("expected PriorFailure to be empty after successful first attempt, got %q", capturingExecutor.receivedTasks[0].PriorFailure)
	}
}

// TC-107-08: Spec files updated (data-model.md and behaviors.md contain required entries)
func TestTaskContractAndBehaviorSpecUpdated(t *testing.T) {
	// Find the project root by checking for docs/spec/data-model.md
	rootDir := findProjectRoot()
	if rootDir == "" {
		t.Skip("could not locate project root")
	}

	dataModelPath := filepath.Join(rootDir, "docs", "spec", "data-model.md")
	behaviorsPath := filepath.Join(rootDir, "docs", "spec", "behaviors.md")

	// Check data-model.md
	dataModelContent, err := os.ReadFile(dataModelPath)
	if err != nil {
		t.Fatalf("failed to read data-model.md: %v", err)
	}

	dataModelStr := string(dataModelContent)
	if !strings.Contains(dataModelStr, "PriorFailure") {
		t.Errorf("expected 'PriorFailure' in data-model.md")
	}

	// Check behaviors.md
	behaviorsContent, err := os.ReadFile(behaviorsPath)
	if err != nil {
		t.Fatalf("failed to read behaviors.md: %v", err)
	}

	behaviorsStr := string(behaviorsContent)
	if !strings.Contains(strings.ToLower(behaviorsStr), "prior failure") &&
		!strings.Contains(strings.ToLower(behaviorsStr), "gate-failure feedback") &&
		!strings.Contains(strings.ToLower(behaviorsStr), "priorfailure") {
		t.Errorf("expected reference to gate-failure feedback in behaviors.md")
	}
}

// Test helpers

type capturingExecutor struct {
	receivedTasks []*supervisor.Task
}

func (e *capturingExecutor) Run(t supervisor.Task) (supervisor.Result, error) {
	e.receivedTasks = append(e.receivedTasks, &t)
	return supervisor.Result{OK: true, Branch: "test-branch"}, nil
}

type stubbedGate struct {
	attempts        int
	failOnFirstCall bool
}

func (g *stubbedGate) Verify(repoPath string) gate.Verdict {
	g.attempts++

	if g.failOnFirstCall && g.attempts == 1 {
		return gate.Verdict{
			OK: false,
			Results: []gate.StepResult{
				{Name: "go-fmt", OK: false, Output: "file.go"},
			},
		}
	}

	return gate.Verdict{
		OK:      true,
		Results: []gate.StepResult{{Name: "go-fmt", OK: true, Output: ""}},
	}
}

type constantTaskSource struct {
	task supervisor.Task
}

func (s *constantTaskSource) Next() (supervisor.Task, bool, error) {
	return s.task, true, nil
}

type stubbedStatusWriter struct {
}

func (w *stubbedStatusWriter) WriteStatus(taskID string, status tasksource.WritableStatus) (tasksource.StatusWriteResult, error) {
	return tasksource.StatusWriteResult{}, nil
}

// findProjectRoot locates the project root by walking up from the current test file
func findProjectRoot() string {
	// Start from the current working directory or use a marker file
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}

	// Walk up the directory tree looking for docs/spec/data-model.md
	current := wd
	for {
		candidate := filepath.Join(current, "docs", "spec", "data-model.md")
		if _, err := os.Stat(candidate); err == nil {
			return current
		}

		parent := filepath.Dir(current)
		if parent == current {
			// Reached filesystem root
			break
		}
		current = parent
	}

	return ""
}
