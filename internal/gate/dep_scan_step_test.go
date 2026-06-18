package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDepScanNoGoSumPassesWithoutInvokingScanner(t *testing.T) {
	// TC-061-01: a module with no go.sum has no third-party deps; the step
	// passes without invoking the scanner — even with an empty PATH (so this is
	// NOT a missing-tool failure).
	emptyPATH := t.TempDir()
	t.Setenv("PATH", emptyPATH)

	step := DepScanStep{}
	if step.Name() != depScanStepName {
		t.Fatalf("Name() = %q, want %q", step.Name(), depScanStepName)
	}

	result := step.Run(depScanFixtureNoSum(t))

	if !result.OK {
		t.Fatalf("DepScanStep.Run().OK = false, want true; output:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "missing tool") {
		t.Fatalf("no-go.sum path must not be a missing-tool failure; output:\n%s", result.Output)
	}
}

func TestDepScanWithGoSumInvokesDepScanWithCorrectArgs(t *testing.T) {
	// TC-061-02
	argvFile := filepath.Join(t.TempDir(), "argv.txt")
	withFakeDepScan(t, "printf 'no findings\\n'\nexit 0\n", argvFile)

	repoPath := depScanFixtureWithSum(t)
	result := DepScanStep{}.Run(repoPath)

	if !result.OK {
		t.Fatalf("DepScanStep.Run().OK = false, want true; output:\n%s", result.Output)
	}

	recorded := readFile(t, argvFile)
	// First line is the working directory; remaining lines are argv.
	lines := strings.Split(strings.TrimRight(recorded, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("fake dep-scan recorded too little:\n%s", recorded)
	}
	if lines[0] != repoPath {
		t.Fatalf("dep-scan cwd = %q, want repoPath %q", lines[0], repoPath)
	}
	gotArgs := strings.Join(lines[1:], " ")
	wantArgs := "check --registry go --lockfile go.sum --lockfile-type go"
	if gotArgs != wantArgs {
		t.Fatalf("dep-scan args = %q, want %q", gotArgs, wantArgs)
	}
}

func TestDepScanHighSeverityFindingFailsWithCapturedOutput(t *testing.T) {
	// TC-061-03
	withFakeDepScan(t, "printf 'CVE-2026-0001 HIGH vulnerable module\\n' >&2\nexit 1\n", "")

	result := DepScanStep{}.Run(depScanFixtureWithSum(t))

	if result.OK {
		t.Fatal("DepScanStep.Run().OK = true, want false")
	}
	assertOutputContains(t, result.Output, "CVE-2026-0001")
	assertOutputContains(t, result.Output, "HIGH")
}

func TestDepScanMissingToolWithGoSumIsHardFailure(t *testing.T) {
	// TC-061-04: go.sum present but dep-scan absent is a configuration error.
	emptyPATH := t.TempDir()
	t.Setenv("PATH", emptyPATH)

	result := DepScanStep{}.Run(depScanFixtureWithSum(t))

	if result.OK {
		t.Fatal("DepScanStep.Run().OK = true, want false")
	}
	assertOutputContains(t, result.Output, "dep-scan")
	assertOutputContains(t, result.Output, "missing tool")
}

func depScanFixtureNoSum(t *testing.T) string {
	t.Helper()

	path := t.TempDir()
	writeFile(t, filepath.Join(path, "go.mod"), "module example.com/depscanfixture\n\ngo 1.26.3\n")

	return path
}

func depScanFixtureWithSum(t *testing.T) string {
	t.Helper()

	path := depScanFixtureNoSum(t)
	writeFile(t, filepath.Join(path, "go.sum"), "example.com/dep v1.0.0 h1:abc=\nexample.com/dep v1.0.0/go.mod h1:def=\n")

	return path
}

func withFakeDepScan(t *testing.T, body, argvFile string) {
	t.Helper()

	binDir := t.TempDir()
	script := "#!/bin/sh\n"
	if argvFile != "" {
		script += "{ pwd; for a in \"$@\"; do printf '%s\\n' \"$a\"; done; } > \"$DEPSCAN_ARGV_FILE\"\n"
		t.Setenv("DEPSCAN_ARGV_FILE", argvFile)
	}
	script += body

	path := filepath.Join(binDir, "dep-scan")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake dep-scan: %v", err)
	}
	t.Setenv("PATH", binDir)
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(contents)
}
