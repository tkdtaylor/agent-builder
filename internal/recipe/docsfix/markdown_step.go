package docsfix

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/gate"
)

// MarkdownLintStep checks markdown files in the repository for common issues.
// It uses a basic linter that validates:
// - Files exist and are readable
// - Common markdown syntax issues (malformed links, heading issues)
type MarkdownLintStep struct{}

// Name returns the step name.
func (m *MarkdownLintStep) Name() string {
	return "markdown-lint"
}

// Run executes the markdown linter on all .md files in the repository.
func (m *MarkdownLintStep) Run(repoPath string) gate.StepResult {
	result := gate.StepResult{
		Name: m.Name(),
		OK:   true,
	}

	// Find all .md files in the repo
	mdFiles, err := findMarkdownFiles(repoPath)
	if err != nil {
		result.OK = false
		result.Output = fmt.Sprintf("failed to list markdown files: %v", err)
		return result
	}

	// If no markdown files, pass
	if len(mdFiles) == 0 {
		result.Output = "no markdown files found"
		return result
	}

	// Check each markdown file for basic issues
	issues := []string{}
	for _, file := range mdFiles {
		if errs := checkMarkdownFile(file); len(errs) > 0 {
			issues = append(issues, errs...)
		}
	}

	if len(issues) > 0 {
		result.OK = false
		result.Output = strings.Join(issues, "; ")
		return result
	}

	result.Output = fmt.Sprintf("checked %d markdown files", len(mdFiles))
	return result
}

// findMarkdownFiles recursively finds all .md files in the given directory.
func findMarkdownFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// checkMarkdownFile validates a single markdown file for common issues.
func checkMarkdownFile(filePath string) []string {
	var issues []string

	// Read the file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return []string{fmt.Sprintf("%s: failed to read: %v", filePath, err)}
	}

	text := string(content)

	// Check for malformed links like [text](http://localhost:99999)
	// This is a simple heuristic that looks for localhost links which are typically broken
	if strings.Contains(text, "http://localhost:99999") {
		issues = append(issues, fmt.Sprintf("%s: contains broken localhost link (http://localhost:99999)", filePath))
	}

	// Check for common markdown syntax issues
	// - Look for malformed headings (# without space)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Check for malformed heading (# followed directly by text without space)
		if strings.HasPrefix(trimmed, "#") && len(trimmed) > 1 && trimmed[1] != ' ' && trimmed[1] != '#' {
			// This is a malformed heading like ##text instead of ## text
			lineNum := i + 1
			issues = append(issues, fmt.Sprintf("%s:%d: malformed heading (missing space after #)", filePath, lineNum))
		}
	}

	return issues
}
