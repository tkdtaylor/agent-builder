package tasksource

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WritableStatus is the constrained set of status markers the writer may place
// in a task source file.
type WritableStatus string

const (
	WritableStatusDone       WritableStatus = "done"
	WritableStatusBlocked    WritableStatus = "blocked"
	WritableStatusNeedsHuman WritableStatus = "needs-human"
)

// StatusWriteResult describes the task file matched by a status-only write.
type StatusWriteResult struct {
	Path    string
	Changed bool
}

// StatusWriter updates only the status marker in task source files.
type StatusWriter struct {
	root     string
	taskDirs []string
}

// NewStatusWriter returns a writer rooted at root for the supplied task dirs.
func NewStatusWriter(root string, taskDirs ...string) *StatusWriter {
	dirs := append([]string(nil), taskDirs...)
	return &StatusWriter{
		root:     root,
		taskDirs: dirs,
	}
}

// WriteStatus updates the matching task's **Status:** metadata line.
func (w *StatusWriter) WriteStatus(taskID string, status WritableStatus) (StatusWriteResult, error) {
	target := strings.TrimSpace(taskID)
	if target == "" {
		return StatusWriteResult{}, errors.New("tasksource: empty task ID")
	}
	if err := validateWritableStatus(status); err != nil {
		return StatusWriteResult{}, err
	}
	if w == nil {
		return StatusWriteResult{}, errors.New("tasksource: nil status writer")
	}

	match, err := w.findTask(target)
	if err != nil {
		return StatusWriteResult{}, err
	}

	updated, changed, err := rewriteStatusLine(match.body, status)
	if err != nil {
		return StatusWriteResult{}, fmt.Errorf("tasksource: %s: %w", match.path, err)
	}
	if !changed {
		return StatusWriteResult{Path: match.path, Changed: false}, nil
	}

	if err := os.WriteFile(match.path, updated, match.mode); err != nil {
		return StatusWriteResult{}, fmt.Errorf("tasksource: write task file %s: %w", match.path, err)
	}
	return StatusWriteResult{Path: match.path, Changed: true}, nil
}

func validateWritableStatus(status WritableStatus) error {
	switch status {
	case WritableStatusDone, WritableStatusBlocked, WritableStatusNeedsHuman:
		return nil
	default:
		return fmt.Errorf("tasksource: invalid writable status %q", status)
	}
}

type statusTaskMatch struct {
	path string
	body []byte
	mode os.FileMode
}

func (w *StatusWriter) findTask(taskID string) (statusTaskMatch, error) {
	if w.root == "" {
		return statusTaskMatch{}, errors.New("tasksource: empty status writer root")
	}

	var found *statusTaskMatch
	for _, taskDir := range w.taskDirs {
		dirPath := filepath.Join(w.root, filepath.FromSlash(taskDir))
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return statusTaskMatch{}, fmt.Errorf("tasksource: read task dir %s: %w", dirPath, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				continue
			}

			taskPath := filepath.Join(dirPath, entry.Name())
			body, err := os.ReadFile(taskPath)
			if err != nil {
				return statusTaskMatch{}, fmt.Errorf("tasksource: read task file %s: %w", taskPath, err)
			}

			idMatch := taskHeadingRE.FindSubmatch(body)
			if idMatch == nil || string(idMatch[1]) != taskID {
				continue
			}
			if found != nil {
				return statusTaskMatch{}, fmt.Errorf("tasksource: duplicate task ID %s", taskID)
			}

			info, err := entry.Info()
			if err != nil {
				return statusTaskMatch{}, fmt.Errorf("tasksource: stat task file %s: %w", taskPath, err)
			}
			found = &statusTaskMatch{
				path: taskPath,
				body: body,
				mode: info.Mode(),
			}
		}
	}

	if found == nil {
		return statusTaskMatch{}, fmt.Errorf("tasksource: task ID %s not found", taskID)
	}
	return *found, nil
}

func rewriteStatusLine(body []byte, status WritableStatus) ([]byte, bool, error) {
	lines := splitLinesKeepingEndings(body)
	statusIndex := -1
	for i, line := range lines {
		content, _ := trimLineEnding(line)
		if bytes.HasPrefix(content, []byte("**Status:**")) {
			if statusIndex != -1 {
				return nil, false, errors.New("multiple status lines")
			}
			statusIndex = i
		}
	}
	if statusIndex == -1 {
		return nil, false, errors.New("missing status line")
	}

	_, ending := trimLineEnding(lines[statusIndex])
	replacement := append([]byte("**Status:** "+string(status)), ending...)
	if bytes.Equal(lines[statusIndex], replacement) {
		return body, false, nil
	}

	updated := make([]byte, 0, len(body)-len(lines[statusIndex])+len(replacement))
	for i, line := range lines {
		if i == statusIndex {
			updated = append(updated, replacement...)
			continue
		}
		updated = append(updated, line...)
	}
	return updated, true, nil
}

func splitLinesKeepingEndings(body []byte) [][]byte {
	if len(body) == 0 {
		return nil
	}

	lines := make([][]byte, 0)
	start := 0
	for i, b := range body {
		if b != '\n' {
			continue
		}
		lines = append(lines, body[start:i+1])
		start = i + 1
	}
	if start < len(body) {
		lines = append(lines, body[start:])
	}
	return lines
}

func trimLineEnding(line []byte) ([]byte, []byte) {
	if bytes.HasSuffix(line, []byte("\r\n")) {
		return line[:len(line)-2], line[len(line)-2:]
	}
	if bytes.HasSuffix(line, []byte("\n")) {
		return line[:len(line)-1], line[len(line)-1:]
	}
	return line, nil
}
