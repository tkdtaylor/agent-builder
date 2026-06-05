// Package armor adapts an external armor process/service to the ingestion guard seam.
package armor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/ingestion"
)

var (
	ErrMissingCommand  = errors.New("armor: missing command")
	ErrMalformedOutput = errors.New("armor: malformed output")
)

// Runner is the external invocation seam used by Guard.
type Runner interface {
	Run(context.Context, Request) (Response, error)
}

// Request is the JSON-compatible payload sent to the external armor process.
type Request struct {
	CandidateID string            `json:"candidate_id"`
	Kind        string            `json:"kind"`
	Content     string            `json:"content,omitempty"`
	SourceURI   string            `json:"source_uri,omitempty"`
	MediaType   string            `json:"media_type,omitempty"`
	ToolName    string            `json:"tool_name,omitempty"`
	Arguments   json.RawMessage   `json:"arguments,omitempty"`
	TargetURI   string            `json:"target_uri,omitempty"`
	Provenance  map[string]string `json:"provenance,omitempty"`
}

// Response is the external armor result shape consumed by the adapter.
type Response struct {
	Decision string            `json:"decision"`
	Reason   string            `json:"reason,omitempty"`
	Findings []Finding         `json:"findings,omitempty"`
	Warnings []string          `json:"warnings,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Finding is one armor finding.
type Finding struct {
	Category string `json:"category"`
	Severity string `json:"severity,omitempty"`
	Message  string `json:"message,omitempty"`
}

// ProcessRunner invokes an external armor-compatible command over JSON stdin/stdout.
type ProcessRunner struct {
	Command []string
}

// Run sends the request JSON to the configured command and parses JSON output.
func (r ProcessRunner) Run(ctx context.Context, request Request) (Response, error) {
	if len(r.Command) == 0 || strings.TrimSpace(r.Command[0]) == "" {
		return Response{}, ErrMissingCommand
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return Response{}, fmt.Errorf("armor: marshal request: %w", err)
	}

	cmd := exec.CommandContext(ctx, r.Command[0], r.Command[1:]...)
	cmd.Stdin = bytes.NewReader(payload)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail == "" {
			detail = err.Error()
		}
		return Response{}, fmt.Errorf("armor: invocation failed: %w: %s", err, detail)
	}

	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrMalformedOutput, err)
	}
	return response, nil
}

// Config configures an armor Guard.
type Config struct {
	Runner  Runner
	Command []string
	Timeout time.Duration
}

// Guard adapts external armor results to ingestion.Decision.
type Guard struct {
	runner  Runner
	timeout time.Duration
}

// NewGuard constructs a Guard. If Runner is nil, Command configures a ProcessRunner.
func NewGuard(config Config) Guard {
	runner := config.Runner
	if runner == nil {
		runner = ProcessRunner{Command: append([]string(nil), config.Command...)}
	}
	return Guard{runner: runner, timeout: config.Timeout}
}

// DecideContent sends a content candidate to armor and maps the result.
func (g Guard) DecideContent(ctx context.Context, candidate ingestion.ContentCandidate) (ingestion.Decision, error) {
	request := Request{
		CandidateID: string(candidate.ID),
		Kind:        string(ingestion.CandidateKindContent),
		Content:     string(candidate.Content),
		SourceURI:   candidate.SourceURI,
		MediaType:   candidate.MediaType,
		Provenance:  provenanceMetadata(candidate.Provenance),
	}
	return g.decide(ctx, candidate.ID, ingestion.CandidateKindContent, request), nil
}

// DecideToolCall sends a tool-call candidate to armor and maps the result.
func (g Guard) DecideToolCall(ctx context.Context, candidate ingestion.ToolCallCandidate) (ingestion.Decision, error) {
	request := Request{
		CandidateID: string(candidate.ID),
		Kind:        string(ingestion.CandidateKindToolCall),
		ToolName:    candidate.ToolName,
		Arguments:   json.RawMessage(append([]byte(nil), candidate.Arguments...)),
		TargetURI:   candidate.TargetURI,
		Provenance:  provenanceMetadata(candidate.Provenance),
	}
	return g.decide(ctx, candidate.ID, ingestion.CandidateKindToolCall, request), nil
}

func (g Guard) decide(ctx context.Context, id ingestion.CandidateID, kind ingestion.CandidateKind, request Request) ingestion.Decision {
	if ctx == nil {
		ctx = context.Background()
	}
	if g.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.timeout)
		defer cancel()
	}
	if g.runner == nil {
		return failClosed(id, kind, "armor unavailable")
	}
	if err := ctx.Err(); err != nil {
		return failClosed(id, kind, err.Error())
	}

	response, err := g.runner.Run(ctx, request)
	if err != nil {
		return failClosed(id, kind, err.Error())
	}
	if err := ctx.Err(); err != nil {
		return failClosed(id, kind, err.Error())
	}
	return mapResponse(id, kind, response)
}

func mapResponse(id ingestion.CandidateID, kind ingestion.CandidateKind, response Response) ingestion.Decision {
	metadata := cloneMetadata(response.Metadata)
	addWarnings(metadata, response.Warnings)
	addFindings(metadata, response.Findings)

	switch strings.ToLower(strings.TrimSpace(response.Decision)) {
	case "allow", "ok", "clean", "pass":
		if len(response.Findings) > 0 {
			return findingDecision(id, kind, ingestion.DecisionBlock, response, metadata)
		}
		return ingestion.Decision{
			CandidateID: id,
			Kind:        kind,
			Outcome:     ingestion.DecisionAllow,
			Reason:      "",
			Metadata:    metadata,
		}
	case "block", "deny", "flag", "flagged":
		return findingDecision(id, kind, ingestion.DecisionBlock, response, metadata)
	case "quarantine":
		return findingDecision(id, kind, ingestion.DecisionQuarantine, response, metadata)
	case "error", "fail", "failed":
		return failClosedWithMetadata(id, kind, reasonOrDefault(response.Reason, "armor error"), metadata)
	default:
		return failClosedWithMetadata(id, kind, "malformed armor decision", metadata)
	}
}

func findingDecision(id ingestion.CandidateID, kind ingestion.CandidateKind, outcome ingestion.DecisionOutcome, response Response, metadata map[string]string) ingestion.Decision {
	return ingestion.Decision{
		CandidateID: id,
		Kind:        kind,
		Outcome:     outcome,
		Reason:      reasonOrDefault(response.Reason, firstFindingMessage(response.Findings)),
		Metadata:    metadata,
	}
}

func failClosed(id ingestion.CandidateID, kind ingestion.CandidateKind, reason string) ingestion.Decision {
	return failClosedWithMetadata(id, kind, reason, nil)
}

func failClosedWithMetadata(id ingestion.CandidateID, kind ingestion.CandidateKind, reason string, metadata map[string]string) ingestion.Decision {
	return ingestion.Decision{
		CandidateID: id,
		Kind:        kind,
		Outcome:     ingestion.DecisionBlock,
		Reason:      "fail closed: " + strings.TrimSpace(reason),
		Metadata:    metadata,
	}
}

func provenanceMetadata(provenance ingestion.Provenance) map[string]string {
	metadata := map[string]string{}
	if strings.TrimSpace(provenance.TaskID) != "" {
		metadata["task_id"] = strings.TrimSpace(provenance.TaskID)
	}
	if strings.TrimSpace(provenance.Executor) != "" {
		metadata["executor"] = strings.TrimSpace(provenance.Executor)
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func cloneMetadata(input map[string]string) map[string]string {
	if len(input) == 0 {
		return map[string]string{}
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func addWarnings(metadata map[string]string, warnings []string) {
	normalized := normalizeList(warnings)
	if len(normalized) > 0 {
		metadata["warnings"] = strings.Join(normalized, ",")
	}
}

func addFindings(metadata map[string]string, findings []Finding) {
	if len(findings) == 0 {
		return
	}

	categories := make([]string, 0, len(findings))
	severities := make([]string, 0, len(findings))
	for _, finding := range findings {
		if category := strings.TrimSpace(finding.Category); category != "" {
			categories = append(categories, category)
		}
		if severity := strings.TrimSpace(finding.Severity); severity != "" {
			severities = append(severities, severity)
		}
	}
	if len(categories) > 0 {
		metadata["finding_categories"] = strings.Join(sortedUnique(categories), ",")
	}
	if len(severities) > 0 {
		metadata["finding_severities"] = strings.Join(sortedUnique(severities), ",")
	}
}

func normalizeList(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return sortedUnique(normalized)
}

func sortedUnique(values []string) []string {
	sort.Strings(values)
	out := values[:0]
	var previous string
	for index, value := range values {
		if index == 0 || value != previous {
			out = append(out, value)
			previous = value
		}
	}
	return out
}

func reasonOrDefault(reason, fallback string) string {
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(fallback); trimmed != "" {
		return trimmed
	}
	return "armor finding"
}

func firstFindingMessage(findings []Finding) string {
	for _, finding := range findings {
		if message := strings.TrimSpace(finding.Message); message != "" {
			return message
		}
		if category := strings.TrimSpace(finding.Category); category != "" {
			return category
		}
	}
	return ""
}
