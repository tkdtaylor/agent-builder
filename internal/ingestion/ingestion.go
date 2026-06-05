// Package ingestion defines the in-box boundary for attacker-reachable content
// and executor tool-call requests before they can enter executor context.
package ingestion

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	defaultMediaType = "application/octet-stream"
)

var (
	ErrBlankSourceURI         = errors.New("ingestion: blank source URI")
	ErrUnsupportedSourceURI   = errors.New("ingestion: unsupported source URI")
	ErrBlankToolName          = errors.New("ingestion: blank tool name")
	ErrMalformedToolArguments = errors.New("ingestion: malformed tool arguments")
	ErrUnsupportedTargetURI   = errors.New("ingestion: unsupported target URI")
)

// CandidateID is a stable correlation ID used to join candidates and decisions.
type CandidateID string

// Provenance identifies the task/executor context that produced a candidate.
type Provenance struct {
	TaskID   string
	Executor string
}

// CandidateKind names the candidate family passed to a guard.
type CandidateKind string

const (
	CandidateKindContent  CandidateKind = "content"
	CandidateKindToolCall CandidateKind = "tool-call"
)

// ContentInput is the caller-facing shape used to construct ContentCandidate.
type ContentInput struct {
	ID          CandidateID
	Content     []byte
	SourceURI   string
	MediaType   string
	RetrievedAt time.Time
	Provenance  Provenance
}

// ContentCandidate is attacker-reachable web-ingested content at the boundary.
type ContentCandidate struct {
	ID          CandidateID
	Content     []byte
	SourceURI   string
	MediaType   string
	RetrievedAt time.Time
	Provenance  Provenance
}

// NewContentCandidate validates and copies attacker-reachable content before it
// enters executor context.
func NewContentCandidate(input ContentInput) (ContentCandidate, error) {
	sourceURI, err := normalizeRequiredURI(input.SourceURI, ErrBlankSourceURI, ErrUnsupportedSourceURI)
	if err != nil {
		return ContentCandidate{}, err
	}

	mediaType := strings.TrimSpace(input.MediaType)
	if mediaType == "" {
		mediaType = defaultMediaType
	}

	content := bytes.Clone(input.Content)
	candidate := ContentCandidate{
		ID:          input.ID,
		Content:     content,
		SourceURI:   sourceURI,
		MediaType:   mediaType,
		RetrievedAt: input.RetrievedAt,
		Provenance:  normalizeProvenance(input.Provenance),
	}
	if candidate.ID == "" {
		candidate.ID = deriveID(
			CandidateKindContent,
			candidate.Content,
			candidate.SourceURI,
			candidate.MediaType,
			candidate.RetrievedAt.UTC().Format(time.RFC3339Nano),
			candidate.Provenance.TaskID,
			candidate.Provenance.Executor,
		)
	}
	return candidate, nil
}

// ToolCallInput is the caller-facing shape used to construct ToolCallCandidate.
type ToolCallInput struct {
	ID         CandidateID
	ToolName   string
	Arguments  json.RawMessage
	TargetURI  string
	Provenance Provenance
}

// ToolCallCandidate is a tool request at the boundary before execution.
type ToolCallCandidate struct {
	ID         CandidateID
	ToolName   string
	Arguments  json.RawMessage
	TargetURI  string
	Provenance Provenance
}

// NewToolCallCandidate validates and copies a requested executor tool call.
func NewToolCallCandidate(input ToolCallInput) (ToolCallCandidate, error) {
	toolName := strings.TrimSpace(input.ToolName)
	if toolName == "" {
		return ToolCallCandidate{}, ErrBlankToolName
	}

	arguments, err := normalizeJSON(input.Arguments)
	if err != nil {
		return ToolCallCandidate{}, err
	}

	targetURI, err := normalizeOptionalURI(input.TargetURI, ErrUnsupportedTargetURI)
	if err != nil {
		return ToolCallCandidate{}, err
	}

	candidate := ToolCallCandidate{
		ID:         input.ID,
		ToolName:   toolName,
		Arguments:  arguments,
		TargetURI:  targetURI,
		Provenance: normalizeProvenance(input.Provenance),
	}
	if candidate.ID == "" {
		candidate.ID = deriveID(
			CandidateKindToolCall,
			candidate.Arguments,
			candidate.ToolName,
			candidate.TargetURI,
			candidate.Provenance.TaskID,
			candidate.Provenance.Executor,
		)
	}
	return candidate, nil
}

// DecisionOutcome is the guard verdict for one candidate.
type DecisionOutcome string

const (
	DecisionAllow      DecisionOutcome = "allow"
	DecisionBlock      DecisionOutcome = "block"
	DecisionQuarantine DecisionOutcome = "quarantine"
)

// Decision records the guard outcome for a candidate.
type Decision struct {
	CandidateID CandidateID
	Kind        CandidateKind
	Outcome     DecisionOutcome
	Reason      string
	Metadata    map[string]string
}

// Guard is the fakeable security seam for boundary candidates.
type Guard interface {
	DecideContent(context.Context, ContentCandidate) (Decision, error)
	DecideToolCall(context.Context, ToolCallCandidate) (Decision, error)
}

// Broker invokes a Guard before releasing candidates to the executor path.
type Broker struct {
	guard   Guard
	timeout time.Duration
}

// NewBroker constructs a Broker. A nil guard is allowed but fails closed.
func NewBroker(guard Guard, timeout time.Duration) Broker {
	return Broker{guard: guard, timeout: timeout}
}

// ContentReview is the broker result for one content candidate.
type ContentReview struct {
	Candidate ContentCandidate
	Decision  Decision
}

// Release returns the candidate only when the guard allowed it.
func (r ContentReview) Release() (ContentCandidate, bool) {
	if !validDecision(r.Decision, r.Candidate.ID, CandidateKindContent) || r.Decision.Outcome != DecisionAllow {
		return ContentCandidate{}, false
	}
	return r.Candidate, true
}

// ToolCallReview is the broker result for one tool-call candidate.
type ToolCallReview struct {
	Candidate ToolCallCandidate
	Decision  Decision
}

// Release returns the candidate only when the guard allowed it.
func (r ToolCallReview) Release() (ToolCallCandidate, bool) {
	if !validDecision(r.Decision, r.Candidate.ID, CandidateKindToolCall) || r.Decision.Outcome != DecisionAllow {
		return ToolCallCandidate{}, false
	}
	return r.Candidate, true
}

// ReviewContent decides whether a content candidate may enter executor context.
func (b Broker) ReviewContent(ctx context.Context, candidate ContentCandidate) ContentReview {
	decision := b.decide(ctx, candidate.ID, CandidateKindContent, func(ctx context.Context) (Decision, error) {
		if b.guard == nil {
			return Decision{}, errors.New("guard unavailable")
		}
		return b.guard.DecideContent(ctx, candidate)
	})
	return ContentReview{Candidate: candidate, Decision: decision}
}

// ReviewToolCall decides whether a tool-call candidate may execute.
func (b Broker) ReviewToolCall(ctx context.Context, candidate ToolCallCandidate) ToolCallReview {
	decision := b.decide(ctx, candidate.ID, CandidateKindToolCall, func(ctx context.Context) (Decision, error) {
		if b.guard == nil {
			return Decision{}, errors.New("guard unavailable")
		}
		return b.guard.DecideToolCall(ctx, candidate)
	})
	return ToolCallReview{Candidate: candidate, Decision: decision}
}

func (b Broker) decide(ctx context.Context, id CandidateID, kind CandidateKind, call func(context.Context) (Decision, error)) Decision {
	if ctx == nil {
		ctx = context.Background()
	}
	if b.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, b.timeout)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return failClosed(id, kind, err)
	}

	result := make(chan guardResult, 1)
	go func() {
		decision, err := call(ctx)
		result <- guardResult{decision: decision, err: err}
	}()

	select {
	case <-ctx.Done():
		return failClosed(id, kind, ctx.Err())
	case guardResult := <-result:
		if guardResult.err != nil {
			return failClosed(id, kind, guardResult.err)
		}
		if !validDecision(guardResult.decision, id, kind) {
			return failClosed(id, kind, errors.New("malformed guard decision"))
		}
		return cloneDecision(guardResult.decision)
	}
}

type guardResult struct {
	decision Decision
	err      error
}

func validDecision(decision Decision, id CandidateID, kind CandidateKind) bool {
	if decision.CandidateID != id || decision.Kind != kind {
		return false
	}
	switch decision.Outcome {
	case DecisionAllow, DecisionBlock, DecisionQuarantine:
		return true
	default:
		return false
	}
}

func failClosed(id CandidateID, kind CandidateKind, err error) Decision {
	return Decision{
		CandidateID: id,
		Kind:        kind,
		Outcome:     DecisionBlock,
		Reason:      fmt.Sprintf("fail closed: %v", err),
	}
}

func normalizeProvenance(provenance Provenance) Provenance {
	return Provenance{
		TaskID:   strings.TrimSpace(provenance.TaskID),
		Executor: strings.TrimSpace(provenance.Executor),
	}
}

func normalizeRequiredURI(raw string, blankErr, unsupportedErr error) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", blankErr
	}
	return normalizeURI(trimmed, unsupportedErr)
}

func normalizeOptionalURI(raw string, unsupportedErr error) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	return normalizeURI(trimmed, unsupportedErr)
}

func normalizeURI(raw string, unsupportedErr error) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return "", unsupportedErr
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		if parsed.Host == "" {
			return "", unsupportedErr
		}
	default:
		return "", unsupportedErr
	}
	return parsed.String(), nil
}

func normalizeJSON(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return nil, ErrMalformedToolArguments
	}
	out := make([]byte, 0, len(trimmed))
	var buffer bytes.Buffer
	if err := json.Compact(&buffer, trimmed); err != nil {
		return nil, ErrMalformedToolArguments
	}
	out = append(out, buffer.Bytes()...)
	return json.RawMessage(out), nil
}

func deriveID(kind CandidateKind, chunks ...any) CandidateID {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(kind))
	for _, chunk := range chunks {
		_, _ = hasher.Write([]byte{0})
		switch value := chunk.(type) {
		case []byte:
			_, _ = hasher.Write(value)
		default:
			_, _ = fmt.Fprint(hasher, value)
		}
	}
	sum := hasher.Sum(nil)
	return CandidateID(string(kind) + "-" + hex.EncodeToString(sum[:12]))
}

func cloneDecision(decision Decision) Decision {
	decision.Metadata = cloneMap(decision.Metadata)
	return decision
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
