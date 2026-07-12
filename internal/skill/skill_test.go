package skill_test

// Task 176: the internal/skill registry seam (ADR 066).

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/skill"
)

// TC-176-02: Manifest carries all five typed concepts.
func TestTC176_02_ManifestShape(t *testing.T) {
	m := skill.Manifest{
		Name:                "coding-agent",
		Description:         "work a target repo's tasks unattended",
		RecipeName:          "coding-agent",
		RequiredPermissions: []string{"run-task", "publish"},
		GateChecks:          []string{"go-test", "dep-scan"},
	}
	if m.Name != "coding-agent" || m.Description == "" || m.RecipeName != "coding-agent" {
		t.Errorf("Manifest scalar fields wrong: %+v", m)
	}
	if len(m.RequiredPermissions) != 2 || m.RequiredPermissions[0] != "run-task" {
		t.Errorf("RequiredPermissions = %v, want [run-task publish]", m.RequiredPermissions)
	}
	if len(m.GateChecks) != 2 || m.GateChecks[1] != "dep-scan" {
		t.Errorf("GateChecks = %v, want [go-test dep-scan]", m.GateChecks)
	}
}

// TC-176-04: Register returns an error (not a panic) on a duplicate name.
// The recover() wrapper is load-bearing: it proves the deliberate divergence
// from recipe.Register's panic-on-duplicate convention (ADR 066).
func TestTC176_04_RegisterDuplicateError(t *testing.T) {
	name := "tc176-dup"
	if err := skill.Register(name, skill.Manifest{Name: name}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("duplicate Register panicked (%v); it must return an error, not panic", r)
			}
		}()
		err = skill.Register(name, skill.Manifest{Name: name})
	}()
	if err == nil {
		t.Fatalf("duplicate Register returned nil, want an error (not a panic)")
	}
	if !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "already registered") {
		t.Errorf("duplicate error %q must name the skill %q and say \"already registered\"", err, name)
	}
}

// TC-176-05: Register succeeds on a unique name; Select returns it.
func TestTC176_05_RegisterUniqueSucceeds(t *testing.T) {
	name := "tc176-unique"
	m := skill.Manifest{Name: name, RecipeName: "coding-agent"}
	if err := skill.Register(name, m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := skill.Select(name)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Name != name || got.RecipeName != "coding-agent" {
		t.Errorf("Select(%s) = %+v, want the registered manifest", name, got)
	}
}

// TC-176-06: Select returns a descriptive not-found error for an unknown skill.
func TestTC176_06_SelectNotFound(t *testing.T) {
	_, err := skill.Select("tc176-does-not-exist")
	if err == nil {
		t.Fatal("Select(unknown) returned nil error, want a not-found error")
	}
	if !strings.Contains(err.Error(), "tc176-does-not-exist") {
		t.Errorf("not-found error %q must name the unknown skill", err)
	}
}

// TC-176-07: List returns names in deterministic sorted order.
func TestTC176_07_ListSorted(t *testing.T) {
	// Register in reverse order; List must return sorted.
	for _, n := range []string{"tc176-list-c", "tc176-list-a", "tc176-list-b"} {
		_ = skill.Register(n, skill.Manifest{Name: n})
	}
	list := skill.List()
	if !sort.StringsAreSorted(list) {
		t.Fatalf("List() = %v, want sorted", list)
	}
	// The three we registered appear in a<b<c relative order.
	ia, ib, ic := indexOf(list, "tc176-list-a"), indexOf(list, "tc176-list-b"), indexOf(list, "tc176-list-c")
	if ia < 0 || ib < 0 || ic < 0 || ia >= ib || ib >= ic {
		t.Errorf("registered names not in sorted order in %v (a=%d b=%d c=%d)", list, ia, ib, ic)
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// TC-176-08: SelectForGoal keyword-matches (pure, explicit registry).
func TestTC176_08_SelectForGoalKeyword(t *testing.T) {
	reg := map[string]skill.Manifest{
		"coding-agent": {Name: "coding-agent", Description: "work a repo's tasks"},
		"research":     {Name: "research", Description: "gather and synthesize sources"},
	}
	got, err := skill.SelectForGoal("please do some research on X", reg, "coding-agent")
	if err != nil {
		t.Fatalf("SelectForGoal: %v", err)
	}
	if got.Name != "research" {
		t.Errorf("SelectForGoal matched %q, want research (substring of the goal)", got.Name)
	}
	// Determinism: matching on Description too.
	got2, _ := skill.SelectForGoal("gather and synthesize sources for me", reg, "coding-agent")
	if got2.Name != "research" {
		t.Errorf("description match selected %q, want research", got2.Name)
	}
}

// TC-176-09: SelectForGoal falls back when nothing matches; errors on a bad fallback.
func TestTC176_09_SelectForGoalFallback(t *testing.T) {
	reg := map[string]skill.Manifest{
		"coding-agent": {Name: "coding-agent", Description: "work a repo's tasks"},
	}
	got, err := skill.SelectForGoal("something entirely unrelated", reg, "coding-agent")
	if err != nil {
		t.Fatalf("SelectForGoal fallback: %v", err)
	}
	if got.Name != "coding-agent" {
		t.Errorf("no-match fallback selected %q, want coding-agent", got.Name)
	}
	if _, err := skill.SelectForGoal("no match", reg, "nonexistent-fallback"); err == nil {
		t.Fatal("SelectForGoal with an unregistered fallback returned nil error, want an error")
	}
}

// TC-176-01: ADR 066 exists and records the required decisions.
func TestTC176_01_ADR066Exists(t *testing.T) {
	data, err := os.ReadFile("../../docs/architecture/decisions/066-general-skill-system-seam.md")
	if err != nil {
		t.Fatalf("read ADR 066: %v", err)
	}
	adr := strings.ToLower(string(data))
	for _, want := range []string{
		"governed capability",
		"execution strategy",
		"task 177",
		"re-evaluation trigger",
	} {
		if !strings.Contains(adr, want) {
			t.Errorf("ADR 066 does not mention %q", want)
		}
	}
	if !strings.Contains(adr, "accepted") {
		t.Error("ADR 066 is not marked accepted")
	}
}
