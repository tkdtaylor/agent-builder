package executorharness_test

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

	"github.com/tkdtaylor/agent-builder/internal/executorharness"
	"github.com/tkdtaylor/agent-builder/internal/ingestion"
)

func TestHarnessConvertsWebEventBeforeContinuation(t *testing.T) {
	retrievedAt := time.Date(2026, 6, 5, 14, 0, 0, 0, time.UTC)
	guard := &recordingGuard{}
	harness := executorharness.New(executorharness.Config{
		Broker: ingestion.NewBroker(guard, 0),
	})

	var continued ingestion.ContentCandidate
	result := harness.HandleWebContent(context.Background(), executorharness.WebContentEvent{
		SourceURI:   " https://example.test/research ",
		MediaType:   " text/html ",
		Content:     []byte("benign research"),
		RetrievedAt: retrievedAt,
		Provenance:  ingestion.Provenance{TaskID: "027", Executor: "fixture-executor"},
	}, func(_ context.Context, release executorharness.ContentRelease) error {
		candidate, err := release.Candidate()
		if err != nil {
			return err
		}
		continued = candidate
		return nil
	})

	if result.Err != nil {
		t.Fatalf("TC-001 HandleWebContent error = %v", result.Err)
	}
	if !result.Released {
		t.Fatal("TC-001 content Released = false, want true")
	}
	if guard.content.ID == "" || guard.content.ID != result.Candidate.ID || continued.ID != result.Candidate.ID {
		t.Fatalf("TC-001 candidate IDs guard=%q result=%q continuation=%q, want same non-empty ID", guard.content.ID, result.Candidate.ID, continued.ID)
	}
	if guard.content.SourceURI != "https://example.test/research" || guard.content.MediaType != "text/html" {
		t.Fatalf("TC-001 content fields not normalized/preserved: %+v", guard.content)
	}
	if string(guard.content.Content) != "benign research" || !guard.content.RetrievedAt.Equal(retrievedAt) {
		t.Fatalf("TC-001 content bytes/retrieval metadata not preserved: %+v", guard.content)
	}
	if guard.content.Provenance.TaskID != "027" || guard.content.Provenance.Executor != "fixture-executor" {
		t.Fatalf("TC-001 provenance = %+v, want task/executor", guard.content.Provenance)
	}
}

func TestHarnessRejectsInvalidWebEventBeforeGuard(t *testing.T) {
	guard := &recordingGuard{}
	harness := executorharness.New(executorharness.Config{
		Broker: ingestion.NewBroker(guard, 0),
	})

	result := harness.HandleWebContent(context.Background(), executorharness.WebContentEvent{
		SourceURI: "file:///tmp/payload",
		Content:   []byte("payload"),
	}, func(context.Context, executorharness.ContentRelease) error {
		t.Fatal("TC-001 invalid web event reached continuation")
		return nil
	})
	if !errors.Is(result.Err, ingestion.ErrUnsupportedSourceURI) {
		t.Fatalf("TC-001 invalid web result error = %v, want ErrUnsupportedSourceURI", result.Err)
	}
	if guard.content.ID != "" {
		t.Fatalf("TC-001 guard saw invalid content candidate: %+v", guard.content)
	}
}

func TestHarnessConvertsToolEventBeforeExecution(t *testing.T) {
	guard := &recordingGuard{}
	harness := executorharness.New(executorharness.Config{
		Broker: ingestion.NewBroker(guard, 0),
	})

	var executed ingestion.ToolCallCandidate
	result := harness.HandleToolCall(context.Background(), executorharness.ToolCallEvent{
		ToolName:   " web.fetch ",
		Arguments:  json.RawMessage(`{"url":"https://example.test","limit":3}`),
		TargetURI:  " https://example.test ",
		Provenance: ingestion.Provenance{TaskID: "027", Executor: "fixture-executor"},
	}, func(_ context.Context, release executorharness.ToolCallRelease) error {
		candidate, err := release.Candidate()
		if err != nil {
			return err
		}
		executed = candidate
		return nil
	})

	if result.Err != nil {
		t.Fatalf("TC-002 HandleToolCall error = %v", result.Err)
	}
	if !result.Released {
		t.Fatal("TC-002 tool Released = false, want true")
	}
	if guard.toolCall.ID == "" || guard.toolCall.ID != result.Candidate.ID || executed.ID != result.Candidate.ID {
		t.Fatalf("TC-002 candidate IDs guard=%q result=%q executor=%q, want same non-empty ID", guard.toolCall.ID, result.Candidate.ID, executed.ID)
	}
	if guard.toolCall.ToolName != "web.fetch" {
		t.Fatalf("TC-002 ToolName = %q, want trimmed web.fetch", guard.toolCall.ToolName)
	}
	if string(guard.toolCall.Arguments) != `{"url":"https://example.test","limit":3}` {
		t.Fatalf("TC-002 Arguments = %s, want compact JSON preserved", guard.toolCall.Arguments)
	}
	if guard.toolCall.TargetURI != "https://example.test" {
		t.Fatalf("TC-002 TargetURI = %q, want normalized target", guard.toolCall.TargetURI)
	}
	if guard.toolCall.Provenance.TaskID != "027" || guard.toolCall.Provenance.Executor != "fixture-executor" {
		t.Fatalf("TC-002 provenance = %+v, want task/executor", guard.toolCall.Provenance)
	}
}

func TestHarnessRejectsInvalidToolEventBeforeGuard(t *testing.T) {
	tests := []struct {
		name  string
		event executorharness.ToolCallEvent
		want  error
	}{
		{
			name:  "blank tool",
			event: executorharness.ToolCallEvent{ToolName: " \t", Arguments: json.RawMessage(`{}`)},
			want:  ingestion.ErrBlankToolName,
		},
		{
			name:  "malformed arguments",
			event: executorharness.ToolCallEvent{ToolName: "web.fetch", Arguments: json.RawMessage(`{"url":`)},
			want:  ingestion.ErrMalformedToolArguments,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guard := &recordingGuard{}
			harness := executorharness.New(executorharness.Config{
				Broker: ingestion.NewBroker(guard, 0),
			})
			result := harness.HandleToolCall(context.Background(), tt.event, func(context.Context, executorharness.ToolCallRelease) error {
				t.Fatal("TC-002 invalid tool event reached executor")
				return nil
			})
			if !errors.Is(result.Err, tt.want) {
				t.Fatalf("TC-002 invalid tool result error = %v, want %v", result.Err, tt.want)
			}
			if guard.toolCall.ID != "" {
				t.Fatalf("TC-002 guard saw invalid tool candidate: %+v", guard.toolCall)
			}
		})
	}
}

func TestHarnessAllowDecisionsReleaseAfterBrokerReview(t *testing.T) {
	trace := &traceRecorder{}
	guard := &recordingGuard{trace: trace}
	harness := executorharness.New(executorharness.Config{
		Broker: ingestion.NewBroker(guard, 0),
		Trace:  trace,
	})

	var continued int
	contentResult := harness.HandleWebContent(context.Background(), validWebEvent(), func(_ context.Context, release executorharness.ContentRelease) error {
		trace.RecordTrace(executorharness.TraceEvent{Stage: traceContentContinuationCalled})
		content, err := release.Content()
		if err != nil {
			return err
		}
		if string(content) != "benign content" {
			t.Fatalf("TC-003 released content = %q, want original bytes", content)
		}
		continued++
		return nil
	})
	if contentResult.Err != nil || !contentResult.Released || continued != 1 {
		t.Fatalf("TC-003 content result = %+v continued=%d, want one release after allow", contentResult, continued)
	}
	assertOrder(t, trace.stages(), executorharness.TraceContentCandidateProduced, traceContentGuardConsumed, executorharness.TraceContentBrokerReviewed, executorharness.TraceContentReleased, traceContentContinuationCalled)

	trace.reset()
	var executed int
	toolResult := harness.HandleToolCall(context.Background(), validToolEvent(), func(_ context.Context, release executorharness.ToolCallRelease) error {
		trace.RecordTrace(executorharness.TraceEvent{Stage: traceToolExecutorCalled})
		args, err := release.Arguments()
		if err != nil {
			return err
		}
		if string(args) != `{"url":"https://example.test"}` {
			t.Fatalf("TC-003 released arguments = %s, want original JSON", args)
		}
		executed++
		return nil
	})
	if toolResult.Err != nil || !toolResult.Released || executed != 1 {
		t.Fatalf("TC-003 tool result = %+v executed=%d, want one execution after allow", toolResult, executed)
	}
	assertOrder(t, trace.stages(), executorharness.TraceToolCandidateProduced, traceToolGuardConsumed, executorharness.TraceToolBrokerReviewed, executorharness.TraceToolReleased, traceToolExecutorCalled, executorharness.TraceToolExecuted)
}

func TestHarnessFailClosedDecisionsDoNotRelease(t *testing.T) {
	tests := []struct {
		name    string
		guard   ingestion.Guard
		timeout time.Duration
		want    ingestion.DecisionOutcome
	}{
		{name: "block", guard: &recordingGuard{contentOutcome: ingestion.DecisionBlock, toolOutcome: ingestion.DecisionBlock}, want: ingestion.DecisionBlock},
		{name: "quarantine", guard: &recordingGuard{contentOutcome: ingestion.DecisionQuarantine, toolOutcome: ingestion.DecisionQuarantine}, want: ingestion.DecisionQuarantine},
		{name: "guard error", guard: &recordingGuard{err: errors.New("guard unavailable")}, want: ingestion.DecisionBlock},
		{name: "unavailable guard", guard: nil, want: ingestion.DecisionBlock},
		{name: "timeout", guard: &recordingGuard{waitForContext: true}, timeout: time.Nanosecond, want: ingestion.DecisionBlock},
		{name: "malformed", guard: &recordingGuard{contentOutcome: "maybe", toolOutcome: "maybe"}, want: ingestion.DecisionBlock},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := executorharness.New(executorharness.Config{
				Broker: ingestion.NewBroker(tt.guard, tt.timeout),
			})
			var continued bool
			contentResult := harness.HandleWebContent(context.Background(), validWebEvent(), func(context.Context, executorharness.ContentRelease) error {
				continued = true
				return nil
			})
			if contentResult.Decision.Outcome != tt.want || contentResult.Released || continued {
				t.Fatalf("TC-004 content result = %+v continued=%v, want fail-closed no release", contentResult, continued)
			}

			var executed bool
			toolResult := harness.HandleToolCall(context.Background(), validToolEvent(), func(context.Context, executorharness.ToolCallRelease) error {
				executed = true
				return nil
			})
			if toolResult.Decision.Outcome != tt.want || toolResult.Released || executed {
				t.Fatalf("TC-004 tool result = %+v executed=%v, want fail-closed no execution", toolResult, executed)
			}
		})
	}
}

func TestHarnessProducerConsumerTraceCoversLivePath(t *testing.T) {
	trace := &traceRecorder{}
	guard := &recordingGuard{trace: trace, blockUnsafe: true}
	harness := executorharness.New(executorharness.Config{
		Broker: ingestion.NewBroker(guard, 0),
		Trace:  trace,
	})

	safeContent := harness.HandleWebContent(context.Background(), validWebEvent(), func(context.Context, executorharness.ContentRelease) error {
		return nil
	})
	blockedContent := harness.HandleWebContent(context.Background(), executorharness.WebContentEvent{
		SourceURI:  "https://example.test/blocked",
		MediaType:  "text/plain",
		Content:    []byte("unsafe prompt injection"),
		Provenance: ingestion.Provenance{TaskID: "027", Executor: "fixture-executor"},
	}, func(context.Context, executorharness.ContentRelease) error {
		t.Fatal("TC-005 blocked content reached continuation")
		return nil
	})
	safeTool := harness.HandleToolCall(context.Background(), validToolEvent(), func(context.Context, executorharness.ToolCallRelease) error {
		return nil
	})
	blockedTool := harness.HandleToolCall(context.Background(), executorharness.ToolCallEvent{
		ToolName:   "shell.exec",
		Arguments:  json.RawMessage(`{"cmd":"cat /tmp/secret"}`),
		TargetURI:  "https://example.test",
		Provenance: ingestion.Provenance{TaskID: "027", Executor: "fixture-executor"},
	}, func(context.Context, executorharness.ToolCallRelease) error {
		t.Fatal("TC-005 blocked tool reached executor")
		return nil
	})

	if !safeContent.Released || blockedContent.Released || !safeTool.Released || blockedTool.Released {
		t.Fatalf("TC-005 release decisions safeContent=%v blockedContent=%v safeTool=%v blockedTool=%v", safeContent.Released, blockedContent.Released, safeTool.Released, blockedTool.Released)
	}
	for _, result := range []struct {
		id       ingestion.CandidateID
		kind     ingestion.CandidateKind
		decision ingestion.Decision
	}{
		{safeContent.Candidate.ID, ingestion.CandidateKindContent, safeContent.Decision},
		{blockedContent.Candidate.ID, ingestion.CandidateKindContent, blockedContent.Decision},
		{safeTool.Candidate.ID, ingestion.CandidateKindToolCall, safeTool.Decision},
		{blockedTool.Candidate.ID, ingestion.CandidateKindToolCall, blockedTool.Decision},
	} {
		if result.decision.CandidateID != result.id || result.decision.Kind != result.kind {
			t.Fatalf("TC-005 decision correlation = (%q,%q), want (%q,%q)", result.decision.CandidateID, result.decision.Kind, result.id, result.kind)
		}
	}
	assertContains(t, trace.stages(), executorharness.TraceContentCandidateProduced, traceContentGuardConsumed, executorharness.TraceContentBrokerReviewed, executorharness.TraceContentReleased)
	assertContains(t, trace.stages(), executorharness.TraceToolCandidateProduced, traceToolGuardConsumed, executorharness.TraceToolBrokerReviewed, executorharness.TraceToolReleased, executorharness.TraceToolExecuted)
	t.Logf("TC-005 producer-consumer trace: %s", strings.Join(stageStrings(trace.stages()), " -> "))
}

func TestHarnessRejectsDirectBypassReleaseValues(t *testing.T) {
	if _, err := (executorharness.ContentRelease{}).Candidate(); !errors.Is(err, executorharness.ErrUnreviewedRelease) {
		t.Fatalf("TC-006 zero content release error = %v, want ErrUnreviewedRelease", err)
	}
	if _, err := (executorharness.ToolCallRelease{}).Candidate(); !errors.Is(err, executorharness.ErrUnreviewedRelease) {
		t.Fatalf("TC-006 zero tool release error = %v, want ErrUnreviewedRelease", err)
	}

	contentBypass := func(_ context.Context, release executorharness.ContentRelease) error {
		_, err := release.Content()
		return err
	}
	if err := contentBypass(context.Background(), executorharness.ContentRelease{}); !errors.Is(err, executorharness.ErrUnreviewedRelease) {
		t.Fatalf("TC-006 direct content bypass error = %v, want ErrUnreviewedRelease", err)
	}

	toolBypass := func(_ context.Context, release executorharness.ToolCallRelease) error {
		_, err := release.Arguments()
		return err
	}
	if err := toolBypass(context.Background(), executorharness.ToolCallRelease{}); !errors.Is(err, executorharness.ErrUnreviewedRelease) {
		t.Fatalf("TC-006 direct tool bypass error = %v, want ErrUnreviewedRelease", err)
	}
}

func TestSupervisorImportIsolationStillHasNoHarnessDependencies(t *testing.T) {
	for _, forbidden := range []string{
		"github.com/tkdtaylor/agent-builder/internal/executorharness",
		"github.com/tkdtaylor/agent-builder/internal/ingestion",
		"github.com/tkdtaylor/agent-builder/internal/executor",
		"github.com/tkdtaylor/agent-builder/internal/armor",
	} {
		if productionPackageImports(t, "internal/supervisor", forbidden) {
			t.Fatalf("TC-007 supervisor imports forbidden harness dependency %q", forbidden)
		}
	}
}

const (
	traceContentGuardConsumed      executorharness.TraceStage = "content-guard-consumed"
	traceContentContinuationCalled executorharness.TraceStage = "content-continuation-called"
	traceToolGuardConsumed         executorharness.TraceStage = "tool-guard-consumed"
	traceToolExecutorCalled        executorharness.TraceStage = "tool-executor-called"
)

type recordingGuard struct {
	content        ingestion.ContentCandidate
	toolCall       ingestion.ToolCallCandidate
	contentOutcome ingestion.DecisionOutcome
	toolOutcome    ingestion.DecisionOutcome
	err            error
	waitForContext bool
	blockUnsafe    bool
	trace          *traceRecorder
}

func (g *recordingGuard) DecideContent(ctx context.Context, candidate ingestion.ContentCandidate) (ingestion.Decision, error) {
	if g.waitForContext {
		<-ctx.Done()
		return ingestion.Decision{}, ctx.Err()
	}
	if g.err != nil {
		return ingestion.Decision{}, g.err
	}
	g.content = candidate
	g.record(traceContentGuardConsumed, candidate.ID, ingestion.CandidateKindContent, "")
	outcome := g.contentOutcome
	if outcome == "" {
		outcome = ingestion.DecisionAllow
	}
	reason := ""
	if g.blockUnsafe && strings.Contains(string(candidate.Content), "unsafe") {
		outcome = ingestion.DecisionBlock
		reason = "unsafe content"
	}
	return ingestion.Decision{
		CandidateID: candidate.ID,
		Kind:        ingestion.CandidateKindContent,
		Outcome:     outcome,
		Reason:      reason,
	}, nil
}

func (g *recordingGuard) DecideToolCall(ctx context.Context, candidate ingestion.ToolCallCandidate) (ingestion.Decision, error) {
	if g.waitForContext {
		<-ctx.Done()
		return ingestion.Decision{}, ctx.Err()
	}
	if g.err != nil {
		return ingestion.Decision{}, g.err
	}
	g.toolCall = candidate
	g.record(traceToolGuardConsumed, candidate.ID, ingestion.CandidateKindToolCall, "")
	outcome := g.toolOutcome
	if outcome == "" {
		outcome = ingestion.DecisionAllow
	}
	reason := ""
	if g.blockUnsafe && candidate.ToolName == "shell.exec" {
		outcome = ingestion.DecisionBlock
		reason = "unsafe tool"
	}
	return ingestion.Decision{
		CandidateID: candidate.ID,
		Kind:        ingestion.CandidateKindToolCall,
		Outcome:     outcome,
		Reason:      reason,
	}, nil
}

func (g *recordingGuard) record(stage executorharness.TraceStage, id ingestion.CandidateID, kind ingestion.CandidateKind, outcome ingestion.DecisionOutcome) {
	if g.trace != nil {
		g.trace.RecordTrace(executorharness.TraceEvent{
			Stage:       stage,
			CandidateID: id,
			Kind:        kind,
			Outcome:     outcome,
		})
	}
}

type traceRecorder struct {
	events []executorharness.TraceEvent
}

func (r *traceRecorder) RecordTrace(event executorharness.TraceEvent) {
	r.events = append(r.events, event)
}

func (r *traceRecorder) stages() []executorharness.TraceStage {
	out := make([]executorharness.TraceStage, 0, len(r.events))
	for _, event := range r.events {
		out = append(out, event.Stage)
	}
	return out
}

func (r *traceRecorder) reset() {
	r.events = nil
}

func validWebEvent() executorharness.WebContentEvent {
	return executorharness.WebContentEvent{
		SourceURI:  "https://example.test/content",
		MediaType:  "text/plain",
		Content:    []byte("benign content"),
		Provenance: ingestion.Provenance{TaskID: "027", Executor: "fixture-executor"},
	}
}

func validToolEvent() executorharness.ToolCallEvent {
	return executorharness.ToolCallEvent{
		ToolName:   "web.fetch",
		Arguments:  json.RawMessage(`{"url":"https://example.test"}`),
		TargetURI:  "https://example.test",
		Provenance: ingestion.Provenance{TaskID: "027", Executor: "fixture-executor"},
	}
}

func assertOrder(t *testing.T, got []executorharness.TraceStage, want ...executorharness.TraceStage) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("trace stages = %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trace stages = %v, want %v", got, want)
		}
	}
}

func assertContains(t *testing.T, got []executorharness.TraceStage, want ...executorharness.TraceStage) {
	t.Helper()
	for _, stage := range want {
		found := false
		for _, candidate := range got {
			if candidate == stage {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("trace stages = %v, missing %s", got, stage)
		}
	}
}

func stageStrings(stages []executorharness.TraceStage) []string {
	out := make([]string, 0, len(stages))
	for _, stage := range stages {
		out = append(out, string(stage))
	}
	return out
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
		t.Fatalf("TC-007 read package dir %s: %v", pkgDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(pkgDir, entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("TC-007 parse imports for %s: %v", entry.Name(), err)
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
		t.Fatalf("TC-007 get working dir: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("TC-007 could not find repo root")
		}
		dir = parent
	}
}
