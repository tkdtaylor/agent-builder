package skill

import (
	"fmt"
	"sort"
	"sync"
)

// The process-wide skill registry. Guarded by registryMu for concurrent Register/
// Select/List.
var (
	registryMu sync.RWMutex
	registry   = make(map[string]Manifest)
)

// Register adds a skill Manifest under name. It returns an ERROR (not a panic) on a
// duplicate name, unlike recipe.Register which panics: skill registration is expected
// to eventually happen from config/discovery (the schedule-file precedent, task 175),
// where a duplicate or bad entry is an operator input error that must surface as a
// clean startup failure, not crash the daemon (ADR 066).
func Register(name string, m Manifest) error {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		return fmt.Errorf("skill.Register: skill %q is already registered", name)
	}
	registry[name] = m
	return nil
}

// Select returns the registered skill Manifest for name, or a descriptive not-found
// error naming the unknown skill (mirrors recipe.SelectRecipe's error shape).
func Select(name string) (Manifest, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	m, ok := registry[name]
	if !ok {
		return Manifest{}, fmt.Errorf("skill.Select: no skill registered under %q", name)
	}
	return m, nil
}

// List returns the registered skill names in deterministic sorted order.
func List() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// snapshot returns a copy of the current registry map, used by callers that need a
// stable map to pass to the pure SelectForGoal.
func snapshot() map[string]Manifest {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make(map[string]Manifest, len(registry))
	for k, v := range registry {
		out[k] = v
	}
	return out
}

// Registry returns a snapshot copy of the registered skills keyed by name, for
// callers that want to drive SelectForGoal against the live registry.
func Registry() map[string]Manifest { return snapshot() }
