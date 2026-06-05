package gate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGolangciLintCleanRepoPasses(t *testing.T) {
	// TC-001
	requireGolangciLint(t)

	step := GolangciLintStep{}
	if step.Name() != goLintStepName {
		t.Fatalf("Name() = %q, want %q", step.Name(), goLintStepName)
	}

	result := step.Run(golangciLintCleanFixture(t))

	if !result.OK {
		t.Fatalf("GolangciLintStep.Run().OK = false, want true; output:\n%s", result.Output)
	}
}

func TestGolangciLintViolationFailsWithCapturedOutput(t *testing.T) {
	// TC-002
	requireGolangciLint(t)

	result := GolangciLintStep{}.Run(golangciLintViolationFixture(t))

	if result.OK {
		t.Fatal("GolangciLintStep.Run().OK = true, want false")
	}
	assertOutputContains(t, result.Output, "violation.go")
	assertOutputContains(t, result.Output, "errcheck")
}

func TestGolangciLintMissingToolIsHardFailure(t *testing.T) {
	// TC-003
	emptyPATH := t.TempDir()
	t.Setenv("PATH", emptyPATH)

	result := GolangciLintStep{}.Run(golangciLintCleanFixture(t))

	if result.OK {
		t.Fatal("GolangciLintStep.Run().OK = true, want false")
	}
	assertOutputContains(t, result.Output, "golangci-lint")
	assertOutputContains(t, result.Output, "missing tool")
}

func requireGolangciLint(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("golangci-lint"); err == nil {
		return
	}

	const toolCache = "/tmp/agent-builder-tools"
	if _, err := os.Stat(filepath.Join(toolCache, "golangci-lint")); err == nil {
		t.Setenv("PATH", toolCache+string(os.PathListSeparator)+os.Getenv("PATH"))
		return
	}

	t.Skip("golangci-lint not available on PATH or in /tmp/agent-builder-tools")
}

func golangciLintCleanFixture(t *testing.T) string {
	t.Helper()

	path := golangciLintFixture(t)
	writeFile(t, filepath.Join(path, "clean.go"), strings.TrimSpace(`
package lintfixture

func Message() string {
	return "clean"
}
`)+"\n")

	return path
}

func golangciLintViolationFixture(t *testing.T) string {
	t.Helper()

	path := golangciLintFixture(t)
	writeFile(t, filepath.Join(path, "violation.go"), strings.TrimSpace(`
package lintfixture

import "os"

func IgnoredError() {
	os.Chdir("/")
}
`)+"\n")

	return path
}

func golangciLintFixture(t *testing.T) string {
	t.Helper()

	path := t.TempDir()
	writeFile(t, filepath.Join(path, "go.mod"), "module example.com/lintfixture\n\ngo 1.26.3\n")
	writeFile(t, filepath.Join(path, ".golangci.yml"), strings.TrimSpace(`
version: "2"
linters:
  enable:
    - errcheck
`)+"\n")

	return path
}
