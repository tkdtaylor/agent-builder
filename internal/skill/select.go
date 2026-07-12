package skill

import (
	"fmt"
	"sort"
	"strings"
)

// SelectForGoal is the v1 skill-selection rule (ADR 066): pure, taking the registry
// as an explicit parameter (no hidden global), deterministic. It case-insensitively
// substring-matches goalText against each Manifest's Name then Description, iterating
// the registry in sorted-key order so the first match is deterministic. On no match
// it returns registry[fallback]; if fallback itself is not registered, it returns a
// descriptive error.
//
// This rule is a deliberate placeholder: the first time a second skill demonstrates
// keyword matching mis-selects, ADR 066's re-evaluation trigger reopens this design.
func SelectForGoal(goalText string, registry map[string]Manifest, fallback string) (Manifest, error) {
	lower := strings.ToLower(goalText)

	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		m := registry[name]
		if m.Name != "" && strings.Contains(lower, strings.ToLower(m.Name)) {
			return m, nil
		}
		if m.Description != "" && strings.Contains(lower, strings.ToLower(m.Description)) {
			return m, nil
		}
	}

	fb, ok := registry[fallback]
	if !ok {
		return Manifest{}, fmt.Errorf("skill.SelectForGoal: no skill matched %q and fallback %q is not registered", goalText, fallback)
	}
	return fb, nil
}
