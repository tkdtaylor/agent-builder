package runstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// countLines returns the number of newline-terminated lines in the journal.
func countLines(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, journalName))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read journal: %v", err)
	}
	if len(data) == 0 {
		return 0
	}
	return strings.Count(string(data), "\n")
}

// TC-167-02: FileStore satisfies Store; basic Save/Load round trip.
func TestTC167_02_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.Save(Record{GoalID: "g1", Status: StatusRunning}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := store.Load("g1")
	if err != nil || !ok {
		t.Fatalf("Load(g1) = ok=%v err=%v, want ok=true err=nil", ok, err)
	}
	if got.GoalID != "g1" || got.Status != StatusRunning {
		t.Fatalf("Load(g1) = %+v, want GoalID=g1 Status=running", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("Save must stamp CreatedAt/UpdatedAt, got %+v", got)
	}
	// Unknown goal.
	rec, ok, err := store.Load("unknown")
	if err != nil || ok || rec.GoalID != "" {
		t.Fatalf("Load(unknown) = (%+v, %v, %v), want (Record{}, false, nil)", rec, ok, err)
	}
}

// TC-167-03: Save writes exactly one crash-safe 0600 JSONL line.
func TestTC167_03_SaveOneLine0600(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)
	if err := store.Save(Record{GoalID: "g1", Goal: "hello", Status: StatusRunning}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if n := countLines(t, dir); n != 1 {
		t.Fatalf("journal line count = %d, want 1", n)
	}
	// Line parses as JSON matching the saved record.
	data, _ := os.ReadFile(filepath.Join(dir, journalName))
	var rec Record
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &rec); err != nil {
		t.Fatalf("journal line not valid JSON: %v", err)
	}
	if rec.GoalID != "g1" || rec.Goal != "hello" {
		t.Fatalf("journal record = %+v, want GoalID=g1 Goal=hello", rec)
	}
	// Mode is 0600.
	fi, err := os.Stat(filepath.Join(dir, journalName))
	if err != nil {
		t.Fatalf("stat journal: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("journal mode = %o, want 600", perm)
	}
}

// TC-167-04: concurrent Save calls never interleave (run under -race).
func TestTC167_04_ConcurrentSaveNoInterleave(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := store.Save(Record{GoalID: fmt.Sprintf("g%d", i), Status: StatusRunning}); err != nil {
				t.Errorf("Save g%d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	// Exactly 50 well-formed lines (no torn bytes).
	if got := countLines(t, dir); got != n {
		t.Fatalf("journal line count = %d, want %d", got, n)
	}
	data, _ := os.ReadFile(filepath.Join(dir, journalName))
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("torn/interleaved journal line %q: %v", line, err)
		}
	}
	// ListInFlight returns all 50, one per GoalID, no dupes.
	inflight, err := store.ListInFlight()
	if err != nil {
		t.Fatalf("ListInFlight: %v", err)
	}
	if len(inflight) != n {
		t.Fatalf("ListInFlight len = %d, want %d", len(inflight), n)
	}
	seen := map[string]bool{}
	for _, r := range inflight {
		if seen[r.GoalID] {
			t.Fatalf("duplicate GoalID %q in ListInFlight", r.GoalID)
		}
		seen[r.GoalID] = true
	}
}

// TC-167-05: Load/ListInFlight return the latest record (last-write-wins).
func TestTC167_05_LastWriteWins(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)
	mustSave(t, store, Record{GoalID: "g1", Status: StatusRunning, Goal: "v1"})
	mustSave(t, store, Record{GoalID: "g1", Status: StatusRunning, Goal: "v2"})

	got, ok, _ := store.Load("g1")
	if !ok || got.Goal != "v2" {
		t.Fatalf("Load(g1).Goal = %q (ok=%v), want v2", got.Goal, ok)
	}
	// Append-only: both writes are still on disk.
	if n := countLines(t, dir); n != 2 {
		t.Fatalf("journal line count = %d, want 2 (append-only, no in-place mutation)", n)
	}
}

// TC-167-06: ListInFlight excludes terminal statuses.
func TestTC167_06_ListInFlightExcludesTerminal(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)
	mustSave(t, store, Record{GoalID: "gp", Status: StatusPending})
	mustSave(t, store, Record{GoalID: "ga", Status: StatusAwaitingApproval})
	mustSave(t, store, Record{GoalID: "gc", Status: StatusCompleted})
	mustSave(t, store, Record{GoalID: "gf", Status: StatusFailed})

	inflight, _ := store.ListInFlight()
	if len(inflight) != 2 {
		t.Fatalf("ListInFlight len = %d, want 2 (only pending+awaiting_approval)", len(inflight))
	}
	got := map[string]bool{}
	for _, r := range inflight {
		got[r.GoalID] = true
	}
	if !got["gp"] || !got["ga"] {
		t.Fatalf("ListInFlight missing pending/awaiting: %v", got)
	}
	if got["gc"] || got["gf"] {
		t.Fatalf("ListInFlight included a terminal goal: %v", got)
	}
	// Stable order: sorted by GoalID.
	if inflight[0].GoalID != "ga" || inflight[1].GoalID != "gp" {
		t.Fatalf("ListInFlight order = [%s %s], want [ga gp] (sorted)", inflight[0].GoalID, inflight[1].GoalID)
	}
}

// TC-167-07: a truncated FINAL line is tolerated (crash-mid-append).
func TestTC167_07_TruncatedFinalLineTolerated(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)
	mustSave(t, store, Record{GoalID: "g1", Status: StatusRunning, Goal: "first"})
	mustSave(t, store, Record{GoalID: "g2", Status: StatusRunning, Goal: "second"})

	// Cut off the last few bytes of the second line (including its newline).
	jp := filepath.Join(dir, journalName)
	data, _ := os.ReadFile(jp)
	if err := os.WriteFile(jp, data[:len(data)-8], fileMode); err != nil {
		t.Fatalf("truncate journal: %v", err)
	}

	fresh, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore on truncated-final-line journal must not error, got %v", err)
	}
	if _, ok, _ := fresh.Load("g1"); !ok {
		t.Fatalf("first (complete) record g1 must survive")
	}
	if _, ok, _ := fresh.Load("g2"); ok {
		t.Fatalf("truncated second record g2 must be silently dropped, but Load returned it")
	}
}

// TC-167-08: a malformed line NOT at the end is a fail-loud error.
func TestTC167_08_MidFileCorruptionFailsLoud(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)
	mustSave(t, store, Record{GoalID: "g1", Status: StatusRunning})
	mustSave(t, store, Record{GoalID: "g2", Status: StatusRunning})
	mustSave(t, store, Record{GoalID: "g3", Status: StatusRunning})

	// Overwrite the SECOND line with junk, keeping its newline so a valid THIRD
	// line still follows.
	jp := filepath.Join(dir, journalName)
	data, _ := os.ReadFile(jp)
	lines := strings.SplitN(string(data), "\n", 3) // [line1, line2, "line3\n"]
	corrupted := lines[0] + "\n" + "not json at all" + "\n" + lines[2]
	if err := os.WriteFile(jp, []byte(corrupted), fileMode); err != nil {
		t.Fatalf("corrupt journal: %v", err)
	}

	_, err := NewFileStore(dir)
	if err == nil {
		t.Fatal("NewFileStore on mid-file corruption must fail loud, got nil error")
	}
	if !strings.Contains(err.Error(), journalName) || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("error %q must name the journal file and mention corruption", err)
	}
}

// TC-167-09: Compact produces an atomic snapshot and truncates the journal.
func TestTC167_09_CompactSnapshotsAndTruncates(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)
	mustSave(t, store, Record{GoalID: "g1", Status: StatusRunning})
	mustSave(t, store, Record{GoalID: "g2", Status: StatusPending})
	mustSave(t, store, Record{GoalID: "g3", Status: StatusCompleted})

	if err := store.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// Journal truncated to empty.
	if fi, err := os.Stat(filepath.Join(dir, journalName)); err != nil || fi.Size() != 0 {
		t.Fatalf("journal after compact: size=%v err=%v, want 0 bytes", statSize(fi), err)
	}
	// Snapshot exists, mode 0600, holds the 3 latest records.
	sp := filepath.Join(dir, snapshotName)
	fi, err := os.Stat(sp)
	if err != nil {
		t.Fatalf("snapshot missing after compact: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("snapshot mode = %o, want 600", perm)
	}
	// A fresh store replaying snapshot-only reflects all 3.
	fresh, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore after compact: %v", err)
	}
	for _, id := range []string{"g1", "g2", "g3"} {
		if _, ok, _ := fresh.Load(id); !ok {
			t.Fatalf("record %s missing after compact+reload", id)
		}
	}
}

// TC-167-10: Compact is atomic under a simulated interruption (rename fails).
func TestTC167_10_CompactAtomicOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)
	mustSave(t, store, Record{GoalID: "g1", Status: StatusRunning})
	// A first successful compact so a real snapshot.json pre-exists.
	if err := store.Compact(); err != nil {
		t.Fatalf("initial compact: %v", err)
	}
	mustSave(t, store, Record{GoalID: "g2", Status: StatusPending})

	// Snapshot both files' bytes before the failing compact.
	jp := filepath.Join(dir, journalName)
	sp := filepath.Join(dir, snapshotName)
	journalBefore, _ := os.ReadFile(jp)
	snapshotBefore, _ := os.ReadFile(sp)

	// Inject a rename that always fails, simulating a crash between temp-write and rename.
	store.renameFunc = func(_, _ string) error { return errors.New("simulated interrupt") }
	if err := store.Compact(); err == nil {
		t.Fatal("Compact with failing rename must return an error, got nil")
	}

	journalAfter, _ := os.ReadFile(jp)
	snapshotAfter, _ := os.ReadFile(sp)
	if string(journalAfter) != string(journalBefore) {
		t.Fatalf("journal changed after failed compact:\n before=%q\n  after=%q", journalBefore, journalAfter)
	}
	if string(snapshotAfter) != string(snapshotBefore) {
		t.Fatalf("snapshot changed after failed compact:\n before=%q\n  after=%q", snapshotBefore, snapshotAfter)
	}
	// The temp file must not survive as (or be confused with) the real snapshot.
	if _, err := os.Stat(filepath.Join(dir, tmpName)); err == nil {
		t.Fatalf("leftover temp file %s must be cleaned up on the error path", tmpName)
	}
}

// TC-167-11: cross-construction durability (L5, the load-bearing proof).
func TestTC167_11_CrossConstructionDurability(t *testing.T) {
	dir := t.TempDir()

	store1, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("store1 NewFileStore: %v", err)
	}
	mustSave(t, store1, Record{GoalID: "running-goal", Status: StatusRunning, Goal: "keep going"})
	mustSave(t, store1, Record{GoalID: "done-goal", Status: StatusCompleted, Goal: "finished"})
	// No Compact. Discard store1 (out of scope); construct a fresh store.
	store1 = nil
	_ = store1

	store2, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("store2 NewFileStore: %v", err)
	}
	inflight, err := store2.ListInFlight()
	if err != nil {
		t.Fatalf("store2.ListInFlight: %v", err)
	}
	if len(inflight) != 1 || inflight[0].GoalID != "running-goal" {
		t.Fatalf("store2.ListInFlight = %+v, want exactly the 1 running goal", inflight)
	}
	if inflight[0].Goal != "keep going" || inflight[0].Status != StatusRunning {
		t.Fatalf("store2 in-flight record fields not durable: %+v", inflight[0])
	}
	// The completed goal is present via Load, just excluded from ListInFlight.
	done, ok, _ := store2.Load("done-goal")
	if !ok || done.Status != StatusCompleted {
		t.Fatalf("store2.Load(done-goal) = (%+v, %v), want present StatusCompleted", done, ok)
	}
}

// TC-167-13: Delete removes a goal from future reads, durably.
func TestTC167_13_DeleteDurablyRemoves(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)
	mustSave(t, store, Record{GoalID: "g1", Status: StatusRunning})
	if err := store.Delete("g1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if rec, ok, err := store.Load("g1"); ok || err != nil || rec.GoalID != "" {
		t.Fatalf("Load(g1) after Delete = (%+v, %v, %v), want (Record{}, false, nil)", rec, ok, err)
	}
	// Tombstone survives replay.
	fresh, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore after delete: %v", err)
	}
	if _, ok, _ := fresh.Load("g1"); ok {
		t.Fatal("tombstone did not survive replay: g1 resurfaced after reconstruction")
	}
	if inflight, _ := fresh.ListInFlight(); len(inflight) != 0 {
		t.Fatalf("ListInFlight after delete+replay = %+v, want empty", inflight)
	}
}

func mustSave(t *testing.T, s *FileStore, rec Record) {
	t.Helper()
	if err := s.Save(rec); err != nil {
		t.Fatalf("Save(%s): %v", rec.GoalID, err)
	}
}

func statSize(fi os.FileInfo) any {
	if fi == nil {
		return "nil"
	}
	return fi.Size()
}
