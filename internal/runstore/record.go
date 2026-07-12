// Package runstore is a stdlib-only, file-backed durable run journal for the
// orchestrator's per-goal run/attempt state. It implements the mechanism ADR 065
// commits to: an append-only JSONL journal with snapshot/compaction and crash-safe
// temp+rename writes, recording goal, plan, per-task attempt state, pending
// approvals, and terminal status.
//
// It is a strict leaf: it imports only the Go standard library, never any other
// agent-builder/internal package (enforced by fitness function F-015). Callers
// (the orchestrator, task 168) compose it on top; the store itself knows nothing
// about them.
//
// On-disk layout, under the directory passed to NewFileStore:
//
//	journal.jsonl  append-only, one JSON-encoded Record per line, fsync'd per Save.
//	snapshot.json  a compacted point-in-time index (map[GoalID]Record), written
//	               atomically (temp file + fsync + rename) by Compact.
//
// Replay order at construction is snapshot.json first, then journal.jsonl, both
// last-write-wins per GoalID. A Record with Deleted == true is a tombstone: it
// removes the goal from the reconstructed index and survives replay.
//
// Crash-safety contract: a truncated/partial FINAL journal line (a crash mid-
// append, before fsync completed) is tolerated and silently dropped on replay. A
// malformed line anywhere earlier is a fail-loud corruption error, never a silent
// reset to empty state.
package runstore

import (
	"encoding/json"
	"time"
)

// Status is the closed set of lifecycle states a run can be in. StatusCompleted
// and StatusFailed are the two TERMINAL statuses excluded from ListInFlight.
type Status string

const (
	StatusPending          Status = "pending"
	StatusRunning          Status = "running"
	StatusAwaitingApproval Status = "awaiting_approval"
	StatusCompleted        Status = "completed"
	StatusFailed           Status = "failed"
	StatusNeedsHuman       Status = "needs-human"
)

// isTerminal reports whether a status excludes a record from ListInFlight. Only
// completed and failed are terminal; needs-human is still in-flight (a human is
// expected to act on it), matching the orchestrator's needs-human semantics.
func (s Status) isTerminal() bool {
	return s == StatusCompleted || s == StatusFailed
}

// AttemptState is the per-task attempt history entry: which task, which attempt
// number, its status, an optional free-text detail, and when it was last updated.
type AttemptState struct {
	TaskID    string    `json:"task_id"`
	Attempt   int       `json:"attempt"`
	Status    Status    `json:"status"`
	Detail    string    `json:"detail,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PendingApproval records that a task is blocked awaiting a human approval
// decision, with the reason the gate gave and when it was requested.
type PendingApproval struct {
	TaskID      string    `json:"task_id"`
	Reason      string    `json:"reason"`
	RequestedAt time.Time `json:"requested_at"`
}

// Record is the full durable state of one goal's run. Plan is stored as raw JSON
// so runstore never needs to import the orchestrator's plan type (leaf discipline).
// Deleted marks a tombstone (see Delete); it is omitted from the wire form on a
// normal record.
type Record struct {
	GoalID    string            `json:"goal_id"`
	Goal      string            `json:"goal,omitempty"`
	Plan      json.RawMessage   `json:"plan,omitempty"`
	Attempts  []AttemptState    `json:"attempts,omitempty"`
	Pending   []PendingApproval `json:"pending,omitempty"`
	Status    Status            `json:"status"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Deleted   bool              `json:"deleted,omitempty"`
}
