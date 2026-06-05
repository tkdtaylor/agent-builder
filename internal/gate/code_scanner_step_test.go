package gate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodeScannerCleanWorktreePasses(t *testing.T) {
	// TC-001
	pwdFile := filepath.Join(t.TempDir(), "pwd.txt")
	withFakeCodeScanner(t, "printf 'no malware findings\\n'\nexit 0\n", pwdFile)

	step := CodeScannerStep{}
	if step.Name() != codeScanStepName {
		t.Fatalf("Name() = %q, want %q", step.Name(), codeScanStepName)
	}

	repoPath := codeScannerFixture(t)
	result := step.Run(repoPath)

	if !result.OK {
		t.Fatalf("CodeScannerStep.Run().OK = false, want true; output:\n%s", result.Output)
	}
	assertOutputContains(t, readFile(t, pwdFile), repoPath)
}

func TestCodeScannerFindingFailsWithCapturedOutput(t *testing.T) {
	// TC-002
	withFakeCodeScanner(t, "printf 'MALWARE backdoor loader found\\n'\nprintf 'credential harvest pattern found\\n' >&2\nexit 1\n", "")

	result := CodeScannerStep{}.Run(codeScannerFlaggedFixture(t))

	if result.OK {
		t.Fatal("CodeScannerStep.Run().OK = true, want false")
	}
	assertOutputContains(t, result.Output, "MALWARE backdoor loader")
	assertOutputContains(t, result.Output, "credential harvest pattern")
}

func TestCodeScannerMissingToolIsHardFailure(t *testing.T) {
	// TC-003
	emptyPATH := t.TempDir()
	t.Setenv("PATH", emptyPATH)

	result := CodeScannerStep{}.Run(codeScannerFixture(t))

	if result.OK {
		t.Fatal("CodeScannerStep.Run().OK = true, want false")
	}
	assertOutputContains(t, result.Output, "code-scanner")
	assertOutputContains(t, result.Output, "missing tool")
}

func codeScannerFixture(t *testing.T) string {
	t.Helper()

	path := t.TempDir()
	writeFile(t, filepath.Join(path, "README.md"), "clean fixture\n")

	return path
}

func codeScannerFlaggedFixture(t *testing.T) string {
	t.Helper()

	path := codeScannerFixture(t)
	writeFile(t, filepath.Join(path, "flagged.go"), "package flagged\n\nfunc ReadTokenFile() {}\n")

	return path
}

func withFakeCodeScanner(t *testing.T, body, pwdFile string) {
	t.Helper()

	binDir := t.TempDir()
	script := "#!/bin/sh\n"
	if pwdFile != "" {
		script += "pwd > \"$CODE_SCANNER_PWD_FILE\"\n"
		t.Setenv("CODE_SCANNER_PWD_FILE", pwdFile)
	}
	script += body

	path := filepath.Join(binDir, "code-scanner")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake code-scanner: %v", err)
	}
	t.Setenv("PATH", binDir)
}
