package orchestrator

import (
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// TC-128-01: HeuristicClarifier returns Ready=true for a sufficiently specific goal
func TestHeuristicClarifierReadyForSpecificGoal(t *testing.T) {
	c := NewHeuristicClarifier()
	goal := supervisor.Task{
		ID:   "goal-1",
		Spec: "add a retry backoff to the exec-sandbox in github.com/tkdtaylor/exec-sandbox",
	}

	res, err := c.Clarify(goal)
	if err != nil {
		t.Fatalf("TC-128-01: unexpected error: %v", err)
	}

	if !res.Ready {
		t.Errorf("TC-128-01: expected Ready == true, got false")
	}

	if len(res.Questions) != 0 {
		t.Errorf("TC-128-01: expected 0 questions, got %d: %v", len(res.Questions), res.Questions)
	}
}

// TC-128-02: HeuristicClarifier returns questions for a vague or repo-less goal
func TestHeuristicClarifierQuestionsForVagueGoal(t *testing.T) {
	c := NewHeuristicClarifier()

	// Sub-case A — empty spec
	t.Run("empty spec", func(t *testing.T) {
		goal := supervisor.Task{ID: "goal-2", Spec: "   "}
		res, err := c.Clarify(goal)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Ready {
			t.Errorf("expected Ready == false")
		}
		if len(res.Questions) != 1 {
			t.Fatalf("expected exactly 1 question, got %d", len(res.Questions))
		}
		if !strings.Contains(strings.ToLower(res.Questions[0]), "build") {
			t.Errorf("question %q does not contain 'build'", res.Questions[0])
		}
	})

	// Sub-case B — very short spec without a repo
	t.Run("no repo", func(t *testing.T) {
		goal := supervisor.Task{ID: "goal-3", Spec: "fix bug"}
		res, err := c.Clarify(goal)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Ready {
			t.Errorf("expected Ready == false")
		}
		if len(res.Questions) != 1 {
			t.Fatalf("expected exactly 1 question, got %d", len(res.Questions))
		}
		q := strings.ToLower(res.Questions[0])
		if !strings.Contains(q, "repo") && !strings.Contains(q, "repository") {
			t.Errorf("question %q does not contain 'repo' or 'repository'", res.Questions[0])
		}
	})

	// Sub-case C — spec with repo but no meaningful action
	t.Run("repo only", func(t *testing.T) {
		goal := supervisor.Task{ID: "goal-4", Spec: "github.com/tkdtaylor/exec-sandbox"}
		res, err := c.Clarify(goal)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Ready {
			t.Errorf("expected Ready == false")
		}
		if len(res.Questions) != 1 {
			t.Fatalf("expected exactly 1 question, got %d", len(res.Questions))
		}
		q := strings.ToLower(res.Questions[0])
		if !strings.Contains(q, "build") {
			t.Errorf("question %q does not contain 'build'", res.Questions[0])
		}
	})
}
