package cli

// Task 171 TC-171-01/02: approve/deny stdin grammar in parseMessageLine.

import (
	"errors"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

func TestTC171_01_ParseApproveDeny(t *testing.T) {
	seq := 0
	msg, ok, err := parseMessageLine("approve goal-7 task-3", &seq)
	if !ok || err != nil {
		t.Fatalf("approve = (ok=%v err=%v), want ok=true err=nil", ok, err)
	}
	if msg.Kind != supervisor.MsgApprove || msg.GoalID != "goal-7" || msg.TaskID != "task-3" {
		t.Errorf("approve msg = %+v, want {MsgApprove goal-7 task-3}", msg)
	}

	msg, ok, err = parseMessageLine("deny goal-7 task-3", &seq)
	if !ok || err != nil {
		t.Fatalf("deny = (ok=%v err=%v), want ok=true err=nil", ok, err)
	}
	if msg.Kind != supervisor.MsgDeny || msg.GoalID != "goal-7" || msg.TaskID != "task-3" {
		t.Errorf("deny msg = %+v, want {MsgDeny goal-7 task-3}", msg)
	}
}

func TestTC171_02_ParseApproveDenyMalformed(t *testing.T) {
	seq := 0
	for _, line := range []string{"approve goal-7", "approve", "deny goal-7", "deny"} {
		_, ok, err := parseMessageLine(line, &seq)
		if ok {
			t.Errorf("%q: ok=true, want false (malformed, never downgraded to new-goal)", line)
		}
		if !errors.Is(err, ErrMalformedInput) {
			t.Errorf("%q: err=%v, want ErrMalformedInput", line, err)
		}
	}
}
