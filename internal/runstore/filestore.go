package runstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	journalName  = "journal.jsonl"
	snapshotName = "snapshot.json"
	tmpName      = "snapshot.json.tmp"
	fileMode     = 0o600
	dirMode      = 0o700
)

// FileStore is the file-backed Store. It keeps an in-memory index rebuilt once at
// construction (snapshot.json then journal.jsonl replay) and updated on every
// mutating call, so reads never touch disk. All exported methods are safe for
// concurrent use.
type FileStore struct {
	mu    sync.Mutex
	dir   string
	index map[string]Record
	// renameFunc performs the atomic snapshot rename in Compact. It defaults to
	// os.Rename and is overridable in tests to simulate an interrupted compact
	// (TC-167-10) without a real process kill.
	renameFunc func(oldpath, newpath string) error
}

// compile-time proof FileStore implements Store.
var _ Store = (*FileStore)(nil)

// NewFileStore opens (creating if absent) the run-journal directory and rebuilds
// the in-memory index by replaying snapshot.json then journal.jsonl. A malformed
// snapshot, or a malformed journal line that is NOT the truncated final line, is a
// fail-loud error (never a silent reset to empty state).
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("runstore: create dir %q: %w", dir, err)
	}
	fs := &FileStore{
		dir:        dir,
		index:      make(map[string]Record),
		renameFunc: os.Rename,
	}
	if err := fs.loadSnapshot(); err != nil {
		return nil, err
	}
	if err := fs.replayJournal(); err != nil {
		return nil, err
	}
	return fs, nil
}

func (fs *FileStore) journalPath() string  { return filepath.Join(fs.dir, journalName) }
func (fs *FileStore) snapshotPath() string { return filepath.Join(fs.dir, snapshotName) }
func (fs *FileStore) tmpPath() string      { return filepath.Join(fs.dir, tmpName) }

// apply folds one record into the index: a tombstone removes the goal, any other
// record is last-write-wins for its GoalID.
func (fs *FileStore) apply(rec Record) {
	if rec.Deleted {
		delete(fs.index, rec.GoalID)
		return
	}
	fs.index[rec.GoalID] = rec
}

// loadSnapshot reads snapshot.json (a map[GoalID]Record) into the index. A missing
// snapshot is fine (first run); a malformed one fails loud.
func (fs *FileStore) loadSnapshot() error {
	data, err := os.ReadFile(fs.snapshotPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("runstore: read snapshot %q: %w", fs.snapshotPath(), err)
	}
	if len(data) == 0 {
		return nil
	}
	var snap map[string]Record
	if jsonErr := json.Unmarshal(data, &snap); jsonErr != nil {
		return fmt.Errorf("runstore: parse snapshot %q: %w (snapshot file may be corrupted)", fs.snapshotPath(), jsonErr)
	}
	for _, rec := range snap {
		fs.apply(rec)
	}
	return nil
}

// replayJournal folds every complete journal line into the index. A truncated
// final line (no trailing newline, a crash mid-append) is tolerated and dropped;
// a malformed line anywhere else fails loud with the file and line number.
func (fs *FileStore) replayJournal() error {
	data, err := os.ReadFile(fs.journalPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("runstore: read journal %q: %w", fs.journalPath(), err)
	}
	if len(data) == 0 {
		return nil
	}
	// A final byte of '\n' means the last line is complete; its absence means the
	// last segment is a partial write we may silently drop.
	finalNewline := data[len(data)-1] == '\n'
	lines := strings.Split(string(data), "\n")
	// strings.Split leaves a trailing "" element when the content ends in '\n'.
	if finalNewline && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		isLast := i == len(lines)-1
		tolerate := isLast && !finalNewline
		if strings.TrimSpace(line) == "" {
			// A genuinely empty interior line is not a record; skip it. (The
			// trailing empty element was already stripped above.)
			continue
		}
		var rec Record
		if jsonErr := json.Unmarshal([]byte(line), &rec); jsonErr != nil {
			if tolerate {
				// Crash-mid-append: the final partial line is expected and dropped.
				break
			}
			return fmt.Errorf("runstore: parse journal %q line %d: %w (journal may be corrupted)", fs.journalPath(), i+1, jsonErr)
		}
		fs.apply(rec)
	}
	return nil
}

// appendLine marshals rec and appends it as one fsync'd 0600 JSONL line.
func (fs *FileStore) appendLine(rec Record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("runstore: marshal record %q: %w", rec.GoalID, err)
	}
	data = append(data, '\n')
	f, err := os.OpenFile(fs.journalPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return fmt.Errorf("runstore: open journal %q: %w", fs.journalPath(), err)
	}
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		return fmt.Errorf("runstore: append journal %q: %w", fs.journalPath(), werr)
	}
	if serr := f.Sync(); serr != nil {
		_ = f.Close()
		return fmt.Errorf("runstore: fsync journal %q: %w", fs.journalPath(), serr)
	}
	if cerr := f.Close(); cerr != nil {
		return fmt.Errorf("runstore: close journal %q: %w", fs.journalPath(), cerr)
	}
	return nil
}

// Save durably appends rec (last-write-wins per GoalID). CreatedAt is stamped only
// on the first write for a goal; UpdatedAt is stamped on every write.
func (fs *FileStore) Save(rec Record) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now
	if err := fs.appendLine(rec); err != nil {
		return err
	}
	fs.apply(rec)
	return nil
}

// Load returns the latest record for goalID, or (Record{}, false, nil) if unknown
// or deleted.
func (fs *FileStore) Load(goalID string) (Record, bool, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	rec, ok := fs.index[goalID]
	if !ok {
		return Record{}, false, nil
	}
	return rec, true, nil
}

// ListInFlight returns the latest non-terminal record for every live goal, sorted
// by GoalID for a stable order.
func (fs *FileStore) ListInFlight() ([]Record, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	out := make([]Record, 0, len(fs.index))
	for _, rec := range fs.index {
		if rec.Status.isTerminal() {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GoalID < out[j].GoalID })
	return out, nil
}

// Delete durably tombstones goalID (a Record with Deleted == true) so it no longer
// appears in Load/ListInFlight, and the removal survives a replay.
func (fs *FileStore) Delete(goalID string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	tombstone := Record{GoalID: goalID, Deleted: true, UpdatedAt: time.Now().UTC()}
	if err := fs.appendLine(tombstone); err != nil {
		return err
	}
	fs.apply(tombstone)
	return nil
}

// Compact atomically snapshots the current index to snapshot.json (temp + fsync +
// rename) and then truncates the journal. If any step before the rename fails, the
// prior snapshot.json and journal.jsonl are left byte-for-byte untouched (no
// partial snapshot is ever visible under the real name).
func (fs *FileStore) Compact() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	data, err := json.MarshalIndent(fs.index, "", "  ")
	if err != nil {
		return fmt.Errorf("runstore: marshal snapshot: %w", err)
	}
	tmp := fs.tmpPath()
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fileMode)
	if err != nil {
		return fmt.Errorf("runstore: open snapshot temp %q: %w", tmp, err)
	}
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("runstore: write snapshot temp %q: %w", tmp, werr)
	}
	if serr := f.Sync(); serr != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("runstore: fsync snapshot temp %q: %w", tmp, serr)
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("runstore: close snapshot temp %q: %w", tmp, cerr)
	}
	// The atomic commit point: until this rename succeeds, nothing durable changed.
	if rerr := fs.renameFunc(tmp, fs.snapshotPath()); rerr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("runstore: rename snapshot into place: %w", rerr)
	}
	// Snapshot is committed; the journal's contents are now redundant.
	if terr := os.Truncate(fs.journalPath(), 0); terr != nil && !os.IsNotExist(terr) {
		return fmt.Errorf("runstore: truncate journal %q after compact: %w", fs.journalPath(), terr)
	}
	return nil
}
