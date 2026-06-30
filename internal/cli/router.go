package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// ErrMalformedInput marks a line the local-test grammar could not parse into a
// valid message (e.g. a `cancel` with no goalID). The control loop treats it as
// non-fatal — it reports the line gracefully and continues reading — distinct from
// a hard source/IO error which drains the plane (ADR 054 §2 fail-loud-but-graceful).
var ErrMalformedInput = errors.New("orchestrate: malformed input line")

// isParseError reports whether err is a recoverable grammar parse error (the
// control loop continues) rather than a fatal source error (the loop drains).
func isParseError(err error) bool {
	return errors.Is(err, ErrMalformedInput)
}

// envMessageSource is the line-oriented local-test MessageSource (ADR 054 §2). It
// generalizes the goal-only env/stdin source into the typed message protocol so the
// operator can drive the whole control plane locally without Telegram — the seam
// every L5/L6 verification of the async control plane hangs off.
//
// Grammar (one message per line / call):
//
//	<bare line>            → MsgNewGoal (the line is the goal Spec)
//	status                 → MsgStatus, GoalID="" (fleet)
//	status <goalID>        → MsgStatus, GoalID=<goalID>
//	info <goalID> <text>   → MsgInfo, GoalID=<goalID>, Text=<text>
//	cancel <goalID>        → MsgCancel, GoalID=<goalID>
//	confirm <goalID>       → MsgConfirm, GoalID=<goalID>
//
// A control verb with a missing required argument (e.g. `cancel` with no goalID,
// `info goal-7` with no text) is a malformed control line: it does NOT silently
// become a new-goal and does NOT panic — Next surfaces a parse error for that line
// (the control loop reports it and continues). EOF / no-more-input returns
// ok=false, nil error, on which the control plane drains and exits.
//
// AGENT_BUILDER_GOAL_SPEC, when set, is delivered as the first message (a single
// MsgNewGoal) before any stdin line — preserving the single-goal env path the
// validation harness uses. The new goal's ID comes from AGENT_BUILDER_GOAL_ID
// (default "goal") and Repo from AGENT_BUILDER_GOAL_REPO, matching the prior
// envGoalSource contract.
type envMessageSource struct {
	getenv func(string) string

	mu      sync.Mutex
	scanner *bufio.Scanner
	envSpec string // AGENT_BUILDER_GOAL_SPEC, consumed once if non-empty
	envID   string
	envRepo string
	envDone bool // env spec already delivered (or absent)
	inited  bool // lazy init guard for scanner + env read
	stdin   io.Reader
	autoSeq int // monotonic counter for auto-assigned new-goal IDs from bare stdin lines
}

func newEnvMessageSource(getenv func(string) string, stdin io.Reader) supervisor.MessageSource {
	return &envMessageSource{getenv: getenv, stdin: stdin}
}

func (s *envMessageSource) init() {
	if s.inited {
		return
	}
	s.inited = true
	s.envSpec = strings.TrimSpace(s.getenv(EnvGoalSpec))
	s.envID = strings.TrimSpace(s.getenv(EnvGoalID))
	s.envRepo = strings.TrimSpace(s.getenv(EnvGoalRepo))
	if s.envSpec == "" {
		s.envDone = true
	}
	if s.stdin != nil {
		s.scanner = bufio.NewScanner(s.stdin)
	}
}

// Next returns the next typed message. It is called only by the single control-loop
// goroutine (no concurrent Next() races — ADR 054 §1); the mutex guards the lazy
// init + scanner state defensively.
func (s *envMessageSource) Next() (supervisor.Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()

	// 1. The env single-goal spec is delivered first, exactly once.
	if !s.envDone {
		s.envDone = true
		id := s.envID
		if id == "" {
			id = "goal"
		}
		return supervisor.Message{
			Kind:   supervisor.MsgNewGoal,
			GoalID: id,
			Goal:   supervisor.Task{ID: id, Repo: s.envRepo, Spec: s.envSpec},
		}, true, nil
	}

	// 2. Then stdin lines, one message per non-blank line.
	if s.scanner == nil {
		return supervisor.Message{}, false, nil
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" {
			continue // skip blank lines
		}
		return parseMessageLine(line, &s.autoSeq)
	}
	if err := s.scanner.Err(); err != nil {
		return supervisor.Message{}, false, fmt.Errorf("orchestrate: read message from stdin: %w", err)
	}
	return supervisor.Message{}, false, nil
}

// parseMessageLine maps one non-empty line to a typed Message per the ADR 054 §2
// grammar. A control verb (status/info/cancel/confirm) with a missing required argument
// returns a parse error — it never silently degrades to a new-goal. A bare line is
// a new-goal whose ID is auto-assigned ("goal-N") so multiple stdin goals stay
// addressable and collision-free.
func parseMessageLine(line string, autoSeq *int) (supervisor.Message, bool, error) {
	fields := strings.Fields(line)
	verb := fields[0]
	switch verb {
	case "status":
		// `status` (fleet) or `status <goalID>`.
		goalID := ""
		if len(fields) >= 2 {
			goalID = fields[1]
		}
		return supervisor.Message{Kind: supervisor.MsgStatus, GoalID: goalID}, true, nil
	case "info":
		// `info <goalID> <text...>` — both goalID and text required.
		if len(fields) < 3 {
			return supervisor.Message{}, false, fmt.Errorf("%w: %q (want: info <goalID> <text>)", ErrMalformedInput, line)
		}
		goalID := fields[1]
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(line, verb)), goalID))
		return supervisor.Message{Kind: supervisor.MsgInfo, GoalID: goalID, Text: text}, true, nil
	case "cancel":
		// `cancel <goalID>` — goalID required.
		if len(fields) < 2 {
			return supervisor.Message{}, false, fmt.Errorf("%w: %q (want: cancel <goalID>)", ErrMalformedInput, line)
		}
		return supervisor.Message{Kind: supervisor.MsgCancel, GoalID: fields[1]}, true, nil
	case "confirm":
		// `confirm <goalID>` — goalID required.
		if len(fields) < 2 {
			return supervisor.Message{Kind: supervisor.MsgConfirm}, false, fmt.Errorf("%w: %q (want: confirm <goalID>)", ErrMalformedInput, line)
		}
		return supervisor.Message{Kind: supervisor.MsgConfirm, GoalID: fields[1]}, true, nil
	default:
		// Bare line → a new goal. Auto-assign a collision-free ID.
		*autoSeq++
		id := fmt.Sprintf("goal-%d", *autoSeq)
		return supervisor.Message{
			Kind:   supervisor.MsgNewGoal,
			GoalID: id,
			Goal:   supervisor.Task{ID: id, Spec: line},
		}, true, nil
	}
}

// commandMailboxes is the per-goal command-mailbox map (ADR 054 §3 / §6 new-race
// surface (b)). Each goal actor gets a small buffered channel keyed by goalID into
// which the control-loop router delivers MsgInfo/MsgCancel. The mailbox MUST be
// created before the goal actor is registered/started (register-then-start), so a
// cancel/info arriving at actor startup is never lost or raced.
//
// It is mutex-guarded shared state: the control loop creates a mailbox (Create) on
// each new-goal and looks one up (Lookup) on each info/cancel. A goal that has no
// mailbox (unknown goalID) yields ok=false from Lookup — the router answers
// "no such goal" gracefully rather than panicking, and never auto-creates a mailbox
// for an unknown goal.
type commandMailboxes struct {
	mu    sync.Mutex
	boxes map[string]chan supervisor.Message
}

// mailboxBuffer is the per-goal mailbox capacity. A small buffer absorbs an
// info/cancel that arrives before the actor's command-drain loop is reading,
// without the router blocking the control loop on delivery.
const mailboxBuffer = 8

func newCommandMailboxes() *commandMailboxes {
	return &commandMailboxes{boxes: make(map[string]chan supervisor.Message)}
}

// Create makes (or returns the existing) mailbox for a goalID. It is called by the
// control loop on a new-goal BEFORE the actor goroutine is spawned, so the mailbox
// exists for any subsequent info/cancel addressed to that goal.
func (m *commandMailboxes) Create(goalID string) chan supervisor.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.boxes[goalID]; ok {
		return ch
	}
	ch := make(chan supervisor.Message, mailboxBuffer)
	m.boxes[goalID] = ch
	return ch
}

// Lookup returns the mailbox for a goalID and whether it exists. It NEVER creates a
// mailbox — an unknown goalID returns (nil, false) so the router can answer "no such
// goal" gracefully without leaving an orphan mailbox behind.
func (m *commandMailboxes) Lookup(goalID string) (chan supervisor.Message, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.boxes[goalID]
	return ch, ok
}

// deliver routes a MsgInfo/MsgCancel to the addressed goal's mailbox. It returns
// true on delivery and false when the goalID is unknown (no mailbox) — the caller
// reports "no such goal" on false. Delivery is non-blocking on a full mailbox: a
// full buffer means the actor is backed up; the message is dropped with a false
// return rather than stalling the single control loop (the actor's own draining is
// task 115/116's concern).
func (m *commandMailboxes) deliver(msg supervisor.Message) bool {
	ch, ok := m.Lookup(msg.GoalID)
	if !ok {
		return false
	}
	select {
	case ch <- msg:
		return true
	default:
		return false
	}
}
