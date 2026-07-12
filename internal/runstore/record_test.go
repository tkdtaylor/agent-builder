package runstore

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// TC-167-01: exported types are JSON round-trippable, field-for-field.
func TestTC167_01_RecordJSONRoundTrip(t *testing.T) {
	// Status constants have the exact documented wire values.
	if string(StatusRunning) != "running" {
		t.Fatalf("StatusRunning = %q, want %q", StatusRunning, "running")
	}
	if string(StatusCompleted) != "completed" || string(StatusFailed) != "failed" {
		t.Fatalf("terminal status values wrong: completed=%q failed=%q", StatusCompleted, StatusFailed)
	}
	if string(StatusPending) != "pending" || string(StatusAwaitingApproval) != "awaiting_approval" || string(StatusNeedsHuman) != "needs-human" {
		t.Fatalf("status values wrong: pending=%q awaiting=%q needs=%q", StatusPending, StatusAwaitingApproval, StatusNeedsHuman)
	}

	ts := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	orig := Record{
		GoalID:    "g1",
		Goal:      "do the thing",
		Plan:      json.RawMessage(`{"goal":"x"}`),
		Attempts:  []AttemptState{{TaskID: "t1", Attempt: 2, Status: StatusRunning, Detail: "retry", UpdatedAt: ts}},
		Pending:   []PendingApproval{{TaskID: "t2", Reason: "high risk", RequestedAt: ts}},
		Status:    StatusAwaitingApproval,
		CreatedAt: ts,
		UpdatedAt: ts,
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, got) {
		t.Fatalf("round trip mismatch:\n orig=%+v\n  got=%+v", orig, got)
	}
	// Plan raw bytes preserved exactly.
	if string(got.Plan) != `{"goal":"x"}` {
		t.Fatalf("Plan raw bytes = %q, want %q", got.Plan, `{"goal":"x"}`)
	}
}
