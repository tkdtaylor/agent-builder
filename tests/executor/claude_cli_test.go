package executor_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/armor"
	"github.com/tkdtaylor/agent-builder/internal/executor"
	"github.com/tkdtaylor/agent-builder/internal/executorharness"
	"github.com/tkdtaylor/agent-builder/internal/ingestion"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

func TestClaudeCLIRunInvokesSubprocessAgainstWorktreeAndCapturesBranch(t *testing.T) {
	worktree := t.TempDir()
	recordPath := filepath.Join(t.TempDir(), "record.env")
	cliPath := writeFakeClaudeCLI(t, recordPath, "task/022-claude-cli-executor", 0, "", "")

	claudeExecutor := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
		CLIPath:   cliPath,
		Worktree:  worktree,
		AuthToken: "test-token-value",
	})

	result, err := claudeExecutor.Run(context.Background(), supervisor.Task{
		ID:   "022",
		Repo: "agent-builder",
		Spec: "docs/tasks/completed/022-claude-cli-executor.md",
	})
	if err != nil {
		t.Fatalf("TC-001 Run returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("TC-003 Result.OK = false, want true")
	}
	if result.Branch != "task/022-claude-cli-executor" {
		t.Fatalf("TC-002 Result.Branch = %q, want task/022-claude-cli-executor", result.Branch)
	}
	t.Logf("TC-002 branch capture: Result.Branch=%q Result.OK=%v", result.Branch, result.OK)

	recordBytes, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read fake CLI record: %v", err)
	}
	record := string(recordBytes)
	if !strings.Contains(record, "PWD="+worktree) {
		t.Fatalf("TC-001 fake CLI PWD record did not contain worktree %q:\n%s", worktree, record)
	}
	for _, want := range []string{
		"Task ID: 022",
		"Repo: agent-builder",
		"Task spec: docs/tasks/completed/022-claude-cli-executor.md",
		"Worktree: " + worktree,
		"produced-branch.txt",
	} {
		if !strings.Contains(record, want) {
			t.Fatalf("TC-005 fake CLI prompt record missing %q:\n%s", want, record)
		}
	}
	branchPath := promptBranchPath(t, record)
	if !filepath.IsAbs(branchPath) {
		t.Fatalf("TC-005 branch-output path = %q, want absolute temp path", branchPath)
	}
	if strings.HasPrefix(branchPath, worktree+string(os.PathSeparator)) || branchPath == worktree {
		t.Fatalf("TC-005 branch-output path %q is inside worktree %q", branchPath, worktree)
	}
	if !strings.Contains(filepath.Base(filepath.Dir(branchPath)), "agent-builder-claude-cli-") {
		t.Fatalf("TC-005 branch-output path %q was not in executor-owned temp storage", branchPath)
	}
	if !strings.Contains(record, executor.ClaudeCLIAuthEnv+"=test-token-value") {
		t.Fatalf("TC-004 fake CLI did not receive %s through environment:\n%s", executor.ClaudeCLIAuthEnv, record)
	}
	if strings.Contains(record, "ARGV_HAS_TOKEN=true") {
		t.Fatalf("TC-004 token leaked through argv:\n%s", record)
	}
	if strings.Contains(record, "HOME="+os.Getenv("HOME")) && os.Getenv("HOME") != "" {
		t.Fatalf("TC-006 fake CLI received host HOME:\n%s", record)
	}
	if strings.Contains(result.Branch, "test-token-value") || strings.Contains(branchPath, "test-token-value") {
		t.Fatalf("TC-004 token leaked through branch result/path: branch=%q path=%q", result.Branch, branchPath)
	}
}

func TestClaudeCLIRunRejectsInvalidInputsBeforeSubprocess(t *testing.T) {
	worktree := t.TempDir()
	recordPath := filepath.Join(t.TempDir(), "record.env")
	cliPath := writeFakeClaudeCLI(t, recordPath, "unused", 0, "", "")

	tests := []struct {
		name string
		exec *executor.ClaudeCLI
		task supervisor.Task
		want error
	}{
		{
			name: "blank explicit CLI path",
			exec: executor.NewClaudeCLI(executor.ClaudeCLIConfig{CLIPath: " \t", Worktree: worktree, AuthToken: "test-token-value"}),
			task: supervisor.Task{ID: "022", Spec: "docs/tasks/completed/022-claude-cli-executor.md"},
			want: executor.ErrBlankCLIPath,
		},
		{
			name: "blank worktree",
			exec: executor.NewClaudeCLI(executor.ClaudeCLIConfig{CLIPath: cliPath, AuthToken: "test-token-value"}),
			task: supervisor.Task{ID: "022", Spec: "docs/tasks/completed/022-claude-cli-executor.md"},
			want: executor.ErrBlankWorktree,
		},
		{
			name: "missing token",
			exec: executor.NewClaudeCLI(executor.ClaudeCLIConfig{CLIPath: cliPath, Worktree: worktree}),
			task: supervisor.Task{ID: "022", Spec: "docs/tasks/completed/022-claude-cli-executor.md"},
			want: executor.ErrMissingClaudeCredential,
		},
		{
			name: "blank task ID",
			exec: executor.NewClaudeCLI(executor.ClaudeCLIConfig{CLIPath: cliPath, Worktree: worktree, AuthToken: "test-token-value"}),
			task: supervisor.Task{Spec: "docs/tasks/completed/022-claude-cli-executor.md"},
			want: executor.ErrBlankTaskID,
		},
		{
			name: "blank task spec",
			exec: executor.NewClaudeCLI(executor.ClaudeCLIConfig{CLIPath: cliPath, Worktree: worktree, AuthToken: "test-token-value"}),
			task: supervisor.Task{ID: "022"},
			want: executor.ErrBlankTaskSpec,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.exec.Run(context.Background(), tt.task)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Run error = %v, want %v", err, tt.want)
			}
			if (tt.want == executor.ErrMissingClaudeToken || tt.want == executor.ErrMissingClaudeCredential) && !strings.Contains(err.Error(), executor.ClaudeCLIAuthEnv) {
				t.Fatalf("TC-004 missing-token error %q does not name %s", err, executor.ClaudeCLIAuthEnv)
			}
			if _, err := os.Stat(recordPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("TC-001 invalid input started subprocess; record stat error = %v", err)
			}
		})
	}
}

func TestClaudeCLIRunReportsMissingBranch(t *testing.T) {
	worktree := t.TempDir()
	recordPath := filepath.Join(t.TempDir(), "record.env")
	cliPath := writeFakeClaudeCLI(t, recordPath, "", 0, "", "")

	claudeExecutor := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
		CLIPath:   cliPath,
		Worktree:  worktree,
		AuthToken: "test-token-value",
	})

	result, err := claudeExecutor.Run(context.Background(), supervisor.Task{ID: "022", Spec: "docs/tasks/completed/022-claude-cli-executor.md"})
	if !errors.Is(err, executor.ErrMissingBranch) {
		t.Fatalf("TC-002 Run error = %v, want ErrMissingBranch", err)
	}
	if result.OK {
		t.Fatalf("TC-003 Result.OK = true, want false when branch is missing")
	}
}

func TestClaudeCLIRunReportsWhitespaceOnlyBranch(t *testing.T) {
	worktree := t.TempDir()
	recordPath := filepath.Join(t.TempDir(), "record.env")
	cliPath := writeFakeClaudeCLI(t, recordPath, "__WHITESPACE_BRANCH__", 0, "", "")

	claudeExecutor := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
		CLIPath:   cliPath,
		Worktree:  worktree,
		AuthToken: "test-token-value",
	})

	result, err := claudeExecutor.Run(context.Background(), supervisor.Task{ID: "022", Spec: "docs/tasks/completed/022-claude-cli-executor.md"})
	if !errors.Is(err, executor.ErrMissingBranch) {
		t.Fatalf("TC-002 whitespace-only branch error = %v, want ErrMissingBranch", err)
	}
	if result.OK || result.Branch != "" {
		t.Fatalf("TC-002 whitespace-only branch result = %+v, want empty failed result", result)
	}
}

func TestClaudeCLIRunReportsFailureWithoutLeakingToken(t *testing.T) {
	worktree := t.TempDir()
	recordPath := filepath.Join(t.TempDir(), "record.env")
	cliPath := writeFakeClaudeCLI(t, recordPath, "", 7, "safe stdout before failure", "safe stderr mentions test-token-value")

	claudeExecutor := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
		CLIPath:   cliPath,
		Worktree:  worktree,
		AuthToken: "test-token-value",
	})

	result, err := claudeExecutor.Run(context.Background(), supervisor.Task{ID: "022", Spec: "docs/tasks/completed/022-claude-cli-executor.md"})
	if err == nil {
		t.Fatal("TC-003 Run error = nil, want subprocess failure")
	}
	if result.OK {
		t.Fatalf("TC-003 Result.OK = true, want false on subprocess failure")
	}
	if !strings.Contains(err.Error(), cliPath) {
		t.Fatalf("TC-003 subprocess error %q does not name failed CLI %q", err, cliPath)
	}
	for _, want := range []string{"safe stdout before failure", "safe stderr mentions"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("TC-003 subprocess error %q does not preserve captured output %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "test-token-value") {
		t.Fatalf("TC-004 subprocess error leaked token: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("TC-004 subprocess error did not show redacted token marker: %v", err)
	}
}

func TestClaudeCLIFromEnvUsesDocumentedTokenVariable(t *testing.T) {
	worktree := t.TempDir()
	recordPath := filepath.Join(t.TempDir(), "record.env")
	cliPath := writeFakeClaudeCLI(t, recordPath, "task/022-claude-cli-executor", 0, "", "")
	t.Setenv(executor.ClaudeCLIAuthEnv, "env-token")
	t.Setenv("PATH", filepath.Dir(cliPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	claudeExecutor := executor.NewClaudeCLIFromEnv(worktree)
	if claudeExecutor == nil {
		t.Fatal("TC-006 constructor returned nil executor")
	}

	result, err := claudeExecutor.Run(context.Background(), supervisor.Task{ID: "022", Spec: "docs/tasks/completed/022-claude-cli-executor.md"})
	if err != nil {
		t.Fatalf("TC-006 Run with documented env token returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("TC-006 Result.OK = false, want true")
	}
	recordBytes, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read fake CLI record: %v", err)
	}
	record := string(recordBytes)
	if !strings.Contains(record, executor.ClaudeCLIAuthEnv+"=env-token") {
		t.Fatalf("TC-006 fake CLI did not receive documented env token:\n%s", recordBytes)
	}
	if strings.Contains(record, "ARGV_HAS_TOKEN=true") || strings.Contains(result.Branch, "env-token") {
		t.Fatalf("TC-004 env token leaked through argv or branch: record=%s branch=%q", record, result.Branch)
	}
}

func TestClaudeIngestionPolicyDeclaresReviewedOrDisabledFailClosed(t *testing.T) {
	worktree := t.TempDir()
	recordPath := filepath.Join(t.TempDir(), "record.env")
	cliPath := writeFakeClaudeCLI(t, recordPath, "task/029-claude-ingestion-control", 0, "", "")

	defaultExecutor := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
		CLIPath:   cliPath,
		Worktree:  worktree,
		AuthToken: "test-token-value",
	})
	if got := defaultExecutor.IngestionPolicy(); got != executor.ClaudeIngestionDisabled {
		t.Fatalf("TC-001 default ingestion policy = %q, want %q", got, executor.ClaudeIngestionDisabled)
	}

	if _, err := executor.ParseClaudeIngestionPolicy(""); !errors.Is(err, executor.ErrUnsupportedClaudeIngestionPolicy) {
		t.Fatalf("TC-001 blank policy parse error = %v, want ErrUnsupportedClaudeIngestionPolicy", err)
	}
	if _, err := executor.ParseClaudeIngestionPolicy("prompt-only"); !errors.Is(err, executor.ErrUnsupportedClaudeIngestionPolicy) {
		t.Fatalf("TC-001 unknown policy parse error = %v, want ErrUnsupportedClaudeIngestionPolicy", err)
	}

	reviewedWithoutHarness := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
		CLIPath:         cliPath,
		Worktree:        worktree,
		AuthToken:       "test-token-value",
		IngestionPolicy: executor.ClaudeIngestionReviewed,
	})
	_, err := reviewedWithoutHarness.Run(context.Background(), supervisor.Task{ID: "029", Spec: "docs/tasks/backlog/029-claude-ingestion-control.md"})
	if !errors.Is(err, executor.ErrMissingClaudeIngestionHarness) {
		t.Fatalf("TC-001 reviewed policy without harness Run error = %v, want ErrMissingClaudeIngestionHarness", err)
	}

	unknownPolicy := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
		CLIPath:         cliPath,
		Worktree:        worktree,
		AuthToken:       "test-token-value",
		IngestionPolicy: executor.ClaudeIngestionPolicy("prompt-only"),
	})
	_, err = unknownPolicy.Run(context.Background(), supervisor.Task{ID: "029", Spec: "docs/tasks/backlog/029-claude-ingestion-control.md"})
	if !errors.Is(err, executor.ErrUnsupportedClaudeIngestionPolicy) {
		t.Fatalf("TC-001 unknown policy Run error = %v, want ErrUnsupportedClaudeIngestionPolicy", err)
	}
}

func TestClaudeReviewedIngestionReleasesContentAndToolOnlyAfterArmorBrokerReview(t *testing.T) {
	trace := &claudeTraceRecorder{}
	runner := &claudePolicyArmorRunner{trace: trace}
	harness := executorharness.NewArmorGuarded(executorharness.ArmorConfig{
		Armor: armor.Config{Runner: runner},
		Trace: trace,
	})
	claudeExecutor := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
		CLIPath:          "unused",
		Worktree:         t.TempDir(),
		AuthToken:        "test-token-value",
		IngestionPolicy:  executor.ClaudeIngestionReviewed,
		IngestionHarness: &harness,
	})

	var continuedContent string
	result := claudeExecutor.HandleWebContent(context.Background(), claudeWebEvent("review me"), func(_ context.Context, release executorharness.ContentRelease) error {
		content, err := release.Content()
		if err != nil {
			return err
		}
		continuedContent = string(content)
		trace.RecordTrace(executorharness.TraceEvent{Stage: claudeTraceContentContinuationCalled, CandidateID: resultID(release)})
		return nil
	})
	if result.Err != nil {
		t.Fatalf("TC-002 HandleWebContent error = %v", result.Err)
	}
	if !result.Released || continuedContent != "review me" {
		t.Fatalf("TC-002 released=%v content=%q, want reviewed content released", result.Released, continuedContent)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("TC-002 armor requests = %d, want 1 before continuation", len(runner.requests))
	}
	request := runner.requests[0]
	if request.CandidateID != string(result.Candidate.ID) || request.Kind != string(ingestion.CandidateKindContent) || request.Content != "review me" {
		t.Fatalf("TC-002 armor request = %+v, want matching content candidate %q", request, result.Candidate.ID)
	}
	assertClaudeTraceOrder(t, trace.stages(),
		executorharness.TraceContentCandidateProduced,
		claudeTraceArmorContentConsumed,
		executorharness.TraceContentBrokerReviewed,
		executorharness.TraceContentReleased,
		claudeTraceContentContinuationCalled,
	)

	var executedTool ingestion.ToolCallCandidate
	toolResult := claudeExecutor.HandleToolCall(context.Background(), claudeToolEvent("web.fetch", json.RawMessage(`{"url":"https://example.test"}`)), func(_ context.Context, release executorharness.ToolCallRelease) error {
		candidate, err := release.Candidate()
		if err != nil {
			return err
		}
		executedTool = candidate
		return nil
	})
	if toolResult.Err != nil {
		t.Fatalf("TC-002 HandleToolCall error = %v", toolResult.Err)
	}
	if !toolResult.Released || executedTool.ID != toolResult.Candidate.ID {
		t.Fatalf("TC-002 tool released=%v executed=%q candidate=%q, want reviewed tool execution", toolResult.Released, executedTool.ID, toolResult.Candidate.ID)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("TC-002 armor requests = %d, want content and tool reviewed", len(runner.requests))
	}
	toolRequest := runner.requests[1]
	if toolRequest.CandidateID != string(toolResult.Candidate.ID) || toolRequest.Kind != string(ingestion.CandidateKindToolCall) || toolRequest.ToolName != "web.fetch" {
		t.Fatalf("TC-002 armor tool request = %+v, want matching tool candidate %q", toolRequest, toolResult.Candidate.ID)
	}
	assertClaudeTraceOrder(t, trace.stages(),
		executorharness.TraceToolCandidateProduced,
		claudeTraceArmorToolConsumed,
		executorharness.TraceToolBrokerReviewed,
		executorharness.TraceToolReleased,
		executorharness.TraceToolExecuted,
	)
}

func TestClaudeDirectIngestionBypassFails(t *testing.T) {
	contentBypass := func(release executorharness.ContentRelease) error {
		_, err := release.Content()
		return err
	}
	if err := contentBypass(executorharness.ContentRelease{}); !errors.Is(err, executorharness.ErrUnreviewedRelease) {
		t.Fatalf("TC-003 direct content bypass error = %v, want ErrUnreviewedRelease", err)
	}

	toolBypass := func(release executorharness.ToolCallRelease) error {
		_, err := release.Arguments()
		return err
	}
	if err := toolBypass(executorharness.ToolCallRelease{}); !errors.Is(err, executorharness.ErrUnreviewedRelease) {
		t.Fatalf("TC-003 direct tool bypass error = %v, want ErrUnreviewedRelease", err)
	}
}

func TestClaudeDisabledIngestionPolicyFailsClosedAndAllowsNormalSubprocess(t *testing.T) {
	worktree := t.TempDir()
	recordPath := filepath.Join(t.TempDir(), "record.env")
	cliPath := writeFakeClaudeCLI(t, recordPath, "task/029-claude-ingestion-control", 0, "", "")
	claudeExecutor := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
		CLIPath:         cliPath,
		Worktree:        worktree,
		AuthToken:       "test-token-value",
		IngestionPolicy: executor.ClaudeIngestionDisabled,
	})

	var continued bool
	contentResult := claudeExecutor.HandleWebContent(context.Background(), claudeWebEvent("blocked by disabled policy"), func(context.Context, executorharness.ContentRelease) error {
		continued = true
		return nil
	})
	if !errors.Is(contentResult.Err, executor.ErrClaudeIngestionDisabled) || continued {
		t.Fatalf("TC-004 disabled content result = %+v continued=%v, want disabled denial before continuation", contentResult, continued)
	}
	if !strings.Contains(contentResult.Err.Error(), "disabled") {
		t.Fatalf("TC-004 disabled content error = %q, want disabled reason", contentResult.Err)
	}

	var executed bool
	toolResult := claudeExecutor.HandleToolCall(context.Background(), claudeToolEvent("web.fetch", json.RawMessage(`{"url":"https://example.test"}`)), func(context.Context, executorharness.ToolCallRelease) error {
		executed = true
		return nil
	})
	if !errors.Is(toolResult.Err, executor.ErrClaudeIngestionDisabled) || executed {
		t.Fatalf("TC-004 disabled tool result = %+v executed=%v, want disabled denial before execution", toolResult, executed)
	}

	result, err := claudeExecutor.Run(context.Background(), supervisor.Task{
		ID:   "029",
		Repo: "agent-builder",
		Spec: "docs/tasks/backlog/029-claude-ingestion-control.md",
	})
	if err != nil {
		t.Fatalf("TC-004 normal subprocess under disabled policy returned error: %v", err)
	}
	if !result.OK || result.Branch != "task/029-claude-ingestion-control" {
		t.Fatalf("TC-004 normal subprocess result = %+v, want branch capture", result)
	}
}

func TestClaudeReviewedIngestionBlocksArmorFailuresBeforeExecutorUse(t *testing.T) {
	tests := []struct {
		name      string
		runner    *claudePolicyArmorRunner
		event     executorharness.ToolCallEvent
		wantErr   error
		wantCalls int
	}{
		{
			name:      "block",
			runner:    &claudePolicyArmorRunner{response: armor.Response{Decision: "block", Reason: "blocked by armor"}},
			event:     claudeToolEvent("web.fetch", json.RawMessage(`{"url":"https://example.test"}`)),
			wantCalls: 1,
		},
		{
			name:      "quarantine",
			runner:    &claudePolicyArmorRunner{response: armor.Response{Decision: "quarantine", Reason: "quarantined by armor"}},
			event:     claudeToolEvent("web.fetch", json.RawMessage(`{"url":"https://example.test"}`)),
			wantCalls: 1,
		},
		{
			name: "allow with findings",
			runner: &claudePolicyArmorRunner{response: armor.Response{
				Decision: "allow",
				Findings: []armor.Finding{{Category: "prompt-injection", Severity: "high", Message: "finding blocks allow"}},
			}},
			event:     claudeToolEvent("web.fetch", json.RawMessage(`{"url":"https://example.test"}`)),
			wantCalls: 1,
		},
		{
			name:      "runner unavailable",
			runner:    &claudePolicyArmorRunner{err: errors.New("armor unavailable")},
			event:     claudeToolEvent("web.fetch", json.RawMessage(`{"url":"https://example.test"}`)),
			wantCalls: 1,
		},
		{
			name:      "malformed arguments",
			runner:    &claudePolicyArmorRunner{response: armor.Response{Decision: "allow"}},
			event:     claudeToolEvent("web.fetch", json.RawMessage(`{"url":`)),
			wantErr:   ingestion.ErrMalformedToolArguments,
			wantCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := executorharness.NewArmorGuarded(executorharness.ArmorConfig{
				Armor: armor.Config{Runner: tt.runner},
			})
			claudeExecutor := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
				CLIPath:          "unused",
				Worktree:         t.TempDir(),
				AuthToken:        "test-token-value",
				IngestionPolicy:  executor.ClaudeIngestionReviewed,
				IngestionHarness: &harness,
			})

			var executed bool
			result := claudeExecutor.HandleToolCall(context.Background(), tt.event, func(context.Context, executorharness.ToolCallRelease) error {
				executed = true
				return nil
			})
			if tt.wantErr != nil {
				if !errors.Is(result.Err, tt.wantErr) {
					t.Fatalf("TC-005 result error = %v, want %v", result.Err, tt.wantErr)
				}
			} else if result.Decision.Outcome != ingestion.DecisionBlock && result.Decision.Outcome != ingestion.DecisionQuarantine {
				t.Fatalf("TC-005 decision outcome = %q, want block/quarantine", result.Decision.Outcome)
			}
			if result.Released || executed {
				t.Fatalf("TC-005 result = %+v executed=%v, want prevented executor use", result, executed)
			}
			if len(tt.runner.requests) != tt.wantCalls {
				t.Fatalf("TC-005 armor calls = %d, want %d", len(tt.runner.requests), tt.wantCalls)
			}
		})
	}

	missingArmor := executorharness.NewArmorGuarded(executorharness.ArmorConfig{})
	claudeExecutor := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
		CLIPath:          "unused",
		Worktree:         t.TempDir(),
		AuthToken:        "test-token-value",
		IngestionPolicy:  executor.ClaudeIngestionReviewed,
		IngestionHarness: &missingArmor,
	})
	var continued bool
	contentResult := claudeExecutor.HandleWebContent(context.Background(), claudeWebEvent("review me"), func(context.Context, executorharness.ContentRelease) error {
		continued = true
		return nil
	})
	if contentResult.Released || continued || contentResult.Decision.Outcome != ingestion.DecisionBlock {
		t.Fatalf("TC-005 missing armor result = %+v continued=%v, want fail-closed block", contentResult, continued)
	}
	t.Log("TC-005 Claude executor web/tool route is reviewed or disabled fail-closed")
}

func promptBranchPath(t *testing.T, record string) string {
	t.Helper()
	lines := strings.Split(record, "\n")
	for _, line := range lines {
		if strings.HasSuffix(line, "produced-branch.txt") {
			return line
		}
	}
	t.Fatalf("TC-005 record did not contain produced branch path:\n%s", record)
	return ""
}

type claudePolicyArmorRunner struct {
	requests []armor.Request
	response armor.Response
	err      error
	trace    *claudeTraceRecorder
}

func (r *claudePolicyArmorRunner) Run(_ context.Context, request armor.Request) (armor.Response, error) {
	r.requests = append(r.requests, cloneClaudeArmorRequest(request))
	if r.trace != nil {
		stage := claudeTraceArmorContentConsumed
		kind := ingestion.CandidateKindContent
		if request.Kind == string(ingestion.CandidateKindToolCall) {
			stage = claudeTraceArmorToolConsumed
			kind = ingestion.CandidateKindToolCall
		}
		r.trace.RecordTrace(executorharness.TraceEvent{
			Stage:       stage,
			CandidateID: ingestion.CandidateID(request.CandidateID),
			Kind:        kind,
		})
	}
	if r.err != nil {
		return armor.Response{}, r.err
	}
	if strings.TrimSpace(r.response.Decision) != "" {
		return r.response, nil
	}
	return armor.Response{Decision: "allow"}, nil
}

type claudeTraceRecorder struct {
	events []executorharness.TraceEvent
}

func (r *claudeTraceRecorder) RecordTrace(event executorharness.TraceEvent) {
	r.events = append(r.events, event)
}

func (r *claudeTraceRecorder) stages() []executorharness.TraceStage {
	stages := make([]executorharness.TraceStage, 0, len(r.events))
	for _, event := range r.events {
		stages = append(stages, event.Stage)
	}
	return stages
}

const (
	claudeTraceArmorContentConsumed      executorharness.TraceStage = "claude-armor-content-consumed"
	claudeTraceArmorToolConsumed         executorharness.TraceStage = "claude-armor-tool-consumed"
	claudeTraceContentContinuationCalled executorharness.TraceStage = "claude-content-continuation-called"
)

func claudeWebEvent(content string) executorharness.WebContentEvent {
	return executorharness.WebContentEvent{
		Content:    []byte(content),
		SourceURI:  "https://example.test/research",
		MediaType:  "text/plain",
		Provenance: ingestion.Provenance{TaskID: "029", Executor: "claude-cli"},
	}
}

func claudeToolEvent(toolName string, arguments json.RawMessage) executorharness.ToolCallEvent {
	return executorharness.ToolCallEvent{
		ToolName:   toolName,
		Arguments:  arguments,
		TargetURI:  "https://example.test",
		Provenance: ingestion.Provenance{TaskID: "029", Executor: "claude-cli"},
	}
}

func cloneClaudeArmorRequest(request armor.Request) armor.Request {
	request.Arguments = append([]byte(nil), request.Arguments...)
	if len(request.Provenance) > 0 {
		provenance := make(map[string]string, len(request.Provenance))
		for key, value := range request.Provenance {
			provenance[key] = value
		}
		request.Provenance = provenance
	}
	return request
}

func resultID(release executorharness.ContentRelease) ingestion.CandidateID {
	candidate, err := release.Candidate()
	if err != nil {
		return ""
	}
	return candidate.ID
}

func assertClaudeTraceOrder(t *testing.T, got []executorharness.TraceStage, want ...executorharness.TraceStage) {
	t.Helper()
	next := 0
	for _, stage := range got {
		if next < len(want) && stage == want[next] {
			next++
		}
	}
	if next != len(want) {
		t.Fatalf("trace stages = %v, want ordered subsequence %v", got, want)
	}
}

func writeFakeClaudeCLI(t *testing.T, recordPath, branch string, exitCode int, stdoutText, stderrText string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake CLI is POSIX-only")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "claude-bin")
	script := `#!/bin/sh
set -eu
record="$1"
branch="$2"
exit_code="$3"
stdout_text="$4"
stderr_text="$5"
prompt="${7:-}"
branch_file=$(printf '%s\n' "$prompt" | awk '/produced-branch.txt$/ { print; exit }')
argv_has_token=false
case "$*" in
  *test-token-value*) argv_has_token=true ;;
esac
{
  printf 'PWD=%s\n' "$PWD"
  printf 'ANTHROPIC_API_KEY=%s\n' "${ANTHROPIC_API_KEY:-}"
  printf 'HOME=%s\n' "${HOME:-}"
  printf 'XDG_CONFIG_HOME=%s\n' "${XDG_CONFIG_HOME:-}"
  printf 'ARGV_HAS_TOKEN=%s\n' "$argv_has_token"
  printf 'PROMPT<<EOF\n%s\nEOF\n' "$prompt"
} > "$record"
if [ "$exit_code" -ne 0 ]; then
  printf '%s\n' "$stdout_text"
  printf '%s\n' "$stderr_text" >&2
  exit "$exit_code"
fi
if [ "$branch" = "__WHITESPACE_BRANCH__" ] && [ -n "$branch_file" ]; then
  printf ' \n\t \n' > "$branch_file"
elif [ -n "$branch" ] && [ -n "$branch_file" ]; then
  printf '%s\n' "$branch" > "$branch_file"
fi
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}

	wrapper := filepath.Join(dir, "claude")
	wrapperScript := fmt.Sprintf("#!/bin/sh\nexec %q %q %q %q %q %q \"$@\"\n", path, recordPath, branch, fmt.Sprint(exitCode), stdoutText, stderrText)
	if err := os.WriteFile(wrapper, []byte(wrapperScript), 0o755); err != nil {
		t.Fatalf("write fake CLI wrapper: %v", err)
	}
	return wrapper
}
