package supervisor

// MessageKind is the type of an inbound operator message on the control plane
// (ADR 054 §2). It generalizes the inbound seam from goal-only to typed messages:
// a new goal to plan, a status query, new info for an in-flight goal, or a cancel.
type MessageKind int

const (
	// MsgNewGoal is a fresh goal to plan. Goal carries the supervisor.Task.
	MsgNewGoal MessageKind = iota
	// MsgStatus queries lifecycle state. GoalID addresses one goal; an empty
	// GoalID means a fleet-wide status query.
	MsgStatus
	// MsgInfo carries new information for an in-flight goal (GoalID + Text). It is
	// folded at the goal's next checkpoint (the fold body is task 115).
	MsgInfo
	// MsgCancel cancels a goal and tears down its in-flight workers (GoalID). The
	// teardown body is task 116.
	MsgCancel
	// MsgConfirm signals that clarification is complete and the orchestrator should
	// proceed to planning (ADR 058).
	MsgConfirm
	// MsgApprove approves a paused sub-goal (GoalID + TaskID) so it re-dispatches
	// (ADR 065, task 171). It resolves a task 170 sub-goal-level require_approval pause.
	MsgApprove
	// MsgDeny denies a paused sub-goal (GoalID + TaskID) so it is marked needs-human
	// without dispatching (ADR 065, task 171).
	MsgDeny
)

// String renders a MessageKind as its lowercase grammar name for reports and
// test assertions.
func (k MessageKind) String() string {
	switch k {
	case MsgNewGoal:
		return "new-goal"
	case MsgStatus:
		return "status"
	case MsgInfo:
		return "info"
	case MsgCancel:
		return "cancel"
	case MsgConfirm:
		return "confirm"
	case MsgApprove:
		return "approve"
	case MsgDeny:
		return "deny"
	default:
		return "unknown"
	}
}

// Message is one typed inbound operator message read off the control plane's
// inbound seam (ADR 054 §2). The control loop is the only reader of the seam
// (no concurrent Next() races); it routes each Message by Kind.
type Message struct {
	Kind   MessageKind // how the control loop dispatches this message
	GoalID string      // addresses status/info/cancel; the new goal's ID for new-goal
	Goal   Task        // populated for MsgNewGoal
	Text   string      // info payload / free-form
	// TaskID addresses a specific paused sub-goal for MsgApprove/MsgDeny (ADR 065,
	// task 171). Zero value for every other kind.
	TaskID string
}

// MessageSource is the inbound operator seam for the async control plane (ADR 054
// §2). It is a NEW seam alongside GoalSource — GoalSource is intentionally left
// intact because it is also the per-worker, in-box recipe task source
// (runtime.Run's GoalSourceFactory path), a different inbound seam that must not
// be disturbed.
//
// Next returns the next typed message, a boolean indicating whether a message was
// read (ok=false at EOF / no-more-input, on which the control plane drains and
// exits), and an optional error. Like GoalSource, the signature is pure-stdlib so
// adding it drags no new import into this package (F-003 / F-007 remain satisfied).
type MessageSource interface {
	Next() (msg Message, ok bool, err error)
}
