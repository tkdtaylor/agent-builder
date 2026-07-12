package runstore

// Store is the durable run-journal seam. FileStore is the file-backed
// implementation; callers depend on this interface so an in-memory or alternative
// backend can be substituted in tests.
type Store interface {
	// Save durably appends a record (last-write-wins per GoalID) and returns only
	// after the write is fsync'd.
	Save(rec Record) error
	// Load returns the latest record for goalID. The bool is false (with a nil
	// error and zero Record) when the goal is unknown or has been deleted.
	Load(goalID string) (Record, bool, error)
	// ListInFlight returns the latest record for every goal whose status is not
	// terminal (not completed/failed) and not deleted, in a stable order.
	ListInFlight() ([]Record, error)
	// Delete durably tombstones a goal so it no longer appears in Load/ListInFlight.
	Delete(goalID string) error
	// Compact atomically snapshots the current state and truncates the journal.
	Compact() error
}
