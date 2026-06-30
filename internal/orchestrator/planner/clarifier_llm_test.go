package planner_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator/planner"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/router"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

type spyClarifierSeams struct {
	mu           sync.Mutex
	resolveErr   error
	invokeErr    error
	resolveCalls int
	invokePrompts []string
	cannedText   string
	entry        registry.RegistryEntry
}

func (s *spyClarifierSeams) Resolve(_ context.Context, _ router.RoutingSpec) (registry.RegistryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolveCalls++
	if s.resolveErr != nil {
		return registry.RegistryEntry{}, s.resolveErr
	}
	return s.entry, nil
}

func (s *spyClarifierSeams) Invoke(_ context.Context, _ registry.RegistryEntry, prompt string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invokePrompts = append(s.invokePrompts, prompt)
	if s.invokeErr != nil {
		return "", s.invokeErr
	}
	return s.cannedText, nil
}

func (s *spyClarifierSeams) prompts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.invokePrompts))
	copy(out, s.invokePrompts)
	return out
}

// TC-131-01: LLMClarifier calls the Invoker with the goal text as part of the prompt
func TestLLMClarifierCallsInvokerWithGoalText(t *testing.T) {
	seams := &spyClarifierSeams{
		cannedText: `{"ready": true, "questions": []}`,
	}
	clar := planner.NewLLMClarifier(seams, seams.Invoke)

	goal := supervisor.Task{
		ID:   "goal-1",
		Spec: "add retry backoff to exec-sandbox in github.com/tkdtaylor/exec-sandbox",
	}
	res, err := clar.Clarify(goal)
	if err != nil {
		t.Fatalf("Clarify returned error: %v", err)
	}

	if !res.Ready {
		t.Error("expected Ready to be true")
	}

	prompts := seams.prompts()
	if len(prompts) != 1 {
		t.Fatalf("expected exactly 1 invoke call, got %d", len(prompts))
	}

	if !strings.Contains(prompts[0], goal.Spec) {
		t.Errorf("expected prompt to contain goal spec %q, prompt: %q", goal.Spec, prompts[0])
	}
}

// TC-131-02: LLMClarifier parses a READY response -> Clarification.Ready=true
func TestLLMClarifierParsesReadyResponse(t *testing.T) {
	tcs := []struct {
		name       string
		cannedText string
	}{
		{
			name:       "Standard JSON",
			cannedText: `{"ready": true, "questions": []}`,
		},
		{
			name:       "JSON with surrounding prose",
			cannedText: `Here is the response: {"ready": true} Hope it helps!`,
		},
		{
			name:       "Lenient plaintext case-insensitive READY",
			cannedText: `READY`,
		},
		{
			name:       "Lenient plaintext ready: true",
			cannedText: `ready: true`,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			seams := &spyClarifierSeams{
				cannedText: tc.cannedText,
			}
			clar := planner.NewLLMClarifier(seams, seams.Invoke)

			res, err := clar.Clarify(supervisor.Task{ID: "g1", Spec: "build something"})
			if err != nil {
				t.Fatalf("Clarify failed: %v", err)
			}

			if !res.Ready {
				t.Errorf("expected Ready == true, got false. response text: %q", tc.cannedText)
			}
			if len(res.Questions) != 0 {
				t.Errorf("expected 0 questions, got %d: %v", len(res.Questions), res.Questions)
			}
		})
	}
}

// TC-131-03: LLMClarifier parses a response with questions -> Ready=false + Questions populated
func TestLLMClarifierParsesQuestionsFromResponse(t *testing.T) {
	tcs := []struct {
		name       string
		cannedText string
		want       []string
	}{
		{
			name:       "Standard JSON with questions",
			cannedText: `{"ready": false, "questions": ["Which repo?", "What should change?"]}`,
			want:       []string{"Which repo?", "What should change?"},
		},
		{
			name:       "Lenient plaintext bullet points",
			cannedText: "The goal is vague.\n- Which repo?\n* What should change?",
			want:       []string{"Which repo?", "What should change?"},
		},
		{
			name:       "Lenient plaintext question lines",
			cannedText: "Which repo?\nWhat should change?",
			want:       []string{"Which repo?", "What should change?"},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			seams := &spyClarifierSeams{
				cannedText: tc.cannedText,
			}
			clar := planner.NewLLMClarifier(seams, seams.Invoke)

			res, err := clar.Clarify(supervisor.Task{ID: "g1", Spec: "build something"})
			if err != nil {
				t.Fatalf("Clarify failed: %v", err)
			}

			if res.Ready {
				t.Error("expected Ready to be false")
			}
			if len(res.Questions) != len(tc.want) {
				t.Fatalf("got %d questions, want %d. questions: %v", len(res.Questions), len(tc.want), res.Questions)
			}
			for i, q := range tc.want {
				if res.Questions[i] != q {
					t.Errorf("Questions[%d] = %q, want %q", i, res.Questions[i], q)
				}
			}
		})
	}
}

func TestLLMClarifierResolverError(t *testing.T) {
	seams := &spyClarifierSeams{
		resolveErr: errors.New("resolver error"),
	}
	clar := planner.NewLLMClarifier(seams, seams.Invoke)

	_, err := clar.Clarify(supervisor.Task{ID: "g1", Spec: "build something"})
	if err == nil {
		t.Error("expected resolve error to propagate, got nil")
	}
}

func TestLLMClarifierInvokeError(t *testing.T) {
	seams := &spyClarifierSeams{
		invokeErr: errors.New("invoke error"),
	}
	clar := planner.NewLLMClarifier(seams, seams.Invoke)

	_, err := clar.Clarify(supervisor.Task{ID: "g1", Spec: "build something"})
	if err == nil {
		t.Error("expected invoke error to propagate, got nil")
	}
}
