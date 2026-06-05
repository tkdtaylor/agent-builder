package ingestion_test

import (
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/ingestion"
)

func TestContentCandidateCarriesProvenanceAndStableID(t *testing.T) {
	retrievedAt := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	input := ingestion.ContentInput{
		Content:     []byte("benign research note"),
		SourceURI:   " https://example.test/research ",
		MediaType:   " text/html ",
		RetrievedAt: retrievedAt,
		Provenance:  ingestion.Provenance{TaskID: "024", Executor: "claude-cli"},
	}

	first, err := ingestion.NewContentCandidate(input)
	if err != nil {
		t.Fatalf("TC-001 NewContentCandidate returned error: %v", err)
	}
	second, err := ingestion.NewContentCandidate(input)
	if err != nil {
		t.Fatalf("TC-001 NewContentCandidate second call returned error: %v", err)
	}
	if first.ID == "" || first.ID != second.ID {
		t.Fatalf("TC-001 derived IDs = %q and %q, want same non-empty stable ID", first.ID, second.ID)
	}
	if first.SourceURI != "https://example.test/research" {
		t.Fatalf("TC-001 SourceURI = %q, want normalized URI", first.SourceURI)
	}
	if first.MediaType != "text/html" {
		t.Fatalf("TC-001 MediaType = %q, want text/html", first.MediaType)
	}
	if string(first.Content) != "benign research note" || first.Provenance.TaskID != "024" || first.Provenance.Executor != "claude-cli" {
		t.Fatalf("TC-001 candidate did not preserve content/provenance: %+v", first)
	}
	if !first.RetrievedAt.Equal(retrievedAt) {
		t.Fatalf("TC-001 RetrievedAt = %s, want %s", first.RetrievedAt, retrievedAt)
	}

	input.ID = "caller-correlation-id"
	withID, err := ingestion.NewContentCandidate(input)
	if err != nil {
		t.Fatalf("TC-001 NewContentCandidate with caller ID returned error: %v", err)
	}
	if withID.ID != "caller-correlation-id" {
		t.Fatalf("TC-001 ID = %q, want caller-supplied ID", withID.ID)
	}

	input.Content[0] = 'X'
	if string(first.Content) != "benign research note" {
		t.Fatalf("TC-003 candidate content mutated after caller changed input: %q", first.Content)
	}
}

func TestContentCandidateEdgeValidation(t *testing.T) {
	emptyBody, err := ingestion.NewContentCandidate(ingestion.ContentInput{
		SourceURI: "https://example.test/empty",
	})
	if err != nil {
		t.Fatalf("TC-001 empty body returned error: %v", err)
	}
	if len(emptyBody.Content) != 0 {
		t.Fatalf("TC-001 empty body length = %d, want 0", len(emptyBody.Content))
	}
	if emptyBody.MediaType != "application/octet-stream" {
		t.Fatalf("TC-001 missing media type = %q, want explicit default", emptyBody.MediaType)
	}

	if _, err := ingestion.NewContentCandidate(ingestion.ContentInput{SourceURI: "file:///tmp/payload"}); !errors.Is(err, ingestion.ErrUnsupportedSourceURI) {
		t.Fatalf("TC-001 unsupported source error = %v, want ErrUnsupportedSourceURI", err)
	}
}

func TestToolCallCandidateCarriesTypedRequestData(t *testing.T) {
	input := ingestion.ToolCallInput{
		ToolName:   " web.fetch ",
		Arguments:  json.RawMessage(`{"url":"https://example.test","limit":3}`),
		TargetURI:  " https://example.test ",
		Provenance: ingestion.Provenance{TaskID: "024", Executor: "claude-cli"},
	}

	first, err := ingestion.NewToolCallCandidate(input)
	if err != nil {
		t.Fatalf("TC-002 NewToolCallCandidate returned error: %v", err)
	}
	second, err := ingestion.NewToolCallCandidate(input)
	if err != nil {
		t.Fatalf("TC-002 NewToolCallCandidate second call returned error: %v", err)
	}
	if first.ID == "" || first.ID != second.ID {
		t.Fatalf("TC-002 derived IDs = %q and %q, want same non-empty stable ID", first.ID, second.ID)
	}
	if first.ToolName != "web.fetch" {
		t.Fatalf("TC-002 ToolName = %q, want trimmed tool name", first.ToolName)
	}
	if string(first.Arguments) != `{"url":"https://example.test","limit":3}` {
		t.Fatalf("TC-002 Arguments = %s, want compact JSON preserved", first.Arguments)
	}
	if first.TargetURI != "https://example.test" || first.Provenance.TaskID != "024" || first.Provenance.Executor != "claude-cli" {
		t.Fatalf("TC-002 target/provenance not preserved: %+v", first)
	}

	input.ID = "tool-correlation-id"
	withID, err := ingestion.NewToolCallCandidate(input)
	if err != nil {
		t.Fatalf("TC-002 NewToolCallCandidate with caller ID returned error: %v", err)
	}
	if withID.ID != "tool-correlation-id" {
		t.Fatalf("TC-002 ID = %q, want caller-supplied ID", withID.ID)
	}

	input.Arguments[2] = 'X'
	if string(first.Arguments) != `{"url":"https://example.test","limit":3}` {
		t.Fatalf("TC-003 candidate arguments mutated after caller changed input: %s", first.Arguments)
	}
}

func TestToolCallCandidateRejectsMalformedRequestsBeforeGuard(t *testing.T) {
	tests := []struct {
		name  string
		input ingestion.ToolCallInput
		want  error
	}{
		{
			name:  "blank tool name",
			input: ingestion.ToolCallInput{ToolName: " \t", Arguments: json.RawMessage(`{}`)},
			want:  ingestion.ErrBlankToolName,
		},
		{
			name:  "malformed arguments",
			input: ingestion.ToolCallInput{ToolName: "web.fetch", Arguments: json.RawMessage(`{"url":`)},
			want:  ingestion.ErrMalformedToolArguments,
		},
		{
			name:  "blank arguments",
			input: ingestion.ToolCallInput{ToolName: "web.fetch"},
			want:  ingestion.ErrMalformedToolArguments,
		},
		{
			name:  "unsupported target",
			input: ingestion.ToolCallInput{ToolName: "web.fetch", Arguments: json.RawMessage(`{}`), TargetURI: "file:///tmp/secret"},
			want:  ingestion.ErrUnsupportedTargetURI,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ingestion.NewToolCallCandidate(tt.input); !errors.Is(err, tt.want) {
				t.Fatalf("TC-002 NewToolCallCandidate error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestBrokerAllowsCandidatesAndDoesNotMutateThem(t *testing.T) {
	content := mustContent(t, "allow-content")
	toolCall := mustToolCall(t, "allow-tool")
	guard := fakeGuard{
		contentDecision:  decision(content.ID, ingestion.CandidateKindContent, ingestion.DecisionAllow, ""),
		toolCallDecision: decision(toolCall.ID, ingestion.CandidateKindToolCall, ingestion.DecisionAllow, ""),
	}
	broker := ingestion.NewBroker(guard, 0)

	contentReview := broker.ReviewContent(context.Background(), content)
	releasedContent, ok := contentReview.Release()
	if !ok {
		t.Fatalf("TC-003 content Release ok = false, want true for allow")
	}
	if releasedContent.ID != content.ID || string(releasedContent.Content) != string(content.Content) {
		t.Fatalf("TC-003 released content mutated: %+v want %+v", releasedContent, content)
	}

	toolReview := broker.ReviewToolCall(context.Background(), toolCall)
	releasedTool, ok := toolReview.Release()
	if !ok {
		t.Fatalf("TC-003 tool Release ok = false, want true for allow")
	}
	if releasedTool.ID != toolCall.ID || string(releasedTool.Arguments) != string(toolCall.Arguments) {
		t.Fatalf("TC-003 released tool call mutated: %+v want %+v", releasedTool, toolCall)
	}
}

func TestBrokerBlocksOrQuarantinesFlaggedCandidates(t *testing.T) {
	content := mustContent(t, "flagged-content")
	toolCall := mustToolCall(t, "flagged-tool")
	guard := fakeGuard{
		contentDecision:  decision(content.ID, ingestion.CandidateKindContent, ingestion.DecisionBlock, "prompt injection"),
		toolCallDecision: decision(toolCall.ID, ingestion.CandidateKindToolCall, ingestion.DecisionQuarantine, "unsafe target"),
	}
	broker := ingestion.NewBroker(guard, 0)

	contentReview := broker.ReviewContent(context.Background(), content)
	if contentReview.Decision.Outcome != ingestion.DecisionBlock || contentReview.Decision.Reason != "prompt injection" {
		t.Fatalf("TC-004 content decision = %+v, want block with preserved reason", contentReview.Decision)
	}
	if contentReview.Decision.CandidateID != content.ID || contentReview.Decision.Kind != ingestion.CandidateKindContent {
		t.Fatalf("TC-004 content decision correlation = (%q, %q), want (%q, %q)", contentReview.Decision.CandidateID, contentReview.Decision.Kind, content.ID, ingestion.CandidateKindContent)
	}
	if _, ok := contentReview.Release(); ok {
		t.Fatal("TC-004 content Release ok = true, want false for block")
	}

	toolReview := broker.ReviewToolCall(context.Background(), toolCall)
	if toolReview.Decision.Outcome != ingestion.DecisionQuarantine || toolReview.Decision.Reason != "unsafe target" {
		t.Fatalf("TC-004 tool decision = %+v, want quarantine with preserved reason", toolReview.Decision)
	}
	if toolReview.Decision.CandidateID != toolCall.ID || toolReview.Decision.Kind != ingestion.CandidateKindToolCall {
		t.Fatalf("TC-004 tool decision correlation = (%q, %q), want (%q, %q)", toolReview.Decision.CandidateID, toolReview.Decision.Kind, toolCall.ID, ingestion.CandidateKindToolCall)
	}
	if _, ok := toolReview.Release(); ok {
		t.Fatal("TC-004 tool Release ok = true, want false for quarantine")
	}
}

func TestBrokerGuardFailuresFailClosed(t *testing.T) {
	content := mustContent(t, "fail-closed-content")
	tests := []struct {
		name  string
		guard ingestion.Guard
		ctx   context.Context
	}{
		{name: "guard error", guard: fakeGuard{contentErr: errors.New("guard exploded")}, ctx: context.Background()},
		{name: "unavailable guard", guard: nil, ctx: context.Background()},
		{name: "timeout", guard: fakeGuard{waitForContext: true}, ctx: context.Background()},
		{name: "malformed outcome", guard: fakeGuard{contentDecision: ingestion.Decision{CandidateID: content.ID, Kind: ingestion.CandidateKindContent, Outcome: "maybe"}}, ctx: context.Background()},
		{name: "wrong candidate ID", guard: fakeGuard{contentDecision: decision("other", ingestion.CandidateKindContent, ingestion.DecisionAllow, "")}, ctx: context.Background()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timeout := time.Duration(0)
			if tt.name == "timeout" {
				timeout = time.Nanosecond
			}
			broker := ingestion.NewBroker(tt.guard, timeout)
			review := broker.ReviewContent(tt.ctx, content)
			if review.Decision.Outcome != ingestion.DecisionBlock {
				t.Fatalf("TC-005 decision = %+v, want fail-closed block", review.Decision)
			}
			if !strings.Contains(review.Decision.Reason, "fail closed") {
				t.Fatalf("TC-005 decision reason = %q, want fail-closed reason", review.Decision.Reason)
			}
			if _, ok := review.Release(); ok {
				t.Fatal("TC-005 Release ok = true, want false for fail-closed decision")
			}
		})
	}
}

func TestSupervisorIsolationFitnessRemainsBoundaryFree(t *testing.T) {
	for _, forbidden := range []string{
		"github.com/tkdtaylor/agent-builder/internal/ingestion",
		"github.com/tkdtaylor/agent-builder/internal/executor",
		"github.com/tkdtaylor/agent-builder/internal/webfetch",
		"github.com/tkdtaylor/agent-builder/internal/armor",
	} {
		if productionPackageImports(t, "internal/supervisor", forbidden) {
			t.Fatalf("TC-006 supervisor imports forbidden boundary dependency %q", forbidden)
		}
	}
}

type fakeGuard struct {
	contentDecision  ingestion.Decision
	contentErr       error
	toolCallDecision ingestion.Decision
	toolCallErr      error
	waitForContext   bool
}

func (g fakeGuard) DecideContent(ctx context.Context, candidate ingestion.ContentCandidate) (ingestion.Decision, error) {
	if g.waitForContext {
		<-ctx.Done()
		return ingestion.Decision{}, ctx.Err()
	}
	if g.contentErr != nil {
		return ingestion.Decision{}, g.contentErr
	}
	return g.contentDecision, nil
}

func (g fakeGuard) DecideToolCall(ctx context.Context, candidate ingestion.ToolCallCandidate) (ingestion.Decision, error) {
	if g.toolCallErr != nil {
		return ingestion.Decision{}, g.toolCallErr
	}
	return g.toolCallDecision, nil
}

func mustContent(t *testing.T, body string) ingestion.ContentCandidate {
	t.Helper()
	candidate, err := ingestion.NewContentCandidate(ingestion.ContentInput{
		ID:        ingestion.CandidateID(body),
		Content:   []byte(body),
		SourceURI: "https://example.test/" + body,
		MediaType: "text/plain",
		Provenance: ingestion.Provenance{
			TaskID:   "024",
			Executor: "test",
		},
	})
	if err != nil {
		t.Fatalf("TC-001 create content fixture: %v", err)
	}
	return candidate
}

func mustToolCall(t *testing.T, id string) ingestion.ToolCallCandidate {
	t.Helper()
	candidate, err := ingestion.NewToolCallCandidate(ingestion.ToolCallInput{
		ID:        ingestion.CandidateID(id),
		ToolName:  "web.fetch",
		Arguments: json.RawMessage(`{"url":"https://example.test"}`),
		TargetURI: "https://example.test",
		Provenance: ingestion.Provenance{
			TaskID:   "024",
			Executor: "test",
		},
	})
	if err != nil {
		t.Fatalf("TC-002 create tool-call fixture: %v", err)
	}
	return candidate
}

func decision(id ingestion.CandidateID, kind ingestion.CandidateKind, outcome ingestion.DecisionOutcome, reason string) ingestion.Decision {
	return ingestion.Decision{
		CandidateID: id,
		Kind:        kind,
		Outcome:     outcome,
		Reason:      reason,
	}
}

func productionPackageImports(t *testing.T, pkgDir, importPath string) bool {
	t.Helper()
	root := repoRoot(t)
	return packageImports(t, filepath.Join(root, pkgDir), importPath)
}

func packageImports(t *testing.T, pkgDir, importPath string) bool {
	t.Helper()
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("TC-006 read package dir %s: %v", pkgDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(pkgDir, entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("TC-006 parse imports for %s: %v", entry.Name(), err)
		}
		for _, spec := range file.Imports {
			if strings.Trim(spec.Path.Value, `"`) == importPath {
				return true
			}
		}
	}
	return false
}

func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("TC-006 get working dir: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("TC-006 could not find repo root")
		}
		dir = parent
	}
}
