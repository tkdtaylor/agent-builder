// Package tasksource reads roadmap/task metadata and selects the next ready task.
// It is intentionally read-only: callers provide an fs.FS, and the package has
// no write-side filesystem API.
package tasksource

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

const (
	// DefaultRoadmapPath is the in-repo roadmap path consumed by the task source.
	DefaultRoadmapPath = "docs/plans/roadmap.md"
)

var (
	// DefaultTaskDirs are the in-repo task directories that hold task files.
	DefaultTaskDirs = []string{
		"docs/tasks/backlog",
		"docs/tasks/active",
		"docs/tasks/completed",
	}

	taskHeadingRE = regexp.MustCompile(`(?m)^# Task ([0-9]+):`)
	fieldRE       = regexp.MustCompile(`(?m)^\*\*([^*]+):\*\*\s*(.+?)\s*$`)
	depsRE        = regexp.MustCompile(`(?m)^-\s*Dependencies:\s*(.+?)\s*$`)
)

// Status is the normalized task lifecycle state understood by the reader.
type Status string

const (
	StatusReady     Status = "ready"
	StatusActive    Status = "active"
	StatusBlocked   Status = "blocked"
	StatusCompleted Status = "completed"
)

// Candidate is a parsed task file plus the metadata required for readiness.
type Candidate struct {
	Task         supervisor.Task
	Status       Status
	Dependencies []string
}

// Source reads roadmap and task files from an fs.FS.
type Source struct {
	fsys        fs.FS
	roadmapPath string
	taskDirs    []string
}

// New returns a read-only task source over fsys.
func New(fsys fs.FS, roadmapPath string, taskDirs ...string) *Source {
	dirs := append([]string(nil), taskDirs...)
	return &Source{
		fsys:        fsys,
		roadmapPath: roadmapPath,
		taskDirs:    dirs,
	}
}

// Candidates parses all configured task directories and returns sorted candidates.
func (s *Source) Candidates() ([]Candidate, error) {
	if s.fsys == nil {
		return nil, errors.New("tasksource: nil filesystem")
	}
	if s.roadmapPath != "" {
		if _, err := fs.ReadFile(s.fsys, s.roadmapPath); err != nil {
			return nil, fmt.Errorf("tasksource: read roadmap %s: %w", s.roadmapPath, err)
		}
	}

	var candidates []Candidate
	for _, dir := range s.taskDirs {
		entries, err := fs.ReadDir(s.fsys, dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("tasksource: read task dir %s: %w", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || path.Ext(entry.Name()) != ".md" {
				continue
			}

			taskPath := path.Join(dir, entry.Name())
			body, err := fs.ReadFile(s.fsys, taskPath)
			if err != nil {
				return nil, fmt.Errorf("tasksource: read task file %s: %w", taskPath, err)
			}

			candidate, err := parseTaskFile(taskPath, string(body))
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, candidate)
		}
	}

	sortCandidates(candidates)
	if err := validateCandidates(candidates); err != nil {
		return nil, err
	}

	return candidates, nil
}

// Next returns the deterministic first ready task whose dependencies are complete.
func (s *Source) Next() (supervisor.Task, bool, error) {
	candidates, err := s.Candidates()
	if err != nil {
		return supervisor.Task{}, false, err
	}

	completed := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.Status == StatusCompleted {
			completed[candidate.Task.ID] = struct{}{}
		}
	}

	for _, candidate := range candidates {
		if candidate.Status != StatusReady {
			continue
		}
		if dependenciesCompleted(candidate.Dependencies, completed) {
			return candidate.Task, true, nil
		}
	}

	return supervisor.Task{}, false, nil
}

func parseTaskFile(taskPath string, body string) (Candidate, error) {
	idMatch := taskHeadingRE.FindStringSubmatch(body)
	if idMatch == nil {
		return Candidate{}, fmt.Errorf("tasksource: %s: missing task heading", taskPath)
	}

	fields := parseFields(body)
	project, ok := fields["project"]
	if !ok || project == "" {
		return Candidate{}, fmt.Errorf("tasksource: %s: missing project", taskPath)
	}

	statusRaw, ok := fields["status"]
	if !ok || statusRaw == "" {
		return Candidate{}, fmt.Errorf("tasksource: %s: missing status", taskPath)
	}
	status, err := normalizeStatus(statusRaw)
	if err != nil {
		return Candidate{}, fmt.Errorf("tasksource: %s: %w", taskPath, err)
	}

	return Candidate{
		Task: supervisor.Task{
			ID:   idMatch[1],
			Repo: project,
			Spec: taskPath,
		},
		Status:       status,
		Dependencies: parseDependencies(body),
	}, nil
}

func parseFields(body string) map[string]string {
	fields := map[string]string{}
	for _, match := range fieldRE.FindAllStringSubmatch(body, -1) {
		key := strings.ToLower(strings.TrimSpace(match[1]))
		fields[key] = strings.TrimSpace(match[2])
	}
	return fields
}

func normalizeStatus(raw string) (Status, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(status, "completed"):
		return StatusCompleted, nil
	case strings.Contains(status, "active"), strings.Contains(status, "in progress"), strings.Contains(status, "⏳"):
		return StatusActive, nil
	case strings.Contains(status, "blocked"), strings.Contains(status, "⚠️"):
		return StatusBlocked, nil
	case strings.Contains(status, "backlog"), strings.Contains(status, "ready"), strings.Contains(status, "❌"):
		return StatusReady, nil
	default:
		return "", fmt.Errorf("unrecognized status %q", raw)
	}
}

func parseDependencies(body string) []string {
	match := depsRE.FindStringSubmatch(body)
	if match == nil {
		return nil
	}

	raw := strings.ToLower(strings.TrimSpace(match[1]))
	if raw == "" || strings.HasPrefix(raw, "none") || raw == "no blocking tasks" {
		return nil
	}

	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == ';'
	})
	deps := make([]string, 0, len(parts))
	for _, part := range parts {
		dep := strings.Trim(part, " .")
		if dep == "" {
			continue
		}
		deps = append(deps, dep)
	}
	return deps
}

func validateCandidates(candidates []Candidate) error {
	ids := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, ok := ids[candidate.Task.ID]; ok {
			return fmt.Errorf("tasksource: duplicate task ID %s", candidate.Task.ID)
		}
		ids[candidate.Task.ID] = struct{}{}
	}

	for _, candidate := range candidates {
		for _, dep := range candidate.Dependencies {
			if _, ok := ids[dep]; !ok {
				return fmt.Errorf("tasksource: task %s depends on missing task %s", candidate.Task.ID, dep)
			}
		}
	}
	return nil
}

func sortCandidates(candidates []Candidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Task.ID == candidates[j].Task.ID {
			return candidates[i].Task.Spec < candidates[j].Task.Spec
		}
		return candidates[i].Task.ID < candidates[j].Task.ID
	})
}

func dependenciesCompleted(deps []string, completed map[string]struct{}) bool {
	for _, dep := range deps {
		if _, ok := completed[dep]; !ok {
			return false
		}
	}
	return true
}
