package supervisor_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupervisorImportsNoForbiddenExecutorLLMOrWebPackages(t *testing.T) {
	imports := supervisorProductionImports(t)
	for _, path := range imports {
		for _, segment := range strings.Split(path, "/") {
			switch segment {
			case "executor", "executors", "llm", "llms", "web", "webfetch", "web-fetch":
				t.Fatalf("TC-005: supervisor imports forbidden package %q via %q", segment, path)
			}
		}
	}
}

func supervisorProductionImports(t *testing.T) []string {
	t.Helper()

	root := repoRoot(t)
	pkgDir := filepath.Join(root, "internal", "supervisor")
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("TC-005: read supervisor dir: %v", err)
	}

	imports := []string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		filePath := filepath.Join(pkgDir, entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), filePath, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("TC-005: parse %s: %v", filePath, err)
		}
		for _, spec := range file.Imports {
			imports = append(imports, strings.Trim(spec.Path.Value, `"`))
		}
	}
	return imports
}

func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("TC-005: get working dir: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("TC-005: could not find repo root")
		}
		dir = parent
	}
}
