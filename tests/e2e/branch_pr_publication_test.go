package e2e_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
)

func TestBranchPRPublication(t *testing.T) {
	binary := buildAgentBuilder(t)
	fixture := newPublicationFixture(t, publicationFixtureConfig{})

	stdout, stderr, code := runAgentBuilder(t, binary, fixture.env(), "run")
	if code != 0 {
		t.Fatalf("TC-001 run exit code = %d, want 0; stdout=%q stderr=%q record=%q", code, stdout, stderr, readOptional(t, fixture.recordPath))
	}
	if !strings.Contains(stdout, "run completed: task 001") {
		t.Fatalf("TC-001 stdout = %q, want completed run", stdout)
	}
	publishLog := readFile(t, fixture.publishLog)
	if !strings.Contains(publishLog, "git push origin task/034-branch-pr-publication") ||
		!strings.Contains(publishLog, "gh pr create --head task/034-branch-pr-publication --fill") {
		t.Fatalf("TC-001 publish log = %q, want push and PR create", publishLog)
	}
	events := readEvents(t, fixture.recordPath)
	assertEventContains(t, events, "stdout", "data", "publication recorded: branch=task/034-branch-pr-publication pr=https://github.com/acme/e2e/pull/34")
	assertEventContains(t, events, "run_finished", "outcome", "completed")
	assertNoSecret(t, "TC-004 stdout", stdout, fixture.gitToken, fixture.ghToken)
	assertNoSecret(t, "TC-004 stderr", stderr, fixture.gitToken, fixture.ghToken)
	assertNoSecret(t, "TC-004 run record", readFile(t, fixture.recordPath), fixture.gitToken, fixture.ghToken)
	t.Log("TC-001 verified branch published as PR artifact")

	t.Run("TC-002 executor failure blocks publisher", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{claudeExit: 17})
		_, _, code := runAgentBuilder(t, binary, fixture.env(), "run")
		if code == 0 {
			t.Fatalf("TC-002 executor failure exit code = 0, want failure")
		}
		assertNoPublishLog(t, fixture.publishLog)
	})

	t.Run("TC-002 blank branch blocks publisher", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{blankBranch: true})
		_, _, code := runAgentBuilder(t, binary, fixture.env(), "run")
		if code == 0 {
			t.Fatalf("TC-002 blank branch exit code = 0, want failure")
		}
		assertNoPublishLog(t, fixture.publishLog)
	})

	t.Run("TC-002 gate failure blocks publisher", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{gateFails: true})
		_, _, code := runAgentBuilder(t, binary, fixture.env(), "run")
		if code == 0 {
			t.Fatalf("TC-002 gate failure exit code = 0, want failure")
		}
		assertNoPublishLog(t, fixture.publishLog)
	})
}

func TestPublisherFailureDoesNotMarkDone(t *testing.T) {
	binary := buildAgentBuilder(t)
	fixture := newPublicationFixture(t, publicationFixtureConfig{publishFails: true})

	stdout, stderr, code := runAgentBuilder(t, binary, fixture.env(), "run")
	if code == 0 {
		t.Fatalf("TC-003 run exit code = 0, want publication failure; stdout=%q stderr=%q", stdout, stderr)
	}
	if strings.Contains(stdout, "run completed") {
		t.Fatalf("TC-003 stdout = %q, should not mark run completed", stdout)
	}
	if !strings.Contains(stderr, "publication failed") && !strings.Contains(stderr, "publish task 001") {
		t.Fatalf("TC-003 stderr = %q, want publication failure surfaced", stderr)
	}
	taskFile := readFile(t, filepath.Join(fixture.taskRoot, "docs/tasks/backlog/001-first.md"))
	if strings.Contains(taskFile, "**Status:** done") {
		t.Fatalf("TC-003 task file was marked done:\n%s", taskFile)
	}
	record := readFile(t, fixture.recordPath)
	if !strings.Contains(record, `"outcome":"failed"`) || !strings.Contains(record, "publication failed") {
		t.Fatalf("TC-003 run record = %q, want failed publication evidence", record)
	}
	assertNoSecret(t, "TC-004 stderr", stderr, fixture.gitToken, fixture.ghToken)
	assertNoSecret(t, "TC-004 run record", record, fixture.gitToken, fixture.ghToken)
	if !strings.Contains(stderr+record, "[REDACTED]") {
		t.Fatalf("TC-004 publication failure did not include redaction marker; stderr=%q record=%q", stderr, record)
	}
	t.Log("TC-003 publisher failure preserved task as not done")
	t.Log("TC-004 publication secrets redacted from CLI and run record")
}

type publicationFixtureConfig struct {
	claudeExit   int
	blankBranch  bool
	gateFails    bool
	publishFails bool
}

type publicationFixture struct {
	taskRoot     string
	worktree     string
	shimDir      string
	claudePath   string
	launcherPath string
	gitPath      string
	ghPath       string
	publishLog   string
	recordPath   string
	gitToken     string
	ghToken      string
}

func newPublicationFixture(t *testing.T, config publicationFixtureConfig) publicationFixture {
	t.Helper()
	root := t.TempDir()
	taskRoot := filepath.Join(root, "tasks")
	worktree := filepath.Join(root, "worktree")
	shimDir := filepath.Join(root, "bin")
	publishLog := filepath.Join(root, "publish.log")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatalf("mkdir shim dir: %v", err)
	}

	writeFile(t, filepath.Join(taskRoot, "docs/plans/roadmap.md"), "# Roadmap\n")
	writeFile(t, filepath.Join(taskRoot, "docs/tasks/backlog/001-first.md"), `# Task 001: first

**Project:** agent-builder
**Created:** 2026-06-05
**Status:** ready

## Goal
Fixture task.
`)
	writeFile(t, filepath.Join(worktree, "go.mod"), "module example.com/branchpr\n\ngo 1.26.3\n")
	writeFile(t, filepath.Join(worktree, "branchpr.go"), "package branchpr\n\nfunc Value() int { return 34 }\n")
	writeFile(t, filepath.Join(worktree, "branchpr_test.go"), `package branchpr

import "testing"

func TestValue(t *testing.T) {
	if Value() != 34 {
		t.Fatal("bad")
	}
}
`)

	fixture := publicationFixture{
		taskRoot:   taskRoot,
		worktree:   worktree,
		shimDir:    shimDir,
		publishLog: publishLog,
		recordPath: filepath.Join(root, "run-record.ndjson"),
		gitToken:   "git-token-034",
		ghToken:    "gh-token-034",
	}
	fixture.claudePath = writeFakeClaude(t, shimDir, config)
	fixture.launcherPath = writeFakeLauncher(t, shimDir)
	fixture.gitPath = writeFakeGit(t, shimDir, publishLog)
	fixture.ghPath = writeFakeGH(t, shimDir, publishLog, config)
	writeGateTools(t, shimDir, config.gateFails)
	return fixture
}

func (f publicationFixture) env() map[string]string {
	return map[string]string{
		"PATH":                           f.shimDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		runtimewiring.EnvTaskRoot:        f.taskRoot,
		runtimewiring.EnvWorktree:        f.worktree,
		runtimewiring.EnvClaudeCLI:       f.claudePath,
		runtimewiring.EnvExecBoxLauncher: f.launcherPath,
		runtimewiring.EnvRunRecord:       f.recordPath,
		runtimewiring.EnvRunTimeout:      "5s",
		runtimewiring.EnvMaxAttempts:     "1",
		runtimewiring.EnvPublishRemote:   "origin",
		runtimewiring.EnvGitCLI:          f.gitPath,
		runtimewiring.EnvGitHubCLI:       f.ghPath,
		runtimewiring.EnvGitToken:        f.gitToken,
		runtimewiring.EnvGitHubToken:     f.ghToken,
		"ANTHROPIC_API_KEY":              "anthropic-token-034",
	}
}

func buildAgentBuilder(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "agent-builder")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/agent-builder")
	cmd.Dir = filepath.Join("..", "..")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build agent-builder: %v\n%s", err, stderr.String())
	}
	return binary
}

func runAgentBuilder(t *testing.T, binary string, env map[string]string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = filteredEnv()
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
	t.Fatalf("run agent-builder: %v", err)
	return "", "", -1
}

func filteredEnv() []string {
	blocked := map[string]struct{}{
		runtimewiring.EnvTaskRoot:        {},
		runtimewiring.EnvWorktree:        {},
		runtimewiring.EnvClaudeCLI:       {},
		runtimewiring.EnvExecBoxLauncher: {},
		runtimewiring.EnvSandboxRuntime:  {},
		runtimewiring.EnvRunRecord:       {},
		runtimewiring.EnvAuditRecord:     {},
		runtimewiring.EnvAuditBin:        {},
		runtimewiring.EnvRunTimeout:      {},
		runtimewiring.EnvMaxAttempts:     {},
		runtimewiring.EnvPublishRemote:   {},
		runtimewiring.EnvGitCLI:          {},
		runtimewiring.EnvGitHubCLI:       {},
		runtimewiring.EnvGitToken:        {},
		runtimewiring.EnvGitHubToken:     {},
		// Executor credentials (ADR 033): block ambient values so the fixture
		// forwards exactly what its env map sets, not whatever the shell exports.
		"ANTHROPIC_API_KEY":       {},
		"CLAUDE_CODE_OAUTH_TOKEN": {},
		// TC-057-03: Block live test gating flags so the gate (go test ./...) can
		// safely set them without causing recursion when the binary runs go test internally.
		// The outer test gates whether to run (e.g., AGENT_BUILDER_LIVE_E2E=1), but the
		// binary never needs or should receive these flags.
		"AGENT_BUILDER_LIVE_E2E":     {},
		"AGENT_BUILDER_LIVE_PUBLISH": {},
		"AGENT_BUILDER_LIVE_PODMAN":  {},
		"AGENT_BUILDER_LIVE_SRT":     {},
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

func writeFakeClaude(t *testing.T, dir string, config publicationFixtureConfig) string {
	t.Helper()
	path := filepath.Join(dir, "claude")
	branch := "task/034-branch-pr-publication"
	if config.blankBranch {
		branch = "   "
	}
	script := fmt.Sprintf(`#!/bin/sh
set -eu
if [ %d -ne 0 ]; then
	exit %d
fi
prompt=$2
branch_file=""
next_branch=0
while IFS= read -r line; do
	case "$line" in
		"When finished, write only the produced branch name to this file:") next_branch=1 ;;
		*) if [ "$next_branch" = "1" ]; then branch_file=$line; next_branch=0; fi ;;
	esac
done <<EOF
$prompt
EOF
printf '%s\n' > "$branch_file"
`, config.claudeExit, config.claudeExit, branch)
	writeFile(t, path, script)
	chmodExecutable(t, path)
	return path
}

// writeFakeLauncher writes a fake Podman execution-box launcher that parses the
// `--worktree X [--egress-allowlist Y] [--] cmd...` flag shape emitted by
// internal/sandbox/podman and execs the wrapped command UNDER /bin/sh, modelling
// the real execution-box image's ENTRYPOINT ["/bin/sh"] (ADR 032) — so commands
// must be sh-compatible (e.g. `-c true`), exactly as against the real image.
func writeFakeLauncher(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "run.sh")
	writeFile(t, path, `#!/bin/sh
set -eu
while [ $# -gt 0 ]; do
	case "$1" in
		--worktree) shift 2 ;;
		--egress-allowlist) shift 2 ;;
		--) shift; break ;;
		*) break ;;
	esac
done
exec /bin/sh "$@"
`)
	chmodExecutable(t, path)
	return path
}

func writeFakeGit(t *testing.T, dir, logPath string) string {
	t.Helper()
	path := filepath.Join(dir, "git")
	writeFile(t, path, fmt.Sprintf("#!/bin/sh\nset -eu\nprintf 'git %%s\\n' \"$*\" >> %s\nexit 0\n", shellQuote(logPath)))
	chmodExecutable(t, path)
	return path
}

func writeFakeGH(t *testing.T, dir, logPath string, config publicationFixtureConfig) string {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
printf 'gh %%s\n' "$*" >> %s
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
	exit 1
fi
if [ "%t" = "true" ]; then
	printf 'stdout %%s\n' "$GH_TOKEN"
	printf 'stderr %%s\n' "$GITHUB_TOKEN" >&2
	exit 42
fi
printf 'https://github.com/acme/e2e/pull/34\n'
`, shellQuote(logPath), config.publishFails)
	writeFile(t, path, script)
	chmodExecutable(t, path)
	return path
}

func writeGateTools(t *testing.T, dir string, gateFails bool) {
	t.Helper()
	for _, tool := range []string{"golangci-lint", "dep-scan", "code-scanner"} {
		exit := "0"
		if tool == "code-scanner" && gateFails {
			exit = "23"
		}
		path := filepath.Join(dir, tool)
		writeFile(t, path, "#!/bin/sh\nexit "+exit+"\n")
		chmodExecutable(t, path)
	}
}

func assertNoPublishLog(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("read publish log: %v", err)
	}
	if strings.TrimSpace(string(data)) != "" {
		t.Fatalf("TC-002 publisher was called before prerequisites: %q", string(data))
	}
}

func readEvents(t *testing.T, path string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(readFile(t, path)), "\n")
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

func assertEventContains(t *testing.T, events []map[string]any, eventType, field, want string) {
	t.Helper()
	for _, event := range events {
		if event["type"] == eventType && strings.Contains(fmt.Sprint(event[field]), want) {
			return
		}
	}
	t.Fatalf("run record missing %s %s containing %q in %#v", eventType, field, want, events)
}

func assertNoSecret(t *testing.T, label, text string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatalf("%s leaked secret %q in %q", label, secret, text)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func chmodExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func readOptional(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
