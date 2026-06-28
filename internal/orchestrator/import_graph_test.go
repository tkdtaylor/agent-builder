package orchestrator_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TC-081-05: internal/orchestrator must have NO DIRECT import of
// internal/executor (REQ-081-05, ADR 046 D-2). The assertion is on the package's
// own (non-test) source files' import lists — NOT the transitive graph. The
// orchestrator dispatches THROUGH internal/runtime, which legitimately imports
// internal/executor; that transitive path is the ADR-042-blessed dispatch path
// (the orchestrator authors no code; the worker it dispatches runs the executor
// inside its box). This test parses the package source directly so it asserts
// exactly "direct import", hermetically (no `go list` subprocess).
func TestTC081_05_NoDirectExecutorImport(t *testing.T) {
	imports := directImports(t)

	const forbidden = "github.com/tkdtaylor/agent-builder/internal/executor"
	for _, imp := range imports {
		if imp == forbidden {
			t.Fatalf("TC-081-05 violated: internal/orchestrator directly imports %s (forbidden by REQ-081-05)", forbidden)
		}
		// Also reject any sub-package of internal/executor.
		if strings.HasPrefix(imp, forbidden+"/") {
			t.Fatalf("TC-081-05 violated: internal/orchestrator directly imports %s (a sub-package of internal/executor)", imp)
		}
	}

	// Positive: the orchestrator DOES directly import the dispatch path (recipe,
	// runtime, policy, supervisor) — proving it reaches the executor only
	// transitively through runtime, the intended path.
	want := []string{
		"github.com/tkdtaylor/agent-builder/internal/recipe",
		"github.com/tkdtaylor/agent-builder/internal/runtime",
		"github.com/tkdtaylor/agent-builder/internal/policy",
		"github.com/tkdtaylor/agent-builder/internal/supervisor",
	}
	for _, w := range want {
		if !contains(imports, w) {
			t.Errorf("expected direct import %s not found (import set: %v)", w, imports)
		}
	}
}

// directImports parses every non-test .go file in the orchestrator package
// directory and returns the de-duplicated set of imported package paths.
func directImports(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import %s in %s: %v", spec.Path.Value, name, err)
			}
			if !seen[path] {
				seen[path] = true
				out = append(out, path)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no imports parsed from orchestrator package — test harness error")
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
