package cli_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/cli"
	"github.com/tkdtaylor/agent-builder/internal/gate"
)

type fakeVerifier struct {
	calls []string
	next  gate.Verdict
}

func (f *fakeVerifier) Verify(repoPath string) gate.Verdict {
	f.calls = append(f.calls, repoPath)
	return f.next
}

func TestVersionPrintsVersion(t *testing.T) {
	// TC-001
	stdout, stderr, code := runCLI(t, cli.Config{
		Args:    []string{"version"},
		Version: "test-version",
	})

	if code != cli.ExitOK {
		t.Fatalf("TC-001 exit code = %d, want %d", code, cli.ExitOK)
	}
	if got, want := stdout, "agent-builder test-version\n"; got != want {
		t.Fatalf("TC-001 stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("TC-001 stderr = %q, want empty", stderr)
	}
}

func TestRunDispatchesInjectedLoop(t *testing.T) {
	// TC-002
	calls := 0
	stdout, stderr, code := runCLI(t, cli.Config{
		Args: []string{"run"},
		Run: func() error {
			calls++
			return nil
		},
	})

	if code != cli.ExitOK {
		t.Fatalf("TC-002 exit code = %d, want %d", code, cli.ExitOK)
	}
	if calls != 1 {
		t.Fatalf("TC-002 run calls = %d, want 1", calls)
	}
	if stdout != "" {
		t.Fatalf("TC-002 stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("TC-002 stderr = %q, want empty", stderr)
	}
}

func TestRunFailureExitsGeneric(t *testing.T) {
	// TC-002
	_, stderr, code := runCLI(t, cli.Config{
		Args: []string{"run"},
		Run: func() error {
			return errors.New("loop failed")
		},
	})

	if code != cli.ExitGeneric {
		t.Fatalf("TC-002 exit code = %d, want %d", code, cli.ExitGeneric)
	}
	if !strings.Contains(stderr, "loop failed") {
		t.Fatalf("TC-002 stderr = %q, want loop failure", stderr)
	}
}

func TestVerifyPassingRepoExitsOK(t *testing.T) {
	// TC-003
	verifier := &fakeVerifier{
		next: gate.Verdict{
			OK: true,
			Results: []gate.StepResult{
				{Name: "go test ./...", OK: true},
			},
		},
	}
	stdout, stderr, code := runCLI(t, cli.Config{
		Args: []string{"verify", "./clean"},
		Gate: verifier,
	})

	if code != cli.ExitOK {
		t.Fatalf("TC-003 exit code = %d, want %d", code, cli.ExitOK)
	}
	if got, want := verifier.calls, []string{"clean"}; !equalStrings(got, want) {
		t.Fatalf("TC-003 verifier calls = %v, want %v", got, want)
	}
	if !strings.Contains(stdout, "PASS go test ./...") {
		t.Fatalf("TC-003 stdout = %q, want passing step", stdout)
	}
	if !strings.Contains(stdout, "verification passed: clean") {
		t.Fatalf("TC-003 stdout = %q, want pass summary", stdout)
	}
	if stderr != "" {
		t.Fatalf("TC-003 stderr = %q, want empty", stderr)
	}
}

func TestVerifyFailingRepoExitsGeneric(t *testing.T) {
	// TC-004
	verifier := &fakeVerifier{
		next: gate.Verdict{
			OK: false,
			Results: []gate.StepResult{
				{Name: "go test ./...", OK: false, Output: "tests failed"},
			},
		},
	}
	stdout, stderr, code := runCLI(t, cli.Config{
		Args: []string{"verify", "dirty"},
		Gate: verifier,
	})

	if code != cli.ExitGeneric {
		t.Fatalf("TC-004 exit code = %d, want %d", code, cli.ExitGeneric)
	}
	if !strings.Contains(stdout, "FAIL go test ./...") || !strings.Contains(stdout, "tests failed") {
		t.Fatalf("TC-004 stdout = %q, want failing step output", stdout)
	}
	if !strings.Contains(stderr, "verification failed: dirty") {
		t.Fatalf("TC-004 stderr = %q, want failure summary", stderr)
	}
}

func TestUnknownSubcommandExitsUsage(t *testing.T) {
	// TC-005
	_, stderr, code := runCLI(t, cli.Config{Args: []string{"bogus"}})

	if code != cli.ExitUsage {
		t.Fatalf("TC-005 exit code = %d, want %d", code, cli.ExitUsage)
	}
	if !strings.Contains(stderr, "unknown subcommand") {
		t.Fatalf("TC-005 stderr = %q, want unknown subcommand usage error", stderr)
	}
}

func TestHelpDocumentsSubcommandsAndExitCodes(t *testing.T) {
	// TC-006
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "short top-level help",
			args: []string{"-h"},
			want: []string{"run", "version", "verify <repo>", "0  success", "1  generic error", "2  usage error"},
		},
		{
			name: "help subcommand",
			args: []string{"help"},
			want: []string{"run", "version", "verify <repo>", "0  success", "1  generic error", "2  usage error"},
		},
		{
			name: "run help",
			args: []string{"run", "-h"},
			want: []string{"Usage: agent-builder run"},
		},
		{
			name: "version help",
			args: []string{"version", "-h"},
			want: []string{"Usage: agent-builder version"},
		},
		{
			name: "verify help",
			args: []string{"verify", "-h"},
			want: []string{"Usage: agent-builder verify <repo>"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runCLI(t, cli.Config{Args: tc.args})
			if code != cli.ExitOK {
				t.Fatalf("TC-006 exit code = %d, want %d", code, cli.ExitOK)
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout, want) {
					t.Fatalf("TC-006 stdout = %q, want %q", stdout, want)
				}
			}
			if stderr != "" {
				t.Fatalf("TC-006 stderr = %q, want empty", stderr)
			}
		})
	}
}

func TestSubcommandFlagParseErrorsExitUsage(t *testing.T) {
	// TC-008
	tests := []struct {
		name string
		args []string
	}{
		{name: "run unknown flag", args: []string{"run", "-bogus"}},
		{name: "version unknown flag", args: []string{"version", "-bogus"}},
		{name: "verify unknown flag", args: []string{"verify", "-bogus", "repo"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runCLI(t, cli.Config{Args: tc.args})
			if code != cli.ExitUsage {
				t.Fatalf("TC-008 exit code = %d, want %d", code, cli.ExitUsage)
			}
			if !strings.Contains(stderr, "flag provided but not defined") {
				t.Fatalf("TC-008 stderr = %q, want flag parse error", stderr)
			}
		})
	}
}

func TestVerifyHelpExposesNoBypassFlag(t *testing.T) {
	// TC-007
	stdout, stderr, code := runCLI(t, cli.Config{Args: []string{"verify", "-h"}})
	if code != cli.ExitOK {
		t.Fatalf("TC-007 exit code = %d, want %d", code, cli.ExitOK)
	}
	if stderr != "" {
		t.Fatalf("TC-007 stderr = %q, want empty", stderr)
	}
	for _, forbidden := range []string{"no-verify", "skip-verify", "skip", "bypass"} {
		if strings.Contains(strings.ToLower(stdout), forbidden) {
			t.Fatalf("TC-007 verify help = %q, contains forbidden %q", stdout, forbidden)
		}
	}
}

func TestMalformedUsageExitsUsage(t *testing.T) {
	// TC-008
	tests := []struct {
		name string
		args []string
	}{
		{name: "no subcommand", args: nil},
		{name: "verify missing repo", args: []string{"verify"}},
		{name: "verify extra repo", args: []string{"verify", "repo", "extra"}},
		{name: "run extra", args: []string{"run", "extra"}},
		{name: "version extra", args: []string{"version", "extra"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runCLI(t, cli.Config{Args: tc.args})
			if code != cli.ExitUsage {
				t.Fatalf("TC-008 exit code = %d, want %d", code, cli.ExitUsage)
			}
			if !strings.Contains(stderr, "usage") {
				t.Fatalf("TC-008 stderr = %q, want usage error", stderr)
			}
		})
	}
}

func TestRuntimeBinaryCLI(t *testing.T) {
	binary := buildBinary(t)
	cleanRepo := writeRepo(t, "clean", false)
	failingRepo := writeRepo(t, "failing", true)
	path := writeToolShims(t)

	t.Run("version", func(t *testing.T) {
		// TC-001
		stdout, stderr, code := runBinary(t, binary, nil, "version")
		t.Logf("agent-builder version: stdout=%q stderr=%q exit=%d", stdout, stderr, code)
		if code != 0 {
			t.Fatalf("TC-001 runtime exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "agent-builder ") {
			t.Fatalf("TC-001 runtime stdout = %q, want version", stdout)
		}
		if stderr != "" {
			t.Fatalf("TC-001 runtime stderr = %q, want empty", stderr)
		}
	})

	t.Run("verify clean", func(t *testing.T) {
		// TC-003
		stdout, stderr, code := runBinary(t, binary, []string{"PATH=" + path}, "verify", cleanRepo)
		t.Logf("agent-builder verify clean: stdout=%q stderr=%q exit=%d", stdout, stderr, code)
		if code != 0 {
			t.Fatalf("TC-003 runtime exit code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "verification passed:") {
			t.Fatalf("TC-003 runtime stdout = %q, want pass summary", stdout)
		}
		if stderr != "" {
			t.Fatalf("TC-003 runtime stderr = %q, want empty", stderr)
		}
	})

	t.Run("verify failing", func(t *testing.T) {
		// TC-004
		stdout, stderr, code := runBinary(t, binary, []string{"PATH=" + path}, "verify", failingRepo)
		t.Logf("agent-builder verify failing: stdout=%q stderr=%q exit=%d", stdout, stderr, code)
		if code != 1 {
			t.Fatalf("TC-004 runtime exit code = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "FAIL go test ./...") {
			t.Fatalf("TC-004 runtime stdout = %q, want failing go test step", stdout)
		}
		if !strings.Contains(stderr, "verification failed:") {
			t.Fatalf("TC-004 runtime stderr = %q, want failure summary", stderr)
		}
	})

	t.Run("bad subcommand", func(t *testing.T) {
		// TC-005
		stdout, stderr, code := runBinary(t, binary, nil, "bogus")
		t.Logf("agent-builder bogus: stdout=%q stderr=%q exit=%d", stdout, stderr, code)
		if code != 2 {
			t.Fatalf("TC-005 runtime exit code = %d, want 2", code)
		}
		if !strings.Contains(stderr, "unknown subcommand") {
			t.Fatalf("TC-005 runtime stderr = %q, want unknown subcommand", stderr)
		}
	})
}

func TestVerifyMissingGateToolFailsBeforeSuccess_TC003(t *testing.T) {
	binary := buildBinary(t)
	repo := writeRepo(t, "missingtool", false)
	// Add go.sum so that dep-scan step is invoked (per ADR 034: no go.sum = pass;
	// go.sum present = invoke scanner). With dep-scan missing, the step must fail.
	writeFile(t, filepath.Join(repo, "go.sum"), "example.com/dep v1.0.0 h1:abc=\nexample.com/dep v1.0.0/go.mod h1:def=\n")
	pathWithHost := writeNamedToolShims(t, map[string]string{
		"go":            "#!/bin/sh\nexit 0\n",
		"gofmt":         "#!/bin/sh\nexit 0\n",
		"golangci-lint": "#!/bin/sh\nexit 0\n",
		"code-scanner":  "#!/bin/sh\nexit 0\n",
	})
	path := strings.Split(pathWithHost, string(os.PathListSeparator))[0]

	stdout, stderr, code := runBinary(t, binary, []string{"PATH=" + path}, "verify", repo)
	t.Logf("agent-builder verify missing dep-scan: stdout=%q stderr=%q exit=%d", stdout, stderr, code)

	if code != 1 {
		t.Fatalf("TC-003 runtime exit code = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "PASS go build ./...") ||
		!strings.Contains(stdout, "PASS go vet ./...") ||
		!strings.Contains(stdout, "PASS go test ./...") ||
		!strings.Contains(stdout, "PASS gofmt -l .") ||
		!strings.Contains(stdout, "PASS golangci-lint run") {
		t.Fatalf("TC-003 stdout = %q, want earlier Gate steps to pass", stdout)
	}
	if !strings.Contains(stdout, "FAIL dep-scan") || !strings.Contains(stdout, "missing tool") {
		t.Fatalf("TC-003 stdout = %q, want missing dep-scan failure", stdout)
	}
	if strings.Contains(stdout, "verification passed:") {
		t.Fatalf("TC-003 stdout = %q, must not report verification success", stdout)
	}
	if !strings.Contains(stderr, "verification failed:") {
		t.Fatalf("TC-003 stderr = %q, want failure summary", stderr)
	}
}

func runCLI(t *testing.T, config cli.Config) (string, string, int) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	config.Stdout = &stdout
	config.Stderr = &stderr

	code := cli.Main(config)
	return stdout.String(), stderr.String(), code
}

func buildBinary(t *testing.T) string {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "agent-builder")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/agent-builder")
	cmd.Dir = repoRoot(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build runtime binary: %v\n%s", err, output)
	}
	return binary
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate current test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func writeRepo(t *testing.T, name string, failing bool) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), name)
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/"+name+"\n\ngo 1.26.3\n")
	writeFile(t, filepath.Join(dir, name+".go"), "package "+name+"\n\nfunc Value() int { return 1 }\n")
	if failing {
		writeFile(t, filepath.Join(dir, name+"_test.go"), "package "+name+"\n\nimport \"testing\"\n\nfunc TestFailure(t *testing.T) { t.Fatal(\"intentional failure\") }\n")
	}
	return dir
}

func writeToolShims(t *testing.T) string {
	t.Helper()

	return writeNamedToolShims(t, map[string]string{
		"golangci-lint": "#!/bin/sh\nexit 0\n",
		"dep-scan":      "#!/bin/sh\nexit 0\n",
		"code-scanner":  "#!/bin/sh\nexit 0\n",
	})
}

func writeNamedToolShims(t *testing.T, tools map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for tool, script := range tools {
		writeFile(t, filepath.Join(dir, tool), script)
		if err := os.Chmod(filepath.Join(dir, tool), 0o755); err != nil {
			t.Fatalf("chmod shim %s: %v", tool, err)
		}
	}
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

func runBinary(t *testing.T, binary string, env []string, args ...string) (string, string, int) {
	t.Helper()

	cmd := exec.Command(binary, args...)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, env...)
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

func writeFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
