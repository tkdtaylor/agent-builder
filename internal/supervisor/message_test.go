package supervisor_test

import (
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// TestMsgConfirmIsDistinctKind verifies that MsgConfirm has a distinct, non-zero,
// non-conflicting kind.
// Satisfies TC-124-01.
func TestMsgConfirmIsDistinctKind(t *testing.T) {
	kinds := map[int]string{
		int(supervisor.MsgNewGoal): "new-goal",
		int(supervisor.MsgStatus):  "status",
		int(supervisor.MsgInfo):    "info",
		int(supervisor.MsgCancel):  "cancel",
		int(supervisor.MsgConfirm): "confirm",
	}

	if len(kinds) != 5 {
		t.Errorf("expected 5 distinct message kinds, got map size %d (overlap detected)", len(kinds))
	}
}

// TestMsgConfirmString verifies that String() renders the canonical lowercase
// name for all message kinds.
// Satisfies TC-124-02.
func TestMsgConfirmString(t *testing.T) {
	tests := []struct {
		kind supervisor.MessageKind
		want string
	}{
		{supervisor.MsgNewGoal, "new-goal"},
		{supervisor.MsgStatus, "status"},
		{supervisor.MsgInfo, "info"},
		{supervisor.MsgCancel, "cancel"},
		{supervisor.MsgConfirm, "confirm"},
		{supervisor.MessageKind(99), "unknown"},
	}

	for _, tc := range tests {
		got := tc.kind.String()
		if got != tc.want {
			t.Errorf("MessageKind(%d).String() = %q; want %q", int(tc.kind), got, tc.want)
		}
	}
}

// TestMessageKindIotaOrderPreserved verifies that existing constants are not
// renumbered and the new MsgConfirm constant is exactly 4.
// Satisfies TC-124-03.
func TestMessageKindIotaOrderPreserved(t *testing.T) {
	if int(supervisor.MsgNewGoal) != 0 {
		t.Errorf("MsgNewGoal changed from 0 to %d", supervisor.MsgNewGoal)
	}
	if int(supervisor.MsgStatus) != 1 {
		t.Errorf("MsgStatus changed from 1 to %d", supervisor.MsgStatus)
	}
	if int(supervisor.MsgInfo) != 2 {
		t.Errorf("MsgInfo changed from 2 to %d", supervisor.MsgInfo)
	}
	if int(supervisor.MsgCancel) != 3 {
		t.Errorf("MsgCancel changed from 3 to %d", supervisor.MsgCancel)
	}
	if int(supervisor.MsgConfirm) != 4 {
		t.Errorf("MsgConfirm is not 4, got %d", supervisor.MsgConfirm)
	}
}
