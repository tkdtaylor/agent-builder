package memoryguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DurableStore is a generic, memory-guard-gated, crash-safe cross-session memory
// store (ADR 065, task 172). Unlike MemoryGuardStore, it is durable: every write
// is gated through Client.ValidateWrite AND persisted to an append-only, fsync'd
// JSONL journal (with snapshot/compaction), so state survives a process restart;
// every read is gated through Client.ValidateRead and fails closed on denial,
// never returning a value the guard just rejected.
//
// The crash-safe journal mechanics mirror internal/runstore's FileStore exactly,
// reimplemented locally because internal/memoryguard must stay a stdlib-only leaf
// (F-012) and cannot import internal/runstore.
//
// On-disk layout under dir:
//
//	journal.jsonl  append-only, one durableLine per line, 0600, fsync'd per Put/Delete.
//	snapshot.json  a compacted map[key]durableEntry, written atomically by Compact.
//
// P must be JSON round-trippable.
type DurableStore[P any] struct {
	mu         sync.Mutex
	client     *Client
	identity   string
	dir        string
	index      map[string]durableEntry[P]
	renameFunc func(oldpath, newpath string) error
}

// durableEntry is the in-memory value plus the memory-guard stored_id handle
// (needed for VerifyDelete on Delete).
type durableEntry[P any] struct {
	Value    P      `json:"value"`
	StoredID string `json:"stored_id,omitempty"`
}

// durableLine is one journal record. Deleted marks a tombstone.
type durableLine[P any] struct {
	Key      string `json:"key"`
	Value    P      `json:"value,omitempty"`
	StoredID string `json:"stored_id,omitempty"`
	Deleted  bool   `json:"deleted,omitempty"`
}

const (
	durableJournalName  = "journal.jsonl"
	durableSnapshotName = "snapshot.json"
	durableTmpName      = "snapshot.json.tmp"
	durableFileMode     = 0o600
	durableDirMode      = 0o700
)

// NewDurableStore opens (creating if absent) dir and rebuilds the in-memory index
// by replaying snapshot.json then journal.jsonl, mirroring runstore.NewFileStore's
// crash-safety rules: a truncated final journal line (crash mid-append) is tolerated
// and dropped; a malformed line earlier, or a malformed snapshot, fails loud.
func NewDurableStore[P any](client *Client, identity, dir string) (*DurableStore[P], error) {
	if err := os.MkdirAll(dir, durableDirMode); err != nil {
		return nil, fmt.Errorf("memoryguard durable: create dir %q: %w", dir, err)
	}
	s := &DurableStore[P]{
		client:     client,
		identity:   identity,
		dir:        dir,
		index:      make(map[string]durableEntry[P]),
		renameFunc: os.Rename,
	}
	if err := s.loadSnapshot(); err != nil {
		return nil, err
	}
	if err := s.replayJournal(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *DurableStore[P]) journalPath() string  { return filepath.Join(s.dir, durableJournalName) }
func (s *DurableStore[P]) snapshotPath() string { return filepath.Join(s.dir, durableSnapshotName) }
func (s *DurableStore[P]) tmpPath() string      { return filepath.Join(s.dir, durableTmpName) }

func (s *DurableStore[P]) apply(line durableLine[P]) {
	if line.Deleted {
		delete(s.index, line.Key)
		return
	}
	s.index[line.Key] = durableEntry[P]{Value: line.Value, StoredID: line.StoredID}
}

func (s *DurableStore[P]) loadSnapshot() error {
	data, err := os.ReadFile(s.snapshotPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("memoryguard durable: read snapshot %q: %w", s.snapshotPath(), err)
	}
	if len(data) == 0 {
		return nil
	}
	var snap map[string]durableEntry[P]
	if jsonErr := json.Unmarshal(data, &snap); jsonErr != nil {
		return fmt.Errorf("memoryguard durable: parse snapshot %q: %w (snapshot may be corrupted)", s.snapshotPath(), jsonErr)
	}
	for k, e := range snap {
		s.index[k] = e
	}
	return nil
}

func (s *DurableStore[P]) replayJournal() error {
	data, err := os.ReadFile(s.journalPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("memoryguard durable: read journal %q: %w", s.journalPath(), err)
	}
	if len(data) == 0 {
		return nil
	}
	finalNewline := data[len(data)-1] == '\n'
	lines := strings.Split(string(data), "\n")
	if finalNewline && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		isLast := i == len(lines)-1
		tolerate := isLast && !finalNewline
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec durableLine[P]
		if jsonErr := json.Unmarshal([]byte(line), &rec); jsonErr != nil {
			if tolerate {
				break
			}
			return fmt.Errorf("memoryguard durable: parse journal %q line %d: %w (journal may be corrupted)", s.journalPath(), i+1, jsonErr)
		}
		s.apply(rec)
	}
	return nil
}

// appendLine marshals line and appends it as one fsync'd 0600 JSONL line.
func (s *DurableStore[P]) appendLine(line durableLine[P]) error {
	data, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("memoryguard durable: marshal line for key %q: %w", line.Key, err)
	}
	data = append(data, '\n')
	f, err := os.OpenFile(s.journalPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, durableFileMode)
	if err != nil {
		return fmt.Errorf("memoryguard durable: open journal %q: %w", s.journalPath(), err)
	}
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		return fmt.Errorf("memoryguard durable: append journal %q: %w", s.journalPath(), werr)
	}
	if serr := f.Sync(); serr != nil {
		_ = f.Close()
		return fmt.Errorf("memoryguard durable: fsync journal %q: %w", s.journalPath(), serr)
	}
	if cerr := f.Close(); cerr != nil {
		return fmt.Errorf("memoryguard durable: close journal %q: %w", s.journalPath(), cerr)
	}
	return nil
}

// Put gates value through the memory-guard write-gate FIRST; on denial it returns
// ErrWriteGateDenied (wrapped) and writes NOTHING to disk. On allow it durably
// appends the entry and updates the in-memory index.
func (s *DurableStore[P]) Put(key string, value P) error {
	entryJSON, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("memoryguard durable: serialise value for key %q: %w", key, err)
	}
	storedID, err := s.client.ValidateWrite(string(entryJSON), s.identity)
	if err != nil {
		return fmt.Errorf("memoryguard durable: write-gate for key %q: %w", key, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	line := durableLine[P]{Key: key, Value: value, StoredID: storedID}
	if aerr := s.appendLine(line); aerr != nil {
		return aerr
	}
	s.apply(line)
	return nil
}

// StoredID returns the memory-guard stored_id handle recorded for key on its last
// Put, and whether the key is present. Used by callers (and tests) that need the
// opaque handle; not gated (it returns no memory content, only the handle).
func (s *DurableStore[P]) StoredID(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.index[key]
	if !ok {
		return "", false
	}
	return e.StoredID, true
}

// Get gates the read through the memory-guard read-gate. A key that was never
// written returns (zero, false, nil) WITHOUT a gate call. A present key is gated:
// on ErrReadGateDenied it returns (zero, false, wrapped err), NEVER the cached
// value; on allow it returns the durably-stored value.
func (s *DurableStore[P]) Get(key string) (P, bool, error) {
	s.mu.Lock()
	entry, ok := s.index[key]
	s.mu.Unlock()

	var zero P
	if !ok {
		return zero, false, nil
	}
	if _, _, err := s.client.ValidateRead(key, s.identity); err != nil {
		return zero, false, fmt.Errorf("memoryguard durable: read-gate for key %q: %w", key, err)
	}
	return entry.Value, true, nil
}

// Delete calls VerifyDelete on the entry's stored_id and durably removes the entry
// (a tombstone) regardless of the tamper signal, mirroring MemoryGuardStore.Delete:
// the in-process index always drops the entry (tampered state is unusable), and the
// tamper error (if any) is returned.
func (s *DurableStore[P]) Delete(key string) error {
	s.mu.Lock()
	entry, ok := s.index[key]
	if ok {
		// Durably tombstone and drop from the index before returning, regardless of
		// the tamper outcome below.
		if aerr := s.appendLine(durableLine[P]{Key: key, Deleted: true}); aerr != nil {
			s.mu.Unlock()
			return aerr
		}
		delete(s.index, key)
	}
	s.mu.Unlock()

	if !ok {
		return nil
	}
	if err := s.client.VerifyDelete(entry.StoredID); err != nil {
		return fmt.Errorf("memoryguard durable: delete-verify for key %q: %w", key, err)
	}
	return nil
}

// Compact atomically snapshots the current index to snapshot.json (temp + fsync +
// rename) and truncates the journal, mirroring runstore.FileStore.Compact. On any
// error before the rename, the prior snapshot/journal are left untouched.
func (s *DurableStore[P]) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(s.index, "", "  ")
	if err != nil {
		return fmt.Errorf("memoryguard durable: marshal snapshot: %w", err)
	}
	tmp := s.tmpPath()
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, durableFileMode)
	if err != nil {
		return fmt.Errorf("memoryguard durable: open snapshot temp %q: %w", tmp, err)
	}
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("memoryguard durable: write snapshot temp %q: %w", tmp, werr)
	}
	if serr := f.Sync(); serr != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("memoryguard durable: fsync snapshot temp %q: %w", tmp, serr)
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("memoryguard durable: close snapshot temp %q: %w", tmp, cerr)
	}
	if rerr := s.renameFunc(tmp, s.snapshotPath()); rerr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("memoryguard durable: rename snapshot into place: %w", rerr)
	}
	if terr := os.Truncate(s.journalPath(), 0); terr != nil && !os.IsNotExist(terr) {
		return fmt.Errorf("memoryguard durable: truncate journal after compact: %w", terr)
	}
	return nil
}
