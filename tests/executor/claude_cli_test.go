package executor_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/executor"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

func TestClaudeCLIRunInvokesSubprocessAgainstWorktreeAndCapturesBranch(t *testing.T) {
	worktree := t.TempDir()
	recordPath := filepath.Join(t.TempDir(), "record.env")
	cliPath := writeFakeClaudeCLI(t, recordPath, "task/022-claude-cli-executor", 0, "")

	claudeExecutor := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
		CLIPath:   cliPath,
		Worktree:  worktree,
		AuthToken: "test-token-value",
	})

	result, err := claudeExecutor.Run(supervisor.Task{
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
	if !strings.Contains(record, executor.ClaudeCLIAuthEnv+"=test-token-value") {
		t.Fatalf("TC-004 fake CLI did not receive %s through environment:\n%s", executor.ClaudeCLIAuthEnv, record)
	}
	if strings.Contains(record, "ARGV_HAS_TOKEN=true") {
		t.Fatalf("TC-004 token leaked through argv:\n%s", record)
	}
	if strings.Contains(record, "HOME="+os.Getenv("HOME")) && os.Getenv("HOME") != "" {
		t.Fatalf("TC-006 fake CLI received host HOME:\n%s", record)
	}
}

func TestClaudeCLIRunRejectsInvalidInputsBeforeSubprocess(t *testing.T) {
	worktree := t.TempDir()
	recordPath := filepath.Join(t.TempDir(), "record.env")
	cliPath := writeFakeClaudeCLI(t, recordPath, "unused", 0, "")

	tests := []struct {
		name string
		exec *executor.ClaudeCLI
		task supervisor.Task
		want error
	}{
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
			want: executor.ErrMissingClaudeToken,
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
			_, err := tt.exec.Run(tt.task)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Run error = %v, want %v", err, tt.want)
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
	cliPath := writeFakeClaudeCLI(t, recordPath, "", 0, "")

	claudeExecutor := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
		CLIPath:   cliPath,
		Worktree:  worktree,
		AuthToken: "test-token-value",
	})

	result, err := claudeExecutor.Run(supervisor.Task{ID: "022", Spec: "docs/tasks/completed/022-claude-cli-executor.md"})
	if !errors.Is(err, executor.ErrMissingBranch) {
		t.Fatalf("TC-002 Run error = %v, want ErrMissingBranch", err)
	}
	if result.OK {
		t.Fatalf("TC-003 Result.OK = true, want false when branch is missing")
	}
}

func TestClaudeCLIRunReportsFailureWithoutLeakingToken(t *testing.T) {
	worktree := t.TempDir()
	recordPath := filepath.Join(t.TempDir(), "record.env")
	cliPath := writeFakeClaudeCLI(t, recordPath, "", 7, "failure mentions test-token-value")

	claudeExecutor := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
		CLIPath:   cliPath,
		Worktree:  worktree,
		AuthToken: "test-token-value",
	})

	result, err := claudeExecutor.Run(supervisor.Task{ID: "022", Spec: "docs/tasks/completed/022-claude-cli-executor.md"})
	if err == nil {
		t.Fatal("TC-003 Run error = nil, want subprocess failure")
	}
	if result.OK {
		t.Fatalf("TC-003 Result.OK = true, want false on subprocess failure")
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
	cliPath := writeFakeClaudeCLI(t, recordPath, "task/022-claude-cli-executor", 0, "")
	t.Setenv(executor.ClaudeCLIAuthEnv, "env-token")
	t.Setenv("PATH", filepath.Dir(cliPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	claudeExecutor := executor.NewClaudeCLIFromEnv(worktree)
	if claudeExecutor == nil {
		t.Fatal("TC-006 constructor returned nil executor")
	}

	result, err := claudeExecutor.Run(supervisor.Task{ID: "022", Spec: "docs/tasks/completed/022-claude-cli-executor.md"})
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
	if !strings.Contains(string(recordBytes), executor.ClaudeCLIAuthEnv+"=env-token") {
		t.Fatalf("TC-006 fake CLI did not receive documented env token:\n%s", recordBytes)
	}
}

func writeFakeClaudeCLI(t *testing.T, recordPath, branch string, exitCode int, failure string) string {
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
failure="$4"
prompt="${6:-}"
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
  printf '%s\n' "$failure" >&2
  exit "$exit_code"
fi
if [ -n "$branch" ] && [ -n "$branch_file" ]; then
  printf '%s\n' "$branch" > "$branch_file"
fi
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}

	wrapper := filepath.Join(dir, "claude")
	wrapperScript := fmt.Sprintf("#!/bin/sh\nexec %q %q %q %q %q \"$@\"\n", path, recordPath, branch, fmt.Sprint(exitCode), failure)
	if err := os.WriteFile(wrapper, []byte(wrapperScript), 0o755); err != nil {
		t.Fatalf("write fake CLI wrapper: %v", err)
	}
	return wrapper
}
