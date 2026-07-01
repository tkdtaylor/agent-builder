// Package authz owns the Telegram channel's opt-in sender-ID auth-mode policy
// (ADR 063): the persisted approved-sender store and the per-update mode decision
// that gates plaintext acceptance ahead of the adapter's envelope pipeline.
//
// Isolation invariant (ADR 063 Decision 5): this package imports stdlib only. It
// is a plain leaf on the channel side of the supervisor.GoalSource seam — it never
// imports internal/supervisor, and it does not reach for crypto/transport. Keeping
// it stdlib-only preserves F-003 (supervisor isolation) and F-007 (envelope leaf
// isolation): the sender-ID gate cannot widen either graph.
//
// The store holds only PUBLIC numeric sender IDs — no keys, no secrets — so it is a
// plain-text, human-inspectable 0600 JSON document by design.
package authz

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Mode is the Telegram channel's inbound auth mode (ADR 063 Decision 1). It is a
// closed enum; unset resolves to ModeEnvelope, the strong-security default.
type Mode string

const (
	// ModeEnvelope is the default (and the value for an empty selector). It reproduces
	// today's adapter exactly: every update is verified+opened via envelope.VerifyAndOpen
	// and the sender ID is never consulted. MUST NOT be weakened.
	ModeEnvelope Mode = "envelope"
	// ModeAllowlist accepts plaintext commands only from sender IDs present in the
	// approved-sender store (statically seeded at startup). armor + size caps + audit
	// are retained on this path.
	ModeAllowlist Mode = "allowlist"
	// ModePairing accepts plaintext from approved senders and routes unknown senders
	// through an owner-approved in-chat flow (runtime behavior lands in task 152). The
	// value is recognized by config validation here.
	ModePairing Mode = "pairing"
	// ModeOpen accepts plaintext from any sender (runtime behavior + startup warning
	// land in task 153). The value is recognized by config validation here.
	ModeOpen Mode = "open"
	// ModeDisabled rejects all inbound traffic before any parsing/armor/authz work.
	ModeDisabled Mode = "disabled"
)

// ErrUnknownMode is returned by ParseMode for an unrecognized (non-empty) selector.
var ErrUnknownMode = errors.New("telegram authz: unknown auth mode")

// ParseMode resolves a raw AGENT_BUILDER_TELEGRAM_AUTH_MODE value to a Mode.
// An empty (or whitespace-only) value resolves to ModeEnvelope — unset ⇒ default,
// never an "unknown value" error (ADR 063 Decision 1). Any other value is matched as an
// EXACT, case-sensitive, whitespace-exact literal — no trimming is applied to a non-blank
// value before matching. This is load-bearing for "open" (ADR 063 Decision 1 / task 153,
// REQ-153-02): a padded or mis-cased near-miss (" open", "open ", "Open", "OPEN") must
// never silently resolve to the highest-risk mode via implicit trimming or case-folding,
// and must never silently fall back to the envelope default either — it is a fail-fast
// ErrUnknownMode, consistent with the AGENT_BUILDER_INBOUND handling.
func ParseMode(raw string) (Mode, error) {
	if strings.TrimSpace(raw) == "" {
		// Only a genuinely blank/whitespace-only value is treated as "unset" ⇒ envelope.
		// A non-blank value is matched EXACTLY below — never trimmed.
		return ModeEnvelope, nil
	}
	switch m := Mode(raw); m {
	case ModeEnvelope, ModeAllowlist, ModePairing, ModeOpen, ModeDisabled:
		return m, nil
	default:
		return "", fmt.Errorf("%w: %q (want one of envelope, allowlist, pairing, open, disabled)", ErrUnknownMode, raw)
	}
}

// ConsultsStore reports whether a mode consults the persisted approved-sender store
// and therefore requires AGENT_BUILDER_TELEGRAM_APPROVED_STORE to be configured
// (ADR 063 Decision 4). allowlist (and pairing, in task 152) read the store; envelope,
// disabled, and open never do.
func (m Mode) ConsultsStore() bool {
	return m == ModeAllowlist || m == ModePairing
}

// Normalize canonicalizes a raw sender ID (string or already-numeric-as-string) to
// its canonical numeric form (ADR 063 Decision 4). Leading zeros and surrounding
// whitespace are stripped; "042", " 42 ", and "42" all normalize to 42. A non-numeric
// ID is rejected with an error — never silently coerced to 0 or treated as a wildcard.
//
// This is the load-bearing anti-bypass primitive: normalizing on every write and every
// membership check means a formatting difference can neither slip past an allowlist gate
// nor split one sender into two approval records.
func Normalize(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, fmt.Errorf("telegram authz: empty sender ID")
	}
	id, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("telegram authz: non-numeric sender ID %q", raw)
	}
	return id, nil
}

// Store is a durable, plain-text, 0600 JSON set of normalized numeric approved sender
// IDs (ADR 063 Decision 4). Approvals persist across restarts: the file is loaded at
// startup and written on every mutation-persist cycle. Safe for concurrent use.
type Store struct {
	path string

	mu       sync.Mutex
	approved map[int64]struct{}
}

// storeFile is the on-disk JSON shape. A flat sorted list of numeric IDs — plain text,
// human-inspectable, no secrets.
type storeFile struct {
	ApprovedIDs []int64 `json:"approved_ids"`
}

// NewStore constructs a Store bound to path. It does not touch the filesystem — call
// Load to read any existing approvals, and Persist to write. A blank path is a
// programming error (config validation must reject a blank path before this point for
// any mode that ConsultsStore); NewStore itself does not enforce that.
func NewStore(path string) *Store {
	return &Store{
		path:     path,
		approved: make(map[int64]struct{}),
	}
}

// Load reads the approved-sender set from the store file into memory.
//
//   - A missing file is graceful absence: the store starts empty, no error (mirrors
//     DiskOAuthSecretSource's missing-file behavior).
//   - A malformed file is a fail-fast error, so a corrupted approval store is noticed
//     by an operator rather than silently treated as empty. (Fail-closed would be
//     acceptable for security, but distinguishing "absent" from "corrupt" is the point.)
func (s *Store) Load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.mu.Lock()
			s.approved = make(map[int64]struct{})
			s.mu.Unlock()
			return nil
		}
		return fmt.Errorf("telegram authz: reading approved store %q: %w", s.path, err)
	}

	var doc storeFile
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("telegram authz: malformed approved store %q: %w", s.path, err)
	}

	loaded := make(map[int64]struct{}, len(doc.ApprovedIDs))
	for _, id := range doc.ApprovedIDs {
		loaded[id] = struct{}{}
	}

	s.mu.Lock()
	s.approved = loaded
	s.mu.Unlock()
	return nil
}

// Persist writes the current approved-sender set to the store file as 0600 JSON,
// creating the file (and using 0600 perms) if absent. The write is atomic-ish via a
// temp file + rename so a crash mid-write cannot leave a truncated (and then
// fail-fast-on-Load) store.
func (s *Store) Persist() error {
	s.mu.Lock()
	ids := make([]int64, 0, len(s.approved))
	for id := range s.approved {
		ids = append(ids, id)
	}
	s.mu.Unlock()

	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	data, err := json.MarshalIndent(storeFile{ApprovedIDs: ids}, "", "  ")
	if err != nil {
		return fmt.Errorf("telegram authz: marshaling approved store: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".approved-store-*.tmp")
	if err != nil {
		return fmt.Errorf("telegram authz: creating temp store file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the rename.
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("telegram authz: chmod temp store file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("telegram authz: writing temp store file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("telegram authz: closing temp store file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("telegram authz: renaming store file into place: %w", err)
	}
	// Guarantee 0600 on the final path regardless of umask on the rename target.
	if err := os.Chmod(s.path, 0o600); err != nil {
		return fmt.Errorf("telegram authz: chmod store file %q: %w", s.path, err)
	}
	return nil
}

// Add inserts a raw sender ID into the approved set after normalization. A non-numeric
// ID is rejected with an error (never silently coerced). Adding an already-present ID
// is a no-op — normalization guarantees at most one entry per semantic sender ID.
// Add mutates in memory only; call Persist to write to disk.
func (s *Store) Add(rawID string) error {
	id, err := Normalize(rawID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.approved[id] = struct{}{}
	s.mu.Unlock()
	return nil
}

// Remove deletes a raw sender ID from the approved set after normalization. A
// non-numeric ID is rejected with an error. Removing an absent ID is a no-op.
// Remove mutates in memory only; call Persist to write to disk.
func (s *Store) Remove(rawID string) error {
	id, err := Normalize(rawID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.approved, id)
	s.mu.Unlock()
	return nil
}

// Contains reports whether a raw sender ID is in the approved set. The ID is normalized
// before the check, so a cross-format lookup ("042" against a stored 42, or vice versa)
// succeeds. A non-numeric ID is not approved and returns false with an error so the
// caller can audit the malformed input rather than silently treating it as a miss.
func (s *Store) Contains(rawID string) (bool, error) {
	id, err := Normalize(rawID)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	_, ok := s.approved[id]
	s.mu.Unlock()
	return ok, nil
}

// Len returns the number of distinct approved sender IDs currently held in memory.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.approved)
}
