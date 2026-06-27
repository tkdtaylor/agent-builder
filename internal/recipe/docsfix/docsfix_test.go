package docsfix

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
)

// TestTC079_01_SelectRecipeDocsFixReturnsNonNilRecipe tests TC-079-01:
// SelectRecipe("docs-fix") returns a non-nil Recipe with a non-Go gate type,
// and ListRecipes includes "docs-fix".
func TestTC079_01_SelectRecipeDocsFixReturnsNonNilRecipe(t *testing.T) {
	// Select the docs-fix recipe
	r, err := recipe.SelectRecipe("docs-fix")
	if err != nil {
		t.Fatalf("SelectRecipe(\"docs-fix\") failed: %v", err)
	}

	// Verify non-nil Recipe
	if r.GoalSourceFactory == nil {
		t.Error("GoalSourceFactory is nil")
	}
	if r.GateFactory == nil {
		t.Error("GateFactory is nil")
	}
	if r.ResultSinkFactory == nil {
		t.Error("ResultSinkFactory is nil")
	}

	// Verify Name field equals "docs-fix"
	if r.Name != "docs-fix" {
		t.Errorf("Name = %q, want \"docs-fix\"", r.Name)
	}

	// Get the gate and verify its type is distinct from the production gate
	g := r.GateFactory()
	if g == nil {
		t.Fatal("GateFactory() returned nil")
	}

	// The docs-fix gate should be a *DocsFixGate, which is distinct from
	// the production gate type (which is *gate.Gate wrapped differently).
	docsFixGate, ok := g.(*DocsFixGate)
	if !ok {
		t.Errorf("gate type = %T, want *DocsFixGate", g)
		docsFixGate = nil // for linting
	}

	// Verify it is a real Blocker
	blocker, ok := g.(gate.Blocker)
	if !ok {
		t.Errorf("gate does not implement Blocker interface")
	}
	if !blocker.Blocks() {
		t.Errorf("gate.Blocks() = false, want true")
	}

	// Verify ListRecipes includes "docs-fix"
	recipes := recipe.ListRecipes()
	found := false
	for _, name := range recipes {
		if name == "docs-fix" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("\"docs-fix\" not found in ListRecipes(): %v", recipes)
	}

	// Use docsFixGate to avoid unused error (it's checked above)
	_ = docsFixGate
}

// TestTC079_02_DocsFixGatePassesWellFormedMarkdown tests TC-079-02:
// The docs-fix gate returns PASS on a well-formed .md fixture.
func TestTC079_02_DocsFixGatePassesWellFormedMarkdown(t *testing.T) {
	// Create a temporary directory with a well-formed markdown file
	tmpDir := t.TempDir()

	wellFormedFile := filepath.Join(tmpDir, "README.md")
	wellFormedContent := `# Valid Markdown

This is a well-formed markdown file.

## Section 2

No issues here. [Valid link](https://example.com)
`
	if err := os.WriteFile(wellFormedFile, []byte(wellFormedContent), 0o644); err != nil {
		t.Fatalf("failed to write test markdown file: %v", err)
	}

	// Create the docs-fix gate
	g := newDocsFixGateFactory()
	if g == nil {
		t.Fatal("newDocsFixGateFactory() returned nil")
	}

	// Verify the gate
	verdict := g.Verify(tmpDir)

	if !verdict.OK {
		t.Errorf("Verdict.OK = %v, want true", verdict.OK)
	}

	// Verify that at least the markdown lint step passed
	if len(verdict.Results) == 0 {
		t.Error("expected at least one step result")
	}

	// Find the markdown-lint step and verify it passed
	markdownLintPassed := false
	for _, result := range verdict.Results {
		if result.Name == "markdown-lint" {
			if !result.OK {
				t.Errorf("markdown-lint step failed: %v", result.Output)
			}
			markdownLintPassed = true
		}
	}
	if !markdownLintPassed {
		t.Error("markdown-lint step not found in verdict results")
	}
}

// TestTC079_02_DocsFixGateFailsMalformedMarkdown tests TC-079-02:
// The docs-fix gate returns FAIL on a malformed .md fixture with a broken link.
func TestTC079_02_DocsFixGateFailsMalformedMarkdown(t *testing.T) {
	// Create a temporary directory with a malformed markdown file
	tmpDir := t.TempDir()

	malformedFile := filepath.Join(tmpDir, "bad.md")
	malformedContent := `# Bad Markdown

This markdown has a broken link: [broken](http://localhost:99999)

That should fail.
`
	if err := os.WriteFile(malformedFile, []byte(malformedContent), 0o644); err != nil {
		t.Fatalf("failed to write malformed markdown file: %v", err)
	}

	// Create the docs-fix gate
	g := newDocsFixGateFactory()
	if g == nil {
		t.Fatal("newDocsFixGateFactory() returned nil")
	}

	// Verify the gate
	verdict := g.Verify(tmpDir)

	if verdict.OK {
		t.Errorf("Verdict.OK = %v, want false", verdict.OK)
	}

	// Verify that the verdict has failures
	if len(verdict.Results) == 0 {
		t.Fatal("expected at least one step result")
	}

	// Find the markdown-lint step and verify it failed
	markdownLintFailed := false
	for _, result := range verdict.Results {
		if result.Name == "markdown-lint" {
			if result.OK {
				t.Error("markdown-lint step passed when it should have failed")
			}
			if result.Output == "" {
				t.Error("markdown-lint step failed but has no output")
			}
			markdownLintFailed = true
		}
	}
	if !markdownLintFailed {
		t.Error("markdown-lint step not found in verdict results")
	}
}

// TestTC079_02_DocsFixGateFailsMalformedHeading tests TC-079-02:
// The docs-fix gate returns FAIL on a markdown file with a malformed heading.
func TestTC079_02_DocsFixGateFailsMalformedHeading(t *testing.T) {
	// Create a temporary directory with a markdown file with malformed heading
	tmpDir := t.TempDir()

	malformedFile := filepath.Join(tmpDir, "heading.md")
	// Malformed heading: # followed directly by text without space
	malformedContent := `#BadHeading

This file has a badly formatted heading above.
`
	if err := os.WriteFile(malformedFile, []byte(malformedContent), 0o644); err != nil {
		t.Fatalf("failed to write markdown file: %v", err)
	}

	// Create the docs-fix gate
	g := newDocsFixGateFactory()
	if g == nil {
		t.Fatal("newDocsFixGateFactory() returned nil")
	}

	// Verify the gate
	verdict := g.Verify(tmpDir)

	if verdict.OK {
		t.Errorf("Verdict.OK = %v, want false", verdict.OK)
	}

	// Verify that the verdict has failures
	if len(verdict.Results) == 0 {
		t.Fatal("expected at least one step result")
	}

	// Find the markdown-lint step and verify it failed with heading issue
	found := false
	for _, result := range verdict.Results {
		if result.Name == "markdown-lint" {
			if result.OK {
				t.Error("markdown-lint step passed when it should have failed")
			}
			if result.Output == "" {
				t.Error("markdown-lint step output is empty")
			}
			found = true
		}
	}
	if !found {
		t.Error("markdown-lint step not found")
	}
}

// TestTC079_02_GateDoesNotInvokeGoBuild verifies the gate does NOT spawn Go tooling.
func TestTC079_02_GateDoesNotInvokeGoBuild(t *testing.T) {
	// Create a temporary directory with a Go project
	tmpDir := t.TempDir()

	// Create a simple Go file
	goFile := filepath.Join(tmpDir, "main.go")
	goContent := `package main
func main() {}
`
	if err := os.WriteFile(goFile, []byte(goContent), 0o644); err != nil {
		t.Fatalf("failed to write Go file: %v", err)
	}

	// Create the docs-fix gate and run it
	g := newDocsFixGateFactory()
	if g == nil {
		t.Fatal("newDocsFixGateFactory() returned nil")
	}

	verdict := g.Verify(tmpDir)

	// Check the steps: there should be NO Go-tooling steps
	for _, result := range verdict.Results {
		stepName := result.Name
		// These steps should NOT be present in the docs-fix gate
		forbiddenSteps := []string{"go-build", "go-vet", "go-test", "go-fmt", "golangci-lint"}
		for _, forbidden := range forbiddenSteps {
			if stepName == forbidden {
				t.Errorf("docs-fix gate should not contain step %q", forbidden)
			}
		}
	}

	// Verify the docs-fix gate only has markdown-lint
	// (code-scanner would be included in production but is omitted in the proof recipe)
	expectedSteps := map[string]bool{
		"markdown-lint": false,
	}
	for _, result := range verdict.Results {
		if _, ok := expectedSteps[result.Name]; ok {
			expectedSteps[result.Name] = true
		}
	}
	for stepName, found := range expectedSteps {
		if !found {
			t.Errorf("expected step %q not found in verdict results", stepName)
		}
	}
}

// TestTC079_02_GateImplementsBlocker verifies the DocsFixGate implements Blocker.
func TestTC079_02_GateImplementsBlocker(t *testing.T) {
	g := newDocsFixGateFactory()
	if g == nil {
		t.Fatal("newDocsFixGateFactory() returned nil")
	}

	blocker, ok := g.(gate.Blocker)
	if !ok {
		t.Errorf("gate type %T does not implement Blocker interface", g)
	}

	if !blocker.Blocks() {
		t.Errorf("gate.Blocks() = false, want true")
	}
}

// TestTC079_03_NoSandboxDependency verifies the docsfix package does not import internal/sandbox.
// This test is a compile-time assertion via the import analysis (see the go list check in the harness).
func TestTC079_03_NoSandboxDependency(t *testing.T) {
	// This is a structural test that verifies:
	// - The DocsFixGate is a real gate.Blocker
	// - The recipe shares block-wiring config with the coding-agent
	// (actual import check is via go list in the harness)

	r, err := recipe.SelectRecipe("docs-fix")
	if err != nil {
		t.Fatalf("SelectRecipe(\"docs-fix\") failed: %v", err)
	}

	// The recipe's BlockWiring should be nil (same as coding-agent's nil BlockWiring).
	// BlockWiring is shared state, not a recipe-specific IO seam.
	if len(r.BlockWiring) != 0 {
		t.Errorf("BlockWiring = %v, want nil or empty", r.BlockWiring)
	}
}

// TestTC079_AllRecipesIncludeDocsFix verifies "docs-fix" is in ListRecipes.
func TestTC079_AllRecipesIncludeDocsFix(t *testing.T) {
	recipes := recipe.ListRecipes()
	found := false
	for _, name := range recipes {
		if name == "docs-fix" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("\"docs-fix\" not in ListRecipes(): %v", recipes)
	}
}

// BenchmarkMarkdownLintStep benchmarks the markdown linter performance.
func BenchmarkMarkdownLintStep(b *testing.B) {
	tmpDir := b.TempDir()

	// Create a test markdown file
	markdownFile := filepath.Join(tmpDir, "test.md")
	content := `# Test Document

This is a test markdown file with multiple lines.

## Section 1

Some content here.

## Section 2

More content here.
`
	if err := os.WriteFile(markdownFile, []byte(content), 0o644); err != nil {
		b.Fatalf("failed to write markdown file: %v", err)
	}

	step := &MarkdownLintStep{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = step.Run(tmpDir)
	}
}

// TestMarkdownLintStepName verifies the step name.
func TestMarkdownLintStepName(t *testing.T) {
	step := &MarkdownLintStep{}
	expected := "markdown-lint"
	if name := step.Name(); name != expected {
		t.Errorf("Name() = %q, want %q", name, expected)
	}
}

// TestMarkdownLintStepWithNoMarkdownFiles verifies behavior when no .md files exist.
func TestMarkdownLintStepWithNoMarkdownFiles(t *testing.T) {
	tmpDir := t.TempDir()

	step := &MarkdownLintStep{}
	result := step.Run(tmpDir)

	if !result.OK {
		t.Errorf("Run() OK = %v, want true", result.OK)
	}
	if result.Output == "" {
		t.Error("Run() Output is empty")
	}
}

// TestMarkdownLintStepWithMultipleFiles verifies behavior with multiple markdown files.
func TestMarkdownLintStepWithMultipleFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple well-formed markdown files
	files := []string{"README.md", "GUIDE.md", "API.md"}
	for _, fname := range files {
		fpath := filepath.Join(tmpDir, fname)
		content := fmt.Sprintf("# %s\n\nThis is %s", fname, fname)
		if err := os.WriteFile(fpath, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write %s: %v", fname, err)
		}
	}

	step := &MarkdownLintStep{}
	result := step.Run(tmpDir)

	if !result.OK {
		t.Errorf("Run() OK = %v, want true", result.OK)
	}
}
