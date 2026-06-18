package gate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	goBuildStepName  = "go build ./..."
	goVetStepName    = "go vet ./..."
	goTestStepName   = "go test ./..."
	goFmtStepName    = "gofmt -l ."
	goLintStepName   = "golangci-lint run"
	depScanStepName  = "dep-scan"
	codeScanStepName = "code-scanner"

	// depScanTool is the supply-chain scanner the gate invokes directly. We do
	// not use the `gods` go-wrapper here: with no package arguments it skips the
	// scan and falls through to a bare `go`, which exits non-zero (ADR 034).
	depScanTool = "dep-scan"
)

// GoBuildStep runs go build across every package in a target worktree.
type GoBuildStep struct{}

func (GoBuildStep) Name() string {
	return goBuildStepName
}

func (GoBuildStep) Run(repoPath string) StepResult {
	return runCommandStep(repoPath, "go", "build", "./...")
}

// GoVetStep runs go vet across every package in a target worktree.
type GoVetStep struct{}

func (GoVetStep) Name() string {
	return goVetStepName
}

func (GoVetStep) Run(repoPath string) StepResult {
	return runCommandStep(repoPath, "go", "vet", "./...")
}

// GoTestStep runs go test across every package in a target worktree.
type GoTestStep struct{}

func (GoTestStep) Name() string {
	return goTestStepName
}

func (GoTestStep) Run(repoPath string) StepResult {
	return runCommandStep(repoPath, "go", "test", "./...")
}

// GoFmtStep fails when gofmt reports any unformatted Go source files.
type GoFmtStep struct{}

func (GoFmtStep) Name() string {
	return goFmtStepName
}

func (GoFmtStep) Run(repoPath string) StepResult {
	result := runCommandStep(repoPath, "gofmt", "-l", ".")
	if result.OK && strings.TrimSpace(result.Output) != "" {
		result.OK = false
	}

	return result
}

// GolangciLintStep runs golangci-lint in a target worktree.
type GolangciLintStep struct{}

func (GolangciLintStep) Name() string {
	return goLintStepName
}

func (GolangciLintStep) Run(repoPath string) StepResult {
	return runCommandStep(repoPath, "golangci-lint", "run")
}

// DepScanStep runs the Go dependency CVE scanner in a target worktree.
type DepScanStep struct{}

func (DepScanStep) Name() string {
	return depScanStepName
}

func (DepScanStep) Run(repoPath string) StepResult {
	// A module with no go.sum has no third-party dependencies, so there is
	// nothing to scan. Go requires a go.sum for any `require`, so its absence
	// reliably means "no external deps" — pass without invoking the scanner
	// (ADR 034). This is the agent-builder case (no deps).
	if _, err := os.Stat(filepath.Join(repoPath, "go.sum")); err != nil {
		if os.IsNotExist(err) {
			return StepResult{
				OK:     true,
				Output: "no go.sum: module has no third-party dependencies; nothing to scan",
			}
		}
		return StepResult{OK: false, Output: fmt.Sprintf("stat go.sum: %v", err)}
	}

	// go.sum present: scan its locked dependencies with dep-scan directly.
	return runCommandStep(repoPath, depScanTool,
		"check", "--registry", "go", "--lockfile", "go.sum", "--lockfile-type", "go")
}

// CodeScannerStep runs the malware/backdoor scanner in a target worktree.
type CodeScannerStep struct{}

func (CodeScannerStep) Name() string {
	return codeScanStepName
}

func (CodeScannerStep) Run(repoPath string) StepResult {
	return runCommandStep(repoPath, "code-scanner")
}

func runCommandStep(repoPath, tool string, args ...string) StepResult {
	if _, err := exec.LookPath(tool); err != nil {
		return StepResult{
			OK:     false,
			Output: fmt.Sprintf("missing tool %q on PATH: %v", tool, err),
		}
	}

	cmd := exec.Command(tool, args...)
	cmd.Dir = repoPath

	output, err := cmd.CombinedOutput()
	result := StepResult{
		OK:     err == nil,
		Output: string(output),
	}
	if err != nil && result.Output == "" {
		result.Output = err.Error()
	}

	return result
}
