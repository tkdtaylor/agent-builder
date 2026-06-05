package supervisor

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

const runRecordVersion = "1"

// RunOutcome is the terminal outcome vocabulary for run-record files.
type RunOutcome string

const (
	RunOutcomeCompleted RunOutcome = "completed"
	RunOutcomeFailed    RunOutcome = "failed"
	RunOutcomeTimedOut  RunOutcome = "timed-out"
)

// RunStreams are host-side writers handed to the in-box loop. The supervisor
// owns the backing writers so output leaves the ephemeral box during the run.
type RunStreams struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Command io.Writer
}

// RunRecordMetadata identifies one dispatched run in the durable record.
type RunRecordMetadata struct {
	RunID    string
	TaskID   string
	Repo     string
	Spec     string
	BoxID    string
	Worktree string
}

// RunRecordWriter writes one UTF-8 NDJSON line per run event.
type RunRecordWriter struct {
	output io.WriteCloser
	meta   RunRecordMetadata
	mu     sync.Mutex
}

// NewRunRecordWriter returns a writer for one durable run-record file.
func NewRunRecordWriter(output io.WriteCloser, meta RunRecordMetadata) *RunRecordWriter {
	return &RunRecordWriter{
		output: output,
		meta:   meta,
	}
}

// Start writes the required metadata line for the run.
func (w *RunRecordWriter) Start() error {
	return w.writeEvent(map[string]any{
		"type":     "run_started",
		"task_id":  w.meta.TaskID,
		"repo":     w.meta.Repo,
		"spec":     w.meta.Spec,
		"box_id":   w.meta.BoxID,
		"worktree": w.meta.Worktree,
	})
}

// Streams returns stream writers bound to this run record.
func (w *RunRecordWriter) Streams() RunStreams {
	return RunStreams{
		Stdout:  runRecordStreamWriter{record: w, eventType: "stdout", field: "data"},
		Stderr:  runRecordStreamWriter{record: w, eventType: "stderr", field: "data"},
		Command: runRecordStreamWriter{record: w, eventType: "command", field: "command"},
	}
}

// Command writes one command-log event to the run record.
func (w *RunRecordWriter) Command(command string) error {
	return w.writeEvent(map[string]any{
		"type":    "command",
		"command": command,
	})
}

// Finish writes the terminal run outcome line.
func (w *RunRecordWriter) Finish(outcome RunOutcome, runErr error) error {
	event := map[string]any{
		"type":    "run_finished",
		"outcome": string(outcome),
	}
	if runErr != nil {
		event["error"] = runErr.Error()
	}
	return w.writeEvent(event)
}

// Close flushes the file descriptor owned by the writer.
func (w *RunRecordWriter) Close() error {
	return w.output.Close()
}

func (w *RunRecordWriter) writeStream(eventType, field string, payload []byte) error {
	return w.writeEvent(map[string]any{
		"type": eventType,
		field:  string(payload),
	})
}

func (w *RunRecordWriter) writeEvent(fields map[string]any) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	event := map[string]any{
		"version":   runRecordVersion,
		"type":      fields["type"],
		"run_id":    w.meta.RunID,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	}
	for key, value := range fields {
		event[key] = value
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := w.output.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return nil
}

type runRecordStreamWriter struct {
	record    *RunRecordWriter
	eventType string
	field     string
}

func (w runRecordStreamWriter) Write(payload []byte) (int, error) {
	if w.record == nil {
		return 0, fmt.Errorf("supervisor: nil run record writer")
	}
	if err := w.record.writeStream(w.eventType, w.field, payload); err != nil {
		return 0, err
	}
	return len(payload), nil
}
