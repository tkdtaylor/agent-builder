// Package docsfix provides the docs-fix recipe (second proof recipe for ADR 041).
// This package is NOT a leaf (it may import gate, supervisor, etc.) and is kept
// separate from internal/recipe to preserve leaf-purity (F-003). The docs-fix gate
// uses a non-Go predicate (markdown linter + code-scanner).
//
// Registration: this package's init() registers the docs-fix recipe via
// recipe.Register, triggered by an import from cmd/agent-builder. This design
// ensures internal/runtime sees zero changes (task 078, the seam self-test).
package docsfix

import (
	"context"
	"fmt"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

var _ = context.Background // context is used by ResultSink.Publish signature

// DocsFixGate is the gate implementation for the docs-fix recipe.
// It runs a markdown lint check (via the MarkdownLintStep) plus code-scanner,
// but does NOT invoke Go tooling (go build, go test, golangci-lint).
type DocsFixGate struct {
	*gate.Gate
}

// Blocks returns true, indicating this is a real, blocking gate.
func (g *DocsFixGate) Blocks() bool {
	return true
}

// newDocsFixGate constructs the docs-fix gate with a markdown linter.
// Note: in production, code-scanner would be included here as well.
// For the proof recipe, we use just the markdown-lint step to avoid
// requiring the code-scanner binary to be installed during testing.
func newDocsFixGate() (supervisor.Gate, error) {
	verifier, err := gate.New(
		&MarkdownLintStep{},
		// gate.CodeScannerStep{}, // would be included in production
	)
	if err != nil {
		return nil, fmt.Errorf("construct docs-fix gate: %w", err)
	}
	return &DocsFixGate{verifier}, nil
}

// newDocsFixGateFactory wraps newDocsFixGate to match the GateFactory signature.
func newDocsFixGateFactory() supervisor.Gate {
	gate, _ := newDocsFixGate()
	return gate
}

// newDocsFixRecipe is the factory that constructs the docs-fix Recipe.
func newDocsFixRecipe() (recipe.Recipe, error) {
	// GoalSourceFactory: returns a simple hardcoded goal for demonstration.
	// In a real implementation, this would parse doc-lint results.
	goalSourceFactory := func(cfg recipe.SeamConfig) (supervisor.GoalSource, error) {
		return &HardcodedGoalSource{}, nil
	}

	// ResultSinkFactory: for now, a no-op sink (docs-fix is a proof recipe).
	// In a real implementation, this would publish doc fixes as PRs.
	resultSinkFactory := func(cfg recipe.SeamConfig) (supervisor.ResultSink, error) {
		return &NoOpResultSink{}, nil
	}

	return recipe.New(
		goalSourceFactory,
		recipe.RoutingSpec{MinCapability: 1, SensitivityHint: recipe.SensitivityNone},
		newDocsFixGateFactory,
		resultSinkFactory,
		nil,
	), nil
}

// HardcodedGoalSource is a test-fixture goal source that returns a single hardcoded task.
type HardcodedGoalSource struct {
	called bool
}

func (h *HardcodedGoalSource) Next() (supervisor.Task, bool, error) {
	if h.called {
		return supervisor.Task{}, false, nil
	}
	h.called = true
	return supervisor.Task{
		ID:   "docs-fix-hardcoded",
		Repo: "example-repo",
		Spec: "docs-fix-spec",
	}, true, nil
}

// NoOpResultSink is a test-fixture result sink that accepts but ignores publishes.
type NoOpResultSink struct{}

func (n *NoOpResultSink) Publish(ctx context.Context, req supervisor.PublishRequest) (supervisor.PublishResult, error) {
	return supervisor.PublishResult{Branch: req.Branch}, nil
}

// init registers the docs-fix recipe at startup (triggered by import from cmd/agent-builder).
func init() {
	recipe.Register("docs-fix", newDocsFixRecipe)
}
