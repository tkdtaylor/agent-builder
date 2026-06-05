// Package executorharness exposes executor-facing web-ingestion and tool-call
// events through the ingestion broker before executor use.
package executorharness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/ingestion"
)

var (
	ErrNilContentContinuation = errors.New("executorharness: nil content continuation")
	ErrNilToolExecutor        = errors.New("executorharness: nil tool executor")
	ErrUnreviewedRelease      = errors.New("executorharness: unreviewed release")
)

// Config configures an executor-facing ingestion harness.
type Config struct {
	Broker ingestion.Broker
	Trace  TraceRecorder
}

// Harness routes executor-facing events through the ingestion broker.
type Harness struct {
	broker ingestion.Broker
	trace  TraceRecorder
}

// New constructs a Harness.
func New(config Config) Harness {
	return Harness{
		broker: config.Broker,
		trace:  config.Trace,
	}
}

// WebContentEvent is executor-facing web content before it enters context.
type WebContentEvent struct {
	ID          ingestion.CandidateID
	Content     []byte
	SourceURI   string
	MediaType   string
	RetrievedAt time.Time
	Provenance  ingestion.Provenance
}

// ToolCallEvent is an executor-facing tool request before execution.
type ToolCallEvent struct {
	ID         ingestion.CandidateID
	ToolName   string
	Arguments  json.RawMessage
	TargetURI  string
	Provenance ingestion.Provenance
}

// ContentContinuation receives reviewed content after an allow decision.
type ContentContinuation func(context.Context, ContentRelease) error

// ToolExecutor receives reviewed tool calls after an allow decision.
type ToolExecutor func(context.Context, ToolCallRelease) error

// ContentResult records the broker outcome for one web content event.
type ContentResult struct {
	Candidate ingestion.ContentCandidate
	Decision  ingestion.Decision
	Released  bool
	Err       error
}

// ToolCallResult records the broker outcome for one tool-call event.
type ToolCallResult struct {
	Candidate ingestion.ToolCallCandidate
	Decision  ingestion.Decision
	Released  bool
	Err       error
}

// HandleWebContent constructs a content candidate, reviews it, and invokes the
// continuation only when the broker releases an allow decision.
func (h Harness) HandleWebContent(ctx context.Context, event WebContentEvent, continuation ContentContinuation) ContentResult {
	candidate, err := ingestion.NewContentCandidate(ingestion.ContentInput{
		ID:          event.ID,
		Content:     event.Content,
		SourceURI:   event.SourceURI,
		MediaType:   event.MediaType,
		RetrievedAt: event.RetrievedAt,
		Provenance:  event.Provenance,
	})
	if err != nil {
		return ContentResult{Err: err}
	}
	h.record(TraceEvent{Stage: TraceContentCandidateProduced, CandidateID: candidate.ID, Kind: ingestion.CandidateKindContent})

	review := h.broker.ReviewContent(ctx, candidate)
	h.record(TraceEvent{
		Stage:       TraceContentBrokerReviewed,
		CandidateID: candidate.ID,
		Kind:        ingestion.CandidateKindContent,
		Outcome:     review.Decision.Outcome,
	})

	released, ok := review.Release()
	if !ok {
		return ContentResult{Candidate: candidate, Decision: review.Decision}
	}
	if continuation == nil {
		return ContentResult{Candidate: candidate, Decision: review.Decision, Err: ErrNilContentContinuation}
	}

	release := ContentRelease{candidate: cloneContentCandidate(released), reviewed: true}
	h.record(TraceEvent{
		Stage:       TraceContentReleased,
		CandidateID: candidate.ID,
		Kind:        ingestion.CandidateKindContent,
		Outcome:     review.Decision.Outcome,
	})
	if err := continuation(ctx, release); err != nil {
		return ContentResult{Candidate: candidate, Decision: review.Decision, Released: true, Err: err}
	}
	return ContentResult{Candidate: candidate, Decision: review.Decision, Released: true}
}

// HandleToolCall constructs a tool-call candidate, reviews it, and invokes the
// executor only when the broker releases an allow decision.
func (h Harness) HandleToolCall(ctx context.Context, event ToolCallEvent, executor ToolExecutor) ToolCallResult {
	candidate, err := ingestion.NewToolCallCandidate(ingestion.ToolCallInput{
		ID:         event.ID,
		ToolName:   event.ToolName,
		Arguments:  event.Arguments,
		TargetURI:  event.TargetURI,
		Provenance: event.Provenance,
	})
	if err != nil {
		return ToolCallResult{Err: err}
	}
	h.record(TraceEvent{Stage: TraceToolCandidateProduced, CandidateID: candidate.ID, Kind: ingestion.CandidateKindToolCall})

	review := h.broker.ReviewToolCall(ctx, candidate)
	h.record(TraceEvent{
		Stage:       TraceToolBrokerReviewed,
		CandidateID: candidate.ID,
		Kind:        ingestion.CandidateKindToolCall,
		Outcome:     review.Decision.Outcome,
	})

	released, ok := review.Release()
	if !ok {
		return ToolCallResult{Candidate: candidate, Decision: review.Decision}
	}
	if executor == nil {
		return ToolCallResult{Candidate: candidate, Decision: review.Decision, Err: ErrNilToolExecutor}
	}

	release := ToolCallRelease{candidate: cloneToolCallCandidate(released), reviewed: true}
	h.record(TraceEvent{
		Stage:       TraceToolReleased,
		CandidateID: candidate.ID,
		Kind:        ingestion.CandidateKindToolCall,
		Outcome:     review.Decision.Outcome,
	})
	if err := executor(ctx, release); err != nil {
		return ToolCallResult{Candidate: candidate, Decision: review.Decision, Released: true, Err: err}
	}
	h.record(TraceEvent{
		Stage:       TraceToolExecuted,
		CandidateID: candidate.ID,
		Kind:        ingestion.CandidateKindToolCall,
		Outcome:     review.Decision.Outcome,
	})
	return ToolCallResult{Candidate: candidate, Decision: review.Decision, Released: true}
}

func (h Harness) record(event TraceEvent) {
	if h.trace != nil {
		h.trace.RecordTrace(event)
	}
}

// ContentRelease is an opaque reviewed content value. External callers cannot
// construct a valid release without this package's broker-reviewed path.
type ContentRelease struct {
	candidate ingestion.ContentCandidate
	reviewed  bool
}

// Candidate returns a copy of the reviewed content candidate.
func (r ContentRelease) Candidate() (ingestion.ContentCandidate, error) {
	if !r.reviewed {
		return ingestion.ContentCandidate{}, ErrUnreviewedRelease
	}
	return cloneContentCandidate(r.candidate), nil
}

// Content returns a copy of the reviewed content bytes.
func (r ContentRelease) Content() ([]byte, error) {
	candidate, err := r.Candidate()
	if err != nil {
		return nil, err
	}
	return bytes.Clone(candidate.Content), nil
}

// ToolCallRelease is an opaque reviewed tool-call value. External callers cannot
// construct a valid release without this package's broker-reviewed path.
type ToolCallRelease struct {
	candidate ingestion.ToolCallCandidate
	reviewed  bool
}

// Candidate returns a copy of the reviewed tool-call candidate.
func (r ToolCallRelease) Candidate() (ingestion.ToolCallCandidate, error) {
	if !r.reviewed {
		return ingestion.ToolCallCandidate{}, ErrUnreviewedRelease
	}
	return cloneToolCallCandidate(r.candidate), nil
}

// Arguments returns a copy of the reviewed JSON arguments.
func (r ToolCallRelease) Arguments() (json.RawMessage, error) {
	candidate, err := r.Candidate()
	if err != nil {
		return nil, err
	}
	return bytes.Clone(candidate.Arguments), nil
}

func cloneContentCandidate(candidate ingestion.ContentCandidate) ingestion.ContentCandidate {
	candidate.Content = bytes.Clone(candidate.Content)
	return candidate
}

func cloneToolCallCandidate(candidate ingestion.ToolCallCandidate) ingestion.ToolCallCandidate {
	candidate.Arguments = bytes.Clone(candidate.Arguments)
	return candidate
}
