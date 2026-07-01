package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/audit"
)

// convRole labels a turn in a KindAnswer conversation transcript (ADR 060 §6).
type convRole string

const (
	roleUser      convRole = "User"
	roleAssistant convRole = "Assistant"
)

// convTurn is one turn of a multi-turn answer conversation.
type convTurn struct {
	role convRole
	text string
}

// conversation is the in-process, per-goal history for a KindAnswer goal that is
// holding a multi-turn conversation (ADR 060 §6). complexity is carried so each
// follow-up routes to the same brain-capability floor as the opening question.
type conversation struct {
	turns      []convTurn
	complexity GoalComplexity
}

// composeTranscript renders the running conversation into the single prompt the
// Answerer takes — each brain CLI is stateless single-shot, so context is carried
// in the prompt. It ends with "Assistant:" to cue the next reply.
func composeTranscript(c *conversation) string {
	var b strings.Builder
	for _, t := range c.turns {
		b.WriteString(string(t.role))
		b.WriteString(": ")
		b.WriteString(t.text)
		b.WriteString("\n")
	}
	b.WriteString("Assistant:")
	return b.String()
}

// startConversation records the opening turn pair and returns nothing; called by
// answerGoal after the first reply so the goal can linger in StateConversing.
func (o *Orchestrator) startConversation(goalID, question, answer string, complexity GoalComplexity) {
	o.convMu.Lock()
	defer o.convMu.Unlock()
	if o.conversations == nil {
		o.conversations = make(map[string]*conversation)
	}
	o.conversations[goalID] = &conversation{
		turns: []convTurn{
			{role: roleUser, text: question},
			{role: roleAssistant, text: answer},
		},
		complexity: complexity,
	}
}

// ContinueAnswer handles a follow-up question in a multi-turn conversation (ADR 060
// §6): it appends the user turn, re-answers with the full transcript as context,
// reports the reply, and appends the assistant turn — staying in StateConversing.
// Read-only inference (no approval), like the opening answer.
func (o *Orchestrator) ContinueAnswer(ctx context.Context, goalID, text string) error {
	o.convMu.Lock()
	conv, ok := o.conversations[goalID]
	if !ok || conv == nil {
		o.convMu.Unlock()
		return fmt.Errorf("orchestrator: no active conversation for goal %q", goalID)
	}
	if o.answerer == nil {
		o.convMu.Unlock()
		return fmt.Errorf("orchestrator: no answerer configured for follow-up on goal %q", goalID)
	}
	conv.turns = append(conv.turns, convTurn{role: roleUser, text: text})
	prompt := composeTranscript(conv)
	complexity := conv.complexity
	o.convMu.Unlock()

	answer, err := o.answerer.Answer(ctx, prompt, complexity)
	if err != nil {
		if repErr := o.reporter.Report(ctx, fmt.Sprintf("Could not answer follow-up for goal %q: %v", goalID, err)); repErr != nil {
			return fmt.Errorf("orchestrator: report follow-up error: %w", repErr)
		}
		return nil
	}

	o.convMu.Lock()
	conv.turns = append(conv.turns, convTurn{role: roleAssistant, text: answer})
	o.convMu.Unlock()

	o.emitFleetEvent(audit.AuditEvent{
		Action: audit.ActionCompletion, TaskID: goalID, RunID: goalID,
		Detail: audit.EventDetail{Reason: string(KindAnswer)},
	})
	if err := o.reporter.Report(ctx, answer); err != nil {
		return fmt.Errorf("orchestrator: report follow-up answer: %w", err)
	}
	return nil
}

// EndConversation marks a conversing goal terminal (StateDone) and drops its
// history. Called by the goal actor on cancel / source EOF.
func (o *Orchestrator) EndConversation(goalID string) {
	o.convMu.Lock()
	delete(o.conversations, goalID)
	o.convMu.Unlock()
	o.registry.SetState(goalID, StateDone)
}

// IsConversing reports whether a goal is currently in a multi-turn conversation.
func (o *Orchestrator) IsConversing(goalID string) bool {
	if o.registry == nil {
		return false
	}
	st, ok := o.registry.Get(goalID)
	return ok && st.State == StateConversing
}
