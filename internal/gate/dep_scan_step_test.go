package gate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDepScanCleanModulePasses(t *testing.T) {
	// TC-001
	pwdFile := filepath.Join(t.TempDir(), "pwd.txt")
	withFakeGods(t, "printf 'no high findings\\n'\nexit 0\n", pwdFile)

	step := DepScanStep{}
	if step.Name() != depScanStepName {
		t.Fatalf("Name() = %q, want %q", step.Name(), depScanStepName)
	}

	repoPath := depScanFixture(t)
	result := step.Run(repoPath)

	if !result.OK {
		t.Fatalf("DepScanStep.Run().OK = false, want true; output:\n%s", result.Output)
	}
	assertOutputContains(t, readFile(t, pwdFile), repoPath)
}

func TestDepScanHighSeverityFindingFailsWithCapturedOutput(t *testing.T) {
	// TC-002
	withFakeGods(t, "printf 'CVE-2026-0001 HIGH vulnerable module\\n' >&2\nexit 1\n", "")

	result := DepScanStep{}.Run(depScanFixture(t))

	if result.OK {
		t.Fatal("DepScanStep.Run().OK = true, want false")
	}
	assertOutputContains(t, result.Output, "CVE-2026-0001")
	assertOutputContains(t, result.Output, "HIGH")
}

func TestDepScanMissingToolIsHardFailure(t *testing.T) {
	// TC-003
	emptyPATH := t.TempDir()
	t.Setenv("PATH", emptyPATH)

	result := DepScanStep{}.Run(depScanFixture(t))

	if result.OK {
		t.Fatal("DepScanStep.Run().OK = true, want false")
	}
	assertOutputContains(t, result.Output, "gods")
	assertOutputContains(t, result.Output, "missing tool")
}

func depScanFixture(t *testing.T) string {
	t.Helper()

	path := t.TempDir()
	writeFile(t, filepath.Join(path, "go.mod"), "module example.com/depscanfixture\n\ngo 1.26.3\n")

	return path
}

func withFakeGods(t *testing.T, body, pwdFile string) {
	t.Helper()

	binDir := t.TempDir()
	script := "#!/bin/sh\n"
	if pwdFile != "" {
		script += "pwd > \"$GODS_PWD_FILE\"\n"
		t.Setenv("GODS_PWD_FILE", pwdFile)
	}
	script += body

	path := filepath.Join(binDir, "gods")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake gods: %v", err)
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
