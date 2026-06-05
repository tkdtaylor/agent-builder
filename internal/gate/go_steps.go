package gate

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	goBuildStepName = "go build ./..."
	goVetStepName   = "go vet ./..."
	goTestStepName  = "go test ./..."
	goFmtStepName   = "gofmt -l ."
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
