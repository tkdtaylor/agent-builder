// Package gate provides the verification orchestrator. It runs named blocking
// steps in order and returns the structured verdict that defines whether a task
// is complete.
package gate

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrNilStep means a Gate was configured with a nil Step.
	ErrNilStep = errors.New("gate: nil step")

	// ErrBlankStepName means a Step returned an empty or whitespace-only name.
	ErrBlankStepName = errors.New("gate: blank step name")

	// ErrDuplicateStepName means two configured Steps share the same name.
	ErrDuplicateStepName = errors.New("gate: duplicate step name")
)

// Step is one blocking verification check in the gate.
type Step interface {
	Name() string
	Run(repoPath string) StepResult
}

// Blocker is a marker interface that identifies a gate as a real, blocking verifier.
// A Gate that does not implement Blocker (or returns false from Blocks()) is
// considered a pass-through (always-OK) gate and will be rejected at runtime
// by the assembler (task 078).
type Blocker interface {
	Blocks() bool
}

// StepResult is the captured outcome for one verification Step.
type StepResult struct {
	Name     string
	OK       bool
	Output   string
	Duration time.Duration
}

// Verdict is the aggregate result of running the verification gate.
type Verdict struct {
	OK      bool
	Results []StepResult
}

// Gate runs configured verification steps in registration order.
type Gate struct {
	steps []registeredStep
}

type registeredStep struct {
	name string
	step Step
}

// New returns a Gate configured with the supplied ordered steps.
func New(steps ...Step) (*Gate, error) {
	registered := make([]registeredStep, 0, len(steps))
	seen := make(map[string]struct{}, len(steps))

	for _, step := range steps {
		if step == nil {
			return nil, ErrNilStep
		}

		name := strings.TrimSpace(step.Name())
		if name == "" {
			return nil, ErrBlankStepName
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateStepName, name)
		}

		seen[name] = struct{}{}
		registered = append(registered, registeredStep{
			name: name,
			step: step,
		})
	}

	return &Gate{steps: registered}, nil
}

// Verify runs every configured blocking step until all pass or one fails.
func (g *Gate) Verify(repoPath string) Verdict {
	verdict := Verdict{
		OK:      true,
		Results: make([]StepResult, 0, len(g.steps)),
	}

	for _, step := range g.steps {
		start := time.Now()
		result := step.step.Run(repoPath)
		result.Name = step.name
		result.Duration = time.Since(start)

		verdict.Results = append(verdict.Results, result)
		if !result.OK {
			verdict.OK = false
			return verdict
		}
	}

	return verdict
}

// Blocks returns true, indicating this Gate is a real, blocking verifier.
// It implements the Blocker marker interface (task 078).
func (g *Gate) Blocks() bool {
	return true
}
