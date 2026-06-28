// Package ollamatoolset provides a concrete tool dispatcher for the Ollama agentic loop.
// It implements path confinement to the worktree and enforces a command allowlist for run_command.
//
// Security (load-bearing):
//   - All path-accepting tools confine paths to the worktree via filepath.Abs + filepath.EvalSymlinks + strings.HasPrefix
//   - run_command enforces an explicit allowlist before any subprocess construction
//   - All path confinement checks must pass before any OS call is made
package ollamatoolset

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/executor/ollamaclient"
)

// ToolSet is the concrete tool dispatcher for the Ollama agentic loop.
type ToolSet struct {
	worktree    string
	worktreeAbs string
}

// NewToolSet creates a new tool set for the given worktree directory.
// It returns an error if the worktree directory cannot be resolved.
func NewToolSet(worktree string) (*ToolSet, error) {
	abs, err := filepath.Abs(worktree)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve worktree path: %w", err)
	}

	// Ensure the directory exists
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("worktree directory does not exist: %w", err)
	}

	return &ToolSet{
		worktree:    worktree,
		worktreeAbs: abs,
	}, nil
}

// Dispatch routes a tool call by name to the correct handler.
// argsJSON is the raw JSON string from ToolCallFunction.Arguments.
// Returns a non-empty result string on success, or a non-nil error.
func (s *ToolSet) Dispatch(toolName string, argsJSON string) (string, error) {
	switch toolName {
	case "write_file":
		return s.writeFile(argsJSON)
	case "read_file":
		return s.readFile(argsJSON)
	case "list_dir":
		return s.listDir(argsJSON)
	case "run_command":
		return s.runCommand(argsJSON)
	case "finish_branch":
		return s.finishBranch(argsJSON)
	default:
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}
}

// writeFile writes content to a file at the given path (relative to the worktree).
// It creates parent directories as needed.
func (s *ToolSet) writeFile(argsJSON string) (string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("failed to parse write_file arguments: %w", err)
	}

	// Path confinement check
	if err := s.checkPathConfinement(args.Path); err != nil {
		return "", err
	}

	abs := filepath.Join(s.worktreeAbs, args.Path)

	// Create parent directories
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create parent directories: %w", err)
	}

	// Write the file
	if err := os.WriteFile(abs, []byte(args.Content), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return fmt.Sprintf("wrote file %s", args.Path), nil
}

// readFile reads and returns the content of a file at the given path (relative to the worktree).
func (s *ToolSet) readFile(argsJSON string) (string, error) {
	var args struct {
		Path string `json:"path"`
	}

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("failed to parse read_file arguments: %w", err)
	}

	// Path confinement check
	if err := s.checkPathConfinement(args.Path); err != nil {
		return "", err
	}

	abs := filepath.Join(s.worktreeAbs, args.Path)

	content, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	return string(content), nil
}

// listDir lists the entries in a directory at the given path (relative to the worktree).
func (s *ToolSet) listDir(argsJSON string) (string, error) {
	var args struct {
		Path string `json:"path"`
	}

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("failed to parse list_dir arguments: %w", err)
	}

	// Path confinement check
	if err := s.checkPathConfinement(args.Path); err != nil {
		return "", err
	}

	abs := filepath.Join(s.worktreeAbs, args.Path)

	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", fmt.Errorf("failed to read directory: %w", err)
	}

	// Build a list of entry names
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}

	// Return as a JSON array string for the model
	result, err := json.Marshal(names)
	if err != nil {
		return "", fmt.Errorf("failed to marshal directory listing: %w", err)
	}

	return string(result), nil
}

// runCommand executes an allowed command in the worktree.
// The command name must be in the allowlist before any subprocess construction.
func (s *ToolSet) runCommand(argsJSON string) (string, error) {
	var args struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("failed to parse run_command arguments: %w", err)
	}

	// CRITICAL: Allowlist check is the first statement in run_command.
	// This must fire before any variable reads (beyond those needed to parse the command name),
	// path resolution, or subprocess construction.
	if !isAllowedCommand(args.Command) {
		return "", fmt.Errorf("command %q is not allowed (allowlist: git, go, gofmt, golangci-lint)", args.Command)
	}

	// Construct the command with the worktree as the working directory
	cmd := exec.Command(args.Command, args.Args...)
	cmd.Dir = s.worktreeAbs

	// Run the command and capture output
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("command failed: %w; output: %s", err, string(output))
	}

	return string(output), nil
}

// finishBranch writes the branch name to the reserved branch file.
func (s *ToolSet) finishBranch(argsJSON string) (string, error) {
	var args struct {
		Branch string `json:"branch"`
	}

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("failed to parse finish_branch arguments: %w", err)
	}

	branchFile := filepath.Join(s.worktreeAbs, ".agent-branch")
	if err := os.WriteFile(branchFile, []byte(strings.TrimSpace(args.Branch)), 0644); err != nil {
		return "", fmt.Errorf("failed to write branch file: %w", err)
	}

	return fmt.Sprintf("recorded branch: %s", args.Branch), nil
}

// checkPathConfinement verifies that the given path (relative to the worktree)
// does not escape the worktree. It resolves symlinks and uses a prefix check.
// Returns a non-nil error if the path is outside the worktree.
func (s *ToolSet) checkPathConfinement(path string) error {
	// Reject absolute paths outright (they cannot be relative to the worktree)
	if filepath.IsAbs(path) {
		return fmt.Errorf("path %q is outside the worktree (confined to %q): absolute paths are not allowed", path, s.worktreeAbs)
	}

	// Construct the absolute path by joining with the worktree root
	// filepath.Join normalizes .. but we also need to resolve symlinks
	abs := filepath.Join(s.worktreeAbs, path)

	// Resolve symlinks BEFORE the prefix check (this prevents symlink-based escape)
	// If the path doesn't exist yet (e.g., for write operations), EvalSymlinks will fail.
	// In that case, we walk up to find an existing parent.
	resolved := abs
	for resolved != filepath.Dir(resolved) { // Stop at the root
		if _, err := os.Lstat(resolved); err == nil {
			// Path exists, try to resolve symlinks
			if symResolved, err := filepath.EvalSymlinks(resolved); err == nil {
				resolved = symResolved
				break
			}
			break
		}
		// Path doesn't exist, try parent
		resolved = filepath.Dir(resolved)
	}

	// Clean the resolved path
	resolved = filepath.Clean(resolved)

	// Prefix check: ensure the resolved path is under the worktree
	// Use HasPrefix with a trailing separator to prevent prefix collisions
	// (e.g., /tmp/wt matching /tmp/wt-escaped)
	expectedPrefix := s.worktreeAbs + string(filepath.Separator)
	if !strings.HasPrefix(resolved, expectedPrefix) && resolved != s.worktreeAbs {
		return fmt.Errorf("path %q is outside the worktree (confined to %q)", path, s.worktreeAbs)
	}

	return nil
}

// isAllowedCommand checks if the command is in the allowlist.
func isAllowedCommand(cmd string) bool {
	allowed := AllowedCommands()
	_, ok := allowed[cmd]
	return ok
}

// AllowedCommands returns the set of commands permitted by run_command.
// Exported for the allowlist enumeration test.
func AllowedCommands() map[string]struct{} {
	return map[string]struct{}{
		"git":            {},
		"go":             {},
		"gofmt":          {},
		"golangci-lint":  {},
	}
}

// ToolSchemas returns the JSON Schema descriptors for all five tools.
func (s *ToolSet) ToolSchemas() []ollamaclient.Tool {
	return []ollamaclient.Tool{
		{
			Type: "function",
			Function: ollamaclient.ToolFunction{
				Name:        "write_file",
				Description: "Write content to a file at the given path (relative to the worktree). Creates parent directories as needed.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "File path relative to the worktree",
						},
						"content": map[string]interface{}{
							"type":        "string",
							"description": "Content to write to the file",
						},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		{
			Type: "function",
			Function: ollamaclient.ToolFunction{
				Name:        "read_file",
				Description: "Read and return the content of a file at the given path (relative to the worktree).",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "File path relative to the worktree",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: ollamaclient.ToolFunction{
				Name:        "list_dir",
				Description: "List the entries in a directory at the given path (relative to the worktree).",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Directory path relative to the worktree",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: ollamaclient.ToolFunction{
				Name:        "run_command",
				Description: "Run an allowed command in the worktree (allowed: git, go, gofmt, golangci-lint).",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{
							"type":        "string",
							"description": "The command to run (must be in the allowlist)",
						},
						"args": map[string]interface{}{
							"type":        "array",
							"items":       map[string]interface{}{"type": "string"},
							"description": "Arguments to pass to the command",
						},
					},
					"required": []string{"command"},
				},
			},
		},
		{
			Type: "function",
			Function: ollamaclient.ToolFunction{
				Name:        "finish_branch",
				Description: "Record the produced branch name for extraction by the loop.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"branch": map[string]interface{}{
							"type":        "string",
							"description": "The branch name to record",
						},
					},
					"required": []string{"branch"},
				},
			},
		},
	}
}

// ExtractBranch reads the reserved branch file from the worktree.
// Returns ("", false) if the file does not exist.
func (s *ToolSet) ExtractBranch() (string, bool) {
	branchFile := filepath.Join(s.worktreeAbs, ".agent-branch")
	content, err := os.ReadFile(branchFile)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(content)), true
}
