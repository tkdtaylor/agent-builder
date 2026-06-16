package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
)

func TestRuntimeRunWiresPhase0Pipeline(t *testing.T) {
	binary := buildBinary(t)
	fixture := newRunFixture(t, "ready")
	env := fixture.env()

	stdout, stderr, code := runBinaryExactEnv(t, binary, env, "run")
	t.Logf("TC-005 runtime run stdout=%q stderr=%q exit=%d", stdout, stderr, code)
	if code != 0 {
		record := ""
		if data, err := os.ReadFile(fixture.recordPath); err == nil {
			record = string(data)
		}
		t.Fatalf("TC-001 runtime run exit code = %d, want 0; stdout=%q stderr=%q record=%q", code, stdout, stderr, record)
	}
	if strings.Contains(stderr, "supervisor: nil containment box") ||
		strings.Contains(stderr, "supervisor: nil in-box loop") ||
		strings.Contains(stderr, "supervisor: missing task") {
		t.Fatalf("TC-001 runtime run reached nil supervisor seam: %q", stderr)
	}
	if !strings.Contains(stdout, "run completed: task 001") {
		t.Fatalf("TC-005 stdout = %q, want completed task summary", stdout)
	}

	claudeLog := readRuntimeText(t, fixture.claudeLog)
	if !strings.Contains(claudeLog, "task=001") {
		t.Fatalf("TC-001 fake executor log = %q, want task 001 attempt", claudeLog)
	}
	if strings.Contains(claudeLog, "task=002") {
		t.Fatalf("TC-002 fake executor log = %q, second ready task must be untouched", claudeLog)
	}
	if got := strings.Count(claudeLog, "task="); got != 1 {
		t.Fatalf("TC-002 executor attempts = %d, want exactly 1", got)
	}
	if launcherLog := readRuntimeText(t, fixture.launcherLog); !strings.Contains(launcherLog, "cmd=/bin/true") {
		t.Fatalf("TC-001 fake launcher log = %q, want containment probe", launcherLog)
	}
	if publishLog := readRuntimeText(t, fixture.publishLog); !strings.Contains(publishLog, "git push origin task/028-default-run-wiring") ||
		!strings.Contains(publishLog, "gh pr create --head task/028-default-run-wiring --fill") {
		t.Fatalf("TC-001 publish log = %q, want git push and gh pr create", publishLog)
	}

	events := readRunRecordEvents(t, fixture.recordPath)
	assertRunRecordContains(t, events, "run_started", "task_id", "001")
	assertRunRecordContains(t, events, "command", "command", "pick task 001")
	assertRunRecordContains(t, events, "command", "command", "attempt task 001")
	assertRunRecordContains(t, events, "command", "command", "verify worktree")
	assertRunRecordContains(t, events, "stdout", "data", "executor attempt completed: branch=task/028-default-run-wiring")
	assertRunRecordContains(t, events, "stdout", "data", "gate passed: PASS go build ./...")
	assertRunRecordContains(t, events, "stdout", "data", "publication recorded: branch=task/028-default-run-wiring pr=https://github.com/acme/runfixture/pull/28")
	assertRunRecordContains(t, events, "run_finished", "outcome", "completed")
	t.Logf("TC-005 runtime run completed one configured task and persisted run_finished: %s", lastRunRecordLine(t, fixture.recordPath))

	t.Run("optional run record disabled", func(t *testing.T) {
		fixture := newRunFixture(t, "ready")
		env := fixture.env()
		delete(env, runtimewiring.EnvRunRecord)

		stdout, stderr, code := runBinaryExactEnv(t, binary, env, "run")
		if code != 0 {
			t.Fatalf("TC-001 no-record exit code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
		}
		if _, err := os.Stat(fixture.recordPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("TC-001 optional run record path stat err = %v, want not exist", err)
		}
	})

	t.Run("no ready task is idle", func(t *testing.T) {
		fixture := newRunFixture(t, "blocked")
		stdout, stderr, code := runBinaryExactEnv(t, binary, fixture.env(), "run")
		if code != 0 {
			t.Fatalf("TC-002 idle exit code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "run idle: no ready task") {
			t.Fatalf("TC-002 idle stdout = %q, want idle summary", stdout)
		}
		if _, err := os.Stat(fixture.claudeLog); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("TC-002 idle executor log err = %v, want no executor attempt", err)
		}
	})
}

func TestRunConfigFailures(t *testing.T) {
	binary := buildBinary(t)
	tests := []struct {
		name      string
		unset     string
		wantError string
	}{
		{name: "task source", unset: runtimewiring.EnvTaskRoot, wantError: runtimewiring.EnvTaskRoot},
		{name: "worktree", unset: runtimewiring.EnvWorktree, wantError: runtimewiring.EnvWorktree},
		{name: "executor token", unset: "ANTHROPIC_API_KEY", wantError: "ANTHROPIC_API_KEY"},
		{name: "timeout run config", unset: runtimewiring.EnvRunTimeout, wantError: runtimewiring.EnvRunTimeout},
		{name: "attempt run config", unset: runtimewiring.EnvMaxAttempts, wantError: runtimewiring.EnvMaxAttempts},
		{name: "publish remote", unset: runtimewiring.EnvPublishRemote, wantError: runtimewiring.EnvPublishRemote},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newRunFixture(t, "ready")
			env := fixture.env()
			delete(env, tc.unset)

			stdout, stderr, code := runBinaryExactEnv(t, binary, env, "run")
			if code == 0 {
				t.Fatalf("TC-004 %s exit code = 0, want non-zero; stdout=%q stderr=%q", tc.name, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.wantError) {
				t.Fatalf("TC-004 %s stderr = %q, want missing %s", tc.name, stderr, tc.wantError)
			}
			if _, err := os.Stat(fixture.claudeLog); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("TC-004 %s executor log err = %v, want no executor attempt", tc.name, err)
			}
			if got := readRuntimeText(t, filepath.Join(fixture.taskRoot, "docs/tasks/backlog/001-first.md")); !strings.Contains(got, "**Status:** ready") {
				t.Fatalf("TC-004 %s task status mutated before config failure:\n%s", tc.name, got)
			}
		})
	}

	t.Run("missing scanner is gate failure after executor", func(t *testing.T) {
		fixture := newRunFixture(t, "ready")
		env := fixture.env()
		env["PATH"] = writeGateToolShims(t, false)

		stdout, stderr, code := runBinaryExactEnv(t, binary, env, "run")
		if code == 0 {
			t.Fatalf("TC-004 missing scanner exit code = 0, want failure; stdout=%q stderr=%q", stdout, stderr)
		}
		if got := readRuntimeText(t, fixture.claudeLog); !strings.Contains(got, "task=001") {
			t.Fatalf("TC-004 missing scanner executor log = %q, want executor attempted before Gate failure", got)
		}
		record := readRuntimeText(t, fixture.recordPath)
		if !strings.Contains(record, `"outcome":"failed"`) || !strings.Contains(record, "code-scanner") {
			t.Fatalf("TC-004 missing scanner run record = %q, want failed Gate evidence naming code-scanner", record)
		}
	})

	t.Run("stale sandbox runtime var is rejected", func(t *testing.T) {
		fixture := newRunFixture(t, "ready")
		env := fixture.env()
		env[runtimewiring.EnvSandboxRuntime] = "/usr/bin/srt"

		stdout, stderr, code := runBinaryExactEnv(t, binary, env, "run")
		if code == 0 {
			t.Fatalf("TC-036-01 stale srt var exit code = 0, want non-zero; stdout=%q stderr=%q", stdout, stderr)
		}
		if !strings.Contains(stderr, runtimewiring.EnvSandboxRuntime) || !strings.Contains(stderr, "removed") {
			t.Fatalf("TC-036-01 stderr = %q, want removed-variable migration error naming %s", stderr, runtimewiring.EnvSandboxRuntime)
		}
		if _, err := os.Stat(fixture.claudeLog); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("TC-036-01 stale srt var executor log err = %v, want no executor attempt", err)
		}
	})
}

// TestRuntimeRunWiresPodmanAdapter (TC-036-02) asserts the default run wiring
// drives the Podman execution-box launcher and emits containment=podman evidence.
func TestRuntimeRunWiresPodmanAdapter(t *testing.T) {
	binary := buildBinary(t)
	fixture := newRunFixture(t, "ready")

	stdout, stderr, code := runBinaryExactEnv(t, binary, fixture.env(), "run")
	if code != 0 {
		t.Fatalf("TC-036-02 run exit code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}

	launcherLog := readRuntimeText(t, fixture.launcherLog)
	if !strings.Contains(launcherLog, "cmd=/bin/true") {
		t.Fatalf("TC-036-02 fake launcher log = %q, want Podman launcher invoked with the /bin/true probe", launcherLog)
	}
	if !strings.Contains(launcherLog, "worktree="+fixture.worktree) {
		t.Fatalf("TC-036-02 fake launcher log = %q, want --worktree passed to the launcher", launcherLog)
	}

	events := readRunRecordEvents(t, fixture.recordPath)
	assertRunRecordContains(t, events, "command", "command", "containment=podman")
	assertRunRecordContains(t, events, "command", "command", "launcher="+fixture.launcherPath)
	t.Log("TC-036-02 runtime uses Podman adapter; sandboxruntime not imported")
}

// TestNoSrtInvocation (TC-036-02) asserts the run pipeline never spawns an srt
// subprocess and the removed AGENT_BUILDER_SANDBOX_RUNTIME var stays absent.
func TestNoSrtInvocation(t *testing.T) {
	binary := buildBinary(t)
	fixture := newRunFixture(t, "ready")
	env := fixture.env()

	// A fake `srt` on PATH that records any invocation: if the pipeline ever
	// execs srt, this log appears and the test fails.
	srtLog := filepath.Join(filepath.Dir(fixture.launcherLog), "srt-invocation.log")
	srtPath := filepath.Join(fixture.shimDir, "srt")
	writeFile(t, srtPath, fmt.Sprintf("#!/bin/sh\nprintf 'srt invoked\\n' >> %s\nexit 0\n", shellQuoteForRun(srtLog)))
	if err := os.Chmod(srtPath, 0o755); err != nil {
		t.Fatalf("chmod fake srt: %v", err)
	}

	stdout, stderr, code := runBinaryExactEnv(t, binary, env, "run")
	if code != 0 {
		t.Fatalf("TC-036-02 run exit code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(srtLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("TC-036-02 srt was invoked by the run pipeline (log %s exists), want no srt subprocess", srtLog)
	}
	if _, present := env[runtimewiring.EnvSandboxRuntime]; present {
		t.Fatalf("TC-036-02 fixture env still sets %s, want it absent from the default run contract", runtimewiring.EnvSandboxRuntime)
	}
	t.Log("TC-036-02 no srt subprocess; AGENT_BUILDER_SANDBOX_RUNTIME absent from run contract")
}

type runFixture struct {
	taskRoot     string
	worktree     string
	shimDir      string
	claudePath   string
	launcherPath string
	gitPath      string
	ghPath       string
	claudeLog    string
	launcherLog  string
	publishLog   string
	recordPath   string
}

func newRunFixture(t *testing.T, status string) runFixture {
	t.Helper()

	root := t.TempDir()
	taskRoot := filepath.Join(root, "tasks")
	worktree := filepath.Join(root, "worktree")
	shimDir := filepath.Join(root, "bin")
	claudeLog := filepath.Join(root, "claude.log")
	launcherLog := filepath.Join(root, "launcher.log")
	publishLog := filepath.Join(root, "publish.log")

	writeFile(t, filepath.Join(taskRoot, "docs/plans/roadmap.md"), "# Roadmap\n")
	writeTaskFixture(t, filepath.Join(taskRoot, "docs/tasks/backlog/001-first.md"), "001", status)
	writeTaskFixture(t, filepath.Join(taskRoot, "docs/tasks/backlog/002-second.md"), "002", status)
	writeFile(t, filepath.Join(worktree, "go.mod"), "module example.com/runfixture\n\ngo 1.26.3\n")
	writeFile(t, filepath.Join(worktree, "runfixture.go"), "package runfixture\n\nfunc Value() int { return 1 }\n")
	writeFile(t, filepath.Join(worktree, "runfixture_test.go"), `package runfixture

import "testing"

func TestValue(t *testing.T) {
	if Value() != 1 {
		t.Fatal("bad value")
	}
}
`)

	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatalf("mkdir shim dir: %v", err)
	}
	claudePath := writeFakeClaude(t, shimDir, claudeLog)
	launcherPath := writeFakeLauncherForRun(t, shimDir, launcherLog)
	gitPath := writeFakeGitForRun(t, shimDir, publishLog)
	ghPath := writeFakeGHForRun(t, shimDir, publishLog)
	writePassingGateTools(t, shimDir)

	return runFixture{
		taskRoot:     taskRoot,
		worktree:     worktree,
		shimDir:      shimDir,
		claudePath:   claudePath,
		launcherPath: launcherPath,
		gitPath:      gitPath,
		ghPath:       ghPath,
		claudeLog:    claudeLog,
		launcherLog:  launcherLog,
		publishLog:   publishLog,
		recordPath:   filepath.Join(root, "run-record.ndjson"),
	}
}

func (f runFixture) env() map[string]string {
	return map[string]string{
		"PATH":                            f.shimDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		runtimewiring.EnvTaskRoot:         f.taskRoot,
		runtimewiring.EnvWorktree:         f.worktree,
		runtimewiring.EnvClaudeCLI:        f.claudePath,
		runtimewiring.EnvExecBoxLauncher:  f.launcherPath,
		runtimewiring.EnvRunRecord:        f.recordPath,
		runtimewiring.EnvRunTimeout:       "5s",
		runtimewiring.EnvMaxAttempts:      "1",
		runtimewiring.EnvPublishRemote:    "origin",
		runtimewiring.EnvGitCLI:           f.gitPath,
		runtimewiring.EnvGitHubCLI:        f.ghPath,
		runtimewiring.EnvGitToken:         "fake-git-token",
		runtimewiring.EnvGitHubToken:      "fake-gh-token",
		"ANTHROPIC_API_KEY":               "fake-token",
		"CLAUDE_CODE_SKIP_PROMPT_HISTORY": "",
	}
}

func writeTaskFixture(t *testing.T, path, id, status string) {
	t.Helper()
	writeFile(t, path, fmt.Sprintf(`# Task %s: fixture

**Project:** agent-builder
**Created:** 2026-06-05
**Status:** %s

## Goal
Fixture task.
`, id, status))
}

func writeFakeClaude(t *testing.T, dir, logPath string) string {
	t.Helper()
	path := filepath.Join(dir, "claude")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
if [ "$1" != "-p" ]; then
    echo "missing prompt flag" >&2
    exit 97
fi
prompt=$2
task_id=""
branch_file=""
next_branch=0
while IFS= read -r line; do
    case "$line" in
        "Task ID: "*) task_id=${line#"Task ID: "} ;;
        "When finished, write only the produced branch name to this file:") next_branch=1 ;;
        *) if [ "$next_branch" = "1" ]; then branch_file=$line; next_branch=0; fi ;;
    esac
done <<EOF
$prompt
EOF
printf 'task=%%s\n' "$task_id" >> %s
printf 'task/028-default-run-wiring\n' > "$branch_file"
`, shellQuoteForRun(logPath))
	writeFile(t, path, script)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod fake claude: %v", err)
	}
	return path
}

func writeFakeGitForRun(t *testing.T, dir, logPath string) string {
	t.Helper()
	path := filepath.Join(dir, "git")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
printf 'git %%s\n' "$*" >> %s
exit 0
`, shellQuoteForRun(logPath))
	writeFile(t, path, script)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod fake git: %v", err)
	}
	return path
}

func writeFakeGHForRun(t *testing.T, dir, logPath string) string {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
printf 'gh %%s\n' "$*" >> %s
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
    exit 1
fi
printf 'https://github.com/acme/runfixture/pull/28\n'
`, shellQuoteForRun(logPath))
	writeFile(t, path, script)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod fake gh: %v", err)
	}
	return path
}

// writeFakeLauncherForRun writes a fake Podman execution-box launcher that
// parses `--worktree X [--egress-allowlist Y] [--] cmd...` (the flag shape
// emitted by internal/sandbox/podman), records the worktree and command, and
// execs the command. The default-run containment probe command is /bin/true.
func writeFakeLauncherForRun(t *testing.T, dir, logPath string) string {
	t.Helper()
	path := filepath.Join(dir, "run.sh")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
worktree=""
while [ $# -gt 0 ]; do
    case "$1" in
        --worktree) worktree=$2; shift 2 ;;
        --egress-allowlist) shift 2 ;;
        --) shift; break ;;
        *) break ;;
    esac
done
printf 'worktree=%%s\n' "$worktree" >> %s
printf 'cmd=%%s\n' "$*" >> %s
exec "$@"
`, shellQuoteForRun(logPath), shellQuoteForRun(logPath))
	writeFile(t, path, script)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod fake launcher: %v", err)
	}
	return path
}

func writePassingGateTools(t *testing.T, dir string) {
	t.Helper()
	for _, tool := range []string{"golangci-lint", "gods", "code-scanner"} {
		writeFile(t, filepath.Join(dir, tool), "#!/bin/sh\nexit 0\n")
		if err := os.Chmod(filepath.Join(dir, tool), 0o755); err != nil {
			t.Fatalf("chmod %s: %v", tool, err)
		}
	}
}

func writeGateToolShims(t *testing.T, includeCodeScanner bool) string {
	t.Helper()
	dir := t.TempDir()
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("locate go: %v", err)
	}
	gofmtPath, err := exec.LookPath("gofmt")
	if err != nil {
		t.Fatalf("locate gofmt: %v", err)
	}
	writeFile(t, filepath.Join(dir, "go"), "#!/bin/sh\nexec "+shellQuoteForRun(goPath)+" \"$@\"\n")
	writeFile(t, filepath.Join(dir, "gofmt"), "#!/bin/sh\nexec "+shellQuoteForRun(gofmtPath)+" \"$@\"\n")
	for _, tool := range []string{"golangci-lint", "gods"} {
		writeFile(t, filepath.Join(dir, tool), "#!/bin/sh\nexit 0\n")
	}
	if includeCodeScanner {
		writeFile(t, filepath.Join(dir, "code-scanner"), "#!/bin/sh\nexit 0\n")
	}
	for _, tool := range []string{"go", "gofmt", "golangci-lint", "gods", "code-scanner"} {
		path := filepath.Join(dir, tool)
		if _, err := os.Stat(path); err == nil {
			if err := os.Chmod(path, 0o755); err != nil {
				t.Fatalf("chmod %s: %v", tool, err)
			}
		}
	}
	return dir
}

func runBinaryExactEnv(t *testing.T, binary string, env map[string]string, args ...string) (string, string, int) {
	t.Helper()

	cmd := exec.Command(binary, args...)
	cmd.Env = filteredBaseEnv()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), stderr.String(), exitErr.ExitCode()
	}
	t.Fatalf("run binary %v: %v", args, err)
	return "", "", -1
}

func filteredBaseEnv() []string {
	blocked := map[string]struct{}{
		runtimewiring.EnvTaskRoot:        {},
		runtimewiring.EnvWorktree:        {},
		runtimewiring.EnvClaudeCLI:       {},
		runtimewiring.EnvExecBoxLauncher: {},
		runtimewiring.EnvSandboxRuntime:  {},
		runtimewiring.EnvRunRecord:       {},
		runtimewiring.EnvRunTimeout:      {},
		runtimewiring.EnvMaxAttempts:     {},
		runtimewiring.EnvPublishRemote:   {},
		runtimewiring.EnvGitCLI:          {},
		runtimewiring.EnvGitHubCLI:       {},
		runtimewiring.EnvGitToken:        {},
		runtimewiring.EnvGitHubToken:     {},
		"ANTHROPIC_API_KEY":              {},
	}
	filtered := []string{}
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, skip := blocked[key]; skip {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func readRunRecordEvents(t *testing.T, path string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(readRuntimeText(t, path)), "\n")
	events := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("parse run record line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func assertRunRecordContains(t *testing.T, events []map[string]any, eventType, field, want string) {
	t.Helper()
	for _, event := range events {
		if event["type"] != eventType {
			continue
		}
		if strings.Contains(fmt.Sprint(event[field]), want) {
			return
		}
	}
	t.Fatalf("run record missing %s %s containing %q in %#v", eventType, field, want, events)
}

func lastRunRecordLine(t *testing.T, path string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(readRuntimeText(t, path)), "\n")
	return lines[len(lines)-1]
}

func readRuntimeText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func shellQuoteForRun(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
