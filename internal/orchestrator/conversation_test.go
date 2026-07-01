package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// echoAnswerer returns a canned sequence of answers and records the prompts it saw.
type echoAnswerer struct {
	answers []string
	i       int
	prompts []string
}

func (e *echoAnswerer) Answer(_ context.Context, prompt string, _ int) (string, error) {
	e.prompts = append(e.prompts, prompt)
	a := "?"
	if e.i < len(e.answers) {
		a = e.answers[e.i]
	}
	e.i++
	return a, nil
}

// TC-141-03: composeTranscript renders the running conversation deterministically.
func TestComposeTranscript(t *testing.T) {
	c := &conversation{turns: []convTurn{
		{roleUser, "What is the capital of France?"},
		{roleAssistant, "Paris"},
		{roleUser, "What about Germany?"},
	}}
	got := composeTranscript(c)
	want := "User: What is the capital of France?\nAssistant: Paris\nUser: What about Germany?\nAssistant:"
	if got != want {
		t.Errorf("composeTranscript =\n%q\nwant\n%q", got, want)
	}
}

// TC-141-01/02: the opening answer starts a conversation (StateConversing); a
// follow-up (ContinueAnswer) re-answers with the full transcript as context and
// grows the history to four turns.
func TestConversingAnswerAndFollowUp(t *testing.T) {
	rep := &recordingAnswerReporter{}
	ans := &echoAnswerer{answers: []string{"Paris", "Berlin"}}
	reg := NewStatusRegistry()
	o := New(nil, nil, rep, runtime.Config{},
		WithStatusRegistry(reg),
		WithGoalAnalyzer(fakeAnalyzer{GoalAnalysis{Kind: KindAnswer, Complexity: ComplexitySimple}}),
		WithAnswerer(ans),
	)

	// Opening question.
	goal := supervisor.Task{ID: "c1", Spec: "What is the capital of France?"}
	if err := o.BeginGoal(context.Background(), goal); err != nil {
		t.Fatalf("BeginGoal error = %v", err)
	}
	if st, _ := reg.Get("c1"); st.State != StateConversing {
		t.Fatalf("after answer, state = %v, want StateConversing", st.State)
	}
	if !o.IsConversing("c1") {
		t.Error("IsConversing(c1) = false, want true")
	}

	// Follow-up question.
	if err := o.ContinueAnswer(context.Background(), "c1", "What about Germany?"); err != nil {
		t.Fatalf("ContinueAnswer error = %v", err)
	}

	// Both replies were reported, in order.
	if len(rep.lines) != 2 || rep.lines[0] != "Paris" || rep.lines[1] != "Berlin" {
		t.Errorf("reported lines = %v, want [Paris Berlin]", rep.lines)
	}
	// The follow-up prompt carried the prior turns as context.
	if len(ans.prompts) != 2 {
		t.Fatalf("answerer saw %d prompts, want 2", len(ans.prompts))
	}
	followup := ans.prompts[1]
	for _, want := range []string{"What is the capital of France?", "Paris", "What about Germany?"} {
		if !strings.Contains(followup, want) {
			t.Errorf("follow-up prompt %q missing context %q", followup, want)
		}
	}
	// History grew to four turns and the goal is still conversing.
	o.convMu.Lock()
	n := len(o.conversations["c1"].turns)
	o.convMu.Unlock()
	if n != 4 {
		t.Errorf("conversation has %d turns, want 4", n)
	}
	if st, _ := reg.Get("c1"); st.State != StateConversing {
		t.Errorf("after follow-up, state = %v, want StateConversing", st.State)
	}
}

// TC-141-04 (partial): EndConversation marks the goal terminal and drops history.
func TestEndConversation(t *testing.T) {
	rep := &recordingAnswerReporter{}
	reg := NewStatusRegistry()
	o := New(nil, nil, rep, runtime.Config{},
		WithStatusRegistry(reg),
		WithGoalAnalyzer(fakeAnalyzer{GoalAnalysis{Kind: KindAnswer, Complexity: ComplexitySimple}}),
		WithAnswerer(&echoAnswerer{answers: []string{"Paris"}}),
	)
	_ = o.BeginGoal(context.Background(), supervisor.Task{ID: "c2", Spec: "capital of France?"})
	o.EndConversation("c2")
	if st, _ := reg.Get("c2"); st.State != StateDone {
		t.Errorf("after EndConversation, state = %v, want StateDone", st.State)
	}
	if o.IsConversing("c2") {
		t.Error("IsConversing(c2) = true after EndConversation, want false")
	}
}
