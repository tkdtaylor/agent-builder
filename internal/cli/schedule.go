package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// EnvSchedulePath configures a schedule file of recurring/deferred goals the daemon
// fires without an inbound channel message (ADR 065, task 175). Unset = no scheduler.
const EnvSchedulePath = "AGENT_BUILDER_SCHEDULE_PATH"

// schedulerPoll is the fixed production tick interval at which the scheduler checks
// its entries against the clock. Tests drive checkEntries directly instead of waiting.
const schedulerPoll = time.Minute

// ScheduleEntry is one scheduled goal: either an interval (`every`) or a daily
// time-of-day (`at`), never both. Goal is the goal text fired at each firing.
type ScheduleEntry struct {
	Goal  string
	Every time.Duration // interval; used when IsAt is false
	At    time.Duration // offset since midnight; used when IsAt is true
	IsAt  bool
}

// scheduleFileEntry is the on-disk JSON shape (either `every` or `at`).
type scheduleFileEntry struct {
	Goal  string `json:"goal"`
	Every string `json:"every,omitempty"`
	At    string `json:"at,omitempty"`
}

type scheduleFile struct {
	Entries []scheduleFileEntry `json:"entries"`
}

// ParseScheduleFile reads and validates a JSON schedule file. Every entry must set
// exactly one of `every` (a Go duration) or `at` ("HH:MM"); both-or-neither, an
// empty goal, or an unparseable value is an error naming the offending entry index.
func ParseScheduleFile(path string) ([]ScheduleEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("schedule: read %q: %w", path, err)
	}
	var sf scheduleFile
	if jerr := json.Unmarshal(data, &sf); jerr != nil {
		return nil, fmt.Errorf("schedule: parse %q: %w", path, jerr)
	}
	out := make([]ScheduleEntry, 0, len(sf.Entries))
	for i, e := range sf.Entries {
		if strings.TrimSpace(e.Goal) == "" {
			return nil, fmt.Errorf("schedule: entry %d has an empty goal", i)
		}
		hasEvery := strings.TrimSpace(e.Every) != ""
		hasAt := strings.TrimSpace(e.At) != ""
		if hasEvery == hasAt {
			return nil, fmt.Errorf("schedule: entry %d must set exactly one of every/at (got every=%q at=%q)", i, e.Every, e.At)
		}
		if hasEvery {
			d, derr := time.ParseDuration(strings.TrimSpace(e.Every))
			if derr != nil || d <= 0 {
				return nil, fmt.Errorf("schedule: entry %d has an invalid every value %q", i, e.Every)
			}
			out = append(out, ScheduleEntry{Goal: e.Goal, Every: d})
			continue
		}
		off, aerr := parseTimeOfDay(strings.TrimSpace(e.At))
		if aerr != nil {
			return nil, fmt.Errorf("schedule: entry %d has an invalid at value %q: %w", i, e.At, aerr)
		}
		out = append(out, ScheduleEntry{Goal: e.Goal, At: off, IsAt: true})
	}
	return out, nil
}

// parseTimeOfDay parses "HH:MM" into an offset since midnight.
func parseTimeOfDay(s string) (time.Duration, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("want HH:MM")
	}
	h, herr := strconv.Atoi(parts[0])
	m, merr := strconv.Atoi(parts[1])
	if herr != nil || merr != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("want HH:MM in 00:00..23:59")
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute, nil
}

// Clock is the scheduler's time seam (mirrors router.Clock), so tests drive firing
// with a fake clock instead of real sleeps.
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Scheduler fires ScheduleEntries via the dispatch seam when the clock crosses each
// entry's interval/daily boundary. It reuses the standard goal-intake path through
// the injected dispatch func; it introduces no parallel dispatch of its own.
type Scheduler struct {
	entries   []ScheduleEntry
	clock     Clock
	dispatch  func(supervisor.Task)
	lastEvery []time.Time // per-entry last fire time (every entries)
	lastAtDay []string    // per-entry last fired calendar day (at entries)
	done      chan struct{}
}

// NewScheduler constructs a Scheduler. lastEvery is seeded from the clock so the
// first `every` firing is one interval after construction.
func NewScheduler(entries []ScheduleEntry, clock Clock, dispatch func(supervisor.Task)) *Scheduler {
	if clock == nil {
		clock = realClock{}
	}
	now := clock.Now()
	s := &Scheduler{
		entries:   entries,
		clock:     clock,
		dispatch:  dispatch,
		lastEvery: make([]time.Time, len(entries)),
		lastAtDay: make([]string, len(entries)),
		done:      make(chan struct{}),
	}
	for i := range entries {
		s.lastEvery[i] = now
	}
	return s
}

// Run blocks until ctx is cancelled, checking entries on a fixed poll tick. It
// closes the done channel on exit so a caller can confirm clean shutdown.
func (s *Scheduler) Run(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(schedulerPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkEntries()
		}
	}
}

// Done returns a channel closed when Run has returned.
func (s *Scheduler) Done() <-chan struct{} { return s.done }

// checkEntries fires every entry whose interval/daily boundary the clock has now
// crossed. Test-driven: tests advance a fake clock then call this directly.
func (s *Scheduler) checkEntries() {
	now := s.clock.Now()
	for i := range s.entries {
		e := s.entries[i]
		if e.IsAt {
			midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			target := midnight.Add(e.At)
			day := now.Format("2006-01-02")
			if !now.Before(target) && s.lastAtDay[i] != day {
				s.fire(i, now)
				s.lastAtDay[i] = day
			}
			continue
		}
		if now.Sub(s.lastEvery[i]) >= e.Every {
			s.fire(i, now)
			s.lastEvery[i] = now
		}
	}
}

// fire builds a deterministic, non-colliding goal Task and hands it to the dispatch
// seam (the standard goal-intake path).
func (s *Scheduler) fire(i int, now time.Time) {
	if s.dispatch == nil {
		return
	}
	id := fmt.Sprintf("sched-%d-%s", i, now.UTC().Format(time.RFC3339))
	s.dispatch(supervisor.Task{ID: id, Spec: s.entries[i].Goal})
}

// mergedMessageSource combines a scheduler-produced message channel with the daemon's
// primary inbound MessageSource, so scheduled goals flow through the SAME control-loop
// intake path as channel-originated goals (task 175). The primary source is read in a
// background goroutine so a scheduled message is never starved by a blocking primary
// Next().
type mergedMessageSource struct {
	primary supervisor.MessageSource
	sched   <-chan supervisor.Message
	ctx     context.Context
	primCh  chan primResult
	started bool
}

type primResult struct {
	msg supervisor.Message
	ok  bool
	err error
}

func newMergedMessageSource(ctx context.Context, primary supervisor.MessageSource, sched <-chan supervisor.Message) *mergedMessageSource {
	return &mergedMessageSource{primary: primary, sched: sched, ctx: ctx}
}

func (m *mergedMessageSource) Next() (supervisor.Message, bool, error) {
	if !m.started {
		m.started = true
		m.primCh = make(chan primResult, 1)
		go func() {
			for {
				msg, ok, err := m.primary.Next()
				m.primCh <- primResult{msg, ok, err}
				if !ok || err != nil {
					return
				}
			}
		}()
	}
	select {
	case <-m.ctx.Done():
		return supervisor.Message{}, false, nil
	case msg := <-m.sched:
		return msg, true, nil
	case r := <-m.primCh:
		return r.msg, r.ok, r.err
	}
}
