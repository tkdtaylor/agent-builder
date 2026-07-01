package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// recordingAnswerReporter records every reported line.
type recordingAnswerReporter struct{ lines []string }

func (r *recordingAnswerReporter) Report(_ context.Context, text string) error {
	r.lines = append(r.lines, text)
	return nil
}

// fakeAnalyzer returns a fixed analysis.
type fakeAnalyzer struct{ analysis GoalAnalysis }

func (f fakeAnalyzer) Analyze(supervisor.Task) (GoalAnalysis, error) { return f.analysis, nil }

// fakeAnswerer records its inputs and returns a canned answer.
type fakeAnswerer struct {
	answer    string
	err       error
	called    bool
	gotPrompt string
	gotTier   int
}

func (f *fakeAnswerer) Answer(_ context.Context, prompt string, tier int) (string, error) {
	f.called = true
	f.gotPrompt = prompt
	f.gotTier = tier
	return f.answer, f.err
}

// TC-139-01: a KindAnswer goal is answered — answerer gets the goal text + the
// emitted capability tier, the answer is reported, and the goal reaches
// StateConversing. No planner/policy involved.
func TestBeginGoalAnswerRoute(t *testing.T) {
	rep := &recordingAnswerReporter{}
	ans := &fakeAnswerer{answer: "Paris"}
	reg := NewStatusRegistry()
	o := New(nil, nil, rep, runtime.Config{},
		WithStatusRegistry(reg),
		WithGoalAnalyzer(fakeAnalyzer{GoalAnalysis{Kind: KindAnswer, Complexity: ComplexitySimple, CapabilityTier: 1}}),
		WithAnswerer(ans),
	)

	goal := supervisor.Task{ID: "g1", Spec: "What is the capital of France?"}
	if err := o.BeginGoal(context.Background(), goal); err != nil {
		t.Fatalf("BeginGoal error = %v", err)
	}

	if !ans.called {
		t.Fatal("answerer was not called for a KindAnswer goal")
	}
	if ans.gotPrompt != goal.Spec {
		t.Errorf("answerer prompt = %q, want %q", ans.gotPrompt, goal.Spec)
	}
	if ans.gotTier != 1 {
		t.Errorf("answerer tier = %d, want 1 (the analyzer's emitted CapabilityTier)", ans.gotTier)
	}
	if len(rep.lines) != 1 || rep.lines[0] != "Paris" {
		t.Errorf("reported lines = %v, want [\"Paris\"]", rep.lines)
	}
	// ADR 060 §6: the answer goal stays open for follow-ups (StateConversing), not terminal.
	if st, ok := reg.Get("g1"); !ok || st.State != StateConversing {
		t.Errorf("goal state = %v (ok=%v), want StateConversing", st.State, ok)
	}
}

// TC-139-02: a KindCoding goal falls through to the existing clarifier path — the
// answerer is NOT called and the coding clarifier's question is reported.
func TestBeginGoalCodingFallsThrough(t *testing.T) {
	rep := &recordingAnswerReporter{}
	ans := &fakeAnswerer{answer: "should not be used"}
	reg := NewStatusRegistry()
	o := New(nil, nil, rep, runtime.Config{},
		WithStatusRegistry(reg),
		WithClarifier(NewHeuristicClarifier()),
		WithGoalAnalyzer(fakeAnalyzer{GoalAnalysis{Kind: KindCoding, Complexity: ComplexitySimple}}),
		WithAnswerer(ans),
		WithGetEnv(func(string) string { return "" }),
	)

	// No repo → the coding clarifier asks which repository (proving the coding path ran).
	goal := supervisor.Task{ID: "g2", Spec: "add a feature"}
	if err := o.BeginGoal(context.Background(), goal); err != nil {
		t.Fatalf("BeginGoal error = %v", err)
	}

	if ans.called {
		t.Error("answerer was called for a KindCoding goal; want coding path")
	}
	joined := strings.Join(rep.lines, " | ")
	if !strings.Contains(strings.ToLower(joined), "repository") {
		t.Errorf("expected the coding clarifier question about a repository; got %v", rep.lines)
	}
	if st, ok := reg.Get("g2"); !ok || st.State != StateClarifying {
		t.Errorf("goal state = %v (ok=%v), want StateClarifying", st.State, ok)
	}
}

// TC-139-03: a KindAnswer goal with no answerer configured reports a clear message
// and fails the goal (never silently drops).
func TestBeginGoalAnswerNoAnswerer(t *testing.T) {
	rep := &recordingAnswerReporter{}
	reg := NewStatusRegistry()
	o := New(nil, nil, rep, runtime.Config{},
		WithStatusRegistry(reg),
		WithGoalAnalyzer(fakeAnalyzer{GoalAnalysis{Kind: KindAnswer, Complexity: ComplexitySimple}}),
		// no WithAnswerer
	)

	if err := o.BeginGoal(context.Background(), supervisor.Task{ID: "g3", Spec: "hi?"}); err != nil {
		t.Fatalf("BeginGoal error = %v", err)
	}
	if len(rep.lines) != 1 || !strings.Contains(rep.lines[0], "no answerer configured") {
		t.Errorf("reported lines = %v, want a 'no answerer configured' message", rep.lines)
	}
	if st, ok := reg.Get("g3"); !ok || st.State != StateFailed {
		t.Errorf("goal state = %v (ok=%v), want StateFailed", st.State, ok)
	}
}
